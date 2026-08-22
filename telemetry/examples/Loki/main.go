package main

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/neatplatform/mint/telemetry"
)

func main() {
	ctx := context.Background()

	// Creating a new probe.
	p := telemetry.NewProbe(
		telemetry.WithMetadata("echo-service", "v0.1.0", map[string]any{
			"environment": "test",
			"region":      "local",
		}),
		telemetry.WithStdoutLogger("info"),
		telemetry.WithLokiLogger(
			"example", "info", []string{},
			"http://localhost:3100/loki/api/v1/push", false, nil,
		),
	)

	defer func() {
		if err := p.Close(ctx); err != nil {
			p.Logger().Errorf("error closing probe: %s", err)
		}
	}()

	// Setting the probe as the singleton instance.
	telemetry.SetProbe(p)

	// Create a child logger
	logger := p.Logger().With(
		"os", runtime.GOOS,
		"arch", runtime.GOARCH,
		"session_id", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
	)

	// Logging
	logger.Debug("Debug message with structured fields.", "user_id", 69, "action", "login")
	logger.Info("Info message with key-value pairs.", "test", true)
	logger.Infof("Info message with formatted arguments: %s", true)
	logger.Warn("Warn message.", "load_factor", 0.88)
	logger.Error("Error message.", "error", errors.New("validation failed"))

	// Give the Loki client a moment to batch and push the logs before the process exits.
	time.Sleep(5 * time.Second)
}
