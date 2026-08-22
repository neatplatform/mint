package telemetry_test

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/neatplatform/mint/telemetry"
)

func Example_basic() {
	p := telemetry.NewProbe(
		telemetry.WithMetadata("my-service", "v0.1.0", map[string]any{
			"environment": "test",
			"region":      "local",
		}),
	)

	defer func() {
		_ = p.Close(context.Background())
	}()

	fmt.Println(p.Info())
}

func Example_stdoutLogger() {
	p := telemetry.NewProbe(
		telemetry.WithMetadata("my-service", "v0.1.0", map[string]any{
			"environment": "test",
			"region":      "local",
		}),
		telemetry.WithStdoutLogger("info"),
	)

	defer func() {
		_ = p.Close(context.Background())
	}()

	// Simulate handling a request and log a message with attributes.
	p.Logger().Info("Request succeeded!",
		"req_method", "GET",
		"req_path", "/user",
		"resp_status", 200,
	)
}

func Example_fileLogger() {
	p := telemetry.NewProbe(
		telemetry.WithMetadata("my-service", "v0.1.0", map[string]any{
			"environment": "test",
			"region":      "local",
		}),
		telemetry.WithFileLogger("info", "my-service.log", 100, 5, 30),
	)

	defer func() {
		_ = p.Close(context.Background())
	}()

	// Simulate handling a request and log a message with attributes.
	p.Logger().Info("Request succeeded!",
		"req_method", "GET",
		"req_path", "/user",
		"resp_status", 200,
	)
}

func Example_multipleLoggers() {
	p := telemetry.NewProbe(
		telemetry.WithMetadata("my-service", "v0.1.0", map[string]any{
			"environment": "test",
			"region":      "local",
		}),
		telemetry.WithStdoutLogger("info"),
		telemetry.WithFileLogger("info", "my-service.log", 100, 5, 30),
	)

	defer func() {
		_ = p.Close(context.Background())
	}()

	// Simulate handling a request and log a message with attributes.
	p.Logger().Info("Request succeeded!",
		"req_method", "GET",
		"req_path", "/user",
		"resp_status", 200,
	)
}

func Example_prometheusMeter() {
	ctx := context.Background()

	p := telemetry.NewProbe(
		telemetry.WithMetadata("my-service", "v0.1.0", map[string]any{
			"environment": "test",
			"region":      "local",
		}),
		telemetry.WithOpenTelemetryMeter(),
	)

	defer func() {
		_ = p.Close(ctx)
	}()

	// Create a counter metric to track the total number of requests.
	counter, _ := p.Meter().Int64Counter(
		"http_requests_total",
		metric.WithDescription("The total number of requests."),
	)

	// Simulate handling a request and increment the counter with attributes.
	counter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("req_method", "GET"),
		attribute.String("req_path", "/user"),
		attribute.Int("resp_status", 200),
	))

	// Simulate scraping the Prometheus metrics endpoint.
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	fmt.Println(string(body))
}
