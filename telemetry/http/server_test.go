package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/neatplatform/mint/telemetry"
)

func TestNewServerMetrics(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("")
	metrics := newServerMetrics(meter)

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.panics)
	assert.NotNil(t, metrics.total)
	assert.NotNil(t, metrics.inflight)
	assert.NotNil(t, metrics.duration)
	assert.NotNil(t, metrics.reqSize)
	assert.NotNil(t, metrics.respSize)
}

func TestNewMiddleware(t *testing.T) {
	tests := []struct {
		name         string
		probe        telemetry.Probe
		opts         Options
		expectedOpts Options
	}{
		{
			name:         "WithDefaultOptions",
			probe:        telemetry.NewNoopProbe(),
			opts:         Options{},
			expectedOpts: Options{}.withDefaults(),
		},
		{
			name:  "WithCustomOptions",
			probe: telemetry.NewNoopProbe(),
			opts: Options{
				UUIDRegexp: regexp.MustCompile("[0-9A-Fa-f]{24}"),
				ExcludeRoutes: []string{
					"/health",
					"/ready",
				},
			},
			expectedOpts: Options{
				UUIDRegexp: regexp.MustCompile("[0-9A-Fa-f]{24}"),
				ExcludeRoutes: []string{
					"/health",
					"/ready",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMiddleware(tc.probe, tc.opts)
			assert.NotNil(t, m)

			mm, ok := m.(*middleware)
			assert.True(t, ok)

			assert.NotNil(t, mm.metrics)
			assert.Same(t, tc.probe, mm.probe)
			assert.Equal(t, tc.expectedOpts, mm.opts)
		})
	}
}

func TestMiddleware_Wrap(t *testing.T) {
	tests := []struct {
		name               string
		probe              telemetry.Probe
		opts               Options
		method             string
		path               string
		headers            map[string]string
		body               string
		handler            http.HandlerFunc
		expectObservation  bool
		expectedStatusCode int
	}{
		{
			name:    "Success",
			probe:   telemetry.NewNoopProbe(),
			opts:    Options{},
			method:  http.MethodGet,
			path:    "/v1/items/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			headers: map[string]string{},
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(time.Millisecond)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"uuid":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}`))
			},
			expectObservation:  true,
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "NotFound",
			probe:   telemetry.NewNoopProbe(),
			opts:    Options{},
			method:  http.MethodGet,
			path:    "/v1/items/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
			headers: map[string]string{},
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(time.Millisecond)
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`NotFound`))
			},
			expectObservation:  true,
			expectedStatusCode: http.StatusNotFound,
		},
		{
			name:    "InternalServerError",
			probe:   telemetry.NewNoopProbe(),
			opts:    Options{},
			method:  http.MethodGet,
			path:    "/v1/items/cccccccc-cccc-cccc-cccc-cccccccccccc",
			headers: map[string]string{},
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(time.Millisecond)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`InternalServerError`))
			},
			expectObservation:  true,
			expectedStatusCode: http.StatusInternalServerError,
		},
		{
			name:    "WithoutWriteHeader",
			probe:   telemetry.NewNoopProbe(),
			opts:    Options{},
			method:  http.MethodGet,
			path:    "/v1/items/dddddddd-dddd-dddd-dddd-dddddddddddd",
			headers: map[string]string{},
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(time.Millisecond)
				_, _ = w.Write([]byte(`{"uuid":"dddddddd-dddd-dddd-dddd-dddddddddddd"}`))
			},
			expectObservation:  true,
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "WithRequestBody",
			probe:   telemetry.NewNoopProbe(),
			opts:    Options{},
			method:  http.MethodPost,
			path:    "/v1/items",
			headers: map[string]string{},
			body:    `{"name":"clover sparkle"}`,
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(time.Millisecond)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`OK`))
			},
			expectObservation:  true,
			expectedStatusCode: http.StatusCreated,
		},
		{
			name:   "WithRequestUUIDHeader",
			probe:  telemetry.NewNoopProbe(),
			opts:   Options{},
			method: http.MethodGet,
			path:   "/v1/items",
			headers: map[string]string{
				"Request-UUID": "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee",
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(time.Millisecond)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
			},
			expectObservation:  true,
			expectedStatusCode: http.StatusOK,
		},
		{
			name:   "WithCallerNameHeader",
			probe:  telemetry.NewNoopProbe(),
			opts:   Options{},
			method: http.MethodGet,
			path:   "/v1/items",
			headers: map[string]string{
				"Caller-Name": "poke-service",
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(time.Millisecond)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
			},
			expectObservation:  true,
			expectedStatusCode: http.StatusOK,
		},
		{
			name:    "HandlerPanics",
			probe:   telemetry.NewNoopProbe(),
			opts:    Options{},
			method:  http.MethodDelete,
			path:    "/v1/items/ffffffff-ffff-ffff-ffff-ffffffffffff",
			headers: map[string]string{},
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(time.Millisecond)
				panic("something went wrong!")
			},
			expectObservation:  true,
			expectedStatusCode: http.StatusInternalServerError,
		},
		{
			name:  "WithExcludeRoutes",
			probe: telemetry.NewNoopProbe(),
			opts: Options{
				ExcludeRoutes: []string{
					"/health",
					"/ready",
				},
			},
			method:  http.MethodGet,
			path:    "/health",
			headers: map[string]string{},
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(time.Millisecond)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`OK`))
			},
			expectObservation:  false,
			expectedStatusCode: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMiddleware(tc.probe, tc.opts)
			assert.NotNil(t, m)

			// Capture values seen inside the handler for assertions.
			var gotMethod, gotPath, uuidFromCtx, uuidFromReq, callerFromReq string
			handler := m.Wrap(func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				gotMethod = r.Method
				gotPath = r.URL.Path

				if tc.expectObservation {
					uuidFromCtx, _ = telemetry.UUIDFromContext(ctx)
					uuidFromReq = r.Header.Get(requestUUIDHeader)
					callerFromReq = r.Header.Get(callerNameHeader)
				}

				assert.NotNil(t, telemetry.LoggerFromContext(ctx))
				assert.NotNil(t, telemetry.MeterFromContext(ctx))
				assert.NotNil(t, telemetry.TracerFromContext(ctx))

				tc.handler(w, r)
			})

			ts := httptest.NewServer(handler)
			defer ts.Close()

			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}

			req, err := http.NewRequest(tc.method, ts.URL+tc.path, body)
			assert.NoError(t, err)

			for header, value := range tc.headers {
				req.Header.Add(header, value)
			}

			resp, err := ts.Client().Do(req)
			assert.NoError(t, err)

			defer func() {
				_ = resp.Body.Close()
			}()

			assert.Equal(t, tc.method, gotMethod)
			assert.Equal(t, tc.path, gotPath)
			assert.Equal(t, tc.expectedStatusCode, resp.StatusCode)

			if tc.expectObservation {
				// Verify a valid request UUID is generated/preserved and propagated.
				uuidFromResp := resp.Header.Get(requestUUIDHeader)
				assert.NoError(t, uuid.Validate(uuidFromResp))
				assert.Equal(t, uuidFromResp, uuidFromCtx)
				assert.Equal(t, uuidFromResp, uuidFromReq)

				if origUUID, ok := tc.headers[requestUUIDHeader]; ok {
					assert.Equal(t, origUUID, uuidFromResp)
				}

				// Verify the caller name is propagated if present on the request.
				callerFromResp := resp.Header.Get(callerNameHeader)
				assert.Equal(t, callerFromResp, callerFromReq)

				if origCaller, ok := tc.headers[callerNameHeader]; ok {
					assert.Equal(t, origCaller, callerFromResp)
				} else {
					assert.Equal(t, defaultCallerName, callerFromResp)
				}
			}

			assert.NoError(t, tc.probe.Close(context.Background()))
		})
	}
}

func TestNewResponseWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)

	assert.NotNil(t, rw)
	assert.Same(t, rec, rw.ResponseWriter)
	assert.Zero(t, rw.StatusCode)
	assert.Zero(t, rw.BytesWritten)
}

func TestResponseWriter_WriteHeader(t *testing.T) {
	tests := []struct {
		name               string
		headers            map[string]string
		statusCodes        []int
		expectedStatusCode int
	}{
		{
			name:               "OK",
			headers:            map[string]string{"Content-Type": "text/plain"},
			statusCodes:        []int{http.StatusOK},
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "NotFound",
			headers:            map[string]string{"Content-Type": "text/plain"},
			statusCodes:        []int{http.StatusNotFound},
			expectedStatusCode: http.StatusNotFound,
		},
		{
			name:               "InternalServerError",
			headers:            map[string]string{"Content-Type": "text/plain"},
			statusCodes:        []int{http.StatusInternalServerError},
			expectedStatusCode: http.StatusInternalServerError,
		},
		{
			name:               "OnlyFirstCallCaptured",
			headers:            map[string]string{"Content-Type": "text/plain"},
			statusCodes:        []int{http.StatusCreated, http.StatusInternalServerError},
			expectedStatusCode: http.StatusCreated,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			rw := newResponseWriter(rec)

			for k, v := range tc.headers {
				rw.Header().Set(k, v)
			}

			for _, statusCode := range tc.statusCodes {
				rw.WriteHeader(statusCode)
			}

			assert.Equal(t, tc.statusCodes[0], rec.Code)
			assert.Equal(t, tc.expectedStatusCode, rw.StatusCode)

			for k, v := range tc.headers {
				assert.Equal(t, v, rw.Header().Get(k))
			}
		})
	}
}

func TestResponseWriter_Write(t *testing.T) {
	tests := []struct {
		name               string
		headers            map[string]string
		statusCode         int
		body               []byte
		expectedStatusCode int
	}{
		{
			name:               "WithoutWriteHeader",
			headers:            map[string]string{"Content-Type": "text/plain"},
			statusCode:         0,
			body:               []byte("Hello, world!"),
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "WithWriteHeader",
			headers:            map[string]string{"Content-Type": "text/plain"},
			statusCode:         http.StatusCreated,
			body:               []byte("Ciao mondo!"),
			expectedStatusCode: http.StatusCreated,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			rw := newResponseWriter(rec)

			for k, v := range tc.headers {
				rw.Header().Set(k, v)
			}

			if tc.statusCode != 0 {
				rw.WriteHeader(tc.statusCode)
			}

			n, err := rw.Write(tc.body)

			assert.NoError(t, err)
			assert.Equal(t, tc.expectedStatusCode, rw.StatusCode)
			assert.Equal(t, tc.body, rec.Body.Bytes())
			assert.Equal(t, len(tc.body), n)
			assert.Equal(t, int64(len(tc.body)), rw.BytesWritten)

			for k, v := range tc.headers {
				assert.Equal(t, v, rw.Header().Get(k))
			}
		})
	}
}
