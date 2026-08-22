package telemetry

import (
	"crypto/tls"
	"os"
	"strconv"
	"strings"

	"google.golang.org/grpc/credentials"
)

// Option represents a function that can configure [options] for a [Probe].
type Option func(*options)

type (
	// options defines all optional configuration for a [Probe].
	options struct {
		Metadata metadataOpts
		Logger   loggerOpts
		Meter    meterOpts
		Tracer   tracerOpts
	}

	// metadataOpts defines name-value pairs describing a [Probe].
	// These name-value pairs will be included on all telemetry emitted by the probe.
	metadataOpts struct {
		Name       string
		Version    string
		Attributes map[string]any
	}

	// loggerOpts defines configuration for the loggers used by a [Probe].
	loggerOpts struct {
		Stdout        *stdoutLoggerOpts
		File          *fileLoggerOpts
		Loki          *lokiLoggerOpts
		Forward       *forwardLoggerOpts
		OpenTelemetry *opentelemetryLoggerOpts
	}

	// stdoutLoggerOpts defines configuration for a logger that writes to standard output.
	stdoutLoggerOpts struct {
		Level string
	}

	// fileLoggerOpts defines configuration for a logger that writes to a file.
	fileLoggerOpts struct {
		Level      string
		Filepath   string // The path to the log file.
		MaxSize    int    // The maximum size of the log file in MB before it gets rotated.
		MaxBackups int    // The maximum number of old log files to retain.
		MaxAge     int    // The maximum age of old log files in days to retain.
	}

	// lokiLoggerOpts defines configuration for a logger that sends logs to a Grafana Loki Push API endpoint.
	lokiLoggerOpts struct {
		Tenant   string
		Level    string
		Labels   []string
		Endpoint string
		TLS      tlsOpts
	}

	// forwardLoggerOpts defines configuration for a logger that sends logs to a Fluentd Forward protocol endpoint.
	forwardLoggerOpts struct {
		Level    string
		Tag      string
		Endpoint string
		TLS      tlsOpts
	}

	// tlsOpts defines configuration for a TLS connection.
	tlsOpts struct {
		Enabled bool
		Config  *tls.Config
	}

	// opentelemetryLoggerOpts defines configuration for a logger that sends logs to an OpenTelemetry Collector endpoint.
	opentelemetryLoggerOpts struct {
		Level    string
		OTLPHTTP *otlpHTTPOpts
		OTLPGRPC *otlpGRPCOpts
	}

	// meterOpts defines configuration for the meters used by a [Probe].
	meterOpts struct {
		OpenTelemetry *opentelemetryMeterOpts
	}

	// opentelemetryMeterOpts defines configuration for a meter that sends metrics to an OpenTelemetry Collector endpoint.
	//
	// If neither OTLPHTTP nor OTLPGRPC is configured,
	// metrics are exposed over an HTTP endpoint in Prometheus format instead of being pushed to the collector.
	opentelemetryMeterOpts struct {
		OTLPHTTP *otlpHTTPOpts
		OTLPGRPC *otlpGRPCOpts
	}

	// tracerOpts defines configuration for the tracers used by a [Probe].
	tracerOpts struct {
		OpenTelemetry *opentelemetryTracerOpts
	}

	// opentelemetryTracerOpts defines configuration for a tracer that sends traces to an OpenTelemetry Collector endpoint.
	opentelemetryTracerOpts struct {
		OTLPHTTP *otlpHTTPOpts
		OTLPGRPC *otlpGRPCOpts
	}

	// otlpHTTPOpts defines configuration for an OpenTelemetry Protocol HTTP endpoint.
	otlpHTTPOpts struct {
		Endpoint string
		Config   *tls.Config
	}

	// otlpGRPCOpts defines configuration for an OpenTelemetry Protocol gRPC endpoint.
	otlpGRPCOpts struct {
		Endpoint string
		Creds    credentials.TransportCredentials
	}
)

// optionsFromEnv reads configuration options from environment variables and returns an [options] struct.
// Environment variables with invalid values will be ignored and default values will be used instead.
func optionsFromEnv() options {
	o := options{}

	// Metadata options
	{
		o.Metadata.Name = os.Getenv("PROBE_METADATA_NAME")
		o.Metadata.Version = os.Getenv("PROBE_METADATA_VERSION")
		o.Metadata.Attributes = map[string]any{}

		for _, env := range os.Environ() {
			pair := strings.Split(env, "=")
			if n, v := pair[0], pair[1]; strings.HasPrefix(n, "PROBE_METADATA_ATTRIBUTE_") {
				n := strings.TrimPrefix(n, "PROBE_METADATA_ATTRIBUTE_")
				n = strings.ToLower(n)

				if b, err := strconv.ParseBool(v); err == nil {
					o.Metadata.Attributes[n] = b
				} else if i, err := strconv.ParseInt(v, 10, 64); err == nil {
					o.Metadata.Attributes[n] = i
				} else if f, err := strconv.ParseFloat(v, 64); err == nil {
					o.Metadata.Attributes[n] = f
				} else {
					o.Metadata.Attributes[n] = v
				}
			}
		}
	}

	// Logger options
	{
		// Stdout Logger
		if enabled, _ := strconv.ParseBool(os.Getenv("PROBE_LOGGER_STDOUT_ENABLED")); enabled {
			o.Logger.Stdout = &stdoutLoggerOpts{
				Level: os.Getenv("PROBE_LOGGER_STDOUT_LEVEL"),
			}
		}

		// File Logger
		if enabled, _ := strconv.ParseBool(os.Getenv("PROBE_LOGGER_FILE_ENABLED")); enabled {
			o.Logger.File = &fileLoggerOpts{
				Level:      os.Getenv("PROBE_LOGGER_FILE_LEVEL"),
				Filepath:   os.Getenv("PROBE_LOGGER_FILE_FILEPATH"),
				MaxSize:    parseEnvInt("PROBE_LOGGER_FILE_MAXSIZE"),
				MaxBackups: parseEnvInt("PROBE_LOGGER_FILE_MAXBACKUPS"),
				MaxAge:     parseEnvInt("PROBE_LOGGER_FILE_MAXAGE"),
			}
		}

		// Loki Logger
		if enabled, _ := strconv.ParseBool(os.Getenv("PROBE_LOGGER_LOKI_ENABLED")); enabled {
			o.Logger.Loki = &lokiLoggerOpts{
				Tenant:   os.Getenv("PROBE_LOGGER_LOKI_TENANT"),
				Level:    os.Getenv("PROBE_LOGGER_LOKI_LEVEL"),
				Labels:   strings.Split(os.Getenv("PROBE_LOGGER_LOKI_LABELS"), ","),
				Endpoint: os.Getenv("PROBE_LOGGER_LOKI_ENDPOINT"),
			}

			if tls, _ := strconv.ParseBool(os.Getenv("PROBE_LOGGER_LOKI_TLS_ENABLED")); tls {
				o.Logger.Loki.TLS.Enabled = true
			}
		}

		// Forward Logger
		if enabled, _ := strconv.ParseBool(os.Getenv("PROBE_LOGGER_FORWARD_ENABLED")); enabled {
			o.Logger.Forward = &forwardLoggerOpts{
				Level:    os.Getenv("PROBE_LOGGER_FORWARD_LEVEL"),
				Tag:      os.Getenv("PROBE_LOGGER_FORWARD_TAG"),
				Endpoint: os.Getenv("PROBE_LOGGER_FORWARD_ENDPOINT"),
			}

			if tls, _ := strconv.ParseBool(os.Getenv("PROBE_LOGGER_FORWARD_TLS_ENABLED")); tls {
				o.Logger.Forward.TLS.Enabled = true
			}
		}

		// OpenTelemetry Logger
		if enabled, _ := strconv.ParseBool(os.Getenv("PROBE_LOGGER_OTEL_ENABLED")); enabled {
			o.Logger.OpenTelemetry = &opentelemetryLoggerOpts{
				Level: os.Getenv("PROBE_LOGGER_OTEL_LEVEL"),
			}

			if endpoint := os.Getenv("PROBE_LOGGER_OTEL_HTTP_ENDPOINT"); endpoint != "" {
				o.Logger.OpenTelemetry.OTLPHTTP = &otlpHTTPOpts{
					Endpoint: endpoint,
				}
			}

			if endpoint := os.Getenv("PROBE_LOGGER_OTEL_GRPC_ENDPOINT"); endpoint != "" {
				o.Logger.OpenTelemetry.OTLPGRPC = &otlpGRPCOpts{
					Endpoint: endpoint,
				}
			}
		}
	}

	// Meter options
	{
		// OpenTelemetry Meter
		if enabled, _ := strconv.ParseBool(os.Getenv("PROBE_METER_OTEL_ENABLED")); enabled {
			o.Meter.OpenTelemetry = &opentelemetryMeterOpts{}

			if endpoint := os.Getenv("PROBE_METER_OTEL_HTTP_ENDPOINT"); endpoint != "" {
				o.Meter.OpenTelemetry.OTLPHTTP = &otlpHTTPOpts{
					Endpoint: endpoint,
				}
			}

			if endpoint := os.Getenv("PROBE_METER_OTEL_GRPC_ENDPOINT"); endpoint != "" {
				o.Meter.OpenTelemetry.OTLPGRPC = &otlpGRPCOpts{
					Endpoint: endpoint,
				}
			}
		}
	}

	// Tracer options
	{
		// OpenTelemetry Tracer
		if enabled, _ := strconv.ParseBool(os.Getenv("PROBE_TRACER_OTEL_ENABLED")); enabled {
			o.Tracer.OpenTelemetry = &opentelemetryTracerOpts{}

			if endpoint := os.Getenv("PROBE_TRACER_OTEL_HTTP_ENDPOINT"); endpoint != "" {
				o.Tracer.OpenTelemetry.OTLPHTTP = &otlpHTTPOpts{
					Endpoint: endpoint,
				}
			}

			if endpoint := os.Getenv("PROBE_TRACER_OTEL_GRPC_ENDPOINT"); endpoint != "" {
				o.Tracer.OpenTelemetry.OTLPGRPC = &otlpGRPCOpts{
					Endpoint: endpoint,
				}
			}
		}
	}

	return o
}

func parseEnvInt(name string) int {
	if v := os.Getenv(name); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}

	// Default
	return 0
}

// WithMetadata sets the name, version, and attributes populated on the probe.
// All arguments are optional.
//
// Do NOT include high-cardinality data in the metadata attributes, as they can lead to performance issues in some storage backends.
func WithMetadata(name, version string, attrs map[string]any) Option {
	return func(o *options) {
		o.Metadata.Name = name
		o.Metadata.Version = version
		o.Metadata.Attributes = attrs
	}
}

// WithStdoutLogger enables a logger on the [Probe] that writes logs to standard output.
//
// level specifies the logger lovel and can be one of "debug", "info", "warn", "error", and "none" (case-insensitive).
func WithStdoutLogger(level string) Option {
	return func(o *options) {
		o.Logger.Stdout = &stdoutLoggerOpts{
			Level: level,
		}
	}
}

// WithFileLogger enables a logger on the [Probe] that writes logs to a file.
//
// level specifies the logger lovel and can be one of "debug", "info", "warn", "error", and "none" (case-insensitive).
// filepath specifies the path to the log file.
// maxSize specifies the maximum size of the log file in MB before it gets rotated.
// maxBackups specifies the maximum number of old log files to retain.
// maxAge specifies the maximum age of old log files in days to retain.
func WithFileLogger(level, filepath string, maxSize, maxBackups, maxAge int) Option {
	return func(o *options) {
		o.Logger.File = &fileLoggerOpts{
			Level:      level,
			Filepath:   filepath,
			MaxSize:    maxSize,
			MaxBackups: maxBackups,
			MaxAge:     maxAge,
		}
	}
}

// WithLokiLogger enables a logger on the [Probe] that sends logs to a Grafana Loki endpoint.
//
// level specifies the logger lovel and can be one of "debug", "info", "warn", "error", and "none" (case-insensitive).
//
// labels specifies which log attribute keys are promoted to Loki labels. Loki indexes only labels, so keep them low-cardinality.
// Matching key-value pairs are sent as labels; all others are sent as structured metadata, which is stored with logs but not indexed.
func WithLokiLogger(tenant, level string, labels []string, endpoint string, tls bool, config *tls.Config) Option {
	return func(o *options) {
		o.Logger.Loki = &lokiLoggerOpts{
			Tenant:   tenant,
			Level:    level,
			Labels:   labels,
			Endpoint: endpoint,
			TLS: tlsOpts{
				Enabled: tls,
				Config:  config,
			},
		}
	}
}

// WithForwardLogger enables a logger on the [Probe] that sends logs to a Fluentd Forward protocol endpoint.
//
// level specifies the logger lovel and can be one of "debug", "info", "warn", "error", and "none" (case-insensitive).
func WithForwardLogger(level, tag, endpoint string, tls bool, config *tls.Config) Option {
	return func(o *options) {
		o.Logger.Forward = &forwardLoggerOpts{
			Level:    level,
			Tag:      tag,
			Endpoint: endpoint,
			TLS: tlsOpts{
				Enabled: tls,
				Config:  config,
			},
		}
	}
}

// WithOpenTelemetryLoggerHTTP enables the OpenTelemetry logger on the [Probe].
// It configures it to export logs using the OpenTelemetry Protocol (OTLP) over HTTP.
//
// level specifies the logger lovel and can be one of "debug", "info", "warn", "error", and "none" (case-insensitive).
//
// The endpoint argument specifies the target endpoint the exporters will connect to.
// If not provided, it defaults to "localhost:4318".
//
// The config argument specifies the TLS configuration to use for the connection.
// If not provided, the connection will be insecure (without TLS).
func WithOpenTelemetryLoggerHTTP(level, endpoint string, config *tls.Config) Option {
	return func(o *options) {
		o.Logger.OpenTelemetry = &opentelemetryLoggerOpts{
			Level: level,
			OTLPHTTP: &otlpHTTPOpts{
				Endpoint: endpoint,
				Config:   config,
			},
		}
	}
}

// WithOpenTelemetryLoggerGRPC enables the OpenTelemetry logger on the [Probe].
// It configures it to export logs using the OpenTelemetry Protocol (OTLP) over gRPC.
//
// level specifies the logger lovel and can be one of "debug", "info", "warn", "error", and "none" (case-insensitive).
//
// The endpoint argument specifies the target endpoint the exporters will connect to.
// If not provided, it defaults to "localhost:4317".
//
// The credential argument specifies the gRPC transport credentials to use for the connection.
// If not provided, the connection will be insecure (without TLS).
func WithOpenTelemetryLoggerGRPC(level, endpoint string, creds credentials.TransportCredentials) Option {
	return func(o *options) {
		o.Logger.OpenTelemetry = &opentelemetryLoggerOpts{
			Level: level,
			OTLPGRPC: &otlpGRPCOpts{
				Endpoint: endpoint,
				Creds:    creds,
			},
		}
	}
}

// WithOpenTelemetryPrometheusMeter enables the OpenTelemetry meter on the [Probe].
// It configures it to export metrics in Prometheus format via an HTTP handler instead of being sent to the collector.
func WithOpenTelemetryMeter() Option {
	return func(o *options) {
		o.Meter.OpenTelemetry = &opentelemetryMeterOpts{}
	}
}

// WithOpenTelemetryMeterHTTP enables the OpenTelemetry meter on the [Probe].
// It configures it to export metrics using the OpenTelemetry Protocol (OTLP) over HTTP.
//
// The endpoint argument specifies the target endpoint the exporters will connect to.
// If not provided, it defaults to "localhost:4318".
//
// The config argument specifies the TLS configuration to use for the connection.
// If not provided, the connection will be insecure (without TLS).
func WithOpenTelemetryMeterHTTP(endpoint string, config *tls.Config) Option {
	return func(o *options) {
		o.Meter.OpenTelemetry = &opentelemetryMeterOpts{
			OTLPHTTP: &otlpHTTPOpts{
				Endpoint: endpoint,
				Config:   config,
			},
		}
	}
}

// WithOpenTelemetryMeterGRPC enables the OpenTelemetry meter on the [Probe].
// It configures it to export metrics using the OpenTelemetry Protocol (OTLP) over gRPC.
//
// The endpoint argument specifies the target endpoint the exporters will connect to.
// If not provided, it defaults to "localhost:4317".
//
// The credential argument specifies the gRPC transport credentials to use for the connection.
// If not provided, the connection will be insecure (without TLS).
func WithOpenTelemetryMeterGRPC(endpoint string, creds credentials.TransportCredentials) Option {
	return func(o *options) {
		o.Meter.OpenTelemetry = &opentelemetryMeterOpts{
			OTLPGRPC: &otlpGRPCOpts{
				Endpoint: endpoint,
				Creds:    creds,
			},
		}
	}
}

// WithOpenTelemetryTracerHTTP enables the OpenTelemetry tracer on the [Probe].
// It configures it to export traces using the OpenTelemetry Protocol (OTLP) over HTTP.
//
// The endpoint argument specifies the target endpoint the exporters will connect to.
// If not provided, it defaults to "localhost:4318".
//
// The config argument specifies the TLS configuration to use for the connection.
// If not provided, the connection will be insecure (without TLS).
func WithOpenTelemetryTracerHTTP(endpoint string, config *tls.Config) Option {
	return func(o *options) {
		o.Tracer.OpenTelemetry = &opentelemetryTracerOpts{
			OTLPHTTP: &otlpHTTPOpts{
				Endpoint: endpoint,
				Config:   config,
			},
		}
	}
}

// WithOpenTelemetryTracerGRPC enables the OpenTelemetry tracer on the [Probe].
// It configures it to export traces using the OpenTelemetry Protocol (OTLP) over gRPC.
//
// The endpoint argument specifies the target endpoint the exporters will connect to.
// If not provided, it defaults to "localhost:4317".
//
// The credential argument specifies the gRPC transport credentials to use for the connection.
// If not provided, the connection will be insecure (without TLS).
func WithOpenTelemetryTracerGRPC(endpoint string, creds credentials.TransportCredentials) Option {
	return func(o *options) {
		o.Tracer.OpenTelemetry = &opentelemetryTracerOpts{
			OTLPGRPC: &otlpGRPCOpts{
				Endpoint: endpoint,
				Creds:    creds,
			},
		}
	}
}
