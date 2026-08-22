package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/neatplatform/mint/httpx"
	"github.com/neatplatform/mint/telemetry"
)

// serverMetrics holds the metrics for observing incoming HTTP requests on the server side.
type serverMetrics struct {
	panics   metric.Int64Counter
	total    metric.Int64Counter
	inflight metric.Int64UpDownCounter
	duration metric.Int64Histogram
	reqSize  metric.Int64Histogram
	respSize metric.Int64Histogram
}

func newServerMetrics(m metric.Meter) *serverMetrics {
	panics, _ := m.Int64Counter(
		"incoming_http_request_panics_total",
		metric.WithDescription("The total number of panics recovered from http handlers (server-side)"),
	)

	total, _ := m.Int64Counter(
		"incoming_http_requests_total",
		metric.WithDescription("The total number of incoming http requests (server-side)"),
	)

	inflight, _ := m.Int64UpDownCounter(
		"incoming_http_requests_inflight",
		metric.WithDescription("The number of in-flight incoming http requests (server-side)"),
	)

	duration, _ := m.Int64Histogram(
		"incoming_http_request_duration",
		metric.WithUnit("milliseconds"),
		metric.WithDescription("The duration of incoming http requests in milliseconds (server-side)"),
		metric.WithExplicitBucketBoundaries(durationBuckets...),
	)

	reqSize, _ := m.Int64Histogram(
		"incoming_http_request_size",
		metric.WithUnit("bytes"),
		metric.WithDescription("The size of incoming http request bodies in bytes (server-side)"),
		metric.WithExplicitBucketBoundaries(bodySizeBuckets...),
	)

	respSize, _ := m.Int64Histogram(
		"outgoing_http_response_size",
		metric.WithUnit("bytes"),
		metric.WithDescription("The size of outgoing http response bodies in bytes (server-side)"),
		metric.WithExplicitBucketBoundaries(bodySizeBuckets...),
	)

	return &serverMetrics{
		panics:   panics,
		total:    total,
		inflight: inflight,
		duration: duration,
		reqSize:  reqSize,
		respSize: respSize,
	}
}

// middleware creates observable http handlers with logging, metrics, and tracing.
type middleware struct {
	probe   telemetry.Probe
	opts    Options
	metrics *serverMetrics
}

// NewMiddleware creates a new observable http middleware.
func NewMiddleware(probe telemetry.Probe, opts Options) httpx.Middleware {
	return &middleware{
		probe:   probe,
		opts:    opts.withDefaults(),
		metrics: newServerMetrics(probe.Meter()),
	}
}

// Wrap wraps an existing http handler function and returns a new observable handler function.
// This can be used for making http handlers observable via logging, metrics, tracing, etc.
// It also observes and recovers panics that happened inside the inner http handler.
//
// It can also wrap an http.Handler by passing its ServeHTTP method as the argument.
// The resulting http.HandlerFunc satisfies http.Handler, so it can be used wherever a Handler is expected.
func (m *middleware) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		ctx := r.Context()

		name, _ := m.probe.Info()
		side := "server"
		method := r.Method
		path := r.URL.Path

		// Replace UUIDs in the path with a placeholder to keep metric cardinality low.
		route := m.opts.UUIDRegexp.ReplaceAllString(path, "{uuid}")

		// Skip explicitly excluded routes to avoid noise.
		for _, x := range m.opts.ExcludeRoutes {
			if route == x {
				m.callHandlerFunc(ctx, next, w, r)
				return
			}
		}

		serviceAttr := serviceKey.String(name)
		httpMethodAttr := semconv.HTTPRequestMethodKey.String(method)
		httpPathAttr := semconv.URLPathKey.String(path)
		httpRouteAttr := semconv.HTTPRouteKey.String(route)
		endpointOpts := metric.WithAttributes(serviceAttr, httpMethodAttr, httpRouteAttr)

		// Track the number of in-flight requests for this server.
		m.metrics.inflight.Add(ctx, 1, endpointOpts)
		defer m.metrics.inflight.Add(ctx, -1, endpointOpts)

		// Record the incoming request body size.
		reqContentLen := r.ContentLength
		m.metrics.reqSize.Record(ctx, reqContentLen, endpointOpts)

		// Extract the trace context the caller propagated via the incoming request headers into ctx.
		ctx = otel.GetTextMapPropagator().Extract(ctx, headerCarrier(r.Header))

		// Start a new server span as a child of whatever span is already in the context, if any.
		ctx, span := m.probe.Tracer().Start(ctx, "http-server-request",
			trace.WithSpanKind(trace.SpanKindServer),
		)
		defer span.End()

		// Reuse the request UUID from the header if the caller already set one,
		// otherwise generate a new one for this incoming request.
		requestUUID := r.Header.Get(requestUUIDHeader)
		if requestUUID == "" {
			requestUUID = uuid.New().String()
			r.Header.Set(requestUUIDHeader, requestUUID)
		}

		// Reuse the caller name from the header if the caller already set one,
		// otherwise use the default caller name.
		callerName := r.Header.Get(callerNameHeader)
		if callerName == "" {
			callerName = defaultCallerName
			r.Header.Set(callerNameHeader, callerName)
		}

		// Send back request metadata to the client by adding them to the outgoing http response headers.
		w.Header().Set(requestUUIDHeader, requestUUID)
		w.Header().Set(callerNameHeader, callerName)

		// Create a request-scoped logger.
		logger := m.probe.Logger().With(
			"traceId", span.SpanContext().TraceID().String(),
			"spanId", span.SpanContext().SpanID().String(),
			"caller_name", callerName,
			"req_uuid", requestUUID,
			"req_side", side,
			"http_req_method", method,
			"http_req_path", path,
			"http_req_route", route,
		)

		// Augment the request context.
		ctx = telemetry.ContextWithUUID(ctx, requestUUID)
		ctx = telemetry.ContextWithLogger(ctx, logger)
		ctx = telemetry.ContextWithMeter(ctx, m.probe.Meter())
		ctx = telemetry.ContextWithTracer(ctx, m.probe.Tracer())

		// Attach the augmented context to the incoming request.
		r = r.WithContext(ctx)

		// Wrap the response writer to record response status code and content length.
		rw := newResponseWriter(w)

		// Call the http handler.
		span.AddEvent("calling http handler")
		m.callHandlerFunc(ctx, next, rw, r)

		duration := time.Since(startTime).Milliseconds()
		statusCode, respContentLen := rw.StatusCode, rw.BytesWritten
		httpStatusCodeAttr := semconv.HTTPResponseStatusCode(statusCode)
		resultOpts := metric.WithAttributes(serviceAttr, httpMethodAttr, httpRouteAttr, httpStatusCodeAttr)

		// Metrics
		m.metrics.total.Add(ctx, 1, resultOpts)
		m.metrics.duration.Record(ctx, duration, resultOpts)
		m.metrics.respSize.Record(ctx, respContentLen, resultOpts)

		// Logs
		message := fmt.Sprintf("%s %s %d %d ms", method, path, statusCode, duration)
		logKV := []any{
			originLogKey, originLogVal,
			"http_resp_duration_ms", duration,
			"http_resp_status_code", statusCode,
			"http_req_body_size", reqContentLen,
			"http_resp_body_size", respContentLen,
		}

		// Include configured request and response headers.
		logKV = append(logKV, getLoggableHeaderKV("http_req_header_", r.Header, m.opts.LogHeaders)...)
		logKV = append(logKV, getLoggableHeaderKV("http_resp_header_", rw.Header(), m.opts.LogHeaders)...)

		// Determine the log level based on the result.
		switch {
		case statusCode >= 500:
			logger.Error(message, logKV...)
		case statusCode >= 400:
			logger.Warn(message, logKV...)
		default:
			logger.Info(message, logKV...)
		}

		// Update the trace span.
		span.SetAttributes(serviceAttr, httpMethodAttr, httpPathAttr, httpRouteAttr, httpStatusCodeAttr)
		if statusCode >= 500 {
			err := errors.New(http.StatusText(statusCode))
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	}
}

// callHandlerFunc invokes handler and recovers any panic raised inside it,
// so a single failing request cannot take down the whole server.
//
// The recovered panic is observed via logs, metrics, and tracing,
// and turned into a 500 response.
func (m *middleware) callHandlerFunc(ctx context.Context, handler http.HandlerFunc, w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			err := fmt.Errorf("panic: %v", v)

			span := trace.SpanFromContext(ctx)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())

			m.probe.Logger().Errorf("Panic recovered: %v", v)
			m.metrics.panics.Add(ctx, 1)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}()

	handler(w, r)
}

// -------------------------------------------------- Auxiliary Types --------------------------------------------------

// responseWriter extends the standard http.ResponseWriter
// so the middleware can observe the status code and body size of a response.
type responseWriter struct {
	http.ResponseWriter

	StatusCode   int
	BytesWritten int64
}

func newResponseWriter(rw http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: rw,
	}
}

// WriteHeader overrides the standard http.ResponseWriter.WriteHeader to record the status code.
func (r *responseWriter) WriteHeader(statusCode int) {
	r.ResponseWriter.WriteHeader(statusCode)
	if r.StatusCode == 0 {
		r.StatusCode = statusCode
	}
}

// Write overrides the standard http.ResponseWriter.Write to record the response body size.
// It also ensures the default status code 200 is captured when WriteHeader was never called.
func (r *responseWriter) Write(b []byte) (int, error) {
	if r.StatusCode == 0 {
		r.StatusCode = http.StatusOK
	}

	n, err := r.ResponseWriter.Write(b)
	r.BytesWritten += int64(n)

	return n, err
}
