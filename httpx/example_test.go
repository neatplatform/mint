package httpx_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/neatplatform/mint/httpx"
)

func ExampleRetry() {
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
	if err != nil {
		panic(err)
	}

	resp, err := httpx.Retry(nil, req, 5, 10*time.Millisecond, 100*time.Millisecond)
	if err != nil {
		panic(err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	fmt.Printf("Status:   %s\n", resp.Status)
	fmt.Printf("Attempts: %d\n", attempts)
}

func ExampleClientError() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"insufficient permissions"}`))
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/objects/69")
	if err != nil {
		panic(err)
	}

	if resp.StatusCode != http.StatusOK {
		ce := httpx.NewClientError(resp)
		fmt.Printf("Error:      %s\n", ce)
		fmt.Printf("StatusCode: %d\n", ce.StatusCode())
	}
}

func ExampleServerError() {
	handler := func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "" {
			err := httpx.NewServerError(errors.New("name is required"), http.StatusBadRequest)
			httpx.Error(w, err)
			return
		}
		_, _ = fmt.Fprintf(w, "Hello, %s!\n", name)
	}

	req := httptest.NewRequest(http.MethodGet, "/greet", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	fmt.Printf("Status: %d\n", rec.Code)
	fmt.Printf("Body:   %s\n", rec.Body)
}

func ExampleMiddleware() {
	m := &loggingMiddleware{}
	handler := m.Wrap(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, "Hello, World!")
	})

	req := httptest.NewRequest(http.MethodGet, "/greet", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	fmt.Println()
	fmt.Printf("Status: %d\n", rec.Code)
	fmt.Printf("Body:   %s\n", rec.Body)
}

// loggingMiddleware is a Middleware implementation that logs every incoming request before calling the next handler.
type loggingMiddleware struct{}

func (loggingMiddleware) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("[logger middleware]: %s %s\n", r.Method, r.URL.Path)
		next(w, r)
	}
}
