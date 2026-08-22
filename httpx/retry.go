package httpx

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxBodyDrainSize is the maximum number of bytes to read from the response body when draining it before closing.
const maxBodyDrainSize = 1 << 20 // 1 MB

// Retry sends an http request, retrying on transient failures with exponential backoff and jitter.
//
//   - client is the HTTP client used to send the request. If nil, http.DefaultClient is used.
//   - req is the http request to send. It must not be nil and is never mutated.
//   - maxRetries is the maximum number of retries after the initial attempt. It must not be negative.
//   - baseBackoff is the delay before the first retry. It must be greater than zero.
//   - maxBackoff caps the computed retry delay.
//     Without a ceiling, the computed delay can overflow for large combinations maxRetries and baseBackoff.
//
// The request is attempted once and then retried up to maxRetries.
// An attempt is retried only if it fails with a retryable error or returns a retryable HTTP status code.
// Before each retry, Retry waits for an exponentially increasing delay, starting at baseBackoff
// and doubling on each subsequent attempt, plus up to 50% random jitter, capped at maxBackoff.
//
// If the response includes a valid Retry-After header,
// Retry honors the server's requested delay instead, ignoring maxBackoff.
// Otherwise, Retry falls back to the computed exponential backoff delay capped at maxBackoff.
//
// If the request's context is canceled, Retry returns immediately with the context error.
// A context deadline exceeded error is treated as a timeout, and the request will be retried.
// If all attempts are exhausted, Retry returns the error from the last attempt.
func Retry(client *http.Client, req *http.Request, maxRetries int, baseBackoff, maxBackoff time.Duration) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}

	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	if maxRetries < 0 {
		return nil, fmt.Errorf("maxRetries cannot be negative")
	}

	if baseBackoff <= 0 {
		return nil, fmt.Errorf("baseBackoff must be greater than zero")
	}

	if maxBackoff <= 0 {
		return nil, fmt.Errorf("maxBackoff must be greater than zero")
	}

	if maxBackoff < baseBackoff {
		return nil, fmt.Errorf("maxBackoff must be greater than or equal to baseBackoff")
	}

	// Clone the request once so retries never mutate the caller's original request,
	// and reuse this single clone across attempts instead of cloning per attempt.
	r := req.Clone(req.Context())

	// Ensure the GetBody function is available.
	// client.Do drains r.Body, so it must be recreated from GetBody before each attempt.
	// This allows the request body to be read multiple times for retries.
	if r.Body != nil && r.GetBody == nil {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return nil, err
		}

		r.Body = io.NopCloser(bytes.NewReader(body))
		r.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}

	var lastErr error
	var retryAfter time.Duration

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Having the delay logic at the start of the loop vs. the end of the loop reads better,
		// as it avoids an unnecessary delay after the last attempt or breaking early from the loop.
		if attempt > 0 {
			var delay time.Duration

			if retryAfter > 0 {
				// Honor the server's requested delay.
				delay = retryAfter
				retryAfter = 0
			} else {
				// baseBackoff << shift can overflow int64 for large combinations of maxRetries and baseBackoff,
				// so cap the exponential backoff at maxBackoff before shifting instead of after.
				delay = maxBackoff
				if shift := attempt - 1; baseBackoff <= maxBackoff>>shift {
					delay = baseBackoff << shift
				}

				// Add up to 50% jitter to avoid thundering-herd problem,
				// where many clients retry at the same time and overwhelm the server.
				delay += time.Duration(rand.Int63n(int64(delay)/2 + 1))

				// Cap the delay at maxBackoff.
				if delay > maxBackoff {
					delay = maxBackoff
				}
			}

			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}

		// Reset the body from GetBody so this attempt gets a fresh, unconsumed reader;
		// client.Do closes r.Body after every call.
		if r.GetBody != nil {
			body, err := r.GetBody()
			if err != nil {
				return nil, fmt.Errorf("error resetting request body: %w", err)
			}
			r.Body = body
		}

		// Send the request and check and determine whether the request should be retried.
		resp, err := client.Do(r)
		if err != nil {
			if !isErrorRetryable(err) {
				return nil, err
			}

			lastErr = err
		} else {
			if !isStatusRetryable(resp.StatusCode) {
				return resp, nil
			}

			lastErr = fmt.Errorf("http server responded with %s", resp.Status)
			retryAfter, _ = parseRetryAfter(resp)

			// Drain and close the body so the transport can reuse the underlying TCP connection.
			// Cap the body size, so a large or slow response body cannot stall the retry loop.
			// If the body exceeds the cap, the connection will not be reused for the next attempt.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyDrainSize))
			_ = resp.Body.Close()
		}
	}

	return nil, lastErr
}

func isErrorRetryable(err error) bool {
	// If there is no error, there is nothing to retry!
	if err == nil {
		return false
	}

	// Do not retry on context cancellation and deadline exceeded.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Do not retry on TLS certificate verification errors.
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return false
	}

	// *tls.CertificateVerificationError exist since Go 1.20.
	// Older versions and custom dialers or VerifyConnection callbacks may surface x509 errors directly.

	var (
		hostnameErr    x509.HostnameError
		unknownAuthErr x509.UnknownAuthorityError
		certInvalidErr x509.CertificateInvalidError
	)

	if errors.As(err, &hostnameErr) ||
		errors.As(err, &unknownAuthErr) ||
		errors.As(err, &certInvalidErr) {
		return false
	}

	// Do not retry on DNS resolution errors that indicate the host could not be found.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return false
	}

	// Non-retryable http errors.
	if errors.Is(err, http.ErrSchemeMismatch) {
		return false
	}

	// Do not retry on URL errors that indicate a malformed URL.
	// These errors are only exposed as error strings,
	// so we can only check the error message against hard-coded substrings.
	if s := err.Error(); strings.Contains(s, "missing protocol scheme") ||
		strings.Contains(s, "unsupported protocol scheme") {
		return false
	}

	return true
}

func isStatusRetryable(status int) bool {
	return status == http.StatusTooManyRequests || (status >= 500 && status < 600)
}

// parseRetryAfter parses the value of an HTTP Retry-After header,
// which per RFC 9110 §10.2.3 is either a non-negative integer number of seconds or an HTTP-date.
//
// See https://datatracker.ietf.org/doc/html/rfc9110#section-10.2.3
// and/or https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Retry-After
func parseRetryAfter(resp *http.Response) (time.Duration, bool) {
	header := resp.Header.Get("Retry-After")
	if header == "" {
		return 0, false
	}

	// Attempt to parse the header as a non-negative integer number of seconds.
	if n, err := strconv.ParseUint(header, 10, 64); err == nil {
		return time.Duration(n) * time.Second, true
	}

	if t, err := http.ParseTime(header); err == nil {
		if d := time.Until(t); d > 0 {
			return d, true
		}

		// The date has already passed, so retry immediately.
		return 0, true
	}

	// The header is invalid, so ignore it.
	return 0, false
}
