package grpc

import (
	"context"
	"fmt"
	"io"
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

	grpcstatus "google.golang.org/grpc/status"

	"github.com/neatplatform/mint/telemetry"
)

// clientMetrics holds the metrics for observing outgoing gRPC requests on the client side.
type clientMetrics struct {
	total        metric.Int64Counter
	inflight     metric.Int64UpDownCounter
	duration     metric.Int64Histogram
	reqSize      metric.Int64Histogram
	respSize     metric.Int64Histogram
	reqMsgCount  metric.Int64Histogram
	respMsgCount metric.Int64Histogram
}

func newClientMetrics(m metric.Meter) *clientMetrics {
	total, _ := m.Int64Counter(
		"outgoing_grpc_requests_total",
		metric.WithDescription("The total number of outgoing grpc requests (client-side)"),
	)

	inflight, _ := m.Int64UpDownCounter(
		"outgoing_grpc_requests_inflight",
		metric.WithDescription("The number of in-flight outgoing grpc requests (client-side)"),
	)

	duration, _ := m.Int64Histogram(
		"outgoing_grpc_request_duration",
		metric.WithUnit("milliseconds"),
		metric.WithDescription("The duration of outgoing grpc requests in milliseconds (client-side)"),
		metric.WithExplicitBucketBoundaries(durationBuckets...),
	)

	reqSize, _ := m.Int64Histogram(
		"outgoing_grpc_request_size",
		metric.WithUnit("bytes"),
		metric.WithDescription("The size of outgoing grpc request messages in bytes (client-side)"),
		metric.WithExplicitBucketBoundaries(messageSizeBuckets...),
	)

	respSize, _ := m.Int64Histogram(
		"incoming_grpc_response_size",
		metric.WithUnit("bytes"),
		metric.WithDescription("The size of incoming grpc response messages in bytes (client-side)"),
		metric.WithExplicitBucketBoundaries(messageSizeBuckets...),
	)

	reqMsgCount, _ := m.Int64Histogram(
		"outgoing_grpc_request_messages_total",
		metric.WithDescription("The number of messages sent to the server per grpc call (client-side)"),
		metric.WithExplicitBucketBoundaries(messageCountBuckets...),
	)

	respMsgCount, _ := m.Int64Histogram(
		"incoming_grpc_response_messages_total",
		metric.WithDescription("The number of messages received from the server per grpc call (client-side)"),
		metric.WithExplicitBucketBoundaries(messageCountBuckets...),
	)

	return &clientMetrics{
		total:        total,
		inflight:     inflight,
		duration:     duration,
		reqSize:      reqSize,
		respSize:     respSize,
		reqMsgCount:  reqMsgCount,
		respMsgCount: respMsgCount,
	}
}

// ClientInterceptor creates observable interceptors with logging, metrics, and tracing for gRPC clients.
type ClientInterceptor struct {
	probe   telemetry.Probe
	opts    Options
	metrics *clientMetrics
}

// NewClientInterceptor creates a new observable gRPC client interceptor.
func NewClientInterceptor(probe telemetry.Probe, opts Options) *ClientInterceptor {
	return &ClientInterceptor{
		probe:   probe,
		opts:    opts.withDefaults(),
		metrics: newClientMetrics(probe.Meter()),
	}
}

// Unary returns a grpc.UnaryClientInterceptor that can be registered with grpc.Dial
// (via grpc.WithUnaryInterceptor or grpc.WithChainUnaryInterceptor) to observe unary gRPC calls.
//
// The returned function observes a single unary RPC with logging, metrics, and tracing.
func (i *ClientInterceptor) Unary() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, fullMethod string, req, res any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		startTime := time.Now()

		side := "client"
		stream := false

		pack, service, method, ok := parseFullMethod(fullMethod)
		if !ok {
			return invoker(ctx, fullMethod, req, res, cc, opts...)
		}

		// Skip explicitly excluded methods to avoid noise.
		for _, x := range i.opts.ExcludeMethods {
			if method == x {
				return invoker(ctx, fullMethod, req, res, cc, opts...)
			}
		}

		streamAttr := gRPCStreamKey.Bool(stream)
		packageAttr := gRPCRequestPackageKey.String(pack)
		serviceAttr := gRPCRequestServiceKey.String(service)
		methodAttr := gRPCRequestMethodKey.String(method)
		endpointOpts := metric.WithAttributes(streamAttr, packageAttr, serviceAttr, methodAttr)

		// Track the number of in-flight requests for this client.
		i.metrics.inflight.Add(ctx, 1, endpointOpts)
		defer i.metrics.inflight.Add(ctx, -1, endpointOpts)

		// Record the outgoing request message size and count.
		// A unary RPC always sends exactly one request message.
		reqMsgSize := getMessageSize(req)
		i.metrics.reqSize.Record(ctx, reqMsgSize, endpointOpts)
		i.metrics.reqMsgCount.Record(ctx, 1, endpointOpts)

		// Start a new client span as a child of whatever span is already in the context, if any.
		ctx, span := i.probe.Tracer().Start(ctx, "grpc-client-unary-request",
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
		name, version := i.probe.Info()
		if name != "" && version != "" {
			callerName = fmt.Sprintf("%s/%s", name, version)
		} else if name != "" {
			callerName = name
		} else {
			callerName = defaultCallerName
		}

		// Get the outgoing metadata from context, if any.
		// FromOutgoingContext returns a copy, so it must be reattached to ctx after mutation,
		// regardless of whether metadata was already present.
		//
		// A client interceptor only runs once the RPC is actually invoked,
		// which is after the caller has already attached its own outgoing metadata to ctx.
		// So FromOutgoingContext here reads whatever the caller already set,
		// and the Set calls below add to the same map rather than replacing it.
		reqMD, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			reqMD = metadata.New(nil)
		}

		// Propagate request metadata to the server by adding them to the outgoing gRPC request metadata.
		reqMD.Set(requestUUIDKey, requestUUID)
		reqMD.Set(callerNameKey, callerName)

		// Inject the trace context from ctx into the outgoing request metadata,
		// so the server can continue the trace.
		otel.GetTextMapPropagator().Inject(ctx, metadataCarrier(reqMD))

		// Update the context after mutating the metadata.
		// Metadata was altered by the Inject call above, so this must come after it.
		ctx = metadata.NewOutgoingContext(ctx, reqMD)

		// Capture response header and trailer metadata from the call.
		var headerMD, trailerMD metadata.MD
		opts = append(opts, grpc.Header(&headerMD), grpc.Trailer(&trailerMD))

		// Make the grpc unary call.
		span.AddEvent("making grpc unary call")
		err := invoker(ctx, fullMethod, req, res, cc, opts...)

		duration := time.Since(startTime).Milliseconds()
		code := grpcstatus.Code(err)
		codeAttr := gRPCCodeKey.Int(int(code))
		resultOpts := metric.WithAttributes(streamAttr, packageAttr, serviceAttr, methodAttr, codeAttr)

		var respMsgSize, respMsgCount int64
		if err == nil {
			respMsgSize = getMessageSize(res)
			respMsgCount = 1 // A unary RPC receives exactly one response message, but only on success.
		}

		// Metrics
		i.metrics.total.Add(ctx, 1, resultOpts)
		i.metrics.duration.Record(ctx, duration, resultOpts)
		i.metrics.respSize.Record(ctx, respMsgSize, resultOpts)
		i.metrics.respMsgCount.Record(ctx, respMsgCount, resultOpts)

		// Logs
		message := fmt.Sprintf("%s %s::%s::%s %d ms", side, pack, service, method, duration)
		logKV := []any{
			"traceId", span.SpanContext().TraceID().String(),
			"spanId", span.SpanContext().SpanID().String(),
			originLogKey, originLogVal,
			"caller_name", callerName,
			"req_uuid", requestUUID,
			"req_side", side,
			"grpc_req_stream", stream,
			"grpc_req_package", pack,
			"grpc_req_service", service,
			"grpc_req_method", method,
			"grpc_resp_duration_ms", duration,
			"grpc_code", code,
			"grpc_req_message_size", reqMsgSize,
			"grpc_resp_message_size", respMsgSize,
		}

		// Include configured request and response metadata.
		logKV = append(logKV, getLoggableMetadataKV("grpc_req_metadata_", reqMD, i.opts.LogMetadata)...)
		logKV = append(logKV, getLoggableMetadataKV("grpc_resp_header_", headerMD, i.opts.LogMetadata)...)
		logKV = append(logKV, getLoggableMetadataKV("grpc_resp_trailer_", trailerMD, i.opts.LogMetadata)...)

		// Determine the log level based on the result.
		if err == nil {
			i.probe.Logger().Info(message, logKV...)
		} else {
			logKV = append(logKV, "grpc_error", err.Error())
			i.probe.Logger().Error(message, logKV...)
		}

		// Update the trace span.
		span.SetAttributes(streamAttr, packageAttr, serviceAttr, methodAttr)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}

		return err
	}
}

// Stream returns a grpc.StreamClientInterceptor that can be registered with grpc.Dial
// (via grpc.WithStreamInterceptor or grpc.WithChainStreamInterceptor) to observe streaming gRPC calls.
//
// The returned function observes a single streaming RPC with logging, metrics, and tracing.
//
// Unlike a unary interceptor, the interceptor here returns as soon as the stream is established, not once the RPC completes.
// To still capture the final outcome, the returned grpc.ClientStream is wrapped
// so that completion can be detected and observed exactly once.
func (i *ClientInterceptor) Stream() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, fullMethod string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		startTime := time.Now()

		side := "client"
		stream := true

		pack, service, method, ok := parseFullMethod(fullMethod)
		if !ok {
			return streamer(ctx, desc, cc, fullMethod, opts...)
		}

		// Skip explicitly excluded methods to avoid noise.
		for _, x := range i.opts.ExcludeMethods {
			if method == x {
				return streamer(ctx, desc, cc, fullMethod, opts...)
			}
		}

		streamAttr := gRPCStreamKey.Bool(stream)
		packageAttr := gRPCRequestPackageKey.String(pack)
		serviceAttr := gRPCRequestServiceKey.String(service)
		methodAttr := gRPCRequestMethodKey.String(method)
		endpointOpts := metric.WithAttributes(streamAttr, packageAttr, serviceAttr, methodAttr)

		// Track the number of in-flight requests for this client.
		// This is decremented once the stream is actually finished, not when it is opened.
		i.metrics.inflight.Add(ctx, 1, endpointOpts)

		// Start a new client span as a child of whatever span is already in the context, if any.
		ctx, span := i.probe.Tracer().Start(ctx, "grpc-client-stream-request",
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
		name, version := i.probe.Info()
		if name != "" && version != "" {
			callerName = fmt.Sprintf("%s/%s", name, version)
		} else if name != "" {
			callerName = name
		} else {
			callerName = defaultCallerName
		}

		// Get the outgoing metadata from context, if any.
		// FromOutgoingContext returns a copy, so it must be reattached to ctx after mutation,
		// regardless of whether metadata was already present.
		//
		// A client interceptor only runs once the RPC is actually invoked,
		// which is after the caller has already attached its own outgoing metadata to ctx.
		// So FromOutgoingContext here reads whatever the caller already set,
		// and the Set calls below add to the same map rather than replacing it.
		reqMD, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			reqMD = metadata.New(nil)
		}

		// Propagate request metadata to the server by adding them to the outgoing gRPC request metadata.
		reqMD.Set(requestUUIDKey, requestUUID)
		reqMD.Set(callerNameKey, callerName)

		// Inject the trace context from ctx into the outgoing request metadata,
		// so the server can continue the trace.
		otel.GetTextMapPropagator().Inject(ctx, metadataCarrier(reqMD))

		// Update the context after mutating the metadata.
		// Metadata was altered by the Inject call above, so this must come after it.
		ctx = metadata.NewOutgoingContext(ctx, reqMD)

		// Capture response header and trailer metadata from the call.
		var headerMD, trailerMD metadata.MD
		opts = append(opts, grpc.Header(&headerMD), grpc.Trailer(&trailerMD))

		logKV := []any{
			"traceId", span.SpanContext().TraceID().String(),
			"spanId", span.SpanContext().SpanID().String(),
			originLogKey, originLogVal,
			"caller_name", callerName,
			"req_uuid", requestUUID,
			"req_side", side,
			"grpc_req_stream", stream,
			"grpc_req_package", pack,
			"grpc_req_service", service,
			"grpc_req_method", method,
		}

		// Include configured request metadata.
		logKV = append(logKV, getLoggableMetadataKV("grpc_req_metadata_", reqMD, i.opts.LogMetadata)...)

		// finish records metrics, logs, and span for this call.
		// It is invoked once the stream has concluded and the stream is closed,
		// It must run exactly once.
		finish := func(err error, reqMsgCount, respMsgCount int64) {
			duration := time.Since(startTime).Milliseconds()
			code := grpcstatus.Code(err)
			codeAttr := gRPCCodeKey.Int(int(code))
			resultOpts := metric.WithAttributes(streamAttr, packageAttr, serviceAttr, methodAttr, codeAttr)

			// Metrics
			i.metrics.inflight.Add(ctx, -1, endpointOpts)
			i.metrics.total.Add(ctx, 1, resultOpts)
			i.metrics.duration.Record(ctx, duration, resultOpts)
			i.metrics.reqMsgCount.Record(ctx, reqMsgCount, endpointOpts)
			i.metrics.respMsgCount.Record(ctx, respMsgCount, endpointOpts)

			// Logs
			message := fmt.Sprintf("%s %s::%s::%s %d ms", side, pack, service, method, duration)
			logKV := append(logKV,
				"grpc_resp_duration_ms", duration,
				"grpc_code", code,
				"grpc_req_message_count", reqMsgCount,
				"grpc_resp_message_count", respMsgCount,
			)

			// Include configured response metadata.
			logKV = append(logKV, getLoggableMetadataKV("grpc_resp_header_", headerMD, i.opts.LogMetadata)...)
			logKV = append(logKV, getLoggableMetadataKV("grpc_resp_trailer_", trailerMD, i.opts.LogMetadata)...)

			// Determine the log level based on the result.
			if err == nil {
				i.probe.Logger().Info(message, logKV...)
			} else {
				logKV = append(logKV, "grpc_error", err.Error())
				i.probe.Logger().Error(message, logKV...)
			}

			// Update the trace span.
			span.SetAttributes(streamAttr, packageAttr, serviceAttr, methodAttr)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
		}

		// Make the grpc stream call.
		span.AddEvent("making grpc stream call")
		cs, err := streamer(ctx, desc, cc, fullMethod, opts...)

		// Bail out if the stream could not be established.
		if err != nil {
			finish(err, 0, 0)
			return nil, err
		}

		// Wrap the stream to count and size the messages exchanged with the server.
		ocs := newObservableClientStream(ctx, cs, desc, i.metrics, endpointOpts, finish)

		return ocs, err
	}
}

// -------------------------------------------------- Auxiliary Types --------------------------------------------------

// observableClientStream wraps a grpc.ClientStream to make streaming RPCs observable.
// It intercepts SendMsg and RecvMsg to count messages and record their sizes.
// It also allows overriding the stream's context with an augmented context.
type observableClientStream struct {
	grpc.ClientStream

	ctx     context.Context
	desc    *grpc.StreamDesc
	metrics *clientMetrics
	opts    metric.MeasurementOption
	finish  finishFunc

	// This cancels the context.AfterFunc registered in newObservableClientStream,
	// releasing its registration on ctx as soon as finish runs rather than waiting for ctx to be done.
	// The caller-provided ctx typically outlives the RPC and may not be done until much later or never;
	// without this cleanup, each call would leak a registration on ctx until then.
	stopAfterFunc func() bool

	finishOnce   sync.Once
	reqMsgCount  atomic.Int64
	respMsgCount atomic.Int64
}

type finishFunc func(err error, reqMsgCount, respMsgCount int64)

func newObservableClientStream(
	ctx context.Context,
	cs grpc.ClientStream,
	desc *grpc.StreamDesc,
	metrics *clientMetrics,
	opts metric.MeasurementOption,
	finish finishFunc,
) *observableClientStream {
	// If cs is already wrapped, reuse it instead of double-wrapping.
	s, ok := cs.(*observableClientStream)
	if ok {
		s.cleanup()
	} else {
		s = &observableClientStream{ClientStream: cs}
	}

	s.ctx = ctx
	s.desc = desc
	s.metrics = metrics
	s.opts = opts
	s.finish = finish

	// A caller may abandon a stream without ever calling SendMsg or RecvMsg
	// (e.g. cancelling ctx mid-stream), in which case neither would trigger finish.
	// Registering finish against ctx guarantees it still runs, so no signal is left dangling.
	s.stopAfterFunc = context.AfterFunc(ctx, func() {
		s.finishOnce.Do(func() {
			s.finish(ctx.Err(), s.reqMsgCount.Load(), s.respMsgCount.Load())
		})
	})

	return s
}

func (s *observableClientStream) Context() context.Context {
	if s.ctx == nil {
		return s.ClientStream.Context()
	}

	return s.ctx
}

func (s *observableClientStream) SendMsg(msg any) error {
	err := s.ClientStream.SendMsg(msg)

	if err == nil {
		s.reqMsgCount.Add(1)
		s.metrics.reqSize.Record(s.Context(), getMessageSize(msg), s.opts)
	} else {
		// An error from SendMsg indicates a broken stream; the actual error will be surfaced by RecvMsg.
		// Finalize the session here in case the caller never calls RecvMsg after a SendMsg error.
		s.finalize(err)
	}

	return err
}

func (s *observableClientStream) RecvMsg(msg any) error {
	err := s.ClientStream.RecvMsg(msg)

	switch err {
	case nil:
		s.respMsgCount.Add(1)
		s.metrics.respSize.Record(s.Context(), getMessageSize(msg), s.opts)

		// A client-streaming call receives exactly one response,
		// so the stream is considered done as soon as RecvMsg call returns, regardless of the error.
		if !s.desc.ServerStreams {
			s.finalize(nil)
		}

	case io.EOF:
		// A server-streaming or bidirectional call completes when RecvMsg returns an error.
		// io.EOF means the server closed the stream successfully, and it is not a real error.
		s.finalize(nil)

	default:
		s.finalize(err)
	}

	return err
}

func (s *observableClientStream) finalize(err error) {
	s.finishOnce.Do(func() {
		s.finish(err, s.reqMsgCount.Load(), s.respMsgCount.Load())
		s.cleanup()
	})
}

// cleanup stops the AfterFunc registered by newObservableClientStream, if any,
// so it does not linger on ctx until ctx itself is eventually canceled/done.
func (s *observableClientStream) cleanup() {
	if s.stopAfterFunc != nil {
		s.stopAfterFunc()
	}
}
