package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"time"

	"github.com/neatplatform/mint/telemetry"

	telehttp "github.com/neatplatform/mint/telemetry/http"
)

const (
	minCallInterval = 500 * time.Millisecond
	maxCallInterval = 5 * time.Second
)

var (
	httpVerbs = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	httpPaths = []string{
		"/icarus",
		"/sisyphus/waffle",
		"/wombat/noodle/sprocket",
		"/pickle/banjo/waffle/elysium/moose",
		"/platypus/gizmo",
		"/snorkel/labyrinth/crumpet/yodel",
		"/bamboozle",
		"/muffin/walrus/gubbins/fandango/tofu",
		"/doohickey/flapjack/squirrel",
		"/pumpernickel/wobble/shenanigan/kamikaze",
	}
)

func main() {
	ctx := context.Background()

	var server string
	flag.StringVar(&server, "server", "http://localhost:8080", "the echo server base address")
	flag.Parse()

	// Configure a new probe.
	probe := telemetry.NewProbe(
		telemetry.WithMetadata("echo-client", "v0.1.0", map[string]any{
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

	// Configure a standard http.Client.
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext:         dialer.DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			MaxConnsPerHost:     0,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Create an http client to record observability signals.
	oc := telehttp.NewClient(client, probe, telehttp.Options{
		LogHeaders: telehttp.HeaderConfig{
			"Authorization": telehttp.Redact,
			"Tenant-ID":     telehttp.Truncate,
			"Content-Type":  telehttp.Truncate,
			"Served-By":     telehttp.Truncate,
		},
	})

	for range 10 {
		// Simulate an interval between calls.
		time.Sleep(minCallInterval + rand.N(maxCallInterval-minCallInterval))

		_ = call(ctx, oc, server)
	}
}

func call(ctx context.Context, client *telehttp.Client, server string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	probe := telemetry.GetProbe()
	logger := probe.Logger()
	tracer := probe.Tracer()

	ctx, span := tracer.Start(ctx, "random-call")
	defer span.End()

	verb := httpVerbs[rand.N(len(httpVerbs))]
	path := httpPaths[rand.N(len(httpPaths))]

	var body io.Reader
	if verb == http.MethodPost || verb == http.MethodPut || verb == http.MethodPatch {
		b, _ := json.Marshal(Request{
			Timestamp: time.Now(),
			Value:     rand.IntN(100),
		})
		body = bytes.NewReader(b)
	}

	req, _ := http.NewRequestWithContext(ctx, verb, server+path, body)

	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Tenant-ID", "internal")

	if verb == http.MethodPost || verb == http.MethodPut || verb == http.MethodPatch {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	resp, err := client.Do(req)
	if err != nil {
		logger.Errorf("error on making request: %s", err)
		return err
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	var respBody Response
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		logger.Errorf("error on decoding response body: %s", err)
		return err
	}

	logger.Debug("server responded.",
		"resp_method", respBody.Method,
		"resp_path", respBody.Path,
	)

	return nil
}

type Request struct {
	Timestamp time.Time `json:"timestamp"`
	Value     int       `json:"value"`
}

type Response struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   map[string][]string `json:"query,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
}
