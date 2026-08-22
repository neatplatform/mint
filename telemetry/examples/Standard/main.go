package main

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/neatplatform/mint/telemetry"
)

const port = ":8080"

func main() {
	ctx := context.Background()

	// Creating a new probe.
	p := telemetry.NewProbe(
		telemetry.WithMetadata("echo-service", "v0.1.0", map[string]any{
			"environment": "test",
			"region":      "local",
		}),
		telemetry.WithStdoutLogger("info"),
		telemetry.WithOpenTelemetryMeter(),
	)

	defer func() {
		if err := p.Close(ctx); err != nil {
			p.Logger().Errorf("error closing probe: %s", err)
		}
	}()

	// Setting the probe as the singleton instance.
	telemetry.SetProbe(p)

	srv := newEchoServer(port, p)

	p.Logger().Infof("starting http server on %s ...", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		p.Logger().Errorf("http server error: %s", err)
	}
}

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second

	minResponseDelay = 5 * time.Millisecond
	maxResponseDelay = 50 * time.Millisecond
)

// echoServer is a simple http server that echoes back the method, url, query, and body of every request.
type echoServer struct {
	*http.Server

	probe   telemetry.Probe
	metrics *metrics
}

type metrics struct {
	reqCounter  metric.Int64Counter
	reqDuration metric.Float64Histogram
}

func newEchoServer(addr string, p telemetry.Probe) *echoServer {
	s := &echoServer{
		probe:   p,
		metrics: newMetrics(p.Meter()),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", p.ServeHTTP)
	mux.HandleFunc("/", s.handle)

	s.Server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	return s
}

func newMetrics(m metric.Meter) *metrics {
	reqCounter, _ := m.Int64Counter("http_requests_total", metric.WithDescription("the total number of requests"))
	reqDuration, _ := m.Float64Histogram("http_request_duration",
		metric.WithUnit("milliseconds"),
		metric.WithDescription("the duration of requests in milliseconds"),
		metric.WithExplicitBucketBoundaries(1, 2, 5, 10, 15, 20, 25, 30, 40, 50, 75, 100, 250, 500, 1000),
	)

	return &metrics{
		reqCounter:  reqCounter,
		reqDuration: reqDuration,
	}
}

func (s *echoServer) handle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	start := time.Now()
	status := s.echo(w, r)
	duration := time.Since(start)

	attrs := []attribute.KeyValue{
		semconv.HTTPRequestMethodKey.String(r.Method),
		semconv.URLPathKey.String(r.URL.Path),
		semconv.HTTPResponseStatusCodeKey.Int(status),
	}

	// Metrics
	opts := metric.WithAttributes(attrs...)
	s.metrics.reqCounter.Add(ctx, 1, opts)
	s.metrics.reqDuration.Record(ctx, float64(duration.Microseconds())/1000, opts)

	// Logging
	logKV := []any{
		"req_method", r.Method,
		"req_path", r.URL.Path,
		"resp_status", status,
	}

	switch {
	case status >= 500:
		s.probe.Logger().Error("Request failed with server error.", logKV...)
	case status >= 400:
		s.probe.Logger().Warn("Request failed with client error.", logKV...)
	default:
		s.probe.Logger().Info("Request succeeded.", logKV...)
	}
}

func (s *echoServer) echo(w http.ResponseWriter, r *http.Request) int {
	// Simulate a processing latency so the duration metric will follow a realistic distribution.
	time.Sleep(minResponseDelay + rand.N(maxResponseDelay-minResponseDelay))

	method := r.Method
	url := r.URL.Path
	query := r.URL.RawQuery

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.probe.Logger().Error("Error on reading request body.", "error", err)

		w.WriteHeader(http.StatusInternalServerError)
		return http.StatusInternalServerError
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	_, _ = fmt.Fprintf(w,
		"method: %s\nurl:    %s\nquery:  %s\nbody:   %s\n",
		method, url, query, string(body),
	)

	return http.StatusOK
}
