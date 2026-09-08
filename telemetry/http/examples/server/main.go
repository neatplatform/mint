package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/neatplatform/mint/grace"
	"github.com/neatplatform/mint/httpx"
	"github.com/neatplatform/mint/telemetry"

	telehttp "github.com/neatplatform/mint/telemetry/http"
)

func main() {
	ctx := context.Background()

	var addr, metricsAddr string
	flag.StringVar(&addr, "addr", ":8080", "the address to listen on for http traffic")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8081", "the address to listen on for the metrics endpoint")
	flag.Parse()

	// Configure a new probe.
	probe := telemetry.NewProbe(
		telemetry.WithMetadata("echo-service", "v0.1.0", map[string]any{
			"environment": "test",
			"region":      "local",
		}),
		telemetry.WithStdoutLogger("debug"),
		telemetry.WithOpenTelemetryMeter(),
		telemetry.WithOpenTelemetryTracerGRPC("localhost:4317", nil),
	)

	defer func() {
		if err := probe.Close(ctx); err != nil {
			panic(err)
		}
	}()

	// Register the probe as the singleton.
	telemetry.SetProbe(probe)

	// Create a middleware to record observability signals.
	mid := telehttp.NewMiddleware(probe, telehttp.Options{
		ExcludeRoutes: []string{
			"/health",
			"/healthcheck",
		},
		LogHeaders: telehttp.HeaderConfig{
			"Authorization": telehttp.Redact,
			"Tenant-ID":     telehttp.Truncate,
			"Content-Type":  telehttp.Truncate,
			"Served-By":     telehttp.Truncate,
		},
	})

	// Create the HTTP echo server and the HTTP metrics server.
	echoServer := newEchoServer("echo-server", addr, probe.ServeHTTP, mid)
	metricsServer := newMetricsServer("metrics-server", metricsAddr, probe.ServeHTTP)

	// Register the servers so they can be started and shut down gracefully on termination signals.
	grace.SetLogger(probe.Logger())
	grace.RegisterServer(echoServer, metricsServer)

	if code := grace.StartAndWait(); code != 0 {
		os.Exit(code)
	}
}

// -------------------------------------------------- Metrics Server --------------------------------------------------

// MetricsServer is a plain http server exposing the Prometheus metrics endpoint,
// and implements the grace.Server interface so it can be started and gracefully shut down.
type MetricsServer struct {
	*http.Server

	name string
}

// newMetricsServer creates a new MetricsServer listening on addr.
func newMetricsServer(name, addr string, handler http.HandlerFunc) *MetricsServer {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", handler)

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return &MetricsServer{
		Server: server,
		name:   name,
	}
}

// String implements the grace.Server interface.
func (s *MetricsServer) String() string {
	return s.name
}

// -------------------------------------------------- Echo Server --------------------------------------------------

const (
	minResponseDelay = 5 * time.Millisecond
	maxResponseDelay = 50 * time.Millisecond
)

// EchoServer is a production-grade http server that echoes back every request it receives,
// and implements the grace.Server interface so it can be started and gracefully shut down.
type EchoServer struct {
	name string
	*http.Server
}

// newEchoServer creates a new EchoServer listening on addr.
func newEchoServer(name, addr string, metricsHandler http.HandlerFunc, mid httpx.Middleware) *EchoServer {
	ctx, cancel := context.WithCancel(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/", mid.Wrap(echoHandler))

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}

	// RegisterOnShutdown cancels the base context when the server begins shutting down.
	//
	// This is necessary to signal long-lived connections that bypass the normal shutdown path,
	// specifically those that have been hijacked (e.g. WebSocket) or upgraded via ALPN (e.g. HTTP/2).
	// Unlike regular connections, these are not closed automatically by Shutdown;
	// their handlers must observe context cancellation and exit on their own.
	server.RegisterOnShutdown(cancel)

	return &EchoServer{
		name:   name,
		Server: server,
	}
}

// String implements the grace.Server interface.
func (s *EchoServer) String() string {
	return s.name
}

// echoHandler is an http.HandlerFunc wrapped by the telemetry middleware,
// which makes the logger, meter, and tracer available on the request context.
func echoHandler(w http.ResponseWriter, r *http.Request) {
	// Simulate a processing latency so the duration metric will follow a realistic distribution.
	time.Sleep(minResponseDelay + rand.N(maxResponseDelay-minResponseDelay))

	ctx := r.Context()
	logger := telemetry.LoggerFromContext(r.Context())
	tracer := telemetry.TracerFromContext(r.Context())

	_, span := tracer.Start(ctx, "dummy-op")
	defer span.End()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Errorf("failed to read request body: %s", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	defer func() {
		if err := r.Body.Close(); err != nil {
			logger.Errorf("failed to close request body: %s", err)
		}
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Served-By", "http-server-0")

	resp := Response{
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   r.URL.Query(),
		Headers: r.Header,
		Body:    string(body),
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Errorf("failed to encode response: %s", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
}

type Response struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   map[string][]string `json:"query,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
}
