package telemetry

import (
	"crypto/tls"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"google.golang.org/grpc/credentials"
)

func TestOptionsFromEnv(t *testing.T) {
	tests := []struct {
		name            string
		envars          map[string]string
		expectedOptions options
	}{
		{
			name:   "WithDefaults",
			envars: map[string]string{},
			expectedOptions: options{
				Metadata: metadataOpts{
					Attributes: map[string]any{},
				},
				Logger: loggerOpts{},
				Meter:  meterOpts{},
				Tracer: tracerOpts{},
			},
		},
		{
			name: "WithEnvVars",
			envars: map[string]string{
				"PROBE_METADATA_NAME":                  "echo-service",
				"PROBE_METADATA_VERSION":               "v0.1.0",
				"PROBE_METADATA_ATTRIBUTE_ENVIRONMENT": "test",
				"PROBE_METADATA_ATTRIBUTE_REGION":      "local",
				"PROBE_METADATA_ATTRIBUTE_ACTIVE":      "true",
				"PROBE_METADATA_ATTRIBUTE_PORT":        "8080",
				"PROBE_METADATA_ATTRIBUTE_WEIGHT":      "0.2",
				"PROBE_LOGGER_STDOUT_LEVEL":            "debug",
				"PROBE_LOGGER_STDOUT_ENABLED":          "true",
				"PROBE_LOGGER_FILE_ENABLED":            "true",
				"PROBE_LOGGER_FILE_LEVEL":              "debug",
				"PROBE_LOGGER_FILE_FILEPATH":           "/var/log/echo-service.log",
				"PROBE_LOGGER_FILE_MAXSIZE":            "100",
				"PROBE_LOGGER_FILE_MAXBACKUPS":         "5",
				"PROBE_LOGGER_FILE_MAXAGE":             "30",
				"PROBE_LOGGER_LOKI_ENABLED":            "true",
				"PROBE_LOGGER_LOKI_TENANT":             "internal",
				"PROBE_LOGGER_LOKI_LEVEL":              "debug",
				"PROBE_LOGGER_LOKI_LABELS":             "environment,region",
				"PROBE_LOGGER_LOKI_ENDPOINT":           "https://loki.example.com:5100/loki/api/v1/push",
				"PROBE_LOGGER_LOKI_TLS_ENABLED":        "true",
				"PROBE_LOGGER_FORWARD_ENABLED":         "true",
				"PROBE_LOGGER_FORWARD_LEVEL":           "debug",
				"PROBE_LOGGER_FORWARD_TAG":             "echo-service.local",
				"PROBE_LOGGER_FORWARD_ENDPOINT":        "fluent-bit.example.com:24224/api/v1/write",
				"PROBE_LOGGER_FORWARD_TLS_ENABLED":     "true",
				"PROBE_LOGGER_OTEL_ENABLED":            "true",
				"PROBE_LOGGER_OTEL_LEVEL":              "debug",
				"PROBE_LOGGER_OTEL_HTTP_ENDPOINT":      "collector.service:4318",
				"PROBE_LOGGER_OTEL_GRPC_ENDPOINT":      "collector.service:4317",
				"PROBE_METER_OTEL_ENABLED":             "true",
				"PROBE_METER_OTEL_HTTP_ENDPOINT":       "collector.service:4318",
				"PROBE_METER_OTEL_GRPC_ENDPOINT":       "collector.service:4317",
				"PROBE_TRACER_OTEL_ENABLED":            "true",
				"PROBE_TRACER_OTEL_HTTP_ENDPOINT":      "collector.service:4318",
				"PROBE_TRACER_OTEL_GRPC_ENDPOINT":      "collector.service:4317",
			},
			expectedOptions: options{
				Metadata: metadataOpts{
					Name:    "echo-service",
					Version: "v0.1.0",
					Attributes: map[string]any{
						"environment": "test",
						"region":      "local",
						"active":      true,
						"port":        int64(8080),
						"weight":      float64(0.2),
					},
				},
				Logger: loggerOpts{
					Stdout: &stdoutLoggerOpts{
						Level: "debug",
					},
					File: &fileLoggerOpts{
						Level:      "debug",
						Filepath:   "/var/log/echo-service.log",
						MaxSize:    100,
						MaxBackups: 5,
						MaxAge:     30,
					},
					Loki: &lokiLoggerOpts{
						Tenant:   "internal",
						Level:    "debug",
						Labels:   []string{"environment", "region"},
						Endpoint: "https://loki.example.com:5100/loki/api/v1/push",
						TLS: tlsOpts{
							Enabled: true,
						},
					},
					Forward: &forwardLoggerOpts{
						Level:    "debug",
						Tag:      "echo-service.local",
						Endpoint: "fluent-bit.example.com:24224/api/v1/write",
						TLS: tlsOpts{
							Enabled: true,
						},
					},
					OpenTelemetry: &opentelemetryLoggerOpts{
						Level: "debug",
						OTLPHTTP: &otlpHTTPOpts{
							Endpoint: "collector.service:4318",
						},
						OTLPGRPC: &otlpGRPCOpts{
							Endpoint: "collector.service:4317",
						},
					},
				},
				Meter: meterOpts{
					OpenTelemetry: &opentelemetryMeterOpts{
						OTLPHTTP: &otlpHTTPOpts{
							Endpoint: "collector.service:4318",
						},
						OTLPGRPC: &otlpGRPCOpts{
							Endpoint: "collector.service:4317",
						},
					},
				},
				Tracer: tracerOpts{
					OpenTelemetry: &opentelemetryTracerOpts{
						OTLPHTTP: &otlpHTTPOpts{
							Endpoint: "collector.service:4318",
						},
						OTLPGRPC: &otlpGRPCOpts{
							Endpoint: "collector.service:4317",
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Set environment variables
			for n, v := range tc.envars {
				if err := os.Setenv(n, v); err != nil {
					t.Fatalf("Failed to set environment variable %q: %s", n, err)
				}

				defer func(name string) {
					assert.NoError(t, os.Unsetenv(name))
				}(n)
			}

			opts := optionsFromEnv()
			assert.Equal(t, tc.expectedOptions, opts)
		})
	}
}

func TestOption(t *testing.T) {
	config := new(tls.Config)
	creds := credentials.NewTLS(nil)

	tests := []struct {
		name            string
		options         *options
		option          Option
		expectedOptions *options
	}{
		{
			name:    "WithMetadata",
			options: &options{},
			option: WithMetadata("echo-service", "v0.1.0", map[string]any{
				"environment": "test",
				"region":      "local",
				"active":      true,
				"port":        8080,
				"weight":      0.2,
			}),
			expectedOptions: &options{
				Metadata: metadataOpts{
					Name:    "echo-service",
					Version: "v0.1.0",
					Attributes: map[string]any{
						"environment": "test",
						"region":      "local",
						"active":      true,
						"port":        8080,
						"weight":      0.2,
					},
				},
			},
		},
		{
			name:    "WithStdoutLogger",
			options: &options{},
			option:  WithStdoutLogger("debug"),
			expectedOptions: &options{
				Logger: loggerOpts{
					Stdout: &stdoutLoggerOpts{
						Level: "debug",
					},
				},
			},
		},
		{
			name:    "WithFileLogger",
			options: &options{},
			option:  WithFileLogger("debug", "/var/log/echo-service.log", 100, 5, 30),
			expectedOptions: &options{
				Logger: loggerOpts{
					File: &fileLoggerOpts{
						Level:      "debug",
						Filepath:   "/var/log/echo-service.log",
						MaxSize:    100,
						MaxBackups: 5,
						MaxAge:     30,
					},
				},
			},
		},
		{
			name:    "WithLokiLogger",
			options: &options{},
			option: WithLokiLogger(
				"internal", "debug", []string{"environment", "region"},
				"https://loki.example.com:5100/loki/api/v1/push", true, &tls.Config{},
			),
			expectedOptions: &options{
				Logger: loggerOpts{
					Loki: &lokiLoggerOpts{
						Tenant:   "internal",
						Level:    "debug",
						Labels:   []string{"environment", "region"},
						Endpoint: "https://loki.example.com:5100/loki/api/v1/push",
						TLS: tlsOpts{
							Enabled: true,
							Config:  &tls.Config{},
						},
					},
				},
			},
		},
		{
			name:    "WithForwardLogger",
			options: &options{},
			option: WithForwardLogger(
				"debug", "echo-service.local",
				"fluent-bit.example.com:24224/api/v1/write", true, &tls.Config{},
			),
			expectedOptions: &options{
				Logger: loggerOpts{
					Forward: &forwardLoggerOpts{
						Level:    "debug",
						Tag:      "echo-service.local",
						Endpoint: "fluent-bit.example.com:24224/api/v1/write",
						TLS: tlsOpts{
							Enabled: true,
							Config:  &tls.Config{},
						},
					},
				},
			},
		},
		{
			name:    "WithOpenTelemetryLoggerHTTP",
			options: &options{},
			option:  WithOpenTelemetryLoggerHTTP("debug", "collector.service:4318", config),
			expectedOptions: &options{
				Logger: loggerOpts{
					OpenTelemetry: &opentelemetryLoggerOpts{
						Level: "debug",
						OTLPHTTP: &otlpHTTPOpts{
							Endpoint: "collector.service:4318",
							Config:   config,
						},
					},
				},
			},
		},
		{
			name:    "WithOpenTelemetryLoggerGRPC",
			options: &options{},
			option:  WithOpenTelemetryLoggerGRPC("debug", "collector.service:4317", creds),
			expectedOptions: &options{
				Logger: loggerOpts{
					OpenTelemetry: &opentelemetryLoggerOpts{
						Level: "debug",
						OTLPGRPC: &otlpGRPCOpts{
							Endpoint: "collector.service:4317",
							Creds:    creds,
						},
					},
				},
			},
		},
		{
			name:    "WithOpenTelemetryMeter",
			options: &options{},
			option:  WithOpenTelemetryMeter(),
			expectedOptions: &options{
				Meter: meterOpts{
					OpenTelemetry: &opentelemetryMeterOpts{},
				},
			},
		},
		{
			name:    "WithOpenTelemetryMeterHTTP",
			options: &options{},
			option:  WithOpenTelemetryMeterHTTP("collector.service:4318", config),
			expectedOptions: &options{
				Meter: meterOpts{
					OpenTelemetry: &opentelemetryMeterOpts{
						OTLPHTTP: &otlpHTTPOpts{
							Endpoint: "collector.service:4318",
							Config:   config,
						},
					},
				},
			},
		},
		{
			name:    "WithOpenTelemetryMeterGRPC",
			options: &options{},
			option:  WithOpenTelemetryMeterGRPC("collector.service:4317", creds),
			expectedOptions: &options{
				Meter: meterOpts{
					OpenTelemetry: &opentelemetryMeterOpts{
						OTLPGRPC: &otlpGRPCOpts{
							Endpoint: "collector.service:4317",
							Creds:    creds,
						},
					},
				},
			},
		},
		{
			name:    "WithOpenTelemetryTracerHTTP",
			options: &options{},
			option:  WithOpenTelemetryTracerHTTP("collector.service:4318", config),
			expectedOptions: &options{
				Tracer: tracerOpts{
					OpenTelemetry: &opentelemetryTracerOpts{
						OTLPHTTP: &otlpHTTPOpts{
							Endpoint: "collector.service:4318",
							Config:   config,
						},
					},
				},
			},
		},
		{
			name:    "WithOpenTelemetryTracerGRPC",
			options: &options{},
			option:  WithOpenTelemetryTracerGRPC("collector.service:4317", creds),
			expectedOptions: &options{
				Tracer: tracerOpts{
					OpenTelemetry: &opentelemetryTracerOpts{
						OTLPGRPC: &otlpGRPCOpts{
							Endpoint: "collector.service:4317",
							Creds:    creds,
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.option(tc.options)
			assert.Equal(t, tc.expectedOptions, tc.options)
		})
	}
}
