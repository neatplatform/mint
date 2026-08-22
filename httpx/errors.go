package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const defaultStatusCode = http.StatusInternalServerError

// Error responds to an HTTP request with an error.
// If the error is [[ServerError]], it uses the status code from the error; otherwise, it defaults to 500 Internal Server Error.
func Error(w http.ResponseWriter, err error) {
	var e *ServerError
	if errors.As(err, &e) {
		http.Error(w, e.Error(), e.StatusCode())
	} else {
		http.Error(w, err.Error(), defaultStatusCode)
	}
}

// ServerError is a custom error type for errors happening in HTTP handlers.
type ServerError struct {
	error
	statusCode int
}

// NewServerError creates a new HTTP server error.
func NewServerError(err error, statusCode int) *ServerError {
	return &ServerError{err, statusCode}
}

func (e *ServerError) Unwrap() error {
	return e.error
}

// StatusCode returns the appropriate HTTP status code for the error.
func (e *ServerError) StatusCode() int {
	return e.statusCode
}

// ClientError is a custom error type for errors happening when calling an HTTP endpoint.
type ClientError struct {
	message    string
	statusCode int
}

// NewClientError creates a new HTTP client error.
func NewClientError(resp *http.Response) *ClientError {
	var message string

	if resp.Body != nil {
		respBody := new(struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		})

		if b, err := io.ReadAll(resp.Body); err == nil {
			_ = json.Unmarshal(b, respBody)

			switch {
			case respBody.Error != "":
				message = respBody.Error
			case respBody.Message != "":
				message = respBody.Message
			default:
				message = string(b)
			}
		}

		// Ensure the response body is closed after reading.
		_ = resp.Body.Close()
	}

	if resp.Request != nil && resp.Request.URL != nil {
		if message == "" {
			message = fmt.Sprintf("%s %s: [%d]", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode)
		} else {
			message = fmt.Sprintf("%s %s: [%d] %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, message)
		}
	}

	return &ClientError{
		message:    message,
		statusCode: resp.StatusCode,
	}
}

func (e *ClientError) Error() string {
	return e.message
}

// StatusCode returns the status code of the HTTP response.
func (e *ClientError) StatusCode() int {
	return e.statusCode
}
