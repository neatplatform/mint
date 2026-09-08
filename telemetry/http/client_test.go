package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"

	"go.opentelemetry.io/otel/metric/noop"

	"github.com/neatplatform/mint/telemetry"
)

func TestNewClientMetrics(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("")
	metrics := newClientMetrics(meter)

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.total)
	assert.NotNil(t, metrics.inflight)
	assert.NotNil(t, metrics.duration)
	assert.NotNil(t, metrics.reqSize)
	assert.NotNil(t, metrics.respSize)
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		name         string
		client       *http.Client
		probe        telemetry.Probe
		opts         Options
		expectedOpts Options
	}{
		{
			name:         "WithDefaultOptions",
			client:       &http.Client{},
			probe:        telemetry.NewNoopProbe(),
			opts:         Options{},
			expectedOpts: Options{}.withDefaults(),
		},
		{
			name:   "WithCustomOptions",
			client: &http.Client{},
			probe:  telemetry.NewNoopProbe(),
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
			c := NewClient(tc.client, tc.probe, tc.opts)

			assert.NotNil(t, c)
			assert.NotNil(t, c.metrics)
			assert.Same(t, tc.client, c.client)
			assert.Same(t, tc.probe, c.probe)
			assert.Equal(t, tc.expectedOpts, c.opts)
		})
	}
}

func TestClient_Do(t *testing.T) {
	tests := []struct {
		name                string
		probe               telemetry.Probe
		opts                Options
		ctx                 context.Context
		method              string
		path                string
		body                string
		respStatusCode      int
		respBody            string
		expectObservation   bool
		expectedCallerName  string
		expectedRequestUUID string
	}{
		{
			name: "Success",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:               Options{},
			ctx:                context.Background(),
			method:             http.MethodGet,
			path:               "/v1/items/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			respStatusCode:     http.StatusOK,
			respBody:           `{"uuid":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}`,
			expectObservation:  true,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "NotFound",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:               Options{},
			ctx:                context.Background(),
			method:             http.MethodGet,
			path:               "/v1/items/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
			respStatusCode:     http.StatusNotFound,
			respBody:           `NotFound`,
			expectObservation:  true,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "InternalServerError",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:               Options{},
			ctx:                context.Background(),
			method:             http.MethodGet,
			path:               "/v1/items/cccccccc-cccc-cccc-cccc-cccccccccccc",
			respStatusCode:     http.StatusInternalServerError,
			respBody:           `InternalServerError`,
			expectObservation:  true,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "WithRequestUUID",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:                Options{},
			ctx:                 telemetry.ContextWithUUID(context.Background(), "dddddddd-dddd-dddd-dddd-dddddddddddd"),
			method:              http.MethodGet,
			path:                "/v1/items",
			respStatusCode:      http.StatusOK,
			respBody:            `[]`,
			expectObservation:   true,
			expectedCallerName:  "poke-service/v0.1.0",
			expectedRequestUUID: "dddddddd-dddd-dddd-dddd-dddddddddddd",
		},
		{
			name: "WithRequestBody",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:               Options{},
			ctx:                context.Background(),
			method:             http.MethodPost,
			path:               "/v1/items",
			body:               `{"name":"clover sparkle"}`,
			respStatusCode:     http.StatusCreated,
			respBody:           `OK`,
			expectObservation:  true,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "WithoutVersion",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "", nil),
			),
			opts:               Options{},
			ctx:                context.Background(),
			method:             http.MethodGet,
			path:               "/v1/items",
			respStatusCode:     http.StatusOK,
			respBody:           `[]`,
			expectObservation:  true,
			expectedCallerName: "poke-service",
		},
		{
			name: "WithoutNameAndVersion",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("", "", nil),
			),
			opts:               Options{},
			ctx:                context.Background(),
			method:             http.MethodGet,
			path:               "/v1/items",
			respStatusCode:     http.StatusOK,
			respBody:           `[]`,
			expectObservation:  true,
			expectedCallerName: "",
		},
		{
			name: "WithExcludeRoutes",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts: Options{
				ExcludeRoutes: []string{
					"/health",
					"/ready",
				},
			},
			ctx:               context.Background(),
			method:            http.MethodGet,
			path:              "/health",
			respStatusCode:    http.StatusOK,
			respBody:          `OK`,
			expectObservation: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Capture values seen inside the handler for assertions.
			var gotMethod, gotPath, uuidFromReq, callerFromReq string
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path

				if tc.expectObservation {
					uuidFromReq = r.Header.Get(requestUUIDHeader)
					callerFromReq = r.Header.Get(callerNameHeader)
				}

				time.Sleep(time.Millisecond)
				w.WriteHeader(tc.respStatusCode)
				_, _ = w.Write([]byte(tc.respBody))
			})

			ts := httptest.NewServer(handler)
			defer ts.Close()

			c := NewClient(ts.Client(), tc.probe, tc.opts)

			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}

			req, err := http.NewRequestWithContext(tc.ctx, tc.method, ts.URL+tc.path, body)
			assert.NoError(t, err)

			resp, err := c.Do(req)
			assert.NoError(t, err)

			assert.Equal(t, tc.method, gotMethod)
			assert.Equal(t, tc.path, gotPath)
			assert.Equal(t, tc.respStatusCode, resp.StatusCode)

			if tc.expectObservation {
				assert.Equal(t, tc.expectedCallerName, callerFromReq)

				if tc.expectedRequestUUID == "" {
					_, err := uuid.Parse(uuidFromReq)
					assert.NoError(t, err)
				} else {
					assert.Equal(t, tc.expectedRequestUUID, uuidFromReq)
				}
			}

			assert.NoError(t, tc.probe.Close(tc.ctx))
		})
	}

	t.Run("RoundTripError", func(t *testing.T) {
		ts := httptest.NewServer(nil)
		url, client := ts.URL, ts.Client()
		ts.Close()

		c := NewClient(client, telemetry.NewNoopProbe(), Options{})

		req, err := http.NewRequest(http.MethodGet, url, nil)
		assert.NoError(t, err)

		resp, err := c.Do(req)

		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "connect: connection refused")
	})
}

func TestClient_Other(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`OK`))
		}))
		defer ts.Close()

		c := NewClient(ts.Client(), telemetry.NewNoopProbe(), Options{})

		t.Run("CloseIdleConnections", func(t *testing.T) {
			c.CloseIdleConnections()
		})

		t.Run("Get", func(t *testing.T) {
			resp, err := c.Get(ts.URL + "/v1/items")

			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("Head", func(t *testing.T) {
			resp, err := c.Head(ts.URL + "/v1/items")

			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("Post", func(t *testing.T) {
			resp, err := c.Post(ts.URL+"/v1/items",
				"application/json; charset=utf-8",
				strings.NewReader(`{"name":"clover sparkle"}`),
			)

			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("PostForm", func(t *testing.T) {
			resp, err := c.PostForm(ts.URL+"/v1/items", url.Values{
				"name": {"clover sparkle"},
			})

			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	})

	t.Run("RoundTripError", func(t *testing.T) {
		ts := httptest.NewServer(nil)
		serverURL, client := ts.URL, ts.Client()
		ts.Close()

		c := NewClient(client, telemetry.NewNoopProbe(), Options{})

		t.Run("CloseIdleConnections", func(t *testing.T) {
			c.CloseIdleConnections()
		})

		t.Run("Get", func(t *testing.T) {
			resp, err := c.Get(serverURL + "/v1/items")

			assert.Nil(t, resp)
			assert.Contains(t, err.Error(), "connect: connection refused")
		})

		t.Run("Head", func(t *testing.T) {
			resp, err := c.Head(serverURL + "/v1/items")

			assert.Nil(t, resp)
			assert.Contains(t, err.Error(), "connect: connection refused")
		})

		t.Run("Post", func(t *testing.T) {
			resp, err := c.Post(serverURL+"/v1/items",
				"application/json; charset=utf-8",
				strings.NewReader(`{"name":"clover sparkle"}`),
			)

			assert.Nil(t, resp)
			assert.Contains(t, err.Error(), "connect: connection refused")
		})

		t.Run("PostForm", func(t *testing.T) {
			resp, err := c.PostForm(serverURL+"/v1/items", url.Values{
				"name": {"clover sparkle"},
			})

			assert.Nil(t, resp)
			assert.Contains(t, err.Error(), "connect: connection refused")
		})
	})
}
