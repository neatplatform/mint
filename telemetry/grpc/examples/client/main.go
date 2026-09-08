package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io"
	"math/rand/v2"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/neatplatform/mint/telemetry"

	telegrpc "github.com/neatplatform/mint/telemetry/grpc"
	filev1 "github.com/neatplatform/mint/telemetry/grpc/examples/proto/file/v1"
)

const (
	minCallInterval = 500 * time.Millisecond
	maxCallInterval = 5 * time.Second

	minFileSize     = 4 * 1024  // 4 KB
	maxFileSize     = 32 * 1024 // 32 KB
	uploadChunkSize = 4 * 1024  // 4 KB
)

var metadataKV = []string{
	"authorization", "Bearer secret-token",
	"x-tenant-id", "internal",
}

func main() {
	ctx := context.Background()

	var server string
	flag.StringVar(&server, "server", "localhost:8080", "the file server address")
	flag.Parse()

	// Configure a new probe.
	probe := telemetry.NewProbe(
		telemetry.WithMetadata("file-client", "v0.1.0", map[string]any{
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

	// Create a grpc client interceptor to record observability signals.
	ci := telegrpc.NewClientInterceptor(probe, telegrpc.Options{
		LogMetadata: telegrpc.MetadataConfig{
			"authorization":  telegrpc.Redact,
			"x-tenant-id":    telegrpc.Truncate,
			"x-served-by":    telegrpc.Truncate,
			"x-request-cost": telegrpc.Truncate,
		},
	})

	// Dial the file server.
	conn, err := grpc.NewClient(server,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(ci.Unary()),
		grpc.WithChainStreamInterceptor(ci.Stream()),
	)

	if err != nil {
		probe.Logger().Errorf("failed to dial file server: %s", err)
		return
	}

	defer func() {
		_ = conn.Close()
	}()

	client := filev1.NewFileServiceClient(conn)

	for range 5 {
		// Simulate an interval between calls.
		time.Sleep(minCallInterval + rand.N(maxCallInterval-minCallInterval))

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		runScenario(ctx, client)
		cancel()
	}
}

// runScenario exercises all four FileService endpoints against a generated pseudo file.
func runScenario(ctx context.Context, client filev1.FileServiceClient) {
	probe := telemetry.GetProbe()
	logger := probe.Logger()
	tracer := probe.Tracer()

	ctx, span := tracer.Start(ctx, "run-scenario")
	defer span.End()

	// Generate a random byte slice to be used as file content.
	size := minFileSize + rand.N(maxFileSize-minFileSize)
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(rand.N(256))
	}

	fileID, checksum, err := uploadFile(ctx, client, data)
	if err != nil {
		logger.Errorf("upload failed: %s", err)
		return
	}

	logger.Debug("file uploaded.",
		"file_id", fileID,
		"checksum", checksum,
	)

	info, err := getFileInfo(ctx, client, fileID)
	if err != nil {
		logger.Errorf("get file info failed: %s", err)
		return
	}

	logger.Debug("file info received.",
		"file_id", info.GetFileId(),
		"name", info.GetName(),
		"size", info.GetSize(),
	)

	downloaded, err := downloadFile(ctx, client, fileID)
	if err != nil {
		logger.Errorf("download failed: %s", err)
		return
	}

	sum := sha256.Sum256(downloaded)
	downloadedChecksum := hex.EncodeToString(sum[:])
	if downloadedChecksum != checksum {
		logger.Errorf("checksum mismatch: uploaded %s, downloaded %s", checksum, downloadedChecksum)
		return
	}

	logger.Debug("file downloaded and verified.",
		"file_id", fileID,
		"size", len(downloaded),
	)

	events := []*filev1.FileEvent{
		{Type: filev1.FileEvent_TYPE_CREATED, FileId: fileID, Name: info.GetName(), Timestamp: time.Now().UnixMilli()},
		{Type: filev1.FileEvent_TYPE_MODIFIED, FileId: fileID, Name: info.GetName(), Timestamp: time.Now().UnixMilli()},
		{Type: filev1.FileEvent_TYPE_DELETED, FileId: fileID, Name: info.GetName(), Timestamp: time.Now().UnixMilli()},
	}

	if err := syncEvents(ctx, client, events); err != nil {
		logger.Errorf("sync events failed: %s", err)
		return
	}

	logger.Debug("sync events succeeded.")
}

// getFileInfo retrieves metadata about a previously uploaded file via a unary RPC.
func getFileInfo(ctx context.Context, client filev1.FileServiceClient, fileID string) (*filev1.FileInfo, error) {
	probe := telemetry.GetProbe()
	tracer := probe.Tracer()

	ctx, span := tracer.Start(ctx, "get-file-info-call")
	defer span.End()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(metadataKV...))
	req := &filev1.GetFileInfoRequest{
		FileId: fileID,
	}

	resp, err := client.GetFileInfo(ctx, req)
	if err != nil {
		// Error is logged by the interceptor.
		return nil, err
	}

	return resp.GetFileInfo(), nil
}

// downloadFile receives a previously uploaded file from the server via a server-streaming RPC.
func downloadFile(ctx context.Context, client filev1.FileServiceClient, fileID string) ([]byte, error) {
	probe := telemetry.GetProbe()
	logger := probe.Logger()
	tracer := probe.Tracer()

	ctx, span := tracer.Start(ctx, "download-file-call")
	defer span.End()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(metadataKV...))
	req := &filev1.DownloadRequest{
		FileId: fileID,
	}

	stream, err := client.Download(ctx, req)
	if err != nil {
		// Error is logged by the interceptor.
		return nil, err
	}

	var buf bytes.Buffer

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.Errorf("failed to receive file chunk: %s", err)
			return nil, err
		}

		buf.Write(resp.GetFileChunk().GetData())
	}

	return buf.Bytes(), nil
}

// uploadFile sends a file as a stream of fixed-size chunks via a client-streaming RPC.
func uploadFile(ctx context.Context, client filev1.FileServiceClient, data []byte) (fileID string, checksum string, err error) {
	probe := telemetry.GetProbe()
	logger := probe.Logger()
	tracer := probe.Tracer()

	ctx, span := tracer.Start(ctx, "upload-file-call")
	defer span.End()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(metadataKV...))

	stream, err := client.Upload(ctx)
	if err != nil {
		// Error is logged by the interceptor.
		return "", "", err
	}

	for offset := 0; offset < len(data); offset += uploadChunkSize {
		end := min(offset+uploadChunkSize, len(data))
		req := &filev1.UploadRequest{
			FileChunk: &filev1.FileChunk{
				Data:   data[offset:end],
				Offset: int64(offset),
			},
			UploadedAt: time.Now().UnixMilli(),
		}

		if err := stream.Send(req); err != nil {
			logger.Errorf("failed to send file chunk: %s", err)
			return "", "", err
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return "", "", err
	}

	return resp.GetFileId(), resp.GetChecksum(), nil
}

// syncEvents exchanges file events with the server via a bidirectional-streaming RPC.
func syncEvents(ctx context.Context, client filev1.FileServiceClient, events []*filev1.FileEvent) error {
	probe := telemetry.GetProbe()
	logger := probe.Logger()
	tracer := probe.Tracer()

	ctx, span := tracer.Start(ctx, "sync-events-call")
	defer span.End()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(metadataKV...))

	stream, err := client.Sync(ctx)
	if err != nil {
		// Error is logged by the interceptor.
		return err
	}

	sendErrCh := make(chan error, 1)

	go func() {
		defer close(sendErrCh)

		for _, event := range events {
			req := &filev1.SyncRequest{
				Event: event,
			}

			if err := stream.Send(req); err != nil {
				logger.Errorf("failed to send file event: %s", err)
				sendErrCh <- err
				return
			}
		}

		sendErrCh <- stream.CloseSend()
	}()

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.Errorf("failed to receive file event: %s", err)
			return err
		}

		event := resp.GetEvent()
		logger.Debug("file event acknowledged.",
			"type", event.GetType(),
			"file_id", event.GetFileId(),
			"name", event.GetName(),
		)
	}

	return <-sendErrCh
}
