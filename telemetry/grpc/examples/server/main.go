package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/neatplatform/mint/grace"
	"github.com/neatplatform/mint/telemetry"

	telegrpc "github.com/neatplatform/mint/telemetry/grpc"
	filev1 "github.com/neatplatform/mint/telemetry/grpc/examples/proto/file/v1"
)

var (
	headerKV = []string{
		"x-served-by", "grpc-server-0",
	}

	trailerKV = []string{
		"x-request-cost", "100",
	}
)

func main() {
	ctx := context.Background()

	var addr, metricsAddr string
	flag.StringVar(&addr, "addr", ":8080", "the address to listen on for grpc traffic")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8081", "the address to listen on for the metrics endpoint")
	flag.Parse()

	// Configure a new probe.
	probe := telemetry.NewProbe(
		telemetry.WithMetadata("file-service", "v0.1.0", map[string]any{
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

	// Create an interceptor to record observability signals.
	si := telegrpc.NewServerInterceptor(probe, telegrpc.Options{
		ExcludeMethods: []string{
			"Check",
			"Watch",
		},
		LogMetadata: telegrpc.MetadataConfig{
			"authorization":  telegrpc.Redact,
			"x-tenant-id":    telegrpc.Truncate,
			"x-served-by":    telegrpc.Truncate,
			"x-request-cost": telegrpc.Truncate,
		},
	})

	// Create the gRPC file server and the HTTP metrics server.
	fileServer := newFileServer("file-server", addr, si, probe)
	metricsServer := newMetricsServer("metrics-server", metricsAddr, probe.ServeHTTP)

	// Register the servers so they can be started and shut down gracefully on termination signals.
	grace.SetLogger(probe.Logger())
	grace.RegisterServer(fileServer, metricsServer)

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

// -------------------------------------------------- File Server --------------------------------------------------

// FileServer is a production-grade grpc server implementing the FileService,
// and implements the grace.Server interface so it can be started and gracefully shut down.
type FileServer struct {
	*grpc.Server

	name string
	addr string
}

// newFileServer creates a new FileServer listening on addr.
func newFileServer(name, addr string, si *telegrpc.ServerInterceptor, probe telemetry.Probe) *FileServer {
	server := grpc.NewServer(
		grpc.UnaryInterceptor(si.Unary()),
		grpc.StreamInterceptor(si.Stream()),
	)

	// Register the standard grpc health service.
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	healthpb.RegisterHealthServer(server, healthServer)
	filev1.RegisterFileServiceServer(server, newFileServiceServer())

	return &FileServer{
		Server: server,
		name:   name,
		addr:   addr,
	}
}

// String implements the grace.Server interface.
func (s *FileServer) String() string {
	return s.name
}

// ListenAndServe implements the grace.Server interface.
func (s *FileServer) ListenAndServe() error {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	return s.Serve(l)
}

// Shutdown implements the grace.Server interface.
func (s *FileServer) Shutdown(ctx context.Context) error {
	stopped := make(chan struct{})

	go func() {
		s.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		s.Stop()
		return ctx.Err()
	}
}

// -------------------------------------------------- FileServiceServer --------------------------------------------------

const (
	minResponseDelay  = 5 * time.Millisecond
	maxResponseDelay  = 50 * time.Millisecond
	downloadChunkSize = 4 * 1024 // 4 KB
)

// file is an in-memory representation of a file.
type file struct {
	name     string
	data     []byte
	checksum string
}

// fileServiceServer implements the filev1.FileServiceServer interface.
type fileServiceServer struct {
	filev1.UnimplementedFileServiceServer

	mu    sync.RWMutex
	files map[string]*file
}

func newFileServiceServer() *fileServiceServer {
	return &fileServiceServer{
		files: make(map[string]*file, 10),
	}
}

// GetFileInfo returns metadata about a previously uploaded file.
func (s *fileServiceServer) GetFileInfo(ctx context.Context, req *filev1.GetFileInfoRequest) (*filev1.GetFileInfoResponse, error) {
	logger := telemetry.LoggerFromContext(ctx)
	tracer := telemetry.TracerFromContext(ctx)

	_, span := tracer.Start(ctx, "read-file-info")
	defer span.End()

	// SetHeader may be called multiple times; each call merges into the pending header metadata.
	// Headers are flushed by SendHeader, by the first response, or when the handler returns.
	if err := grpc.SetHeader(ctx, metadata.Pairs(headerKV...)); err != nil {
		logger.Errorf("failed to set header: %s", err)
	}

	defer func() {
		// SetTrailer may be called multiple times; each call merges into the pending trailer metadata.
		// Trailers are sent when the handler returns, along with the RPC status, including the error path.
		if err := grpc.SetTrailer(ctx, metadata.Pairs(trailerKV...)); err != nil {
			logger.Errorf("failed to set trailer: %s", err)
		}
	}()

	fileID := req.GetFileId()
	if fileID == "" {
		logger.Errorf("file_id is missing")
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}

	sleep()

	s.mu.RLock()
	file, ok := s.files[fileID]
	s.mu.RUnlock()

	if !ok {
		logger.Errorf("file not found: %s", fileID)
		return nil, status.Errorf(codes.NotFound, "file not found: %s", fileID)
	}

	return &filev1.GetFileInfoResponse{
		FileInfo: &filev1.FileInfo{
			FileId: fileID,
			Name:   file.name,
			Size:   int64(len(file.data)),
		},
	}, nil
}

// Download streams a previously uploaded file to the client in fixed-size chunks.
func (s *fileServiceServer) Download(req *filev1.DownloadRequest, stream filev1.FileService_DownloadServer) error {
	ctx := stream.Context()
	logger := telemetry.LoggerFromContext(ctx)
	tracer := telemetry.TracerFromContext(ctx)

	_, span := tracer.Start(ctx, "download-file")
	defer span.End()

	// SetHeader may be called multiple times; each call merges into the pending header metadata.
	// Headers are flushed by SendHeader, by the first response, or when the handler returns.
	if err := stream.SetHeader(metadata.Pairs(headerKV...)); err != nil {
		logger.Errorf("failed to set header: %s", err)
	}

	defer func() {
		// SetTrailer may be called multiple times; each call merges into the pending trailer metadata.
		// Trailers are sent when the handler returns, along with the RPC status, including the error path.
		stream.SetTrailer(metadata.Pairs(trailerKV...))
	}()

	fileID := req.GetFileId()
	if fileID == "" {
		logger.Errorf("file_id is missing")
		return status.Error(codes.InvalidArgument, "file_id is required")
	}

	s.mu.RLock()
	file, ok := s.files[fileID]
	s.mu.RUnlock()

	if !ok {
		logger.Errorf("file not found: %s", fileID)
		return status.Errorf(codes.NotFound, "file not found: %s", fileID)
	}

	for offset := 0; offset < len(file.data); offset += downloadChunkSize {
		if err := ctx.Err(); err != nil {
			return status.FromContextError(err).Err()
		}

		sleep()

		end := min(offset+downloadChunkSize, len(file.data))
		resp := &filev1.DownloadResponse{
			FileChunk: &filev1.FileChunk{
				Data:   file.data[offset:end],
				Offset: int64(offset),
			},
			DownloadedAt: time.Now().UnixMilli(),
		}

		if err := stream.Send(resp); err != nil {
			logger.Errorf("failed to send file chunk: %s", err)
			return err
		}
	}

	return nil
}

// Upload receives a file as a stream of chunks and stores it in memory.
func (s *fileServiceServer) Upload(stream filev1.FileService_UploadServer) error {
	ctx := stream.Context()
	logger := telemetry.LoggerFromContext(ctx)
	tracer := telemetry.TracerFromContext(ctx)

	_, span := tracer.Start(ctx, "upload-file")
	defer span.End()

	// SetHeader may be called multiple times; each call merges into the pending header metadata.
	// Headers are flushed by SendHeader, by the first response, or when the handler returns.
	if err := stream.SetHeader(metadata.Pairs(headerKV...)); err != nil {
		logger.Errorf("failed to set header: %s", err)
	}

	defer func() {
		// SetTrailer may be called multiple times; each call merges into the pending trailer metadata.
		// Trailers are sent when the handler returns, along with the RPC status, including the error path.
		stream.SetTrailer(metadata.Pairs(trailerKV...))
	}()

	var buf bytes.Buffer

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.Errorf("failed to receive file chunk: %s", err)
			return err
		}

		sleep()
		buf.Write(req.GetFileChunk().GetData())
	}

	fileID := uuid.New().String()
	data := buf.Bytes()
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])

	s.mu.Lock()
	s.files[fileID] = &file{
		name:     fileID,
		data:     data,
		checksum: checksum,
	}
	s.mu.Unlock()

	logger.Infof("file uploaded: %s (%d bytes)", fileID, len(data))

	return stream.SendAndClose(&filev1.UploadResponse{
		FileId:   fileID,
		Size:     int64(len(data)),
		Checksum: checksum,
	})
}

// Sync exchanges file events with the client, acknowledging every event it receives.
func (s *fileServiceServer) Sync(stream filev1.FileService_SyncServer) error {
	ctx := stream.Context()
	logger := telemetry.LoggerFromContext(ctx)
	tracer := telemetry.TracerFromContext(ctx)

	_, span := tracer.Start(ctx, "sync-events")
	defer span.End()

	// SetHeader may be called multiple times; each call merges into the pending header metadata.
	// Headers are flushed by SendHeader, by the first response, or when the handler returns.
	if err := stream.SetHeader(metadata.Pairs(headerKV...)); err != nil {
		logger.Errorf("failed to set header: %s", err)
	}

	defer func() {
		// SetTrailer may be called multiple times; each call merges into the pending trailer metadata.
		// Trailers are sent when the handler returns, along with the RPC status, including the error path.
		stream.SetTrailer(metadata.Pairs(trailerKV...))
	}()

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			logger.Errorf("failed to receive file event: %s", err)
			return err
		}

		sleep()
		event := req.GetEvent()

		logger.Debug("file event received.",
			"type", event.GetType(),
			"file_id", event.GetFileId(),
			"name", event.GetName(),
		)

		resp := &filev1.SyncResponse{
			Event: event,
		}

		if err := stream.Send(resp); err != nil {
			logger.Errorf("failed to send file event: %s", err)
			return err
		}
	}
}

// Simulate a processing latency so the duration metric will follow a realistic distribution.
func sleep() {
	time.Sleep(minResponseDelay + rand.N(maxResponseDelay-minResponseDelay))
}
