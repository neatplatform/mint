package grpc

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/neatplatform/mint/telemetry"
)

// serverMetrics holds the metrics for observing incoming gRPC requests on the server side.
type serverMetrics struct {
	panics       metric.Int64Counter
	total        metric.Int64Counter
	inflight     metric.Int64UpDownCounter
	duration     metric.Int64Histogram
	reqSize      metric.Int64Histogram
	respSize     metric.Int64Histogram
	reqMsgCount  metric.Int64Histogram
	respMsgCount metric.Int64Histogram
}

func newServerMetrics(m metric.Meter) *serverMetrics {
	panics, _ := m.Int64Counter(
		"incoming_grpc_request_panics_total",
		metric.WithDescription("The total number of panics recovered from grpc handlers (server-side)"),
	)

	total, _ := m.Int64Counter(
		"incoming_grpc_requests_total",
		metric.WithDescription("The total number of incoming grpc requests (server-side)"),
	)

	inflight, _ := m.Int64UpDownCounter(
		"incoming_grpc_requests_inflight",
		metric.WithDescription("The number of in-flight incoming grpc requests (server-side)"),
	)

	duration, _ := m.Int64Histogram(
		"incoming_grpc_request_duration",
		metric.WithUnit("milliseconds"),
		metric.WithDescription("The duration of incoming grpc requests in milliseconds (server-side)"),
		metric.WithExplicitBucketBoundaries(durationBuckets...),
	)

	reqSize, _ := m.Int64Histogram(
		"incoming_grpc_request_size",
		metric.WithUnit("bytes"),
		metric.WithDescription("The size of incoming grpc request messages in bytes (server-side)"),
		metric.WithExplicitBucketBoundaries(messageSizeBuckets...),
	)

	respSize, _ := m.Int64Histogram(
		"outgoing_grpc_response_size",
		metric.WithUnit("bytes"),
		metric.WithDescription("The size of outgoing grpc response messages in bytes (server-side)"),
		metric.WithExplicitBucketBoundaries(messageSizeBuckets...),
	)

	reqMsgCount, _ := m.Int64Histogram(
		"incoming_grpc_request_messages_total",
		metric.WithDescription("The number of messages received from the client per grpc call (server-side)"),
		metric.WithExplicitBucketBoundaries(messageCountBuckets...),
	)

	respMsgCount, _ := m.Int64Histogram(
		"outgoing_grpc_response_messages_total",
		metric.WithDescription("The number of messages sent to the client per grpc call (server-side)"),
		metric.WithExplicitBucketBoundaries(messageCountBuckets...),
	)

	return &serverMetrics{
		panics:       panics,
		total:        total,
		inflight:     inflight,
		duration:     duration,
		reqSize:      reqSize,
		respSize:     respSize,
		reqMsgCount:  reqMsgCount,
		respMsgCount: respMsgCount,
	}
}

// ServerInterceptor creates observable interceptors with logging, metrics, and tracing for gRPC servers.
type ServerInterceptor struct {
	probe   telemetry.Probe
	opts    Options
	metrics *serverMetrics
}

// NewServerInterceptor creates a new observable gRPC server interceptor.
func NewServerInterceptor(probe telemetry.Probe, opts Options) *ServerInterceptor {
	return &ServerInterceptor{
		probe:   probe,
		opts:    opts.withDefaults(),
		metrics: newServerMetrics(probe.Meter()),
	}
}

// Unary returns a grpc.UnaryServerInterceptor that can be registered with grpc.NewServer
// (via grpc.UnaryInterceptor or grpc.ChainUnaryInterceptor) to observe unary gRPC calls.
//
// The returned function observes a single unary RPC with logging, metrics, and tracing,
// and recovers any panic raised inside the handler so it cannot crash the server.
func (i *ServerInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		startTime := time.Now()

		name, _ := i.probe.Info()
		side := "server"
		stream := false

		pack, service, method, ok := parseFullMethod(info.FullMethod)
		if !ok {
			return i.callUnaryHandler(ctx, handler, req)
		}

		// Skip explicitly excluded methods to avoid noise.
		for _, x := range i.opts.ExcludeMethods {
			if method == x {
				return i.callUnaryHandler(ctx, handler, req)
			}
		}

		serviceAttr := serviceKey.String(name)
		grpcStreamAttr := gRPCStreamKey.Bool(stream)
		grpcPackageAttr := gRPCRequestPackageKey.String(pack)
		grpcServiceAttr := gRPCRequestServiceKey.String(service)
		grpcMethodAttr := gRPCRequestMethodKey.String(method)
		endpointOpts := metric.WithAttributes(serviceAttr, grpcStreamAttr, grpcPackageAttr, grpcServiceAttr, grpcMethodAttr)

		// Track the number of in-flight requests for this server.
		i.metrics.inflight.Add(ctx, 1, endpointOpts)
		defer i.metrics.inflight.Add(ctx, -1, endpointOpts)

		// Record the incoming request message size and count.
		// A unary RPC always receives exactly one request message.
		reqMsgSize := getMessageSize(req)
		i.metrics.reqSize.Record(ctx, reqMsgSize, endpointOpts)
		i.metrics.reqMsgCount.Record(ctx, 1, endpointOpts)

		// Get the incoming metadata from context, if any.
		// FromIncomingContext returns a copy, so it must be reattached to ctx after mutation,
		// regardless of whether metadata was already present.
		//
		// A server interceptor runs first: the gRPC runtime routes every incoming call
		// through registered interceptors, and it is this code that decides when to call the handler.
		// So FromIncomingContext below sees the metadata as of this point in the interceptor chain.
		// Any mutations made here are visible to the handler and to interceptors further down the chain.
		reqMD, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			reqMD = metadata.New(nil)
			ctx = metadata.NewIncomingContext(ctx, reqMD)
		}

		// Extract the trace context the caller propagated via the incoming request metadata into ctx.
		ctx = otel.GetTextMapPropagator().Extract(ctx, metadataCarrier(reqMD))

		// Start a new server span as a child of whatever span is already in the context, if any.
		ctx, span := i.probe.Tracer().Start(ctx, "grpc-server-unary-request",
			trace.WithSpanKind(trace.SpanKindServer),
		)
		defer span.End()

		// Reuse the request UUID from the metadata if the caller already sent one,
		// otherwise generate a new UUID and store it in the metadata.
		var requestUUID string
		if vals := reqMD.Get(requestUUIDKey); len(vals) > 0 {
			requestUUID = vals[0]
		}

		if requestUUID == "" {
			requestUUID = uuid.New().String()
			reqMD.Set(requestUUIDKey, requestUUID)
		}

		// Reuse the caller name from the metadata if the caller already sent one,
		// otherwise use the default caller name and store it in the metadata.
		var callerName string
		if vals := reqMD.Get(callerNameKey); len(vals) > 0 {
			callerName = vals[0]
		}

		if callerName == "" {
			callerName = defaultCallerName
			reqMD.Set(callerNameKey, callerName)
		}

		// Update the context after mutating the metadata.
		ctx = metadata.NewIncomingContext(ctx, reqMD)

		// Create a request-scoped logger.
		logger := i.probe.Logger().With(
			"traceId", span.SpanContext().TraceID().String(),
			"spanId", span.SpanContext().SpanID().String(),
			"caller_name", callerName,
			"req_uuid", requestUUID,
			"req_side", side,
			"grpc_req_stream", stream,
			"grpc_req_package", pack,
			"grpc_req_service", service,
			"grpc_req_method", method,
		)

		// Augment the request context.
		ctx = telemetry.ContextWithUUID(ctx, requestUUID)
		ctx = telemetry.ContextWithLogger(ctx, logger)
		ctx = telemetry.ContextWithMeter(ctx, i.probe.Meter())
		ctx = telemetry.ContextWithTracer(ctx, i.probe.Tracer())

		// Wrap the server transport stream already stored in ctx by the gRPC runtime,
		// so we can capture the header and trailer metadata sent to the client.
		var os *observableServerTransportStream
		if s := grpc.ServerTransportStreamFromContext(ctx); s != nil {
			os = newObservableServerTransportStream(s)
			ctx = grpc.NewContextWithServerTransportStream(ctx, os)
		}

		// Send back request metadata to the client by adding them to the outgoing gRPC response header.
		_ = grpc.SetHeader(ctx, metadata.Pairs(
			requestUUIDKey, requestUUID,
			callerNameKey, callerName,
		))

		// Call the grpc unary handler.
		span.AddEvent("calling grpc unary handler")
		res, err := i.callUnaryHandler(ctx, handler, req)

		duration := time.Since(startTime).Milliseconds()
		code := grpcstatus.Code(err)
		grpcCodeAttr := gRPCCodeKey.Int(int(code))
		resultOpts := metric.WithAttributes(serviceAttr, grpcStreamAttr, grpcPackageAttr, grpcServiceAttr, grpcMethodAttr, grpcCodeAttr)

		var respMsgSize, respMsgCount int64
		if err == nil {
			respMsgSize = getMessageSize(res)
			respMsgCount = 1 // A unary RPC sends exactly one response message, but only on success.
		}

		// Gather the header and trailer metadata that were set/sent to the client.
		var headerMD, trailerMD metadata.MD
		if os != nil {
			headerMD, trailerMD = os.Header(), os.Trailer()
		}

		// Metrics
		i.metrics.total.Add(ctx, 1, resultOpts)
		i.metrics.duration.Record(ctx, duration, resultOpts)
		i.metrics.respSize.Record(ctx, respMsgSize, resultOpts)
		i.metrics.respMsgCount.Record(ctx, respMsgCount, resultOpts)

		// Logs
		message := fmt.Sprintf("%s %s::%s::%s %d ms", side, pack, service, method, duration)
		logKV := []any{
			originLogKey, originLogVal,
			"grpc_resp_duration_ms", duration,
			"grpc_code", code,
			"grpc_req_message_size", reqMsgSize,
			"grpc_resp_message_size", respMsgSize,
		}

		// Include configured request and response headers.
		logKV = append(logKV, getLoggableMetadataKV("grpc_req_metadata_", reqMD, i.opts.LogMetadata)...)
		logKV = append(logKV, getLoggableMetadataKV("grpc_resp_header_", headerMD, i.opts.LogMetadata)...)
		logKV = append(logKV, getLoggableMetadataKV("grpc_resp_trailer_", trailerMD, i.opts.LogMetadata)...)

		// Determine the log level based on the result.
		if err == nil {
			logger.Info(message, logKV...)
		} else {
			logKV = append(logKV, "grpc_error", err.Error())
			logger.Error(message, logKV...)
		}

		// Update the trace span.
		span.SetAttributes(serviceAttr, grpcStreamAttr, grpcPackageAttr, grpcServiceAttr, grpcMethodAttr, grpcCodeAttr)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}

		return res, err
	}
}

// callUnaryHandler invokes handler and recovers any panic raised inside it,
// so a single failing request cannot take down the whole server.
//
// The recovered panic is observed via logs, metrics, and tracing,
// and turned into a gRPC Internal error.
func (i *ServerInterceptor) callUnaryHandler(ctx context.Context, handler grpc.UnaryHandler, req any) (resp any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = grpcstatus.Errorf(grpccodes.Internal, "panic: %v", r)

			span := trace.SpanFromContext(ctx)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())

			i.probe.Logger().Errorf("Panic recovered: %v", r)
			i.metrics.panics.Add(ctx, 1)
		}
	}()

	return handler(ctx, req)
}

// Stream returns a grpc.StreamServerInterceptor that can be registered with grpc.NewServer
// (via grpc.StreamInterceptor or grpc.ChainStreamInterceptor) to observe streaming gRPC calls.
//
// The returned function observes a single streaming RPC with logging, metrics, and tracing,
// and recovers any panic raised inside the handler so it cannot crash the server.
func (i *ServerInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		startTime := time.Now()
		ctx := ss.Context()

		name, _ := i.probe.Info()
		side := "server"
		stream := true

		pack, service, method, ok := parseFullMethod(info.FullMethod)
		if !ok {
			return i.callStreamHandler(ctx, handler, srv, ss)
		}

		// Skip explicitly excluded methods to avoid noise.
		for _, x := range i.opts.ExcludeMethods {
			if method == x {
				return i.callStreamHandler(ctx, handler, srv, ss)
			}
		}

		serviceAttr := serviceKey.String(name)
		grpcStreamAttr := gRPCStreamKey.Bool(stream)
		grpcPackageAttr := gRPCRequestPackageKey.String(pack)
		grpcServiceAttr := gRPCRequestServiceKey.String(service)
		grpcMethodAttr := gRPCRequestMethodKey.String(method)
		endpointOpts := metric.WithAttributes(serviceAttr, grpcStreamAttr, grpcPackageAttr, grpcServiceAttr, grpcMethodAttr)

		// Track the number of in-flight requests for this server.
		i.metrics.inflight.Add(ctx, 1, endpointOpts)
		defer i.metrics.inflight.Add(ctx, -1, endpointOpts)

		// Get the incoming metadata from context, if any.
		// FromIncomingContext returns a copy, so it must be reattached to ctx after mutation,
		// regardless of whether metadata was already present.
		//
		// A server interceptor runs first: the gRPC runtime routes every incoming call
		// through registered interceptors, and it is this code that decides when to call the handler.
		// So FromIncomingContext below sees the metadata as of this point in the interceptor chain.
		// Any mutations made here are visible to the handler and to interceptors further down the chain.
		reqMD, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			reqMD = metadata.New(nil)
		}

		// Extract the trace context the caller propagated via the incoming request metadata into ctx.
		ctx = otel.GetTextMapPropagator().Extract(ctx, metadataCarrier(reqMD))

		// Start a new server span as a child of whatever span is already in the context, if any.
		ctx, span := i.probe.Tracer().Start(ctx, "grpc-server-stream-request",
			trace.WithSpanKind(trace.SpanKindServer),
		)
		defer span.End()

		// Reuse the request UUID from the metadata if the caller already sent one,
		// otherwise generate a new UUID and store it in the metadata.
		var requestUUID string
		if vals := reqMD.Get(requestUUIDKey); len(vals) > 0 {
			requestUUID = vals[0]
		}

		if requestUUID == "" {
			requestUUID = uuid.New().String()
			reqMD.Set(requestUUIDKey, requestUUID)
		}

		// Reuse the caller name from the metadata if the caller already sent one,
		// otherwise use the default caller name and store it in the metadata.
		var callerName string
		if vals := reqMD.Get(callerNameKey); len(vals) > 0 {
			callerName = vals[0]
		}

		if callerName == "" {
			callerName = defaultCallerName
			reqMD.Set(callerNameKey, callerName)
		}

		// Update the context after mutating the metadata.
		ctx = metadata.NewIncomingContext(ctx, reqMD)

		// Create a request-scoped logger.
		logger := i.probe.Logger().With(
			"traceId", span.SpanContext().TraceID().String(),
			"spanId", span.SpanContext().SpanID().String(),
			"caller_name", callerName,
			"req_uuid", requestUUID,
			"req_side", side,
			"grpc_req_stream", stream,
			"grpc_req_package", pack,
			"grpc_req_service", service,
			"grpc_req_method", method,
		)

		// Augment the request context.
		ctx = telemetry.ContextWithUUID(ctx, requestUUID)
		ctx = telemetry.ContextWithLogger(ctx, logger)
		ctx = telemetry.ContextWithMeter(ctx, i.probe.Meter())
		ctx = telemetry.ContextWithTracer(ctx, i.probe.Tracer())

		// Wrap the stream to count and size the messages exchanged with the client,
		// and to capture the header and trailer metadata sent to the client.
		oss := newObservableServerStream(ctx, ss, i.metrics, endpointOpts)

		// Send back request metadata to the client by adding them to the outgoing gRPC response header.
		_ = oss.SetHeader(metadata.Pairs(
			requestUUIDKey, requestUUID,
			callerNameKey, callerName,
		))

		// Call the grpc stream handler.
		span.AddEvent("calling grpc stream handler")
		err := i.callStreamHandler(ctx, handler, srv, oss)

		duration := time.Since(startTime).Milliseconds()
		code := grpcstatus.Code(err)
		grpcCodeAttr := gRPCCodeKey.Int(int(code))
		resultOpts := metric.WithAttributes(serviceAttr, grpcStreamAttr, grpcPackageAttr, grpcServiceAttr, grpcMethodAttr, grpcCodeAttr)

		// Metrics
		i.metrics.total.Add(ctx, 1, resultOpts)
		i.metrics.duration.Record(ctx, duration, resultOpts)
		i.metrics.reqMsgCount.Record(ctx, oss.reqMsgCount.Load(), resultOpts)
		i.metrics.respMsgCount.Record(ctx, oss.respMsgCount.Load(), resultOpts)

		// Logs
		message := fmt.Sprintf("%s %s::%s::%s %d ms", side, pack, service, method, duration)
		logKV := []any{
			originLogKey, originLogVal,
			"grpc_resp_duration_ms", duration,
			"grpc_code", code,
			"grpc_req_message_count", oss.reqMsgCount.Load(),
			"grpc_resp_message_count", oss.respMsgCount.Load(),
		}

		// Include configured request and response headers.
		logKV = append(logKV, getLoggableMetadataKV("grpc_req_metadata_", reqMD, i.opts.LogMetadata)...)
		logKV = append(logKV, getLoggableMetadataKV("grpc_resp_header_", oss.Header(), i.opts.LogMetadata)...)
		logKV = append(logKV, getLoggableMetadataKV("grpc_resp_trailer_", oss.Trailer(), i.opts.LogMetadata)...)

		// Determine the log level based on the result.
		if err == nil {
			logger.Info(message, logKV...)
		} else {
			logKV = append(logKV, "grpc_error", err.Error())
			logger.Error(message, logKV...)
		}

		// Update the trace span.
		span.SetAttributes(serviceAttr, grpcStreamAttr, grpcPackageAttr, grpcServiceAttr, grpcMethodAttr, grpcCodeAttr)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}

		return err
	}
}

// callStreamHandler invokes handler and recovers any panic raised inside it,
// so a single failing request cannot take down the whole server.
//
// The recovered panic is observed via logs, metrics, and tracing,
// and turned into a gRPC Internal error.
func (i *ServerInterceptor) callStreamHandler(ctx context.Context, handler grpc.StreamHandler, srv any, stream grpc.ServerStream) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = grpcstatus.Errorf(grpccodes.Internal, "panic: %v", r)

			span := trace.SpanFromContext(ctx)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())

			i.probe.Logger().Errorf("Panic recovered: %v", r)
			i.metrics.panics.Add(ctx, 1)
		}
	}()

	return handler(srv, stream)
}

// -------------------------------------------------- Auxiliary Types --------------------------------------------------

// observableServerStream wraps a grpc.ServerStream to make streaming RPCs observable.
// It allows overriding the stream's context with an augmented context.
// It intercepts SendHeader, SetHeader, and SetTrailer to capture the header and trailer metadata
// sent to the client, and it intercepts SendMsg and RecvMsg to count messages and record their sizes.
type observableServerStream struct {
	grpc.ServerStream

	ctx     context.Context
	metrics *serverMetrics
	opts    metric.MeasurementOption

	mu           sync.Mutex
	headerMD     metadata.MD
	trailerMD    metadata.MD
	reqMsgCount  atomic.Int64
	respMsgCount atomic.Int64
}

func newObservableServerStream(
	ctx context.Context,
	ss grpc.ServerStream,
	metrics *serverMetrics,
	opts metric.MeasurementOption,
) *observableServerStream {
	// If ss is already wrapped, reuse it instead of double-wrapping.
	s, ok := ss.(*observableServerStream)
	if !ok {
		s = &observableServerStream{ServerStream: ss}
	}

	s.ctx = ctx
	s.metrics = metrics
	s.opts = opts

	return s
}

func (s *observableServerStream) Context() context.Context {
	if s.ctx == nil {
		return s.ServerStream.Context()
	}

	return s.ctx
}

func (s *observableServerStream) SetHeader(md metadata.MD) error {
	if err := s.ServerStream.SetHeader(md); err != nil {
		return err
	}

	s.mu.Lock()
	s.headerMD = metadata.Join(s.headerMD, md)
	s.mu.Unlock()

	return nil
}

func (s *observableServerStream) SendHeader(md metadata.MD) error {
	if err := s.ServerStream.SendHeader(md); err != nil {
		return err
	}

	s.mu.Lock()
	s.headerMD = metadata.Join(s.headerMD, md)
	s.mu.Unlock()

	return nil
}

func (s *observableServerStream) SetTrailer(md metadata.MD) {
	s.ServerStream.SetTrailer(md)

	s.mu.Lock()
	s.trailerMD = metadata.Join(s.trailerMD, md)
	s.mu.Unlock()
}

func (s *observableServerStream) SendMsg(msg any) error {
	if err := s.ServerStream.SendMsg(msg); err != nil {
		return err
	}

	s.respMsgCount.Add(1)
	s.metrics.respSize.Record(s.Context(), getMessageSize(msg), s.opts)

	return nil
}

func (s *observableServerStream) RecvMsg(msg any) error {
	if err := s.ServerStream.RecvMsg(msg); err != nil {
		return err
	}

	s.reqMsgCount.Add(1)
	s.metrics.reqSize.Record(s.Context(), getMessageSize(msg), s.opts)

	return nil
}

// Header returns a copy of all header metadata set/sent to the client.
func (s *observableServerStream) Header() metadata.MD {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.headerMD.Copy()
}

// Trailer returns a copy of all trailer metadata set/sent to the client.
func (s *observableServerStream) Trailer() metadata.MD {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.trailerMD.Copy()
}

// observableServerTransportStream wraps a grpc.ServerTransportStream
// to capture the header and trailer metadata sent to the client during a unary RPC.
// It intercepts SetHeader, SendHeader, and SetTrailer, the same calls the grpc.SetHeader, grpc.SendHeader,
// and grpc.SetTrailer package functions make against the transport stream stored in the context.
type observableServerTransportStream struct {
	grpc.ServerTransportStream

	mu        sync.Mutex
	headerMD  metadata.MD
	trailerMD metadata.MD
}

func newObservableServerTransportStream(sts grpc.ServerTransportStream) *observableServerTransportStream {
	// If sts is already wrapped, reuse it instead of double-wrapping.
	s, ok := sts.(*observableServerTransportStream)
	if !ok {
		s = &observableServerTransportStream{ServerTransportStream: sts}
	}

	return s
}

func (s *observableServerTransportStream) SetHeader(md metadata.MD) error {
	if err := s.ServerTransportStream.SetHeader(md); err != nil {
		return err
	}

	s.mu.Lock()
	s.headerMD = metadata.Join(s.headerMD, md)
	s.mu.Unlock()

	return nil
}

func (s *observableServerTransportStream) SendHeader(md metadata.MD) error {
	if err := s.ServerTransportStream.SendHeader(md); err != nil {
		return err
	}

	s.mu.Lock()
	s.headerMD = metadata.Join(s.headerMD, md)
	s.mu.Unlock()

	return nil
}

func (s *observableServerTransportStream) SetTrailer(md metadata.MD) error {
	if err := s.ServerTransportStream.SetTrailer(md); err != nil {
		return err
	}

	s.mu.Lock()
	s.trailerMD = metadata.Join(s.trailerMD, md)
	s.mu.Unlock()

	return nil
}

// Header returns a copy of all header metadata set/sent to the client.
func (s *observableServerTransportStream) Header() metadata.MD {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.headerMD.Copy()
}

// Trailer returns a copy of all trailer metadata set/sent to the client.
func (s *observableServerTransportStream) Trailer() metadata.MD {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.trailerMD.Copy()
}
