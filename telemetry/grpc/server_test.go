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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/neatplatform/mint/telemetry"
)

func TestNewServerMetrics(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("")
	metrics := newServerMetrics(meter)

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.panics)
	assert.NotNil(t, metrics.total)
	assert.NotNil(t, metrics.inflight)
	assert.NotNil(t, metrics.duration)
	assert.NotNil(t, metrics.reqSize)
	assert.NotNil(t, metrics.respSize)
	assert.NotNil(t, metrics.reqMsgCount)
	assert.NotNil(t, metrics.respMsgCount)
}

func TestNewServerInterceptor(t *testing.T) {
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
			i := NewServerInterceptor(tc.probe, tc.opts)

			assert.NotNil(t, i)
			assert.NotNil(t, i.metrics)
			assert.Same(t, tc.probe, i.probe)
			assert.Equal(t, tc.opts, i.opts)
		})
	}
}

func TestServerInterceptor_Unary(t *testing.T) {
	tests := []struct {
		name                string
		probe               telemetry.Probe
		opts                Options
		ctx                 context.Context
		req                 any
		info                *grpc.UnaryServerInfo
		handler             grpc.UnaryHandler
		expectObservation   bool
		expectedError       string
		expectedCode        grpccodes.Code
		expectedResponse    any
		expectedRequestUUID string
		expectedCallerName  string
	}{
		{
			name:  "InvalidFullMethod",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			ctx:   context.Background(),
			req:   wrapperspb.String("request"),
			info:  &grpc.UnaryServerInfo{},
			handler: func(context.Context, any) (any, error) {
				time.Sleep(time.Millisecond)
				return wrapperspb.String("response"), nil
			},
			expectObservation: false,
			expectedError:     "",
			expectedCode:      grpccodes.OK,
			expectedResponse:  wrapperspb.String("response"),
		},
		{
			name:  "WithExcludeMethods",
			probe: telemetry.NewNoopProbe(),
			opts: Options{
				ExcludeMethods: []string{"CheckHealth"},
			},
			ctx:  context.Background(),
			req:  wrapperspb.String("request"),
			info: &grpc.UnaryServerInfo{FullMethod: "/myapp.v1.UserService/CheckHealth"},
			handler: func(context.Context, any) (any, error) {
				time.Sleep(time.Millisecond)
				return wrapperspb.String("OK"), nil
			},
			expectObservation: false,
			expectedError:     "",
			expectedCode:      grpccodes.OK,
			expectedResponse:  wrapperspb.String("OK"),
		},
		{
			name:  "HandlerPanics",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			ctx:   context.Background(),
			req:   wrapperspb.String("request"),
			info:  &grpc.UnaryServerInfo{FullMethod: "/myapp.v1.UserService/GetUser"},
			handler: func(context.Context, any) (any, error) {
				time.Sleep(time.Millisecond)
				panic("something went wrong")
			},
			expectObservation: true,
			expectedError:     "rpc error: code = Internal desc = panic: something went wrong",
			expectedCode:      grpccodes.Internal,
			expectedResponse:  nil,
		},
		{
			name:  "HandlerFails_WithGenericError",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			ctx:   context.Background(),
			req:   wrapperspb.String("request"),
			info:  &grpc.UnaryServerInfo{FullMethod: "/myapp.v1.UserService/GetUser"},
			handler: func(context.Context, any) (any, error) {
				time.Sleep(time.Millisecond)
				return nil, errors.New("error on retrieving data")
			},
			expectObservation: true,
			expectedError:     "error on retrieving data",
			expectedCode:      grpccodes.Unknown,
			expectedResponse:  nil,
		},
		{
			name:  "HandlerFails_WithGRPCError",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			ctx:   context.Background(),
			req:   wrapperspb.String("request"),
			info:  &grpc.UnaryServerInfo{FullMethod: "/myapp.v1.UserService/GetUser"},
			handler: func(context.Context, any) (any, error) {
				time.Sleep(time.Millisecond)
				return nil, grpcstatus.Error(grpccodes.Unauthenticated, "user is not authenticated")
			},
			expectObservation: true,
			expectedError:     "rpc error: code = Unauthenticated desc = user is not authenticated",
			expectedCode:      grpccodes.Unauthenticated,
			expectedResponse:  nil,
		},
		{
			name:  "HandlerSucceeds",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			ctx:   context.Background(),
			req:   wrapperspb.String("request"),
			info:  &grpc.UnaryServerInfo{FullMethod: "/myapp.v1.UserService/GetUser"},
			handler: func(context.Context, any) (any, error) {
				time.Sleep(time.Millisecond)
				return wrapperspb.String("response"), nil
			},
			expectObservation: true,
			expectedError:     "",
			expectedCode:      grpccodes.OK,
			expectedResponse:  wrapperspb.String("response"),
		},
		{
			name:  "HandlerSucceeds_WithRequestMetadata",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"x-request-uuid", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
				"x-caller-name", "test-client",
			)),
			req:  wrapperspb.String("request"),
			info: &grpc.UnaryServerInfo{FullMethod: "/myapp.v1.UserService/GetUser"},
			handler: func(context.Context, any) (any, error) {
				time.Sleep(time.Millisecond)
				return wrapperspb.String("response"), nil
			},
			expectObservation:   true,
			expectedError:       "",
			expectedCode:        grpccodes.OK,
			expectedResponse:    wrapperspb.String("response"),
			expectedRequestUUID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			expectedCallerName:  "test-client",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := NewServerInterceptor(tc.probe, tc.opts)
			assert.NotNil(t, i)

			var gotReq any
			var uuidFromCtx, uuidFromMD, callerFromMD string

			// Capture values seen inside the handler for assertions.
			wrappedHandler := func(ctx context.Context, req any) (any, error) {
				gotReq = req
				uuidFromCtx, _ = telemetry.UUIDFromContext(ctx)

				if tc.expectObservation {
					md, ok := metadata.FromIncomingContext(ctx)
					assert.True(t, ok)

					uuidFromMD = md.Get(requestUUIDKey)[0]
					callerFromMD = md.Get(callerNameKey)[0]
				}

				assert.NotNil(t, telemetry.LoggerFromContext(ctx))
				assert.NotNil(t, telemetry.MeterFromContext(ctx))
				assert.NotNil(t, telemetry.TracerFromContext(ctx))

				return tc.handler(ctx, req)
			}

			// Mock the grpc.SetHeader call.
			mockServerTransportStream := &MockServerTransportStream{
				SetHeaderMocks: []MockServerTransportStream_SetHeaderMock{
					{OutError: nil},
				},
			}
			ctx := grpc.NewContextWithServerTransportStream(tc.ctx, mockServerTransportStream)

			res, err := i.Unary()(ctx, tc.req, tc.info, wrappedHandler)

			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}

			assert.Equal(t, tc.expectedCode, grpcstatus.Code(err))
			assertEqualProtoMessages(t, tc.expectedResponse, res)
			assert.Equal(t, tc.req, gotReq)

			if tc.expectObservation {
				// Verify a valid request UUID is generated/preserved and propagated.
				uuidFromResp := mockServerTransportStream.SetHeaderMocks[0].InMD.Get(requestUUIDKey)[0]
				assert.NoError(t, uuid.Validate(uuidFromResp))
				assert.Equal(t, uuidFromResp, uuidFromCtx)
				assert.Equal(t, uuidFromResp, uuidFromMD)

				if tc.expectedRequestUUID != "" {
					assert.Equal(t, tc.expectedRequestUUID, uuidFromResp)
				}

				// Verify the caller name is propagated if present on the request.
				callerFromResp := mockServerTransportStream.SetHeaderMocks[0].InMD.Get(callerNameKey)[0]
				assert.Equal(t, callerFromResp, callerFromMD)

				if tc.expectedCallerName == "" {
					assert.Equal(t, defaultCallerName, callerFromResp)
				} else {
					assert.Equal(t, tc.expectedCallerName, callerFromResp)
				}
			}

			assert.NoError(t, tc.probe.Close(tc.ctx))
		})
	}
}

func assertEqualProtoMessages(t *testing.T, expected, actual any) {
	t.Helper()

	if expected == nil && actual == nil {
		return
	}

	if expected == nil || actual == nil {
		assert.Fail(t, "expected and actual must both be nil or both be non-nil")
		return
	}

	em, ok := expected.(proto.Message)
	assert.True(t, ok)
	am, ok := actual.(proto.Message)
	assert.True(t, ok)

	eb, err := proto.Marshal(em)
	assert.NoError(t, err)
	ab, err := proto.Marshal(am)
	assert.NoError(t, err)

	assert.Equal(t, eb, ab)
}

func TestServerInterceptor_Stream(t *testing.T) {
	tests := []struct {
		name                string
		probe               telemetry.Probe
		opts                Options
		srv                 any
		ss                  *MockServerStream
		info                *grpc.StreamServerInfo
		handler             grpc.StreamHandler
		expectObservation   bool
		expectedError       string
		expectedCode        grpccodes.Code
		expectedRequestUUID string
		expectedCallerName  string
	}{
		{
			name:  "InvalidFullMethod",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			srv:   nil,
			ss: &MockServerStream{
				ContextMocks: []MockServerStream_ContextMock{
					{OutContext: context.Background()},
					{OutContext: context.Background()},
				},
				SendMsgMocks: []MockServerStream_SendMsgMock{
					{OutError: nil},
					{OutError: nil},
				},
				RecvMsgMocks: []MockServerStream_RecvMsgMock{
					{OutError: nil},
				},
			},
			info: &grpc.StreamServerInfo{},
			handler: func(srv any, stream grpc.ServerStream) error {
				time.Sleep(time.Millisecond)
				_ = stream.SendMsg(wrapperspb.String("part-1"))
				_ = stream.SendMsg(wrapperspb.String("part-2"))
				_ = stream.RecvMsg(&wrapperspb.StringValue{})
				return nil
			},
			expectObservation: false,
			expectedError:     "",
			expectedCode:      grpccodes.OK,
		},
		{
			name:  "WithExcludeMethods",
			probe: telemetry.NewNoopProbe(),
			opts: Options{
				ExcludeMethods: []string{"CheckHealth"},
			},
			srv: nil,
			ss: &MockServerStream{
				ContextMocks: []MockServerStream_ContextMock{
					{OutContext: context.Background()},
					{OutContext: context.Background()},
				},
				SendMsgMocks: []MockServerStream_SendMsgMock{
					{OutError: nil},
				},
			},
			info: &grpc.StreamServerInfo{FullMethod: "/myapp.v1.FileService/CheckHealth"},
			handler: func(srv any, stream grpc.ServerStream) error {
				time.Sleep(time.Millisecond)
				_ = stream.SendMsg(wrapperspb.String("OK"))
				return nil
			},
			expectObservation: false,
			expectedError:     "",
			expectedCode:      grpccodes.OK,
		},
		{
			name:  "HandlerPanics",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			srv:   nil,
			ss: &MockServerStream{
				SetHeaderMocks: []MockServerStream_SetHeaderMock{
					{OutError: nil},
				},
				ContextMocks: []MockServerStream_ContextMock{
					{OutContext: context.Background()},
				},
			},
			info: &grpc.StreamServerInfo{FullMethod: "/myapp.v1.FileService/Download"},
			handler: func(srv any, stream grpc.ServerStream) error {
				time.Sleep(time.Millisecond)
				panic("something went wrong")
			},
			expectObservation: true,
			expectedError:     "rpc error: code = Internal desc = panic: something went wrong",
			expectedCode:      grpccodes.Internal,
		},
		{
			name:  "HandlerFails_WithGenericError",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			srv:   nil,
			ss: &MockServerStream{
				SetHeaderMocks: []MockServerStream_SetHeaderMock{
					{OutError: nil},
				},
				ContextMocks: []MockServerStream_ContextMock{
					{OutContext: context.Background()},
				},
			},
			info: &grpc.StreamServerInfo{FullMethod: "/myapp.v1.FileService/Download"},
			handler: func(srv any, stream grpc.ServerStream) error {
				time.Sleep(time.Millisecond)
				return errors.New("error on retrieving data")
			},
			expectObservation: true,
			expectedError:     "error on retrieving data",
			expectedCode:      grpccodes.Unknown,
		},
		{
			name:  "HandlerFails_WithGRPCError",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			srv:   nil,
			ss: &MockServerStream{
				SetHeaderMocks: []MockServerStream_SetHeaderMock{
					{OutError: nil},
				},
				ContextMocks: []MockServerStream_ContextMock{
					{OutContext: context.Background()},
				},
			},
			info: &grpc.StreamServerInfo{FullMethod: "/myapp.v1.FileService/Download"},
			handler: func(srv any, stream grpc.ServerStream) error {
				time.Sleep(time.Millisecond)
				return grpcstatus.Error(grpccodes.Unauthenticated, "user is not authenticated")
			},
			expectObservation: true,
			expectedError:     "rpc error: code = Unauthenticated desc = user is not authenticated",
			expectedCode:      grpccodes.Unauthenticated,
		},
		{
			name:  "HandlerSucceeds",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			srv:   nil,
			ss: &MockServerStream{
				SetHeaderMocks: []MockServerStream_SetHeaderMock{
					{OutError: nil},
				},
				ContextMocks: []MockServerStream_ContextMock{
					{OutContext: context.Background()},
				},
				SendMsgMocks: []MockServerStream_SendMsgMock{
					{OutError: nil},
					{OutError: nil},
				},
				RecvMsgMocks: []MockServerStream_RecvMsgMock{
					{OutError: nil},
				},
			},
			info: &grpc.StreamServerInfo{FullMethod: "/myapp.v1.FileService/Download"},
			handler: func(srv any, stream grpc.ServerStream) error {
				time.Sleep(time.Millisecond)
				_ = stream.SendMsg(wrapperspb.String("part-1"))
				_ = stream.SendMsg(wrapperspb.String("part-2"))
				_ = stream.RecvMsg(&wrapperspb.StringValue{})
				return nil
			},
			expectObservation: true,
			expectedError:     "",
			expectedCode:      grpccodes.OK,
		},
		{
			name:  "HandlerSucceeds_WithRequestMetadata",
			probe: telemetry.NewNoopProbe(),
			opts:  Options{},
			srv:   nil,
			ss: &MockServerStream{
				SetHeaderMocks: []MockServerStream_SetHeaderMock{
					{OutError: nil},
				},
				ContextMocks: []MockServerStream_ContextMock{
					{
						OutContext: metadata.NewIncomingContext(context.Background(), metadata.Pairs(
							"x-request-uuid", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
							"x-caller-name", "test-client",
						)),
					},
				},
				SendMsgMocks: []MockServerStream_SendMsgMock{
					{OutError: nil},
					{OutError: nil},
				},
				RecvMsgMocks: []MockServerStream_RecvMsgMock{
					{OutError: nil},
				},
			},
			info: &grpc.StreamServerInfo{FullMethod: "/myapp.v1.UserService/GetUsers"},
			handler: func(srv any, stream grpc.ServerStream) error {
				time.Sleep(time.Millisecond)
				_ = stream.SendMsg(wrapperspb.String("part-1"))
				_ = stream.SendMsg(wrapperspb.String("part-2"))
				_ = stream.RecvMsg(&wrapperspb.StringValue{})
				return nil
			},
			expectObservation:   true,
			expectedError:       "",
			expectedCode:        grpccodes.OK,
			expectedRequestUUID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			expectedCallerName:  "test-client",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			i := NewServerInterceptor(tc.probe, tc.opts)
			assert.NotNil(t, i)

			var uuidFromCtx, uuidFromMD, callerFromMD string

			// Capture values seen inside the handler for assertions.
			wrappedHandler := func(srv any, stream grpc.ServerStream) error {
				ctx := stream.Context()
				uuidFromCtx, _ = telemetry.UUIDFromContext(ctx)

				if tc.expectObservation {
					_, ok := stream.(*observableServerStream)
					assert.True(t, ok)

					md, ok := metadata.FromIncomingContext(ctx)
					assert.True(t, ok)

					uuidFromMD = md.Get(requestUUIDKey)[0]
					callerFromMD = md.Get(callerNameKey)[0]
				}

				assert.NotNil(t, telemetry.LoggerFromContext(ctx))
				assert.NotNil(t, telemetry.MeterFromContext(ctx))
				assert.NotNil(t, telemetry.TracerFromContext(ctx))

				return tc.handler(srv, stream)
			}

			err := i.Stream()(tc.srv, tc.ss, tc.info, wrappedHandler)

			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}

			assert.Equal(t, tc.expectedCode, grpcstatus.Code(err))

			if tc.expectObservation {
				// Verify a valid request UUID is generated/preserved and propagated.
				uuidFromResp := tc.ss.SetHeaderMocks[0].InMD.Get(requestUUIDKey)[0]
				assert.NoError(t, uuid.Validate(uuidFromResp))
				assert.Equal(t, uuidFromResp, uuidFromCtx)
				assert.Equal(t, uuidFromResp, uuidFromMD)

				if tc.expectedRequestUUID != "" {
					assert.Equal(t, tc.expectedRequestUUID, uuidFromResp)
				}

				// Verify the caller name is propagated if present on the request.
				callerFromResp := tc.ss.SetHeaderMocks[0].InMD.Get(callerNameKey)[0]
				assert.Equal(t, callerFromResp, callerFromMD)

				if tc.expectedCallerName == "" {
					assert.Equal(t, defaultCallerName, callerFromResp)
				} else {
					assert.Equal(t, tc.expectedCallerName, callerFromResp)
				}
			}

			assert.NoError(t, tc.probe.Close(context.Background()))
		})
	}
}

// -------------------------------------------------- Auxiliary Types --------------------------------------------------

func TestNewObservableServerStream(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("")

	tests := []struct {
		name    string
		ctx     context.Context
		ss      grpc.ServerStream
		metrics *serverMetrics
		opts    metric.MeasurementOption
	}{
		{
			name:    "WithServerStream",
			ctx:     context.Background(),
			ss:      &MockServerStream{},
			metrics: newServerMetrics(meter),
			opts: metric.WithAttributes(
				attribute.String("service", "echo-service"),
			),
		},
		{
			name: "WithObservableServerStream",
			ctx:  context.Background(),
			ss: &observableServerStream{
				ServerStream: &MockServerStream{},
			},
			metrics: newServerMetrics(meter),
			opts: metric.WithAttributes(
				attribute.String("service", "echo-service"),
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oss := newObservableServerStream(tc.ctx, tc.ss, tc.metrics, tc.opts)

			assert.NotNil(t, oss)
			assert.NotNil(t, oss.ServerStream)
			assert.Equal(t, tc.ctx, oss.ctx)
			assert.Same(t, tc.metrics, oss.metrics)
			assert.Same(t, tc.opts, oss.opts)
		})
	}
}

func TestObservableServerStream_Context(t *testing.T) {
	tests := []struct {
		name            string
		s               *observableServerStream
		expectedContext context.Context
	}{
		{
			name: "WithoutExplicitContext",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					ContextMocks: []MockServerStream_ContextMock{
						{OutContext: context.Background()},
					},
				},
			},
			expectedContext: context.Background(),
		},
		{
			name: "WithExplicitContext",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					ContextMocks: []MockServerStream_ContextMock{
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

func TestObservableServerStream_SetHeader(t *testing.T) {
	tests := []struct {
		name             string
		s                *observableServerStream
		md               metadata.MD
		expectedError    string
		expectedHeaderMD metadata.MD
	}{
		{
			name: "Success",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					SetHeaderMocks: []MockServerStream_SetHeaderMock{
						{OutError: nil},
					},
				},
				headerMD: metadata.Pairs("existing-key", "existing-value"),
			},
			md:            metadata.Pairs("x-served-by", "test-server-0"),
			expectedError: "",
			expectedHeaderMD: metadata.Join(
				metadata.Pairs("existing-key", "existing-value"),
				metadata.Pairs("x-served-by", "test-server-0"),
			),
		},
		{
			name: "WithError",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					SetHeaderMocks: []MockServerStream_SetHeaderMock{
						{OutError: errors.New("connection failed")},
					},
				},
				headerMD: metadata.Pairs("existing-key", "existing-value"),
			},
			md:            metadata.Pairs("x-served-by", "test-server-0"),
			expectedError: "connection failed",
			expectedHeaderMD: metadata.Join(
				metadata.Pairs("existing-key", "existing-value"),
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.SetHeader(tc.md)

			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}

			assert.Equal(t, tc.expectedHeaderMD, tc.s.headerMD)
		})
	}
}

func TestObservableServerStream_SendHeader(t *testing.T) {
	tests := []struct {
		name             string
		s                *observableServerStream
		md               metadata.MD
		expectedError    string
		expectedHeaderMD metadata.MD
	}{
		{
			name: "Success",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					SendHeaderMocks: []MockServerStream_SendHeaderMock{
						{OutError: nil},
					},
				},
				headerMD: metadata.Pairs("existing-key", "existing-value"),
			},
			md:            metadata.Pairs("x-served-by", "test-server-0"),
			expectedError: "",
			expectedHeaderMD: metadata.Join(
				metadata.Pairs("existing-key", "existing-value"),
				metadata.Pairs("x-served-by", "test-server-0"),
			),
		},
		{
			name: "WithError",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					SendHeaderMocks: []MockServerStream_SendHeaderMock{
						{OutError: errors.New("connection failed")},
					},
				},
				headerMD: metadata.Pairs("existing-key", "existing-value"),
			},
			md:            metadata.Pairs("x-served-by", "test-server-0"),
			expectedError: "connection failed",
			expectedHeaderMD: metadata.Join(
				metadata.Pairs("existing-key", "existing-value"),
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.SendHeader(tc.md)

			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}

			assert.Equal(t, tc.expectedHeaderMD, tc.s.headerMD)
		})
	}
}

func TestObservableServerStream_SetTrailer(t *testing.T) {
	tests := []struct {
		name              string
		s                 *observableServerStream
		md                metadata.MD
		expectedTrailerMD metadata.MD
	}{
		{
			name: "Success",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					SetTrailerMocks: []MockServerStream_SetTrailerMock{
						{},
					},
				},
				trailerMD: metadata.Pairs("existing-key", "existing-value"),
			},
			md: metadata.Pairs("x-request-cost", "100"),
			expectedTrailerMD: metadata.Join(
				metadata.Pairs("existing-key", "existing-value"),
				metadata.Pairs("x-request-cost", "100"),
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.s.SetTrailer(tc.md)

			assert.Equal(t, tc.expectedTrailerMD, tc.s.trailerMD)
		})
	}
}

func TestObservableServerStream_SendMsg(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("")

	tests := []struct {
		name          string
		s             *observableServerStream
		msg           any
		expectedError string
	}{
		{
			name: "Success",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					SendMsgMocks: []MockServerStream_SendMsgMock{
						{OutError: nil},
					},
				},
				ctx:     context.Background(),
				metrics: newServerMetrics(meter),
			},
			msg: wrapperspb.String("Hello, World!"),
		},
		{
			name: "WithError_EOF",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					SendMsgMocks: []MockServerStream_SendMsgMock{
						{OutError: io.EOF},
					},
				},
				ctx:     context.Background(),
				metrics: newServerMetrics(meter),
			},
			msg:           wrapperspb.String("Hello, World!"),
			expectedError: "EOF",
		},
		{
			name: "WithError",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					SendMsgMocks: []MockServerStream_SendMsgMock{
						{OutError: errors.New("connection failed")},
					},
				},
				ctx:     context.Background(),
				metrics: newServerMetrics(meter),
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

func TestObservableServerStream_RecvMsg(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("")

	tests := []struct {
		name          string
		s             *observableServerStream
		msg           any
		expectedError string
	}{
		{
			name: "Success",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					RecvMsgMocks: []MockServerStream_RecvMsgMock{
						{OutError: nil},
					},
				},
				ctx:     context.Background(),
				metrics: newServerMetrics(meter),
			},
			msg: &wrapperspb.StringValue{},
		},
		{
			name: "WithError_EOF",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					RecvMsgMocks: []MockServerStream_RecvMsgMock{
						{OutError: io.EOF},
					},
				},
				ctx:     context.Background(),
				metrics: newServerMetrics(meter),
			},
			msg:           &wrapperspb.StringValue{},
			expectedError: "EOF",
		},
		{
			name: "WithError",
			s: &observableServerStream{
				ServerStream: &MockServerStream{
					RecvMsgMocks: []MockServerStream_RecvMsgMock{
						{OutError: errors.New("connection failed")},
					},
				},
				ctx:     context.Background(),
				metrics: newServerMetrics(meter),
			},
			msg:           &wrapperspb.StringValue{},
			expectedError: "connection failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.RecvMsg(tc.msg)

			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}
		})
	}
}

func TestObservableServerStream_Header(t *testing.T) {
	tests := []struct {
		name           string
		s              *observableServerStream
		expectedHeader metadata.MD
	}{
		{
			name:           "Empty",
			s:              &observableServerStream{},
			expectedHeader: metadata.MD{},
		},
		{
			name: "WithHeader",
			s: &observableServerStream{
				headerMD: metadata.Pairs("x-served-by", "test-server-0"),
			},
			expectedHeader: metadata.Pairs("x-served-by", "test-server-0"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedHeader, tc.s.Header())
		})
	}
}

func TestObservableServerStream_Trailer(t *testing.T) {
	tests := []struct {
		name            string
		s               *observableServerStream
		expectedTrailer metadata.MD
	}{
		{
			name:            "Empty",
			s:               &observableServerStream{},
			expectedTrailer: metadata.MD{},
		},
		{
			name: "WithTrailer",
			s: &observableServerStream{
				trailerMD: metadata.Pairs("x-request-cost", "100"),
			},
			expectedTrailer: metadata.Pairs("x-request-cost", "100"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedTrailer, tc.s.Trailer())
		})
	}
}

func TestNewObservableServerTransportStream(t *testing.T) {
	tests := []struct {
		name string
		s    grpc.ServerTransportStream
	}{
		{
			name: "WithServerTransportStream",
			s:    &MockServerTransportStream{},
		},
		{
			name: "WithObservableServerTransportStream",
			s: &observableServerTransportStream{
				ServerTransportStream: &MockServerTransportStream{},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			os := newObservableServerTransportStream(tc.s)

			assert.NotNil(t, os)
			assert.NotNil(t, os.ServerTransportStream)
		})
	}
}

func TestObservableServerTransportStream_SetHeader(t *testing.T) {
	tests := []struct {
		name             string
		s                *observableServerTransportStream
		md               metadata.MD
		expectedError    string
		expectedHeaderMD metadata.MD
	}{
		{
			name: "Success",
			s: &observableServerTransportStream{
				ServerTransportStream: &MockServerTransportStream{
					SetHeaderMocks: []MockServerTransportStream_SetHeaderMock{
						{OutError: nil},
					},
				},
				headerMD: metadata.Pairs("existing-key", "existing-value"),
			},
			md:            metadata.Pairs("x-served-by", "test-server-0"),
			expectedError: "",
			expectedHeaderMD: metadata.Join(
				metadata.Pairs("existing-key", "existing-value"),
				metadata.Pairs("x-served-by", "test-server-0"),
			),
		},
		{
			name: "WithError",
			s: &observableServerTransportStream{
				ServerTransportStream: &MockServerTransportStream{
					SetHeaderMocks: []MockServerTransportStream_SetHeaderMock{
						{OutError: errors.New("connection failed")},
					},
				},
				headerMD: metadata.Pairs("existing-key", "existing-value"),
			},
			md:            metadata.Pairs("x-served-by", "test-server-0"),
			expectedError: "connection failed",
			expectedHeaderMD: metadata.Join(
				metadata.Pairs("existing-key", "existing-value"),
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.SetHeader(tc.md)

			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}

			assert.Equal(t, tc.expectedHeaderMD, tc.s.headerMD)
		})
	}
}

func TestObservableServerTransportStream_SendHeader(t *testing.T) {
	tests := []struct {
		name             string
		s                *observableServerTransportStream
		md               metadata.MD
		expectedError    string
		expectedHeaderMD metadata.MD
	}{
		{
			name: "Success",
			s: &observableServerTransportStream{
				ServerTransportStream: &MockServerTransportStream{
					SendHeaderMocks: []MockServerTransportStream_SendHeaderMock{
						{OutError: nil},
					},
				},
				headerMD: metadata.Pairs("existing-key", "existing-value"),
			},
			md:            metadata.Pairs("x-served-by", "test-server-0"),
			expectedError: "",
			expectedHeaderMD: metadata.Join(
				metadata.Pairs("existing-key", "existing-value"),
				metadata.Pairs("x-served-by", "test-server-0"),
			),
		},
		{
			name: "WithError",
			s: &observableServerTransportStream{
				ServerTransportStream: &MockServerTransportStream{
					SendHeaderMocks: []MockServerTransportStream_SendHeaderMock{
						{OutError: errors.New("connection failed")},
					},
				},
				headerMD: metadata.Pairs("existing-key", "existing-value"),
			},
			md:            metadata.Pairs("x-served-by", "test-server-0"),
			expectedError: "connection failed",
			expectedHeaderMD: metadata.Join(
				metadata.Pairs("existing-key", "existing-value"),
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.SendHeader(tc.md)

			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}

			assert.Equal(t, tc.expectedHeaderMD, tc.s.headerMD)
		})
	}
}

func TestObservableServerTransportStream_SetTrailer(t *testing.T) {
	tests := []struct {
		name              string
		s                 *observableServerTransportStream
		md                metadata.MD
		expectedError     string
		expectedTrailerMD metadata.MD
	}{
		{
			name: "Success",
			s: &observableServerTransportStream{
				ServerTransportStream: &MockServerTransportStream{
					SetTrailerMocks: []MockServerTransportStream_SetTrailerMock{
						{OutError: nil},
					},
				},
				trailerMD: metadata.Pairs("existing-key", "existing-value"),
			},
			md:            metadata.Pairs("x-request-cost", "100"),
			expectedError: "",
			expectedTrailerMD: metadata.Join(
				metadata.Pairs("existing-key", "existing-value"),
				metadata.Pairs("x-request-cost", "100"),
			),
		},
		{
			name: "Success",
			s: &observableServerTransportStream{
				ServerTransportStream: &MockServerTransportStream{
					SetTrailerMocks: []MockServerTransportStream_SetTrailerMock{
						{OutError: errors.New("connection failed")},
					},
				},
				trailerMD: metadata.Pairs("existing-key", "existing-value"),
			},
			md:            metadata.Pairs("x-request-cost", "100"),
			expectedError: "connection failed",
			expectedTrailerMD: metadata.Join(
				metadata.Pairs("existing-key", "existing-value"),
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.SetTrailer(tc.md)

			if tc.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.expectedError)
			}

			assert.Equal(t, tc.expectedTrailerMD, tc.s.trailerMD)
		})
	}
}

func TestObservableServerTransportStream_Header(t *testing.T) {
	tests := []struct {
		name           string
		s              *observableServerTransportStream
		expectedHeader metadata.MD
	}{
		{
			name:           "Empty",
			s:              &observableServerTransportStream{},
			expectedHeader: metadata.MD{},
		},
		{
			name: "WithHeader",
			s: &observableServerTransportStream{
				headerMD: metadata.Pairs("x-served-by", "test-server-0"),
			},
			expectedHeader: metadata.Pairs("x-served-by", "test-server-0"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedHeader, tc.s.Header())
		})
	}
}

func TestObservableServerTransportStream_Trailer(t *testing.T) {
	tests := []struct {
		name            string
		s               *observableServerTransportStream
		expectedTrailer metadata.MD
	}{
		{
			name:            "Empty",
			s:               &observableServerTransportStream{},
			expectedTrailer: metadata.MD{},
		},
		{
			name: "WithTrailer",
			s: &observableServerTransportStream{
				trailerMD: metadata.Pairs("x-request-cost", "100"),
			},
			expectedTrailer: metadata.Pairs("x-request-cost", "100"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedTrailer, tc.s.Trailer())
		})
	}
}

// -------------------------------------------------- Mock Types --------------------------------------------------

type (
	MockServerStream struct {
		ContextIndex int
		ContextMocks []MockServerStream_ContextMock

		SetHeaderIndex int
		SetHeaderMocks []MockServerStream_SetHeaderMock

		SendHeaderIndex int
		SendHeaderMocks []MockServerStream_SendHeaderMock

		SetTrailerIndex int
		SetTrailerMocks []MockServerStream_SetTrailerMock

		SendMsgIndex int
		SendMsgMocks []MockServerStream_SendMsgMock

		RecvMsgIndex int
		RecvMsgMocks []MockServerStream_RecvMsgMock
	}

	MockServerStream_ContextMock struct {
		OutContext context.Context
	}

	MockServerStream_SetHeaderMock struct {
		InMD     metadata.MD
		OutError error
	}

	MockServerStream_SendHeaderMock struct {
		InMD     metadata.MD
		OutError error
	}

	MockServerStream_SetTrailerMock struct {
		InMD metadata.MD
	}

	MockServerStream_SendMsgMock struct {
		InMsg    any
		OutError error
	}

	MockServerStream_RecvMsgMock struct {
		InMsg    any
		OutError error
	}
)

func (m *MockServerStream) Context() context.Context {
	if m.ContextIndex >= len(m.ContextMocks) {
		panic("Context called more times than expected")
	}

	i := m.ContextIndex
	m.ContextIndex++

	return m.ContextMocks[i].OutContext
}

func (m *MockServerStream) SetHeader(md metadata.MD) error {
	if m.SetHeaderIndex >= len(m.SetHeaderMocks) {
		panic("SetHeader called more times than expected")
	}

	i := m.SetHeaderIndex
	m.SetHeaderIndex++

	m.SetHeaderMocks[i].InMD = md

	return m.SetHeaderMocks[i].OutError
}

func (m *MockServerStream) SendHeader(md metadata.MD) error {
	if m.SendHeaderIndex >= len(m.SendHeaderMocks) {
		panic("SendHeader called more times than expected")
	}

	i := m.SendHeaderIndex
	m.SendHeaderIndex++

	m.SendHeaderMocks[i].InMD = md

	return m.SendHeaderMocks[i].OutError
}

func (m *MockServerStream) SetTrailer(md metadata.MD) {
	if m.SetTrailerIndex >= len(m.SetTrailerMocks) {
		panic("SetTrailer called more times than expected")
	}

	i := m.SetTrailerIndex
	m.SetTrailerIndex++

	m.SetTrailerMocks[i].InMD = md
}

func (m *MockServerStream) SendMsg(msg any) error {
	if m.SendMsgIndex >= len(m.SendMsgMocks) {
		panic("SendMsg called more times than expected")
	}

	i := m.SendMsgIndex
	m.SendMsgIndex++

	m.SendMsgMocks[i].InMsg = msg

	return m.SendMsgMocks[i].OutError
}

func (m *MockServerStream) RecvMsg(msg any) error {
	if m.RecvMsgIndex >= len(m.RecvMsgMocks) {
		panic("RecvMsg called more times than expected")
	}

	i := m.RecvMsgIndex
	m.RecvMsgIndex++

	m.RecvMsgMocks[i].InMsg = msg

	return m.RecvMsgMocks[i].OutError
}

type (
	MockServerTransportStream struct {
		OutMethod string

		SetHeaderIndex int
		SetHeaderMocks []MockServerTransportStream_SetHeaderMock

		SendHeaderIndex int
		SendHeaderMocks []MockServerTransportStream_SendHeaderMock

		SetTrailerIndex int
		SetTrailerMocks []MockServerTransportStream_SetTrailerMock
	}

	MockServerTransportStream_SetHeaderMock struct {
		InMD     metadata.MD
		OutError error
	}

	MockServerTransportStream_SendHeaderMock struct {
		InMD     metadata.MD
		OutError error
	}

	MockServerTransportStream_SetTrailerMock struct {
		InMD     metadata.MD
		OutError error
	}
)

func (m *MockServerTransportStream) Method() string {
	return m.OutMethod
}

func (m *MockServerTransportStream) SetHeader(md metadata.MD) error {
	if m.SetHeaderIndex >= len(m.SetHeaderMocks) {
		panic("SetHeader called more times than expected")
	}

	i := m.SetHeaderIndex
	m.SetHeaderIndex++

	m.SetHeaderMocks[i].InMD = md

	return m.SetHeaderMocks[i].OutError
}

func (m *MockServerTransportStream) SendHeader(md metadata.MD) error {
	if m.SendHeaderIndex >= len(m.SendHeaderMocks) {
		panic("SendHeader called more times than expected")
	}

	i := m.SendHeaderIndex
	m.SendHeaderIndex++

	m.SendHeaderMocks[i].InMD = md

	return m.SendHeaderMocks[i].OutError
}

func (m *MockServerTransportStream) SetTrailer(md metadata.MD) error {
	if m.SetTrailerIndex >= len(m.SetTrailerMocks) {
		panic("SetTrailer called more times than expected")
	}

	i := m.SetTrailerIndex
	m.SetTrailerIndex++

	m.SetTrailerMocks[i].InMD = md

	return m.SetTrailerMocks[i].OutError
}
