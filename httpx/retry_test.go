package httpx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// roundTripFunc lets a test control exactly what a *http.Client returns for a request,
// without depending on real network conditions (DNS, TLS, dial failures, etc.).
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRetry(t *testing.T) {
	t.Run("RequestNil", func(t *testing.T) {
		_, err := Retry(nil, nil, 0, 0, 0)
		assert.EqualError(t, err, "request cannot be nil")
	})

	t.Run("NegativeMaxRetries", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		assert.NoError(t, err)

		_, err = Retry(nil, req, -1, 0, 0)
		assert.EqualError(t, err, "maxRetries cannot be negative")
	})

	t.Run("ZeroBaseBackoff", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		assert.NoError(t, err)

		_, err = Retry(nil, req, 0, 0, 0)
		assert.EqualError(t, err, "baseBackoff must be greater than zero")
	})

	t.Run("NegativeBaseBackoff", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		assert.NoError(t, err)

		_, err = Retry(nil, req, 0, -1, 0)
		assert.EqualError(t, err, "baseBackoff must be greater than zero")
	})

	t.Run("ZeroMaxBackoff", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		assert.NoError(t, err)

		_, err = Retry(nil, req, 5, time.Millisecond, 0)
		assert.EqualError(t, err, "maxBackoff must be greater than zero")
	})

	t.Run("NegativeMaxBackoff", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		assert.NoError(t, err)

		_, err = Retry(nil, req, 5, time.Millisecond, -1)
		assert.EqualError(t, err, "maxBackoff must be greater than zero")
	})

	t.Run("MaxBackoffLessThanBaseBackoff", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		assert.NoError(t, err)

		_, err = Retry(nil, req, 5, 10*time.Millisecond, time.Millisecond)
		assert.EqualError(t, err, "maxBackoff must be greater than or equal to baseBackoff")
	})

	t.Run("NilClient_UsesDefaultClient", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(nil, req, 5, time.Millisecond, 10*time.Millisecond)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NoError(t, resp.Body.Close())
	})

	t.Run("SuccessOnFirstAttempt", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 5, time.Millisecond, 10*time.Millisecond)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
		assert.NoError(t, resp.Body.Close())
	})

	t.Run("RequestWithoutGetBody", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		serverURL, err := url.Parse(server.URL)
		assert.NoError(t, err)

		req := &http.Request{
			Method: http.MethodGet,
			URL:    serverURL,
			Body:   io.NopCloser(strings.NewReader("hello world")),
		}

		resp, err := Retry(server.Client(), req, 5, time.Millisecond, 10*time.Millisecond)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
		assert.NoError(t, resp.Body.Close())
	})

	t.Run("SuccessAfterRetry_500", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&attempts, 1) < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 5, time.Millisecond, time.Millisecond)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
		assert.NoError(t, resp.Body.Close())
	})

	t.Run("SuccessAfterRetry_503", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&attempts, 1) < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 5, time.Millisecond, 10*time.Millisecond)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
		assert.NoError(t, resp.Body.Close())
	})

	t.Run("SuccessAfterRetry_429", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&attempts, 1) < 3 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 5, time.Millisecond, 10*time.Millisecond)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
		assert.NoError(t, resp.Body.Close())
	})

	t.Run("NonRetryableStatus_404", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 5, time.Millisecond, 10*time.Millisecond)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
		assert.NoError(t, resp.Body.Close())
	})

	t.Run("NonRetryableStatus_401", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 5, time.Millisecond, 10*time.Millisecond)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
		assert.NoError(t, resp.Body.Close())
	})

	t.Run("NonRetryableTransportError_CertificateVerificationError", func(t *testing.T) {
		var attempts int32
		client := &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				atomic.AddInt32(&attempts, 1)
				return nil, &tls.CertificateVerificationError{
					Err: errors.New("x509: certificate signed by unknown authority"),
				}
			}),
		}

		req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		assert.NoError(t, err)

		resp, err := Retry(client, req, 5, time.Millisecond, 10*time.Millisecond)
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.ErrorContains(t, err, "tls: failed to verify certificate: x509: certificate signed by unknown authority")
		assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
	})

	t.Run("ExhaustsRetries_ReturnsStatusError", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 3, time.Millisecond, 10*time.Millisecond)
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.EqualError(t, err, "http server responded with 500 Internal Server Error")
		assert.Equal(t, int32(4), atomic.LoadInt32(&attempts))
	})

	t.Run("ExhaustsRetries_ReturnsTransportError", func(t *testing.T) {
		var attempts int32
		client := &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				atomic.AddInt32(&attempts, 1)
				return nil, &net.OpError{
					Op:  "dial",
					Err: errors.New("connection refused"),
				}
			}),
		}

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		assert.NoError(t, err)

		resp, err := Retry(client, req, 3, time.Millisecond, 10*time.Millisecond)
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.ErrorContains(t, err, "dial: connection refused")
		assert.Equal(t, int32(4), atomic.LoadInt32(&attempts))
	})

	t.Run("RetryAfterHeader_OverridesExponentialBackoff", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt32(&attempts, 1)
			if n == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		start := time.Now()
		resp, err := Retry(server.Client(), req, 3, time.Millisecond, 10*time.Millisecond)
		elapsed := time.Since(start)

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(2), atomic.LoadInt32(&attempts))
		assert.Greater(t, elapsed, time.Second)
		assert.NoError(t, resp.Body.Close())
	})

	t.Run("RetryAfterHeader_FallsBackToExponentialBackoff", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt32(&attempts, 1)
			if n == 1 {
				w.Header().Set("Retry-After", "not-a-valid-value")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		start := time.Now()
		resp, err := Retry(server.Client(), req, 3, time.Millisecond, 10*time.Millisecond)
		elapsed := time.Since(start)

		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(2), atomic.LoadInt32(&attempts))
		assert.Less(t, elapsed, 10*time.Millisecond)
		assert.NoError(t, resp.Body.Close())
	})

	t.Run("ContextCanceled_BeforeFirstAttempt", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 3, time.Millisecond, 10*time.Millisecond)
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(0), atomic.LoadInt32(&attempts))
	})

	t.Run("ContextCanceled_DuringRetry", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(9*time.Millisecond, cancel)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 3, 10*time.Millisecond, 100*time.Millisecond)
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), atomic.LoadInt32(&attempts)) // canceled while waiting for the first retry
	})

	t.Run("ContextDeadlineExceeded_BeforeFirstAttempt", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			time.Sleep(10 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 3, 10*time.Millisecond, 100*time.Millisecond)
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
	})

	t.Run("ContextDeadlineExceeded_DuringRetry", func(t *testing.T) {
		var attempts int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			time.Sleep(10 * time.Millisecond)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 3, 10*time.Millisecond, 100*time.Millisecond)
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
	})

	t.Run("RequestBodySentOnEachAttempt", func(t *testing.T) {
		var attempts int32
		var received []string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt32(&attempts, 1)

			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			received = append(received, string(b))

			if n < 3 {
				w.WriteHeader(http.StatusInternalServerError)
			} else {
				w.WriteHeader(http.StatusOK)
			}
		}))

		defer server.Close()

		const payload = `hello world`
		req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(payload))
		assert.NoError(t, err)

		resp, err := Retry(server.Client(), req, 3, time.Millisecond, 10*time.Millisecond)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
		assert.Equal(t, []string{payload, payload, payload}, received)
		assert.NoError(t, resp.Body.Close())
	})
}

func TestIsErrorRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "NilError",
			err:      nil,
			expected: false,
		},
		{
			name:     "ContextCanceled",
			err:      context.Canceled,
			expected: false,
		},
		{
			name:     "WrappedContextCanceled",
			err:      fmt.Errorf("request failed: %w", context.Canceled),
			expected: false,
		},
		{
			name:     "ContextDeadlineExceeded",
			err:      context.DeadlineExceeded,
			expected: false,
		},
		{
			name:     "WrappedContextDeadlineExceeded",
			err:      fmt.Errorf("request failed: %w", context.DeadlineExceeded),
			expected: false,
		},
		{
			name: "TLSCertificateVerificationError",
			err: &tls.CertificateVerificationError{
				Err: errors.New("invalid cert"),
			},
			expected: false,
		},
		{
			name:     "X509HostnameError",
			err:      fmt.Errorf("dial: %w", x509.HostnameError{Host: "example.com"}),
			expected: false,
		},
		{
			name:     "X509UnknownAuthorityError",
			err:      fmt.Errorf("dial: %w", x509.UnknownAuthorityError{}),
			expected: false,
		},
		{
			name:     "X509CertificateInvalidError",
			err:      fmt.Errorf("dial: %w", x509.CertificateInvalidError{}),
			expected: false,
		},
		{
			name: "DNSError_NotFound",
			err: &net.DNSError{
				Err:        "no such host",
				Name:       "example.invalid",
				IsNotFound: true,
			},
			expected: false,
		},
		{
			name: "DNSError_Timeout",
			err: &net.DNSError{
				Err:       "timeout",
				Name:      "example.com",
				IsTimeout: true,
			},
			expected: true,
		},
		{
			name:     "HTTPSchemeMismatch",
			err:      http.ErrSchemeMismatch,
			expected: false,
		},
		{
			name:     "UnsupportedProtocolScheme",
			err:      errors.New(`Get "ftp://example.com": unsupported protocol scheme "ftp"`),
			expected: false,
		},
		{
			name:     "MissingProtocolScheme",
			err:      errors.New(`parse "://example.com": missing protocol scheme`),
			expected: false,
		},
		{
			name:     "GenericRetryableError",
			err:      errors.New("connection reset by peer"),
			expected: true,
		},
		{
			name:     "UnexpectedEOF",
			err:      io.ErrUnexpectedEOF,
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isErrorRetryable(tc.err))
		})
	}
}

func TestIsStatusRetryable(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		expected bool
	}{
		{name: "OK", status: http.StatusOK, expected: false},
		{name: "BadRequest", status: http.StatusBadRequest, expected: false},
		{name: "Unauthorized", status: http.StatusUnauthorized, expected: false},
		{name: "NotFound", status: http.StatusNotFound, expected: false},
		{name: "TooManyRequests", status: http.StatusTooManyRequests, expected: true},
		{name: "JustBelowServerErrorRange", status: 499, expected: false},
		{name: "LowestServerError", status: 500, expected: true},
		{name: "InternalServerError", status: http.StatusInternalServerError, expected: true},
		{name: "NotImplemented", status: http.StatusNotImplemented, expected: true},
		{name: "BadGateway", status: http.StatusBadGateway, expected: true},
		{name: "InternalServerError", status: http.StatusInternalServerError, expected: true},
		{name: "ServiceUnavailable", status: http.StatusServiceUnavailable, expected: true},
		{name: "GatewayTimeout", status: http.StatusGatewayTimeout, expected: true},
		{name: "HTTPVersionNotSupported", status: http.StatusHTTPVersionNotSupported, expected: true},
		{name: "HighestServerError", status: 599, expected: true},
		{name: "JustAboveServerErrorRange", status: 600, expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isStatusRetryable(tc.status))
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name             string
		header           string
		expectedDuration time.Duration
		expectedOK       bool
	}{
		{
			name:             "NoHeader",
			header:           "",
			expectedDuration: 0,
			expectedOK:       false,
		},
		{
			name:             "NegativeSeconds",
			header:           "-120",
			expectedDuration: 0,
			expectedOK:       false,
		},
		{
			name:             "ZeroSeconds",
			header:           "0",
			expectedDuration: 0,
			expectedOK:       true,
		},
		{
			name:             "PositiveSeconds",
			header:           "120",
			expectedDuration: 120 * time.Second,
			expectedOK:       true,
		},
		{
			name:             "InvalidValue",
			header:           "not-a-number-or-date",
			expectedDuration: 0,
			expectedOK:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			if tc.header != "" {
				resp.Header.Set("Retry-After", tc.header)
			}

			d, ok := parseRetryAfter(resp)

			assert.Equal(t, tc.expectedDuration, d)
			assert.Equal(t, tc.expectedOK, ok)
		})
	}

	t.Run("FutureHTTPDate", func(t *testing.T) {
		future := time.Now().Add(2 * time.Hour)
		resp := &http.Response{Header: http.Header{}}
		resp.Header.Set("Retry-After", future.UTC().Format(http.TimeFormat))

		dur, ok := parseRetryAfter(resp)
		assert.True(t, ok)
		assert.InDelta(t, 2*time.Hour, dur, float64(5*time.Second))
	})

	t.Run("PastHTTPDate", func(t *testing.T) {
		past := time.Now().Add(-2 * time.Hour)
		resp := &http.Response{Header: http.Header{}}
		resp.Header.Set("Retry-After", past.UTC().Format(http.TimeFormat))

		dur, ok := parseRetryAfter(resp)
		assert.True(t, ok)
		assert.Equal(t, time.Duration(0), dur)
	})
}
