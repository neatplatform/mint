package telemetry

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"google.golang.org/grpc/credentials"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestProbe(t *testing.T) {
	tests := []struct {
		name               string
		p                  *probe
		expectedStatusCode int
		expectedCloseError string
	}{
		{
			name: "Success",
			p: &probe{
				name:   "my-service",
				logger: new(noopLogger),
				meter:  metricnoop.NewMeterProvider().Meter(""),
				tracer: tracenoop.NewTracerProvider().Tracer(""),
				closeFuncs: []closeFunc{
					func(context.Context) error { return nil },
				},
			},
			expectedStatusCode: http.StatusOK,
		},
		{
			name: "CloseError",
			p: &probe{
				name:   "my-service",
				logger: new(noopLogger),
				meter:  metricnoop.NewMeterProvider().Meter(""),
				tracer: tracenoop.NewTracerProvider().Tracer(""),
				closeFuncs: []closeFunc{
					func(context.Context) error { return errors.New("error on closing") },
				},
			},
			expectedStatusCode: http.StatusOK,
			expectedCloseError: "error on closing",
		},
		{
			name: "WithPrometheusHandler",
			p: &probe{
				name:   "my-service",
				logger: new(noopLogger),
				meter:  metricnoop.NewMeterProvider().Meter(""),
				tracer: tracenoop.NewTracerProvider().Tracer(""),
				closeFuncs: []closeFunc{
					func(context.Context) error { return nil },
				},
				promHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				}),
			},
			expectedStatusCode: http.StatusNoContent,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, version := tc.p.Info()
			assert.Equal(t, tc.p.name, name)
			assert.Equal(t, tc.p.version, version)

			assert.Same(t, tc.p.logger, tc.p.Logger())
			assert.Equal(t, tc.p.meter, tc.p.Meter())
			assert.Equal(t, tc.p.tracer, tc.p.Tracer())

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			tc.p.ServeHTTP(w, r)
			assert.Equal(t, tc.expectedStatusCode, w.Result().StatusCode)

			err := tc.p.Close(context.Background())
			if tc.expectedCloseError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedCloseError)
			}
		})
	}
}

func TestNewNoopProbe(t *testing.T) {
	p := NewNoopProbe()
	assert.NotNil(t, p)

	name, version := p.Info()
	assert.Empty(t, name)
	assert.Empty(t, version)

	assert.NotNil(t, p.Logger())
	assert.NotNil(t, p.Meter())
	assert.NotNil(t, p.Tracer())
}

func TestNewProbe(t *testing.T) {
	srv, err := newTestTCPServer()
	assert.NoError(t, err)

	defer func() {
		_ = srv.Close()
	}()

	tlsSrv, err := newTestTLSTCPServer()
	assert.NoError(t, err)

	defer func() {
		_ = tlsSrv.Close()
	}()

	f, err := os.CreateTemp("", "echo-service-*.log")
	assert.NoError(t, err)

	defer func() {
		_ = os.Remove("app.log") // Default log file
		_ = os.Remove(f.Name())  // Custom log file
	}()

	tests := []struct {
		name      string
		ctx       context.Context
		opts      []Option
		skipClose bool
	}{
		{
			name: "WithDefaults",
			ctx:  context.Background(),
			opts: []Option{},
		},
		{
			name: "WithName",
			ctx:  context.Background(),
			opts: []Option{
				WithMetadata("echo-service", "", nil),
			},
		},
		{
			name: "WithNameAndVersion",
			ctx:  context.Background(),
			opts: []Option{
				WithMetadata("echo-service", "v0.1.0", nil),
			},
		},
		{
			name: "WithMetadata",
			ctx:  context.Background(),
			opts: []Option{
				WithMetadata("echo-service", "v0.1.0", map[string]any{
					"environment": "test",
					"region":      "local",
				}),
			},
		},
		{
			name: "WithStdoutLogger_Default",
			ctx:  context.Background(),
			opts: []Option{
				WithStdoutLogger(""),
			},
		},
		{
			name: "WithStdoutLogger_Custom",
			ctx:  context.Background(),
			opts: []Option{
				WithStdoutLogger("debug"),
			},
		},
		{
			name: "WithFileLogger_Default",
			ctx:  context.Background(),
			opts: []Option{
				WithFileLogger("", "", 0, 0, 0),
			},
		},
		{
			name: "WithFileLogger_Custom",
			ctx:  context.Background(),
			opts: []Option{
				WithFileLogger("debug", f.Name(), 100, 5, 30),
			},
		},
		{
			name: "WithLokiLogger_Default",
			ctx:  context.Background(),
			opts: []Option{
				WithLokiLogger("", "", nil, "", false, nil),
			},
		},
		{
			name: "WithLokiLogger_Custom",
			ctx:  context.Background(),
			opts: []Option{
				WithLokiLogger(
					"internal", "debug", []string{"environment", "region"},
					"https://loki.example.com:5100/loki/api/v1/push", true, &tls.Config{},
				),
			},
		},
		{
			name: "WithForwardLogger_Default",
			ctx:  context.Background(),
			opts: []Option{
				WithForwardLogger("", "", srv.Addr, false, nil),
			},
		},
		{
			name: "WithForwardLogger_Custom",
			ctx:  context.Background(),
			opts: []Option{
				WithForwardLogger(
					"debug", "echo-service.local",
					tlsSrv.Addr, true, &tls.Config{
						InsecureSkipVerify: true,
					},
				),
			},
		},
		{
			name: "WithOpenTelemetryLoggerHTTP_Default",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryLoggerHTTP("", "", nil),
			},
		},
		{
			name: "WithOpenTelemetryLoggerHTTP_Custom",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryLoggerHTTP("debug", "collector.service:4318", &tls.Config{}),
			},
		},
		{
			name: "WithOpenTelemetryLoggerGRPC_Default",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryLoggerGRPC("", "", nil),
			},
		},
		{
			name: "WithOpenTelemetryLoggerGRPC_Custom",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryLoggerGRPC("debug", "collector.service:4317", credentials.NewTLS(nil)),
			},
		},
		{
			name: "WithOpenTelemetryMeter",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryMeter(),
			},
		},
		{
			name: "WithOpenTelemetryMeterHTTP_Default",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryMeterHTTP("", nil),
			},
			skipClose: true, // Closing tries to flush/export metrics remotely.
		},
		{
			name: "WithOpenTelemetryMeterHTTP_Custom",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryMeterHTTP("collector.service:4318", &tls.Config{}),
			},
			skipClose: true, // Closing tries to flush/export metrics remotely.
		},
		{
			name: "WithOpenTelemetryMeterGRPC_Default",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryMeterGRPC("", nil),
			},
			skipClose: true, // Closing tries to flush/export metrics remotely.
		},
		{
			name: "WithOpenTelemetryMeterGRPC_Custom",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryMeterGRPC("collector.service:4317", credentials.NewTLS(nil)),
			},
			skipClose: true, // Closing tries to flush/export metrics remotely.
		},
		{
			name: "WithOpenTelemetryTracerHTTP_Default",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryTracerHTTP("", nil),
			},
		},
		{
			name: "WithOpenTelemetryTracerHTTP_Custom",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryTracerHTTP("collector.service:4318", &tls.Config{}),
			},
		},
		{
			name: "WithOpenTelemetryTracerGRPC_Default",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryTracerGRPC("", nil),
			},
		},
		{
			name: "WithOpenTelemetryTracerGRPC_Custom",
			ctx:  context.Background(),
			opts: []Option{
				WithOpenTelemetryTracerGRPC("collector.service:4317", credentials.NewTLS(nil)),
			},
		},
		{
			name: "WithMultipleLoggers",
			ctx:  context.Background(),
			opts: []Option{
				WithStdoutLogger("debug"),
				WithFileLogger("debug", f.Name(), 100, 5, 30),
				WithLokiLogger(
					"internal", "debug", []string{"environment", "region"},
					"https://loki.example.com:5100/loki/api/v1/push", true, &tls.Config{},
				),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProbe(tc.opts...)

			assert.NotNil(t, p)
			assert.NotNil(t, p.Logger())
			assert.NotNil(t, p.Meter())
			assert.NotNil(t, p.Tracer())

			if !tc.skipClose {
				assert.NoError(t, p.Close(tc.ctx))
			}
		})
	}
}
