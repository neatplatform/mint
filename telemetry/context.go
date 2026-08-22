package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type contextKey string

const (
	uuidContextKey   = contextKey("TELEMETRY_UUID")
	loggerContextKey = contextKey("TELEMETRY_LOGGER")
	meterContextKey  = contextKey("TELEMETRY_METER")
	tracerContextKey = contextKey("TELEMETRY_TRACER")
)

// ContextWithUUID returns a new context carrying the given UUID.
func ContextWithUUID(ctx context.Context, uuid string) context.Context {
	return context.WithValue(ctx, uuidContextKey, uuid)
}

// UUIDFromContext returns the UUID stored in ctx, if any.
func UUIDFromContext(ctx context.Context) (string, bool) {
	uuid, ok := ctx.Value(uuidContextKey).(string)
	return uuid, ok
}

// ContextWithLogger returns a new context that holds a reference to a [Logger].
func ContextWithLogger(ctx context.Context, logger Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey, logger)
}

// LoggerFromContext returns the [Logger] stored in ctx.
// If no logger is found, the singleton logger is returned.
func LoggerFromContext(ctx context.Context) Logger {
	if logger, ok := ctx.Value(loggerContextKey).(Logger); ok {
		return logger
	}

	return singleton.Logger()
}

// ContextWithMeter returns a new context that holds a reference to a [Meter].
func ContextWithMeter(ctx context.Context, meter metric.Meter) context.Context {
	return context.WithValue(ctx, meterContextKey, meter)
}

// MeterFromContext returns the [Meter] stored in ctx.
// If no meter is found, the singleton meter is returned.
func MeterFromContext(ctx context.Context) metric.Meter {
	if meter, ok := ctx.Value(meterContextKey).(metric.Meter); ok {
		return meter
	}

	return singleton.Meter()
}

// ContextWithTracer returns a new context that holds a reference to a [Tracer].
func ContextWithTracer(ctx context.Context, tracer trace.Tracer) context.Context {
	return context.WithValue(ctx, tracerContextKey, tracer)
}

// TracerFromContext returns the [Tracer] stored in ctx.
// If no tracer is found, the singleton tracer is returned.
func TracerFromContext(ctx context.Context) trace.Tracer {
	if tracer, ok := ctx.Value(tracerContextKey).(trace.Tracer); ok {
		return tracer
	}

	return singleton.Tracer()
}
