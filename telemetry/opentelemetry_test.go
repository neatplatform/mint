package telemetry

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

func TestCreateOTelLogger(t *testing.T) {
	tests := []struct {
		name string
		o    opentelemetryLoggerOpts
		m    metadataOpts
	}{
		{
			name: "WithOTLPHTTP",
			o: opentelemetryLoggerOpts{
				OTLPHTTP: &otlpHTTPOpts{
					Endpoint: "collector.service:4318",
					Config:   &tls.Config{},
				},
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
		},
		{
			name: "WithOTLPGRPC",
			o: opentelemetryLoggerOpts{
				OTLPGRPC: &otlpGRPCOpts{
					Endpoint: "collector.service:4317",
					Creds:    credentials.NewTLS(nil),
				},
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger, close := createOTelLogger(tc.o, tc.m)

			defer func() {
				err := close(context.Background())
				assert.NoError(t, err)
			}()

			assert.NotNil(t, logger)
			assert.NotNil(t, close)
		})
	}
}

func TestCreateHTTPLogExporter(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		o    otlpHTTPOpts
	}{
		{
			name: "WithDefaultEndpoint",
			ctx:  context.Background(),
			o:    otlpHTTPOpts{},
		},
		{
			name: "WithCustomEndpoint",
			ctx:  context.Background(),
			o: otlpHTTPOpts{
				Endpoint: "collector.service:4318",
			},
		},
		{
			name: "WithTLSCredentials",
			ctx:  context.Background(),
			o: otlpHTTPOpts{
				Endpoint: "collector.service:4318",
				Config:   &tls.Config{},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter := createHTTPLogExporter(tc.ctx, tc.o)

			assert.NotNil(t, exporter)
		})
	}
}

func TestCreateGRPCLogExporter(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		o    otlpGRPCOpts
	}{
		{
			name: "WithDefaultEndpoint",
			ctx:  context.Background(),
			o:    otlpGRPCOpts{},
		},
		{
			name: "WithCustomEndpoint",
			ctx:  context.Background(),
			o: otlpGRPCOpts{
				Endpoint: "collector.service:4317",
			},
		},
		{
			name: "WithTLSCredentials",
			ctx:  context.Background(),
			o: otlpGRPCOpts{
				Endpoint: "collector.service:4317",
				Creds:    credentials.NewTLS(nil),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter := createGRPCLogExporter(tc.ctx, tc.o)

			assert.NotNil(t, exporter)
		})
	}
}

func TestCreateOTelMeter(t *testing.T) {
	tests := []struct {
		name          string
		o             opentelemetryMeterOpts
		m             metadataOpts
		expectHandler bool
	}{
		{
			name: "Default",
			o:    opentelemetryMeterOpts{},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
			expectHandler: true,
		},
		{
			name: "WithOTLPHTTP",
			o: opentelemetryMeterOpts{
				OTLPHTTP: &otlpHTTPOpts{
					Endpoint: "collector.service:4318",
					Config:   &tls.Config{},
				},
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
			expectHandler: false,
		},
		{
			name: "WithOTLPGRPC",
			o: opentelemetryMeterOpts{
				OTLPGRPC: &otlpGRPCOpts{
					Endpoint: "collector.service:4317",
					Creds:    credentials.NewTLS(nil),
				},
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
			expectHandler: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meter, close, handler := createOTelMeter(tc.o, tc.m)

			defer func() {
				// Meter close will try connecting to remote endpoints, so ignore its error in tests.
				// err := close(context.Background())
				// assert.NoError(t, err)
			}()

			assert.NotNil(t, meter)
			assert.NotNil(t, close)
			assert.Equal(t, tc.expectHandler, handler != nil)
		})
	}
}

func TestCreatePrometheusMetricExporter(t *testing.T) {
	exporter, handler := createPrometheusMetricExporter()

	assert.NotNil(t, exporter)
	assert.NotNil(t, handler)
}

func TestCreateHTTPMetricExporter(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		o    otlpHTTPOpts
	}{
		{
			name: "WithDefaultEndpoint",
			ctx:  context.Background(),
			o:    otlpHTTPOpts{},
		},
		{
			name: "WithCustomEndpoint",
			ctx:  context.Background(),
			o: otlpHTTPOpts{
				Endpoint: "collector.service:4318",
			},
		},
		{
			name: "WithTLSCredentials",
			ctx:  context.Background(),
			o: otlpHTTPOpts{
				Endpoint: "collector.service:4318",
				Config:   &tls.Config{},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter := createHTTPMetricExporter(tc.ctx, tc.o)

			assert.NotNil(t, exporter)
		})
	}
}

func TestCreateGRPCMetricExporter(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		o    otlpGRPCOpts
	}{
		{
			name: "WithDefaultEndpoint",
			ctx:  context.Background(),
			o:    otlpGRPCOpts{},
		},
		{
			name: "WithCustomEndpoint",
			ctx:  context.Background(),
			o: otlpGRPCOpts{
				Endpoint: "collector.service:4317",
			},
		},
		{
			name: "WithTLSCredentials",
			ctx:  context.Background(),
			o: otlpGRPCOpts{
				Endpoint: "collector.service:4317",
				Creds:    credentials.NewTLS(nil),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter := createGRPCMetricExporter(tc.ctx, tc.o)

			assert.NotNil(t, exporter)
		})
	}
}

func TestCreateOTelTracer(t *testing.T) {
	tests := []struct {
		name string
		o    opentelemetryTracerOpts
		m    metadataOpts
	}{
		{
			name: "WithOTLPHTTP",
			o: opentelemetryTracerOpts{
				OTLPHTTP: &otlpHTTPOpts{
					Endpoint: "collector.service:4318",
					Config:   &tls.Config{},
				},
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
		},
		{
			name: "WithOTLPGRPC",
			o: opentelemetryTracerOpts{
				OTLPGRPC: &otlpGRPCOpts{
					Endpoint: "collector.service:4317",
					Creds:    credentials.NewTLS(nil),
				},
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracer, close := createOTelTracer(tc.o, tc.m)

			defer func() {
				err := close(context.Background())
				assert.NoError(t, err)
			}()

			assert.NotNil(t, tracer)
			assert.NotNil(t, close)
		})
	}
}

func TestCreateHTTPTraceExporter(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		o    otlpHTTPOpts
	}{
		{
			name: "WithDefaultEndpoint",
			ctx:  context.Background(),
			o:    otlpHTTPOpts{},
		},
		{
			name: "WithCustomEndpoint",
			ctx:  context.Background(),
			o: otlpHTTPOpts{
				Endpoint: "collector.service:4318",
			},
		},
		{
			name: "WithTLSCredentials",
			ctx:  context.Background(),
			o: otlpHTTPOpts{
				Endpoint: "collector.service:4318",
				Config:   &tls.Config{},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter := createHTTPTraceExporter(tc.ctx, tc.o)

			assert.NotNil(t, exporter)
		})
	}
}

func TestCreateGRPCTraceExporter(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		o    otlpGRPCOpts
	}{
		{
			name: "WithDefaultEndpoint",
			ctx:  context.Background(),
			o:    otlpGRPCOpts{},
		},
		{
			name: "WithCustomEndpoint",
			ctx:  context.Background(),
			o: otlpGRPCOpts{
				Endpoint: "collector.service:4317",
			},
		},
		{
			name: "WithTLSCredentials",
			ctx:  context.Background(),
			o: otlpGRPCOpts{
				Endpoint: "collector.service:4317",
				Creds:    credentials.NewTLS(nil),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exporter := createGRPCTraceExporter(tc.ctx, tc.o)

			assert.NotNil(t, exporter)
		})
	}
}

func TestCreateResource(t *testing.T) {
	tests := []struct {
		name string
		m    metadataOpts
	}{
		{
			name: "OK",
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := createResource(tc.m)

			assert.NotNil(t, r)
			assert.NotEmpty(t, r.Attributes())
		})
	}
}

func TestLevelToSeverity(t *testing.T) {
	tests := []struct {
		name             string
		l                string
		expectedSeverity log.Severity
	}{
		{
			name:             "Debug",
			l:                "debug",
			expectedSeverity: log.SeverityDebug,
		},
		{
			name:             "Info",
			l:                "info",
			expectedSeverity: log.SeverityInfo,
		},
		{
			name:             "Warn",
			l:                "warn",
			expectedSeverity: log.SeverityWarn,
		},
		{
			name:             "Error",
			l:                "error",
			expectedSeverity: log.SeverityError,
		},
		{
			name:             "None",
			l:                "none",
			expectedSeverity: log.SeverityUndefined,
		},
		{
			name:             "Unknown",
			l:                "unknown",
			expectedSeverity: log.SeverityUndefined,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedSeverity, levelToSeverity(tc.l))
		})
	}
}

func TestAppendOTelKV(t *testing.T) {
	tests := []struct {
		name       string
		b          []attribute.KeyValue
		kv         []any
		expectedKV []attribute.KeyValue
	}{
		{
			name: "StringKey",
			b: []attribute.KeyValue{
				attribute.String("level", "debug"),
			},
			kv: []any{"env", "test"},
			expectedKV: []attribute.KeyValue{
				attribute.String("level", "debug"),
				attribute.String("env", "test"),
			},
		},
		{
			name: "BytesKey",
			b: []attribute.KeyValue{
				attribute.String("level", "debug"),
			},
			kv: []any{[]byte{0x65, 0x6E, 0x76}, "test"},
			expectedKV: []attribute.KeyValue{
				attribute.String("level", "debug"),
				attribute.String("env", "test"),
			},
		},
		{
			name: "NonStandardKey",
			b: []attribute.KeyValue{
				attribute.String("level", "debug"),
			},
			kv: []any{true, "yes"},
			expectedKV: []attribute.KeyValue{
				attribute.String("level", "debug"),
				attribute.String("true", "yes"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kv := appendOTelKV(tc.b, tc.kv)

			assert.Equal(t, tc.expectedKV, kv)
		})
	}
}

func TestCreateOTelKV(t *testing.T) {
	tests := []struct {
		name       string
		k          string
		v          any
		expectedKV attribute.KeyValue
	}{
		{
			name:       "Null",
			k:          "msg",
			v:          nil,
			expectedKV: attribute.String("msg", ""),
		},
		{
			name:       "False",
			k:          "msg",
			v:          false,
			expectedKV: attribute.Bool("msg", false),
		},
		{
			name:       "True",
			k:          "msg",
			v:          true,
			expectedKV: attribute.Bool("msg", true),
		},
		{
			name:       "String",
			k:          "msg",
			v:          "ok",
			expectedKV: attribute.String("msg", "ok"),
		},
		{
			name:       "Bytes",
			k:          "msg",
			v:          []byte{0x6f, 0x6b},
			expectedKV: attribute.String("msg", "ok"),
		},
		{
			name:       "Error",
			k:          "msg",
			v:          errors.New("io error"),
			expectedKV: attribute.String("msg", "io error"),
		},
		{
			name:       "Int",
			k:          "msg",
			v:          4214172627767632518,
			expectedKV: attribute.Int("msg", 4214172627767632518),
		},
		{
			name:       "Int8",
			k:          "msg",
			v:          int8(120),
			expectedKV: attribute.Int64("msg", 120),
		},
		{
			name:       "Int16",
			k:          "msg",
			v:          int16(21270),
			expectedKV: attribute.Int64("msg", 21270),
		},
		{
			name:       "Int32",
			k:          "msg",
			v:          int32(2098013252),
			expectedKV: attribute.Int64("msg", 2098013252),
		},
		{
			name:       "Int64",
			k:          "msg",
			v:          int64(7988977323551769556),
			expectedKV: attribute.Int64("msg", 7988977323551769556),
		},
		{
			name:       "Uint",
			k:          "msg",
			v:          uint(8508453290381091020),
			expectedKV: attribute.Int("msg", 8508453290381091020),
		},
		{
			name:       "Uint8",
			k:          "msg",
			v:          uint8(175),
			expectedKV: attribute.Int64("msg", 175),
		},
		{
			name:       "Uint16",
			k:          "msg",
			v:          uint16(60760),
			expectedKV: attribute.Int64("msg", 60760),
		},
		{
			name:       "Uint32",
			k:          "msg",
			v:          uint32(837254510),
			expectedKV: attribute.Int64("msg", 837254510),
		},
		{
			name:       "Uint64",
			k:          "msg",
			v:          uint64(6645248128090971766),
			expectedKV: attribute.Int64("msg", 6645248128090971766),
		},
		{
			name:       "Float32",
			k:          "msg",
			v:          float32(3.14159265),
			expectedKV: attribute.Float64("msg", float64(float32(3.14159265))),
		},
		{
			name:       "Float64",
			k:          "msg",
			v:          float64(3.1415926535897932),
			expectedKV: attribute.Float64("msg", 3.1415926535897932),
		},
		{
			name:       "EmptyAnySlice",
			k:          "msg",
			v:          []any{},
			expectedKV: attribute.String("msg", `[]`),
		},
		{
			name:       "AnySlice",
			k:          "msg",
			v:          []any{1, "two", true, nil},
			expectedKV: attribute.String("msg", `[1,"two",true,null]`),
		},
		{
			name:       "BoolSlice",
			k:          "msg",
			v:          []bool{true, false},
			expectedKV: attribute.String("msg", `[true,false]`),
		},
		{
			name:       "StringSlice",
			k:          "msg",
			v:          []string{"a", "b", "c"},
			expectedKV: attribute.String("msg", `["a","b","c"]`),
		},
		{
			name:       "IntSlice",
			k:          "msg",
			v:          []int{-64, -32, -16, -8, -4, -2, -1, 1, 2, 4, 8, 16, 32, 64},
			expectedKV: attribute.String("msg", `[-64,-32,-16,-8,-4,-2,-1,1,2,4,8,16,32,64]`),
		},
		{
			name:       "Int8Slice",
			k:          "msg",
			v:          []int8{-128, 0, 127},
			expectedKV: attribute.String("msg", `[-128,0,127]`),
		},
		{
			name:       "Int16Slice",
			k:          "msg",
			v:          []int16{-32768, 0, 32767},
			expectedKV: attribute.String("msg", `[-32768,0,32767]`),
		},
		{
			name:       "Int32Slice",
			k:          "msg",
			v:          []int32{-2147483648, 0, 2147483647},
			expectedKV: attribute.String("msg", `[-2147483648,0,2147483647]`),
		},
		{
			name:       "Int64Slice",
			k:          "msg",
			v:          []int64{-9223372036854775808, 0, 9223372036854775807},
			expectedKV: attribute.String("msg", `[-9223372036854775808,0,9223372036854775807]`),
		},
		{
			name:       "UintSlice",
			k:          "msg",
			v:          []uint{1, 1, 2, 3, 5, 8, 13, 21, 34, 55},
			expectedKV: attribute.String("msg", `[1,1,2,3,5,8,13,21,34,55]`),
		},
		{
			name:       "Uint16Slice",
			k:          "msg",
			v:          []uint16{0, 1, 65535},
			expectedKV: attribute.String("msg", `[0,1,65535]`),
		},
		{
			name:       "Uint32Slice",
			k:          "msg",
			v:          []uint32{0, 1, 4294967295},
			expectedKV: attribute.String("msg", `[0,1,4294967295]`),
		},
		{
			name:       "Uint64Slice",
			k:          "msg",
			v:          []uint64{0, 1, 18446744073709551615},
			expectedKV: attribute.String("msg", `[0,1,18446744073709551615]`),
		},
		{
			name:       "Float32Slice",
			k:          "msg",
			v:          []float32{3.14159265, 2.71828182},
			expectedKV: attribute.String("msg", `[3.1415927,2.7182817]`),
		},
		{
			name:       "Float64Slice",
			k:          "msg",
			v:          []float64{3.1415926535897932, 2.7182818284590452},
			expectedKV: attribute.String("msg", `[3.141592653589793,2.718281828459045]`),
		},
		{
			name:       "MapStringAny",
			k:          "msg",
			v:          map[string]any{"b": 2, "a": "one"},
			expectedKV: attribute.String("msg", `{"a":"one","b":2}`),
		},
		{
			name:       "MapStringBool",
			k:          "msg",
			v:          map[string]bool{"b": true, "a": false},
			expectedKV: attribute.String("msg", `{"a":false,"b":true}`),
		},
		{
			name:       "MapStringString",
			k:          "msg",
			v:          map[string]string{"b": "two", "a": "one"},
			expectedKV: attribute.String("msg", `{"a":"one","b":"two"}`),
		},
		{
			name:       "MapStringInt",
			k:          "msg",
			v:          map[string]int{"b": 1, "a": -1},
			expectedKV: attribute.String("msg", `{"a":-1,"b":1}`),
		},
		{
			name:       "MapStringInt8",
			k:          "msg",
			v:          map[string]int8{"b": 127, "a": -128},
			expectedKV: attribute.String("msg", `{"a":-128,"b":127}`),
		},
		{
			name:       "MapStringInt16",
			k:          "msg",
			v:          map[string]int16{"b": 32767, "a": -32768},
			expectedKV: attribute.String("msg", `{"a":-32768,"b":32767}`),
		},
		{
			name:       "MapStringInt32",
			k:          "msg",
			v:          map[string]int32{"b": 2147483647, "a": -2147483648},
			expectedKV: attribute.String("msg", `{"a":-2147483648,"b":2147483647}`),
		},
		{
			name:       "MapStringInt64",
			k:          "msg",
			v:          map[string]int64{"b": 9223372036854775807, "a": -9223372036854775808},
			expectedKV: attribute.String("msg", `{"a":-9223372036854775808,"b":9223372036854775807}`),
		},
		{
			name:       "MapStringUint",
			k:          "msg",
			v:          map[string]uint{"b": 28, "a": 6},
			expectedKV: attribute.String("msg", `{"a":6,"b":28}`),
		},
		{
			name:       "MapStringUint8",
			k:          "msg",
			v:          map[string]uint8{"b": 255, "a": 0},
			expectedKV: attribute.String("msg", `{"a":0,"b":255}`),
		},
		{
			name:       "MapStringUint16",
			k:          "msg",
			v:          map[string]uint16{"b": 65535, "a": 0},
			expectedKV: attribute.String("msg", `{"a":0,"b":65535}`),
		},
		{
			name:       "MapStringUint32",
			k:          "msg",
			v:          map[string]uint32{"b": 4294967295, "a": 0},
			expectedKV: attribute.String("msg", `{"a":0,"b":4294967295}`),
		},
		{
			name:       "MapStringUint64",
			k:          "msg",
			v:          map[string]uint64{"b": 18446744073709551615, "a": 0},
			expectedKV: attribute.String("msg", `{"a":0,"b":18446744073709551615}`),
		},
		{
			name:       "MapStringFloat32",
			k:          "msg",
			v:          map[string]float32{"b": 3.14159265, "a": 2.71828182},
			expectedKV: attribute.String("msg", `{"a":2.7182817,"b":3.1415927}`),
		},
		{
			name:       "MapStringFloat64",
			k:          "msg",
			v:          map[string]float64{"b": 3.1415926535897932, "a": 2.7182818284590452},
			expectedKV: attribute.String("msg", `{"a":2.718281828459045,"b":3.141592653589793}`),
		},
		{
			name:       "MapStringAnySlice",
			k:          "msg",
			v:          map[string][]any{"a": {1, "two", true, nil}},
			expectedKV: attribute.String("msg", `{"a":[1,"two",true,null]}`),
		},
		{
			name:       "MapStringBoolSlice",
			k:          "msg",
			v:          map[string][]bool{"a": {true, false}},
			expectedKV: attribute.String("msg", `{"a":[true,false]}`),
		},
		{
			name:       "MapStringStringSlice",
			k:          "msg",
			v:          map[string][]string{"a": {"x", "y"}},
			expectedKV: attribute.String("msg", `{"a":["x","y"]}`),
		},
		{
			name:       "MapStringIntSlice",
			k:          "msg",
			v:          map[string][]int{"a": {-8, -4, -2, -1, 1, 2, 4, 8}},
			expectedKV: attribute.String("msg", `{"a":[-8,-4,-2,-1,1,2,4,8]}`),
		},
		{
			name:       "MapStringInt8Slice",
			k:          "msg",
			v:          map[string][]int8{"a": {-128, 0, 127}},
			expectedKV: attribute.String("msg", `{"a":[-128,0,127]}`),
		},
		{
			name:       "MapStringInt16Slice",
			k:          "msg",
			v:          map[string][]int16{"a": {-32768, 0, 32767}},
			expectedKV: attribute.String("msg", `{"a":[-32768,0,32767]}`),
		},
		{
			name:       "MapStringInt32Slice",
			k:          "msg",
			v:          map[string][]int32{"a": {-2147483648, 0, 2147483647}},
			expectedKV: attribute.String("msg", `{"a":[-2147483648,0,2147483647]}`),
		},
		{
			name:       "MapStringInt64Slice",
			k:          "msg",
			v:          map[string][]int64{"a": {-9223372036854775808, 0, 9223372036854775807}},
			expectedKV: attribute.String("msg", `{"a":[-9223372036854775808,0,9223372036854775807]}`),
		},
		{
			name:       "MapStringUintSlice",
			k:          "msg",
			v:          map[string][]uint{"a": {1, 1, 2, 3, 5, 8}},
			expectedKV: attribute.String("msg", `{"a":[1,1,2,3,5,8]}`),
		},
		{
			name:       "MapStringUint8Slice",
			k:          "msg",
			v:          map[string][]uint8{"a": {0x6f, 0x6b}},
			expectedKV: attribute.String("msg", `{"a":"ok"}`),
		},
		{
			name:       "MapStringUint16Slice",
			k:          "msg",
			v:          map[string][]uint16{"a": {0, 1, 65535}},
			expectedKV: attribute.String("msg", `{"a":[0,1,65535]}`),
		},
		{
			name:       "MapStringUint32Slice",
			k:          "msg",
			v:          map[string][]uint32{"a": {0, 1, 4294967295}},
			expectedKV: attribute.String("msg", `{"a":[0,1,4294967295]}`),
		},
		{
			name:       "MapStringUint64Slice",
			k:          "msg",
			v:          map[string][]uint64{"a": {0, 1, 18446744073709551615}},
			expectedKV: attribute.String("msg", `{"a":[0,1,18446744073709551615]}`),
		},
		{
			name:       "MapStringFloat32Slice",
			k:          "msg",
			v:          map[string][]float32{"a": {3.14159265, 2.71828182}},
			expectedKV: attribute.String("msg", `{"a":[3.1415927,2.7182817]}`),
		},
		{
			name:       "MapStringFloat64Slice",
			k:          "msg",
			v:          map[string][]float64{"a": {3.1415926535897932, 2.7182818284590452}},
			expectedKV: attribute.String("msg", `{"a":[3.141592653589793,2.718281828459045]}`),
		},
		{
			name: "HTTPHeader",
			k:    "msg",
			v: http.Header{
				"Content-Type": {"application/json"},
				"User-Agent":   {"client-name/1.0"},
			},
			expectedKV: attribute.String("msg", `{"Content-Type":["application/json"],"User-Agent":["client-name/1.0"]}`),
		},
		{
			name: "GRPCMetadata",
			k:    "msg",
			v: metadata.MD{
				"authorization": {"Bearer token"},
				"user-agent":    {"client-name/1.0"},
			},
			expectedKV: attribute.String("msg", `{"authorization":["Bearer token"],"user-agent":["client-name/1.0"]}`),
		},
		{
			name:       "Struct",
			k:          "msg",
			v:          struct{ ID string }{ID: "1234-5678"},
			expectedKV: attribute.String("msg", `{ID:1234-5678}`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kv := createOTelKV(tc.k, tc.v)

			assert.Equal(t, tc.expectedKV, kv)
		})
	}
}
