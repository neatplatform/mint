package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestContextWithUUID(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		uuid string
	}{
		{
			name: "OK",
			ctx:  context.Background(),
			uuid: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ContextWithUUID(tc.ctx, tc.uuid)

			assert.Equal(t, tc.uuid, ctx.Value(uuidContextKey))
		})
	}
}

func TestUUIDFromContext(t *testing.T) {
	tests := []struct {
		name         string
		ctx          context.Context
		expectedUUID string
		expectedOK   bool
	}{
		{
			name:         "WithoutUUID",
			ctx:          context.Background(),
			expectedUUID: "",
			expectedOK:   false,
		},
		{
			name:         "WithUUID",
			ctx:          context.WithValue(context.Background(), uuidContextKey, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
			expectedUUID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			expectedOK:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uuid, ok := UUIDFromContext(tc.ctx)

			assert.Equal(t, tc.expectedUUID, uuid)
			assert.Equal(t, tc.expectedOK, ok)
		})
	}
}

func TestContextWithLogger(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		logger Logger
	}{
		{
			name:   "OK",
			ctx:    context.Background(),
			logger: newNoopLogger(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ContextWithLogger(tc.ctx, tc.logger)

			logger, ok := ctx.Value(loggerContextKey).(Logger)
			assert.True(t, ok)
			assert.Equal(t, tc.logger, logger)
		})
	}
}

func TestLoggerFromContext(t *testing.T) {
	logger := newNoopLogger()

	tests := []struct {
		name           string
		ctx            context.Context
		expectedLogger Logger
	}{
		{
			name:           "SingletonLogger",
			ctx:            context.Background(),
			expectedLogger: singleton.Logger(),
		},
		{
			name:           "CustomLogger",
			ctx:            context.WithValue(context.Background(), loggerContextKey, logger),
			expectedLogger: logger,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger := LoggerFromContext(tc.ctx)

			assert.Equal(t, tc.expectedLogger, logger)
		})
	}
}

func TestContextWithMeter(t *testing.T) {
	tests := []struct {
		name  string
		ctx   context.Context
		meter metric.Meter
	}{
		{
			name:  "OK",
			ctx:   context.Background(),
			meter: metricnoop.NewMeterProvider().Meter(""),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ContextWithMeter(tc.ctx, tc.meter)

			meter, ok := ctx.Value(meterContextKey).(metric.Meter)
			assert.True(t, ok)
			assert.Equal(t, tc.meter, meter)
		})
	}
}

func TestMeterFromContext(t *testing.T) {
	meter := metricnoop.NewMeterProvider().Meter("")

	tests := []struct {
		name          string
		ctx           context.Context
		expectedMeter metric.Meter
	}{
		{
			name:          "SingletonMeter",
			ctx:           context.Background(),
			expectedMeter: singleton.Meter(),
		},
		{
			name:          "CustomMeter",
			ctx:           context.WithValue(context.Background(), meterContextKey, meter),
			expectedMeter: meter,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meter := MeterFromContext(tc.ctx)

			assert.Equal(t, tc.expectedMeter, meter)
		})
	}
}

func TestContextWithTracer(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		tracer trace.Tracer
	}{
		{
			name:   "OK",
			ctx:    context.Background(),
			tracer: tracenoop.NewTracerProvider().Tracer(""),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ContextWithTracer(tc.ctx, tc.tracer)

			tracer, ok := ctx.Value(tracerContextKey).(trace.Tracer)
			assert.True(t, ok)
			assert.Equal(t, tc.tracer, tracer)
		})
	}
}

func TestTracerFromContext(t *testing.T) {
	tracer := tracenoop.NewTracerProvider().Tracer("")

	tests := []struct {
		name           string
		ctx            context.Context
		expectedTracer trace.Tracer
	}{
		{
			name:           "SingletonTracer",
			ctx:            context.Background(),
			expectedTracer: singleton.Tracer(),
		},
		{
			name:           "CustomTracer",
			ctx:            context.WithValue(context.Background(), tracerContextKey, tracer),
			expectedTracer: tracer,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracer := TracerFromContext(tc.ctx)

			assert.Equal(t, tc.expectedTracer, tracer)
		})
	}
}
