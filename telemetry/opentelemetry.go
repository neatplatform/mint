package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"

	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	logsdk "go.opentelemetry.io/otel/sdk/log"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const (
	samplingRate = 0.1
)

// closeFunc defines the function type for closing OpenTelemetry providers.
type closeFunc func(context.Context) error

func createOTelLogger(o opentelemetryLoggerOpts, m metadataOpts) (log.Logger, closeFunc) {
	ctx := context.Background()

	var exporter logsdk.Exporter
	if o.OTLPGRPC != nil {
		exporter = createGRPCLogExporter(ctx, *o.OTLPGRPC)
	} else { // Default to HTTP as well
		exporter = createHTTPLogExporter(ctx, *o.OTLPHTTP)
	}

	provider := logsdk.NewLoggerProvider(
		logsdk.WithProcessor(
			logsdk.NewBatchProcessor(exporter),
		),
		logsdk.WithResource(
			createResource(m),
		),
	)

	// Unlike the meter and tracer providers, the logger provider is not registered globally:
	// this package exposes its own logger interface instead of relying on the OpenTelemetry log API.

	// Configure scope attributes for the logger.
	var opts []log.LoggerOption
	if m.Version != "" {
		opts = append(opts, log.WithInstrumentationVersion(m.Version))
	}

	// Skip adding metadata attributes to the scope as they are already added to resource.

	logger := provider.Logger(m.Name, opts...)
	close := provider.Shutdown

	return logger, close
}

func createHTTPLogExporter(ctx context.Context, o otlpHTTPOpts) *otlploghttp.Exporter {
	opts := []otlploghttp.Option{
		otlploghttp.WithCompression(otlploghttp.GzipCompression),
	}

	if o.Endpoint != "" {
		opts = append(opts, otlploghttp.WithEndpoint(o.Endpoint))
	}

	if o.Config == nil {
		opts = append(opts, otlploghttp.WithInsecure())
	} else {
		opts = append(opts, otlploghttp.WithTLSClientConfig(o.Config))
	}

	logExporter, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		panic(err)
	}

	return logExporter
}

func createGRPCLogExporter(ctx context.Context, o otlpGRPCOpts) *otlploggrpc.Exporter {
	opts := []otlploggrpc.Option{
		otlploggrpc.WithCompressor("gzip"),
	}

	if o.Endpoint != "" {
		opts = append(opts, otlploggrpc.WithEndpoint(o.Endpoint))
	}

	if o.Creds == nil {
		opts = append(opts, otlploggrpc.WithInsecure())
	} else {
		opts = append(opts, otlploggrpc.WithTLSCredentials(o.Creds))
	}

	logExporter, err := otlploggrpc.New(ctx, opts...)
	if err != nil {
		panic(err)
	}

	return logExporter
}

func createOTelMeter(o opentelemetryMeterOpts, m metadataOpts) (metric.Meter, closeFunc, http.Handler) {
	ctx := context.Background()

	var reader metricsdk.Reader
	var handler http.Handler

	if o.OTLPGRPC != nil {
		// The PeriodicReader collects and exports metrics to the exporter on a fixed interval.
		reader = metricsdk.NewPeriodicReader(
			createGRPCMetricExporter(ctx, *o.OTLPGRPC),
			metricsdk.WithInterval(30*time.Second), // Durations below 30s are rounded up to 30s.
			metricsdk.WithTimeout(30*time.Second),  // Durations below 30s are rounded up to 30s.
		)
	} else if o.OTLPHTTP != nil {
		// The PeriodicReader collects and exports metrics to the exporter on a fixed interval.
		reader = metricsdk.NewPeriodicReader(
			createHTTPMetricExporter(ctx, *o.OTLPHTTP),
			metricsdk.WithInterval(30*time.Second), // Durations below 30s are rounded up to 30s.
			metricsdk.WithTimeout(30*time.Second),  // Durations below 30s are rounded up to 30s.
		)
	} else {
		reader, handler = createPrometheusMetricExporter()
	}

	meterProvider := metricsdk.NewMeterProvider(
		metricsdk.WithReader(reader),
		metricsdk.WithResource(
			createResource(m),
		),
	)

	// Register the meter provider as the global one.
	otel.SetMeterProvider(meterProvider)

	// Configure scope attributes for the meter.
	var opts []metric.MeterOption
	if m.Version != "" {
		opts = append(opts, metric.WithInstrumentationVersion(m.Version))
	}

	// Skip adding metadata attributes to the scope as they are already added to resource.

	meter := meterProvider.Meter(m.Name, opts...)
	close := meterProvider.Shutdown

	return meter, close, handler
}

func createPrometheusMetricExporter() (*promexporter.Exporter, http.Handler) {
	// Create a new Prometheus registry and register custom collectors.
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(
		collectors.ProcessCollectorOpts{},
	))

	exporter, err := promexporter.New(
		promexporter.WithRegisterer(reg),
		promexporter.WithoutScopeInfo(),
		promexporter.WithoutTargetInfo(),
	)

	if err != nil {
		panic(err)
	}

	handler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		Registry:            reg,
		ErrorHandling:       promhttp.ContinueOnError,
		MaxRequestsInFlight: 5,
		Timeout:             10 * time.Second,
	})

	return exporter, handler
}

func createHTTPMetricExporter(ctx context.Context, o otlpHTTPOpts) *otlpmetrichttp.Exporter {
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression),
	}

	if o.Endpoint != "" {
		opts = append(opts, otlpmetrichttp.WithEndpoint(o.Endpoint))
	}

	if o.Config == nil {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	} else {
		opts = append(opts, otlpmetrichttp.WithTLSClientConfig(o.Config))
	}

	metricExporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		panic(err)
	}

	return metricExporter
}

func createGRPCMetricExporter(ctx context.Context, o otlpGRPCOpts) *otlpmetricgrpc.Exporter {
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithCompressor("gzip"),
	}

	if o.Endpoint != "" {
		opts = append(opts, otlpmetricgrpc.WithEndpoint(o.Endpoint))
	}

	if o.Creds == nil {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	} else {
		opts = append(opts, otlpmetricgrpc.WithTLSCredentials(o.Creds))
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		panic(err)
	}

	return metricExporter
}

func createOTelTracer(o opentelemetryTracerOpts, m metadataOpts) (trace.Tracer, closeFunc) {
	ctx := context.Background()

	var exporter tracesdk.SpanExporter
	if o.OTLPGRPC != nil {
		exporter = createGRPCTraceExporter(ctx, *o.OTLPGRPC)
	} else { // Default to HTTP as well
		exporter = createHTTPTraceExporter(ctx, *o.OTLPHTTP)
	}

	// Respect the sampling decision of upstream callers (e.g., API gateway),
	// and samples a percentage of new root spans independently.
	sampler := tracesdk.ParentBased(
		tracesdk.TraceIDRatioBased(samplingRate),
	)

	traceProvider := tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exporter), // async, batched, non-blocking
		tracesdk.WithSampler(sampler),
		tracesdk.WithResource(
			createResource(m),
		),
	)

	// Register the tracer provider as the global one.
	otel.SetTracerProvider(traceProvider)

	// Register a composite TextMapPropagator combining trace context and baggage propagation,
	// so trace context and baggage are correctly propagated across process boundaries between clients and servers.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, // W3C traceparent/tracestate
		propagation.Baggage{},
	))

	// Configure scope attributes for the tracer.
	var opts []trace.TracerOption
	if m.Version != "" {
		opts = append(opts, trace.WithInstrumentationVersion(m.Version))
	}

	// Skip adding metadata attributes to the scope as they are already added to resource.

	tracer := traceProvider.Tracer(m.Name, opts...)
	close := traceProvider.Shutdown

	return tracer, close
}

func createHTTPTraceExporter(ctx context.Context, o otlpHTTPOpts) *otlptrace.Exporter {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithCompression(otlptracehttp.GzipCompression),
	}

	if o.Endpoint != "" {
		opts = append(opts, otlptracehttp.WithEndpoint(o.Endpoint))
	}

	if o.Config == nil {
		opts = append(opts, otlptracehttp.WithInsecure())
	} else {
		opts = append(opts, otlptracehttp.WithTLSClientConfig(o.Config))
	}

	traceExporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		panic(err)
	}

	return traceExporter
}

func createGRPCTraceExporter(ctx context.Context, o otlpGRPCOpts) *otlptrace.Exporter {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithCompressor("gzip"),
	}

	if o.Endpoint != "" {
		opts = append(opts, otlptracegrpc.WithEndpoint(o.Endpoint))
	}

	if o.Creds == nil {
		opts = append(opts, otlptracegrpc.WithInsecure())
	} else {
		opts = append(opts, otlptracegrpc.WithTLSCredentials(o.Creds))
	}

	traceExporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		panic(err)
	}

	return traceExporter
}

func createResource(o metadataOpts) *resource.Resource {
	attrs := []attribute.KeyValue{
		semconv.ServiceNameKey.String(o.Name),
		semconv.ServiceVersionKey.String(o.Version),
	}

	for n, v := range o.Attributes {
		attrs = append(attrs, createOTelKV(n, v))
	}

	resource := resource.NewWithAttributes(
		semconv.SchemaURL,
		attrs...,
	)

	return resource
}

// levelToSeverity converts a Level constant to its corresponding OpenTelemetry Severity.
func levelToSeverity(l string) log.Severity {
	if sev, ok := severityMap[l]; ok {
		return sev
	}

	return log.SeverityUndefined
}

var severityMap = map[string]log.Severity{
	"none":  log.SeverityUndefined,
	"error": log.SeverityError,
	"warn":  log.SeverityWarn,
	"info":  log.SeverityInfo,
	"debug": log.SeverityDebug,
}

// appendOTelKV appends a variadic list of key-value pairs to a list of OpenTelemetry KeyValues.
// The keys can be of type string or []byte, and the values can be of any type.
func appendOTelKV(b []attribute.KeyValue, kv []any) []attribute.KeyValue {
	for i := 0; i+1 < len(kv); i += 2 {
		var ks string
		switch k := kv[i].(type) {
		case string:
			ks = k
		case []byte:
			ks = string(k)
		default:
			ks = fmt.Sprint(k)
		}

		b = append(b, createOTelKV(ks, kv[i+1]))
	}

	return b
}

// createOTelKV converts a key and value into an OpenTelemetry KeyValue.
// Slices and maps are encoded as JSON; any other type is formatted with its default Go representation.
func createOTelKV(k string, v any) attribute.KeyValue {
	switch val := v.(type) {
	case nil:
		return attribute.String(k, "")

	case bool:
		return attribute.Bool(k, val)

	case string:
		return attribute.String(k, val)
	case []byte:
		return attribute.String(k, string(val))
	case error:
		return attribute.String(k, val.Error())

	// Signed integers
	case int:
		return attribute.Int(k, val)
	case int8:
		return attribute.Int64(k, int64(val))
	case int16:
		return attribute.Int64(k, int64(val))
	case int32:
		return attribute.Int64(k, int64(val))
	case int64:
		return attribute.Int64(k, val)

	// Unsigned integers
	case uint:
		return attribute.Int(k, int(val))
	case uint8:
		return attribute.Int64(k, int64(val))
	case uint16:
		return attribute.Int64(k, int64(val))
	case uint32:
		return attribute.Int64(k, int64(val))
	case uint64:
		return attribute.Int64(k, int64(val))

	// Floating point
	case float32:
		return attribute.Float64(k, float64(val))
	case float64:
		return attribute.Float64(k, val)

	// Slices of basic types
	// Maps of basic types
	// Maps of slices basic types
	// And anything else
	default:
		return attribute.String(k, getJSONValue(val))
	}
}
