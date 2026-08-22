// Package telemetry provides a unified observability layer built on top of the OpenTelemetry API.
// It consolidates the three pillars of observability — logs, metrics, and traces — into a single [Probe] interface,
// reducing boilerplate and preventing common misconfiguration mistakes.
//
// A package-level singleton [Probe] is provided for convenience and is initialized and configured with default options.
// Use [SetProbe] to replace it with a custom probe, and [GetProbe] to retrieve the current one.
package telemetry

// The singleton probe.
var singleton Probe

// init initializes the singleton probe with a default probe.
// init function will be only called once in runtime regardless of how many times the package is imported.
func init() {
	singleton = NewProbe(
		WithStdoutLogger("info"),
		WithOpenTelemetryMeter(),
	)
}

// GetProbe returns the singleton probe.
func GetProbe() Probe {
	return singleton
}

// SetProbe sets the singleton probe.
func SetProbe(p Probe) {
	singleton = p
}
