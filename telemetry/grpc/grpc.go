package grpc

import (
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

const (
	requestUUIDKey    = "x-request-uuid"
	callerNameKey     = "x-caller-name"
	defaultCallerName = ""

	originLogKey        = "log_origin"
	originLogVal        = "telemetry"
	maxMetadataValueLen = 64

	serviceKey            attribute.Key = "service"
	gRPCStreamKey         attribute.Key = "grpc.stream"
	gRPCRequestPackageKey attribute.Key = "grpc.request.package"
	gRPCRequestServiceKey attribute.Key = "grpc.request.service"
	gRPCRequestMethodKey  attribute.Key = "grpc.request.method"
	gRPCCodeKey           attribute.Key = "grpc.code"
)

var (
	durationBuckets     = []float64{1, 5, 10, 25, 50, 75, 100, 250, 500, 1000}
	messageCountBuckets = []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 10000}
	messageSizeBuckets  = []float64{
		0,
		512,        // 512 B
		1024,       // 1 KB
		5120,       // 5 KB
		10240,      // 10 KB
		51200,      // 50 KB
		102400,     // 100 KB
		512000,     // 500 KB
		1048576,    // 1 MB
		5242880,    // 5 MB
		10485760,   // 10 MB
		52428800,   // 50 MB
		104857600,  // 100 MB
		524288000,  // 500 MB
		1073741824, // 1 GB
	}
)

// LogMode controls whether and how a key-value pair recorded in logs.
type LogMode int

const (
	Drop     LogMode = iota // Drop excludes the pair from logs entirely.
	Truncate                // Truncate logs the value, capped to a maximum length.
	Redact                  // Redact logs that the key was present without exposing its value.
)

// MetadataConfig maps metadata names to how their values should be logged.
type MetadataConfig map[string]LogMode

func (m MetadataConfig) canonical() MetadataConfig {
	if m == nil {
		return nil
	}

	canonical := make(MetadataConfig, len(m))
	for name, mode := range m {
		canonical[strings.ToLower(name)] = mode
	}

	return canonical
}

// Options configures the instrumented gRPC interceptors.
// The zero value is valid and uses default settings.
type Options struct {
	ExcludeMethods []string
	LogMetadata    MetadataConfig
}

func (o Options) withDefaults() Options {
	// By default, no method is silently excluded.

	o.LogMetadata = o.LogMetadata.canonical()

	return o
}

// metadataCarrier adapts metadata.MD to OpenTelemetry's propagation.TextMapCarrier,
// so trace context can be injected into outgoing request metadata and extracted from incoming request metadata.
// It additionally implements the propagation.ValuesGetter interface.
type metadataCarrier metadata.MD

func (c metadataCarrier) Get(key string) string {
	if vs := metadata.MD(c).Get(key); len(vs) > 0 {
		return vs[0]
	}

	return ""
}

func (c metadataCarrier) Set(key, value string) {
	metadata.MD(c).Set(key, value)
}

func (c metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}

	return keys
}

func (c metadataCarrier) Values(key string) []string {
	return metadata.MD(c).Get(key)
}

// parseFullMethod splits a gRPC fullMethod string of the form
// "/package.service/method" into its package, service, and method components.
func parseFullMethod(fullMethod string) (pack string, service string, method string, ok bool) {
	fullMethod = strings.TrimPrefix(fullMethod, "/")
	slash := strings.LastIndex(fullMethod, "/")
	if slash < 0 {
		return
	}

	packageService := fullMethod[:slash]
	dot := strings.LastIndex(packageService, ".")
	if dot < 0 {
		return
	}

	pack = packageService[:dot]
	service = packageService[dot+1:]
	method = fullMethod[slash+1:]
	ok = true

	return
}

// getMessageSize returns the marshaled size of m in bytes, or 0 if m is not a protobuf message.
func getMessageSize(m any) int64 {
	if pm, ok := m.(proto.Message); ok {
		return int64(proto.Size(pm))
	}

	return 0
}

// getLoggableMetadataKV builds a slice of alternating log keys and values from md,
// keeping only metadata present in allowlist and truncating or redacting each value.
// Metadata not in allowlist are omitted.
func getLoggableMetadataKV(keyPrefix string, md metadata.MD, allowlist MetadataConfig) []any {
	kv := make([]any, 0, 2*len(allowlist))

	for k := range md {
		k = strings.ToLower(k)

		switch allowlist[k] {
		case Truncate:
			kv = append(kv,
				keyPrefix+k,
				truncate(metadataCarrier(md).Get(k), maxMetadataValueLen),
			)
		case Redact:
			kv = append(kv,
				keyPrefix+k,
				fmt.Sprintf("<redacted len=%d>", len(metadataCarrier(md).Get(k))),
			)
		}
	}

	return kv
}

// truncate caps s to at most maxRunes runes, cutting on a rune boundary so
// multi-byte UTF-8 characters are never split. maxRunes <= 0 yields "".
func truncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	count := 0
	for i := range s {
		if count++; count > maxRunes {
			return s[:i] + "..."
		}
	}

	return s
}
