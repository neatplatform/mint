package http

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

const (
	requestUUIDHeader = "Request-UUID"
	callerNameHeader  = "Caller-Name"
	defaultCallerName = ""

	originLogKey      = "log_origin"
	originLogVal      = "telemetry"
	maxHeaderValueLen = 64

	serviceKey attribute.Key = "service"
)

var (
	defaultUUIDRegexp = regexp.MustCompile("[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}")

	durationBuckets = []float64{1, 5, 10, 25, 50, 75, 100, 250, 500, 1000}
	bodySizeBuckets = []float64{
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

// HeaderConfig maps header names to how their values should be logged.
type HeaderConfig map[string]LogMode

func (m HeaderConfig) canonical() HeaderConfig {
	if m == nil {
		return nil
	}

	canonical := make(HeaderConfig, len(m))
	for header, mode := range m {
		canonical[strings.ToLower(header)] = mode
	}

	return canonical
}

// Options configures the instrumented HTTP middleware and clients.
// The zero value is valid and uses default settings.
type Options struct {
	UUIDRegexp    *regexp.Regexp
	ExcludeRoutes []string
	LogHeaders    HeaderConfig
}

func (o Options) withDefaults() Options {
	if o.UUIDRegexp == nil {
		o.UUIDRegexp = defaultUUIDRegexp
	}

	// By default, no route is silently excluded.

	o.LogHeaders = o.LogHeaders.canonical()

	return o
}

// headerCarrier adapts http.Header to OpenTelemetry's propagation.TextMapCarrier,
// so trace context can be injected into outgoing request headers and extracted from incoming request headers.
// It additionally implements the propagation.ValuesGetter interface.
type headerCarrier http.Header

func (c headerCarrier) Get(key string) string {
	return http.Header(c).Get(key)
}

func (c headerCarrier) Set(key, value string) {
	http.Header(c).Set(key, value)
}

func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}

	return keys
}

func (c headerCarrier) Values(key string) []string {
	return http.Header(c).Values(key)
}

// getLoggableHeaderKV builds a slice of alternating log keys and values from header,
// keeping only headers present in allowlist and truncating or redacting each value.
// Headers not in allowlist are omitted.
func getLoggableHeaderKV(keyPrefix string, header http.Header, allowlist HeaderConfig) []any {
	kv := make([]any, 0, 2*len(allowlist))

	for k := range header {
		k = strings.ToLower(k)
		switch allowlist[k] {
		case Truncate:
			kv = append(kv,
				keyPrefix+k,
				truncate(header.Get(k), maxHeaderValueLen),
			)
		case Redact:
			kv = append(kv,
				keyPrefix+k,
				fmt.Sprintf("<redacted len=%d>", len(header.Get(k))),
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
