package grpc

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/wrapperspb"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/neatplatform/mint/telemetry"
)

func TestNewClientMetrics(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("")
	metrics := newClientMetrics(meter)

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.total)
	assert.NotNil(t, metrics.inflight)
	assert.NotNil(t, metrics.duration)
	assert.NotNil(t, metrics.reqSize)
	assert.NotNil(t, metrics.respSize)
	assert.NotNil(t, metrics.reqMsgCount)
	assert.NotNil(t, metrics.respMsgCount)
}

func TestNewClientInterceptor(t *testing.T) {
	tests := []struct {
		name  string
		probe telemetry.Probe
		opts  Options
	}{
		{
			name:  "WithDefaultOptions",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
		},
		{
			name:  "WithCustomOptions",
			probe: telemetry.NewNoopProbe(),
			opts: Options{
				ExcludeMethods: []string{
					"CheckHealth",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := NewClientInterceptor(tc.probe, tc.opts)

			assert.NotNil(t, i)
			assert.NotNil(t, i.metrics)
			assert.Same(t, tc.probe, i.probe)
			assert.Equal(t, tc.opts, i.opts)
		})
	}
}

func TestClientInterceptor_Unary(t *testing.T) {
	tests := []struct {
		name                string
		probe               telemetry.Probe
		opts                Options
		ctx                 context.Context
		fullMethod          string
		req                 any
		res                 any
		cc                  *grpc.ClientConn
		callOpts            []grpc.CallOption
		invoker             grpc.UnaryInvoker
		expectObservation   bool
		expectedError       string
		expectedCode        grpccodes.Code
		expectedCallerName  string
		expectedRequestUUID string
	}{
		{
			name: "InvalidFullMethod",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			fullMethod: "",
			req:        wrapperspb.String("request"),
			res:        &wrapperspb.StringValue{},
			cc:         &grpc.ClientConn{},
			callOpts:   []grpc.CallOption{},
			invoker: func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				time.Sleep(time.Millisecond)
				return nil
			},
			expectObservation:  false,
			expectedError:      "",
			expectedCode:       grpccodes.OK,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "WithExcludeMethods",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts: Options{
				ExcludeMethods: []string{"CheckHealth"},
			},
			ctx:        context.Background(),
			fullMethod: "/myapp.v1.UserService/CheckHealth",
			req:        wrapperspb.String("request"),
			res:        &wrapperspb.StringValue{},
			cc:         &grpc.ClientConn{},
			callOpts:   []grpc.CallOption{},
			invoker: func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				time.Sleep(time.Millisecond)
				return nil
			},
			expectObservation: false,
			expectedError:     "",
			expectedCode:      grpccodes.OK,
		},
		{
			name: "InvokerFails_WithGenericError",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			fullMethod: "/myapp.v1.UserService/GetUser",
			req:        wrapperspb.String("request"),
			res:        &wrapperspb.StringValue{},
			cc:         &grpc.ClientConn{},
			callOpts:   []grpc.CallOption{},
			invoker: func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				time.Sleep(time.Millisecond)
				return errors.New("error on reading data")
			},
			expectObservation:  true,
			expectedError:      "error on reading data",
			expectedCode:       grpccodes.Unknown,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "InvokerFails_WithGRPCError",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			fullMethod: "/myapp.v1.UserService/GetUser",
			req:        wrapperspb.String("request"),
			res:        &wrapperspb.StringValue{},
			cc:         &grpc.ClientConn{},
			callOpts:   []grpc.CallOption{},
			invoker: func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				time.Sleep(time.Millisecond)
				return grpcstatus.Error(grpccodes.Unauthenticated, "user is not authenticated")
			},
			expectObservation:  true,
			expectedError:      "rpc error: code = Unauthenticated desc = user is not authenticated",
			expectedCode:       grpccodes.Unauthenticated,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "InvokerSucceeds",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			fullMethod: "/myapp.v1.UserService/GetUser",
			req:        wrapperspb.String("request"),
			res:        &wrapperspb.StringValue{},
			cc:         &grpc.ClientConn{},
			callOpts:   []grpc.CallOption{},
			invoker: func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				time.Sleep(time.Millisecond)
				return nil
			},
			expectObservation:  true,
			expectedError:      "",
			expectedCode:       grpccodes.OK,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "InvokerSucceeds_WithRequestUUID",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:       Options{},
			ctx:        telemetry.ContextWithUUID(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
			fullMethod: "/myapp.v1.UserService/GetUser",
			req:        wrapperspb.String("request"),
			res:        &wrapperspb.StringValue{},
			cc:         &grpc.ClientConn{},
			callOpts:   []grpc.CallOption{},
			invoker: func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				time.Sleep(time.Millisecond)
				return nil
			},
			expectObservation:   true,
			expectedError:       "",
			expectedCode:        grpccodes.OK,
			expectedCallerName:  "poke-service/v0.1.0",
			expectedRequestUUID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name: "InvokerSucceeds_WithMetadata",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts: Options{},
			ctx: metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
				"authorization", "Bearer token",
			)),
			fullMethod: "/myapp.v1.UserService/GetUser",
			req:        wrapperspb.String("request"),
			res:        &wrapperspb.StringValue{},
			cc:         &grpc.ClientConn{},
			callOpts:   []grpc.CallOption{},
			invoker: func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				time.Sleep(time.Millisecond)
				return nil
			},
			expectObservation:  true,
			expectedError:      "",
			expectedCode:       grpccodes.OK,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "InvokerSucceeds_WithoutVersion",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			fullMethod: "/myapp.v1.UserService/GetUser",
			req:        wrapperspb.String("request"),
			res:        &wrapperspb.StringValue{},
			cc:         &grpc.ClientConn{},
			callOpts:   []grpc.CallOption{},
			invoker: func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				time.Sleep(time.Millisecond)
				return nil
			},
			expectObservation:  true,
			expectedError:      "",
			expectedCode:       grpccodes.OK,
			expectedCallerName: "poke-service",
		},
		{
			name: "InvokerSucceeds_WithoutNameAndVersion",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("", "", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			fullMethod: "/myapp.v1.UserService/GetUser",
			req:        wrapperspb.String("request"),
			res:        &wrapperspb.StringValue{},
			cc:         &grpc.ClientConn{},
			callOpts:   []grpc.CallOption{},
			invoker: func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				time.Sleep(time.Millisecond)
				return nil
			},
			expectObservation:  true,
			expectedError:      "",
			expectedCode:       grpccodes.OK,
			expectedCallerName: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := NewClientInterceptor(tc.probe, tc.opts)
			assert.NotNil(t, i)

			var gotFullMethod string
			var gotReq any
			var uuidFromMD, callerFromMD string

			// Capture values seen inside the invoker for assertions.
			wrappedInvoker := func(ctx context.Context, fullMethod string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
				gotFullMethod = fullMethod
				gotReq = req

				if tc.expectObservation {
					md, ok := metadata.FromOutgoingContext(ctx)
					assert.True(t, ok)

					uuidFromMD = md.Get(requestUUIDKey)[0]
					callerFromMD = md.Get(callerNameKey)[0]
				}

				return tc.invoker(ctx, fullMethod, req, reply, cc, opts...)
			}

			err := i.Unary()(tc.ctx, tc.fullMethod, tc.req, tc.res, tc.cc, wrappedInvoker, tc.callOpts...)

			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}

			assert.Equal(t, tc.expectedCode, grpcstatus.Code(err))
			assert.Equal(t, tc.fullMethod, gotFullMethod)
			assert.Equal(t, tc.req, gotReq)

			if tc.expectObservation {
				assert.Equal(t, tc.expectedCallerName, callerFromMD)

				if tc.expectedRequestUUID == "" {
					assert.NoError(t, uuid.Validate(uuidFromMD))
				} else {
					assert.Equal(t, tc.expectedRequestUUID, uuidFromMD)
				}
			}

			assert.NoError(t, tc.probe.Close(tc.ctx))
		})
	}
}

func TestClientInterceptor_Stream(t *testing.T) {
	tests := []struct {
		name                string
		probe               telemetry.Probe
		opts                Options
		ctx                 context.Context
		desc                *grpc.StreamDesc
		cc                  *grpc.ClientConn
		fullMethod          string
		callOpts            []grpc.CallOption
		streamer            grpc.Streamer
		expectObservation   bool
		expectedError       string
		expectedCode        grpccodes.Code
		expectedCallerName  string
		expectedRequestUUID string
	}{
		{
			name: "InvalidFullMethod",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			desc:       &grpc.StreamDesc{},
			cc:         &grpc.ClientConn{},
			fullMethod: "",
			callOpts:   []grpc.CallOption{},
			streamer: func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
				time.Sleep(time.Millisecond)
				return &MockClientStream{}, nil
			},
			expectObservation:  false,
			expectedError:      "",
			expectedCode:       grpccodes.OK,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "WithExcludeMethods",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts: Options{
				ExcludeMethods: []string{"CheckHealth"},
			},
			ctx:        context.Background(),
			desc:       &grpc.StreamDesc{},
			cc:         &grpc.ClientConn{},
			fullMethod: "/myapp.v1.FileService/CheckHealth",
			callOpts:   []grpc.CallOption{},
			streamer: func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
				time.Sleep(time.Millisecond)
				return &MockClientStream{}, nil
			},
			expectObservation: false,
			expectedError:     "",
			expectedCode:      grpccodes.OK,
		},
		{
			name: "StreamerFails_WithGenericError",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			desc:       &grpc.StreamDesc{},
			cc:         &grpc.ClientConn{},
			fullMethod: "/myapp.v1.FileService/Upload",
			callOpts:   []grpc.CallOption{},
			streamer: func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
				time.Sleep(time.Millisecond)
				return nil, errors.New("error on reading data")
			},
			expectObservation:  true,
			expectedError:      "error on reading data",
			expectedCode:       grpccodes.Unknown,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "StreamerFails_WithGRPCError",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			desc:       &grpc.StreamDesc{},
			cc:         &grpc.ClientConn{},
			fullMethod: "/myapp.v1.FileService/Upload",
			callOpts:   []grpc.CallOption{},
			streamer: func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
				time.Sleep(time.Millisecond)
				return nil, grpcstatus.Error(grpccodes.PermissionDenied, "user is not authorized")
			},
			expectObservation:  true,
			expectedError:      "rpc error: code = PermissionDenied desc = user is not authorized",
			expectedCode:       grpccodes.PermissionDenied,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "StreamerSucceeds",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			desc:       &grpc.StreamDesc{},
			cc:         &grpc.ClientConn{},
			fullMethod: "/myapp.v1.FileService/Upload",
			callOpts:   []grpc.CallOption{},
			streamer: func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
				time.Sleep(time.Millisecond)
				return &MockClientStream{}, nil
			},
			expectObservation:  true,
			expectedError:      "",
			expectedCode:       grpccodes.OK,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "StreamerSucceeds_WithRequestUUID",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts:       Options{},
			ctx:        telemetry.ContextWithUUID(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
			desc:       &grpc.StreamDesc{},
			cc:         &grpc.ClientConn{},
			fullMethod: "/myapp.v1.FileService/Upload",
			callOpts:   []grpc.CallOption{},
			streamer: func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
				time.Sleep(time.Millisecond)
				return &MockClientStream{}, nil
			},
			expectObservation:   true,
			expectedError:       "",
			expectedCode:        grpccodes.OK,
			expectedCallerName:  "poke-service/v0.1.0",
			expectedRequestUUID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		},
		{
			name: "StreamerSucceeds_WithMetadata",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "v0.1.0", nil),
			),
			opts: Options{},
			ctx: metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
				"authorization", "Bearer token",
			)),
			desc:       &grpc.StreamDesc{},
			cc:         &grpc.ClientConn{},
			fullMethod: "/myapp.v1.FileService/Upload",
			callOpts:   []grpc.CallOption{},
			streamer: func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
				time.Sleep(time.Millisecond)
				return &MockClientStream{}, nil
			},
			expectObservation:  true,
			expectedError:      "",
			expectedCode:       grpccodes.OK,
			expectedCallerName: "poke-service/v0.1.0",
		},
		{
			name: "StreamerSucceeds_WithVersion",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("poke-service", "", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			desc:       &grpc.StreamDesc{},
			cc:         &grpc.ClientConn{},
			fullMethod: "/myapp.v1.FileService/Upload",
			callOpts:   []grpc.CallOption{},
			streamer: func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
				time.Sleep(time.Millisecond)
				return &MockClientStream{}, nil
			},
			expectObservation:  true,
			expectedError:      "",
			expectedCode:       grpccodes.OK,
			expectedCallerName: "poke-service",
		},
		{
			name: "StreamerSucceeds_WithNameAndVersion",
			probe: telemetry.NewProbe(
				telemetry.WithMetadata("", "", nil),
			),
			opts:       Options{},
			ctx:        context.Background(),
			desc:       &grpc.StreamDesc{},
			cc:         &grpc.ClientConn{},
			fullMethod: "/myapp.v1.FileService/Upload",
			callOpts:   []grpc.CallOption{},
			streamer: func(context.Context, *grpc.StreamDesc, *grpc.ClientConn, string, ...grpc.CallOption) (grpc.ClientStream, error) {
				time.Sleep(time.Millisecond)
				return &MockClientStream{}, nil
			},
			expectObservation:  true,
			expectedError:      "",
			expectedCode:       grpccodes.OK,
			expectedCallerName: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := NewClientInterceptor(tc.probe, tc.opts)
			assert.NotNil(t, i)

			var gotFullMethod string
			var uuidFromMD, callerFromMD string

			// Capture values seen inside the streamer for assertions.
			wrappedStreamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, fullMethod string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
				gotFullMethod = fullMethod

				if tc.expectObservation {
					md, ok := metadata.FromOutgoingContext(ctx)
					assert.True(t, ok)

					uuidFromMD = md.Get(requestUUIDKey)[0]
					callerFromMD = md.Get(callerNameKey)[0]
				}

				return tc.streamer(ctx, desc, cc, fullMethod, opts...)
			}

			cs, err := i.Stream()(tc.ctx, tc.desc, tc.cc, tc.fullMethod, wrappedStreamer, tc.callOpts...)

			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}

			assert.Equal(t, tc.expectedCode, grpcstatus.Code(err))
			assert.Equal(t, tc.fullMethod, gotFullMethod)

			if tc.expectObservation {
				if cs != nil {
					_, ok := cs.(*observableClientStream)
					assert.True(t, ok)
				}

				assert.Equal(t, tc.expectedCallerName, callerFromMD)

				if tc.expectedRequestUUID == "" {
					assert.NoError(t, uuid.Validate(uuidFromMD))
				} else {
					assert.Equal(t, tc.expectedRequestUUID, uuidFromMD)
				}
			}

			assert.NoError(t, tc.probe.Close(tc.ctx))
		})
	}
}

// -------------------------------------------------- Auxiliary Types --------------------------------------------------

func TestNewObservableClientStream(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("")

	tests := []struct {
		name    string
		cs      grpc.ClientStream
		desc    *grpc.StreamDesc
		metrics *clientMetrics
		opts    metric.MeasurementOption
		finish  finishFunc
	}{
		{
			name:    "WithClientStream",
			cs:      &MockClientStream{},
			desc:    &grpc.StreamDesc{},
			metrics: newClientMetrics(meter),
			opts: metric.WithAttributes(
				attribute.String("environment", "test"),
			),
			finish: func(error, int64, int64) {},
		},
		{
			name: "WithObservableClientStream",
			cs: &observableClientStream{
				ClientStream: &MockClientStream{},
			},
			desc:    &grpc.StreamDesc{},
			metrics: newClientMetrics(meter),
			opts: metric.WithAttributes(
				attribute.String("environment", "test"),
			),
			finish: func(error, int64, int64) {},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
			defer cancel()

			ocs := newObservableClientStream(ctx, tc.cs, tc.desc, tc.metrics, tc.opts, tc.finish)

			assert.NotNil(t, ocs)
			assert.NotNil(t, ocs.ClientStream)
			assert.Equal(t, ctx, ocs.ctx)
			assert.Same(t, tc.desc, ocs.desc)
			assert.Same(t, tc.metrics, ocs.metrics)
			assert.Same(t, tc.opts, ocs.opts)
			assert.Equal(t, tc.finish == nil, ocs.finish == nil)
			assert.NotNil(t, ocs.stopAfterFunc)
		})
	}
}

func TestObservableClientStream_Context(t *testing.T) {
	tests := []struct {
		name            string
		s               *observableClientStream
		expectedContext context.Context
	}{
		{
			name: "WithoutExplicitContext",
			s: &observableClientStream{
				ClientStream: &MockClientStream{
					ContextMocks: []MockClientStream_ContextMock{
						{OutContext: context.Background()},
					},
				},
			},
			expectedContext: context.Background(),
		},
		{
			name: "WithExplicitContext",
			s: &observableClientStream{
				ClientStream: &MockClientStream{
					ContextMocks: []MockClientStream_ContextMock{
						{OutContext: context.Background()},
					},
				},
				ctx: context.WithValue(context.Background(), testContextKey("foo"), "bar"),
			},
			expectedContext: context.WithValue(context.Background(), testContextKey("foo"), "bar"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedContext, tc.s.Context())
		})
	}
}

func TestObservableClientStream_SendMsg(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("")

	tests := []struct {
		name          string
		s             *observableClientStream
		msg           any
		expectedError string
	}{
		{
			name: "Success",
			s: &observableClientStream{
				ClientStream: &MockClientStream{
					SendMsgMocks: []MockClientStream_SendMsgMock{
						{},
					},
				},
				ctx:           context.Background(),
				desc:          &grpc.StreamDesc{},
				metrics:       newClientMetrics(meter),
				opts:          nil,
				finish:        func(error, int64, int64) {},
				stopAfterFunc: func() bool { return true },
			},
			msg: wrapperspb.String("Hello, World!"),
		},
		{
			name: "WithError_EOF",
			s: &observableClientStream{
				ClientStream: &MockClientStream{
					SendMsgMocks: []MockClientStream_SendMsgMock{
						{OutError: io.EOF},
					},
				},
				ctx:           context.Background(),
				desc:          &grpc.StreamDesc{},
				metrics:       newClientMetrics(meter),
				opts:          nil,
				finish:        func(error, int64, int64) {},
				stopAfterFunc: func() bool { return true },
			},
			msg:           wrapperspb.String("Hello, World!"),
			expectedError: "EOF",
		},
		{
			name: "WithError",
			s: &observableClientStream{
				ClientStream: &MockClientStream{
					SendMsgMocks: []MockClientStream_SendMsgMock{
						{OutError: errors.New("connection failed")},
					},
				},
				ctx:           context.Background(),
				desc:          &grpc.StreamDesc{},
				metrics:       newClientMetrics(meter),
				opts:          nil,
				finish:        func(error, int64, int64) {},
				stopAfterFunc: func() bool { return true },
			},
			msg:           wrapperspb.String("Hello, World!"),
			expectedError: "connection failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.SendMsg(tc.msg)

			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}
		})
	}
}

func TestObservableClientStream_RecvMsg(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("")

	tests := []struct {
		name          string
		s             *observableClientStream
		msg           any
		expectedError string
	}{
		{
			name: "Success_WithServerStreaming",
			s: &observableClientStream{
				ClientStream: &MockClientStream{
					RecvMsgMocks: []MockClientStream_RecvMsgMock{
						{},
					},
				},
				ctx: context.Background(),
				desc: &grpc.StreamDesc{
					ServerStreams: true,
				},
				metrics:       newClientMetrics(meter),
				opts:          nil,
				finish:        func(error, int64, int64) {},
				stopAfterFunc: func() bool { return true },
			},
			msg: &wrapperspb.StringValue{},
		},
		{
			name: "Success_WithoutServerStreaming",
			s: &observableClientStream{
				ClientStream: &MockClientStream{
					RecvMsgMocks: []MockClientStream_RecvMsgMock{
						{},
					},
				},
				ctx:           context.Background(),
				desc:          &grpc.StreamDesc{},
				metrics:       newClientMetrics(meter),
				opts:          nil,
				finish:        func(error, int64, int64) {},
				stopAfterFunc: func() bool { return true },
			},
			msg: &wrapperspb.StringValue{},
		},
		{
			name: "WithError_EOF",
			s: &observableClientStream{
				ClientStream: &MockClientStream{
					RecvMsgMocks: []MockClientStream_RecvMsgMock{
						{OutError: io.EOF},
					},
				},
				ctx:           context.Background(),
				desc:          &grpc.StreamDesc{},
				metrics:       newClientMetrics(meter),
				opts:          nil,
				finish:        func(error, int64, int64) {},
				stopAfterFunc: func() bool { return true },
			},
			msg:           &wrapperspb.StringValue{},
			expectedError: "EOF",
		},
		{
			name: "WithError",
			s: &observableClientStream{
				ClientStream: &MockClientStream{
					RecvMsgMocks: []MockClientStream_RecvMsgMock{
						{OutError: errors.New("connection failed")},
					},
				},
				ctx:           context.Background(),
				desc:          &grpc.StreamDesc{},
				metrics:       newClientMetrics(meter),
				opts:          nil,
				finish:        func(error, int64, int64) {},
				stopAfterFunc: func() bool { return true },
			},
			msg:           &wrapperspb.StringValue{},
			expectedError: "connection failed",
		},
	}

	for _, tc := range tests {
		err := tc.s.RecvMsg(tc.msg)

		t.Run(tc.name, func(t *testing.T) {
			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}
		})
	}
}

// -------------------------------------------------- Mock Types --------------------------------------------------

type (
	MockClientStream struct {
		HeaderIndex int
		HeaderMocks []MockClientStream_HeaderMock

		TrailerIndex int
		TrailerMocks []MockClientStream_TrailerMock

		CloseSendIndex int
		CloseSendMocks []MockClientStream_CloseSendMock

		ContextIndex int
		ContextMocks []MockClientStream_ContextMock

		SendMsgIndex int
		SendMsgMocks []MockClientStream_SendMsgMock

		RecvMsgIndex int
		RecvMsgMocks []MockClientStream_RecvMsgMock
	}

	MockClientStream_HeaderMock struct {
		OutMD    metadata.MD
		OutError error
	}

	MockClientStream_TrailerMock struct {
		OutMD metadata.MD
	}

	MockClientStream_CloseSendMock struct {
		OutError error
	}

	MockClientStream_ContextMock struct {
		OutContext context.Context
	}

	MockClientStream_SendMsgMock struct {
		InMsg    any
		OutError error
	}

	MockClientStream_RecvMsgMock struct {
		InMsg    any
		OutError error
	}
)

func (m *MockClientStream) Header() (metadata.MD, error) {
	if m.HeaderIndex >= len(m.HeaderMocks) {
		panic("Header called more times than expected")
	}

	i := m.HeaderIndex
	m.HeaderIndex++

	return m.HeaderMocks[i].OutMD, m.HeaderMocks[i].OutError
}

func (m *MockClientStream) Trailer() metadata.MD {
	if m.TrailerIndex >= len(m.TrailerMocks) {
		panic("Trailer called more times than expected")
	}

	i := m.TrailerIndex
	m.TrailerIndex++

	return m.TrailerMocks[i].OutMD
}

func (m *MockClientStream) CloseSend() error {
	if m.CloseSendIndex >= len(m.CloseSendMocks) {
		panic("CloseSend called more times than expected")
	}

	i := m.CloseSendIndex
	m.CloseSendIndex++

	return m.CloseSendMocks[i].OutError
}

func (m *MockClientStream) Context() context.Context {
	if m.ContextIndex >= len(m.ContextMocks) {
		panic("Context called more times than expected")
	}

	i := m.ContextIndex
	m.ContextIndex++

	return m.ContextMocks[i].OutContext
}

func (m *MockClientStream) SendMsg(msg any) error {
	if m.SendMsgIndex >= len(m.SendMsgMocks) {
		panic("SendMsg called more times than expected")
	}

	i := m.SendMsgIndex
	m.SendMsgIndex++

	m.SendMsgMocks[i].InMsg = msg

	return m.SendMsgMocks[i].OutError
}

func (m *MockClientStream) RecvMsg(msg any) error {
	if m.RecvMsgIndex >= len(m.RecvMsgMocks) {
		panic("RecvMsg called more times than expected")
	}

	i := m.RecvMsgIndex
	m.RecvMsgIndex++

	m.RecvMsgMocks[i].InMsg = msg

	return m.RecvMsgMocks[i].OutError
}
