package http

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"uuid"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/neatplatform/mint/telemetry"
)

// clientMetrics holds the metrics for observing outgoing HTTP requests on the client side.
type clientMetrics struct {
	total    metric.Int64Counter
	inflight metric.Int64UpDownCounter
	duration metric.Int64Histogram
	reqSize  metric.Int64Histogram
	respSize metric.Int64Histogram
}

func newClientMetrics(m metric.Meter) *clientMetrics {
	total, _ := m.Int64Counter(
		"outgoing_http_requests_total",
		metric.WithDescription("The total number of outgoing http requests (client-side)"),
	)

	inflight, _ := m.Int64UpDownCounter(
		"outgoing_http_requests_inflight",
		metric.WithDescription("The number of in-flight outgoing http requests (client-side)"),
	)

	duration, _ := m.Int64Histogram(
		"outgoing_http_request_duration",
		metric.WithUnit("milliseconds"),
		metric.WithDescription("The duration of outgoing http requests in milliseconds (client-side)"),
		metric.WithExplicitBucketBoundaries(durationBuckets...),
	)

	reqSize, _ := m.Int64Histogram(
		"outgoing_http_request_size",
		metric.WithUnit("bytes"),
		metric.WithDescription("The size of outgoing http request bodies in bytes (client-side)"),
		metric.WithExplicitBucketBoundaries(bodySizeBuckets...),
	)

	respSize, _ := m.Int64Histogram(
		"incoming_http_response_size",
		metric.WithUnit("bytes"),
		metric.WithDescription("The size of incoming http response bodies in bytes (client-side)"),
		metric.WithExplicitBucketBoundaries(bodySizeBuckets...),
	)

	return &clientMetrics{
		total:    total,
		inflight: inflight,
		duration: duration,
		reqSize:  reqSize,
		respSize: respSize,
	}
}

// Client is a drop-in replacement for the standard http.Client.
// It is an observable http client with logging, metrics, and tracing.
type Client struct {
	client  *http.Client
	probe   telemetry.Probe
	opts    Options
	metrics *clientMetrics
}

// NewClient creates a new observable http client.
func NewClient(client *http.Client, probe telemetry.Probe, opts Options) *Client {
	return &Client{
		client:  client,
		probe:   probe,
		opts:    opts.withDefaults(),
		metrics: newClientMetrics(probe.Meter()),
	}
}

// CloseIdleConnections is the observable counterpart of standard http Client.CloseIdleConnections.
func (c *Client) CloseIdleConnections() {
	c.client.CloseIdleConnections()
}

// Get is the observable counterpart of standard http Client.Get.
//
// It reuses the request UUID already present in req's context if any, otherwise it generates a new one.
// The request UUID, the client name, and the trace context are all propagated via outgoing request headers.
func (c *Client) Get(url string) (resp *http.Response, err error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	return c.Do(req)
}

// Head is the observable counterpart of standard http Client.Head.
//
// It reuses the request UUID already present in req's context if any, otherwise it generates a new one.
// The request UUID, the client name, and the trace context are all propagated via outgoing request headers.
func (c *Client) Head(url string) (resp *http.Response, err error) {
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return nil, err
	}

	return c.Do(req)
}

// Post is the observable counterpart of standard http Client.Post.
//
// It reuses the request UUID already present in req's context if any, otherwise it generates a new one.
// The request UUID, the client name, and the trace context are all propagated via outgoing request headers.
func (c *Client) Post(url, contentType string, body io.Reader) (resp *http.Response, err error) {
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", contentType)

	return c.Do(req)
}

// PostForm is the observable counterpart of standard http Client.PostForm.
//
// It reuses the request UUID already present in req's context if any, otherwise it generates a new one.
// The request UUID, the client name, and the trace context are all propagated via outgoing request headers.
func (c *Client) PostForm(url string, data url.Values) (resp *http.Response, err error) {
	contentType := "application/x-www-form-urlencoded"
	body := strings.NewReader(data.Encode())
	return c.Post(url, contentType, body)
}

// Do is the observable counterpart of standard http Client.Do.
//
// It reuses the request UUID already present in req's context if any, otherwise it generates a new one.
// The request UUID, the client name, and the trace context are all propagated via outgoing request headers.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	startTime := time.Now()
	ctx := req.Context()

	side := "client"
	method := req.Method
	path := req.URL.Path

	// Replace UUIDs in the path with a placeholder to keep metric cardinality low.
	route := c.opts.UUIDRegexp.ReplaceAllString(path, "{uuid}")

	// Skip explicitly excluded routes to avoid noise.
	for _, x := range c.opts.ExcludeRoutes {
		if route == x {
			return c.client.Do(req)
		}
	}

	methodAttr := semconv.HTTPRequestMethodKey.String(method)
	pathAttr := semconv.URLPathKey.String(path)
	routeAttr := semconv.HTTPRouteKey.String(route)
	endpointOpts := metric.WithAttributes(methodAttr, routeAttr)

	// Track the number of in-flight requests for this client.
	c.metrics.inflight.Add(ctx, 1, endpointOpts)
	defer c.metrics.inflight.Add(ctx, -1, endpointOpts)

	// Record the outgoing request body size.
	reqContentLen := req.ContentLength
	c.metrics.reqSize.Record(ctx, reqContentLen, endpointOpts)

	// Start a new client span as a child of whatever span is already in the context, if any.
	ctx, span := c.probe.Tracer().Start(ctx, "http-client-request",
		trace.WithSpanKind(trace.SpanKindClient),
	)
	defer span.End()

	// Reuse the request UUID from the context if the caller already set one,
	// otherwise generate a new one for this outgoing request.
	requestUUID, ok := telemetry.UUIDFromContext(ctx)
	if !ok || requestUUID == "" {
		requestUUID = uuid.New().String()
	}

	// Construct a name for the caller.
	var callerName string
	name, version := c.probe.Info()
	if name != "" && version != "" {
		callerName = fmt.Sprintf("%s/%s", name, version)
	} else if name != "" {
		callerName = name
	} else {
		callerName = defaultCallerName
	}

	// Propagate request metadata to the server by adding them to the outgoing http request headers.
	req.Header.Set(requestUUIDHeader, requestUUID)
	req.Header.Set(callerNameHeader, callerName)

	// Inject the trace context from ctx into the outgoing request headers,
	// so the server can continue the trace.
	otel.GetTextMapPropagator().Inject(ctx, headerCarrier(req.Header))

	// Attach the span-augmented context to the outgoing request so it can be propagated.
	req = req.WithContext(ctx)

	// Make the http call.
	span.AddEvent("making http call")
	resp, err := c.client.Do(req)

	duration := time.Since(startTime).Milliseconds()

	var statusCode int
	var respHeader http.Header
	var respContentLen int64

	if err == nil {
		statusCode = resp.StatusCode
		respHeader = resp.Header
		respContentLen = resp.ContentLength
	} else {
		// The round trip failed before a response was received.
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	statusCodeAttr := semconv.HTTPResponseStatusCode(statusCode)
	resultOpts := metric.WithAttributes(methodAttr, routeAttr, statusCodeAttr)

	// Metrics
	c.metrics.total.Add(ctx, 1, resultOpts)
	c.metrics.duration.Record(ctx, duration, resultOpts)
	c.metrics.respSize.Record(ctx, respContentLen, resultOpts)

	// Logs
	message := fmt.Sprintf("%s %s %d %d ms", method, path, statusCode, duration)
	logKV := []any{
		"traceId", span.SpanContext().TraceID().String(),
		"spanId", span.SpanContext().SpanID().String(),
		originLogKey, originLogVal,
		"caller_name", callerName,
		"req_uuid", requestUUID,
		"req_side", side,
		"http_req_method", method,
		"http_req_path", path,
		"http_req_route", route,
		"http_resp_duration_ms", duration,
		"http_resp_status_code", statusCode,
		"http_req_body_size", reqContentLen,
		"http_resp_body_size", respContentLen,
	}

	// Include configured request and response headers.
	logKV = append(logKV, getLoggableHeaderKV("http_req_header_", req.Header, c.opts.LogHeaders)...)
	logKV = append(logKV, getLoggableHeaderKV("http_resp_header_", respHeader, c.opts.LogHeaders)...)

	if err != nil {
		logKV = append(logKV, "error", err.Error())
	}

	switch {
	case err != nil, statusCode >= 500:
		c.probe.Logger().Error(message, logKV...)
	case statusCode >= 400:
		c.probe.Logger().Warn(message, logKV...)
	default:
		c.probe.Logger().Info(message, logKV...)
	}

	// Update the trace span.
	span.SetAttributes(methodAttr, pathAttr, routeAttr, statusCodeAttr)
	if statusCode >= 500 {
		err := errors.New(http.StatusText(statusCode))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return resp, err
}
