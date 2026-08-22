package telemetry

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"

	"github.com/neatplatform/mint/internal/forward"
	"github.com/neatplatform/mint/internal/loki"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name          string
		s             string
		expectedLevel Level
	}{
		{
			name:          "Debug",
			s:             "debug",
			expectedLevel: LevelDebug,
		},
		{
			name:          "Info",
			s:             "info",
			expectedLevel: LevelInfo,
		},
		{
			name:          "Warn",
			s:             "warn",
			expectedLevel: LevelWarn,
		},
		{
			name:          "Error",
			s:             "error",
			expectedLevel: LevelError,
		},
		{
			name:          "None",
			s:             "none",
			expectedLevel: LevelNone,
		},
		{
			name:          "Unknown",
			s:             "unknown",
			expectedLevel: Level(99),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedLevel, parseLevel(tc.s))
		})
	}
}

func TestLevelString(t *testing.T) {
	tests := []struct {
		name           string
		l              Level
		expectedString string
	}{
		{
			name:           "Debug",
			l:              LevelDebug,
			expectedString: "debug",
		},
		{
			name:           "Info",
			l:              LevelInfo,
			expectedString: "info",
		},
		{
			name:           "Warn",
			l:              LevelWarn,
			expectedString: "warn",
		},
		{
			name:           "Error",
			l:              LevelError,
			expectedString: "error",
		},
		{
			name:           "None",
			l:              LevelNone,
			expectedString: "none",
		},
		{
			name:           "Unknown",
			l:              Level(99),
			expectedString: "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedString, levelString(tc.l))
		})
	}
}

// -------------------------------------------------- Reusable Functions --------------------------------------------------

func TestHandleError(t *testing.T) {
	tests := []struct {
		name           string
		format         string
		args           []any
		expectedOutput string
	}{
		{
			name:           "OK",
			format:         "error on logging: %s",
			args:           []any{"logger closed"},
			expectedOutput: "error on logging: logger closed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStderr(t, func() {
				handleError(tc.format, tc.args...)
			})

			assert.Contains(t, out, tc.expectedOutput)
		})
	}
}

func TestFormatMessage(t *testing.T) {
	tests := []struct {
		name           string
		format         string
		args           []any
		expectedString string
	}{
		{
			name:           "NoArgs",
			format:         "Request succeeded",
			args:           []any{},
			expectedString: "Request succeeded",
		},
		{
			name:           "EmptyFormat",
			format:         "",
			args:           []any{"GET", "/user", 200},
			expectedString: "GET /user 200",
		},
		{
			name:           "FormatWithArgs",
			format:         "Request succeeded: %s %s %d",
			args:           []any{"GET", "/user", 200},
			expectedString: "Request succeeded: GET /user 200",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedString, formatMessage(tc.format, tc.args))
		})
	}
}

// -------------------------------------------------- Noop Logger --------------------------------------------------

func TestNoopLogger(t *testing.T) {
	l := newNoopLogger()
	assert.NotNil(t, l)

	assert.Equal(t, LevelNone, l.Level())

	for _, level := range []string{"debug", "info", "warn", "error", "none", "invalid"} {
		l.SetLevel(level)
		assert.Equal(t, LevelNone, l.Level())
	}

	child := l.With("component", "test")
	assert.Same(t, l, child)

	child.Debug("debug message", "method", "GET", "statusCode", 101)
	child.Debugf("debug %s %d", "GET", 101)
	child.Info("info message", "method", "GET", "statusCode", 200)
	child.Infof("info %s %d", "GET", 200)
	child.Warn("warn message", "method", "GET", "statusCode", 400)
	child.Warnf("warn %s %d", "GET", 400)
	child.Error("error message", "method", "GET", "statusCode", 500)
	child.Errorf("error %s %d", "GET", 500)

	assert.NoError(t, child.Close())
}

// -------------------------------------------------- Async Logger --------------------------------------------------

func TestTask(t *testing.T) {
	tests := []struct {
		name string
		t    task
	}{
		{
			name: "OK",
			t: func() {
				fmt.Println("Hello, World!")
			},
		},
		{
			name: "Panic",
			t: func() {
				panic("test panic")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.t.SafeRun()
		})
	}
}

func TestNewAsyncLogger(t *testing.T) {
	tests := []struct {
		name   string
		logger Logger
	}{
		{
			name:   "OK",
			logger: new(noopLogger),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := newAsyncLogger(tc.logger).(*asyncLogger)

			assert.NotNil(t, l)
			assert.NotNil(t, l.queue)
			assert.NotNil(t, l.done)
			assert.Equal(t, tc.logger, l.logger)

			assert.NoError(t, l.Close())
			assert.NoError(t, l.Close())
		})
	}
}

func TestAsyncLogger(t *testing.T) {
	tests := []struct {
		name  string
		level string
		l     *asyncLogger
	}{
		{
			name:  "OK",
			level: "none",
			l: &asyncLogger{
				logger: newNoopLogger(),
				queue:  make(chan task, 10),
				done:   make(chan struct{}),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.l.SetLevel(tc.level)
			assert.Equal(t, parseLevel(tc.level), tc.l.Level())

			child := tc.l.With("component", "test")
			assert.NotNil(t, child)

			_, ok := child.(*asyncLogger)
			assert.True(t, ok)

			child.Debug("debug message", "method", "GET", "statusCode", 101)
			child.Debugf("debug %s %d", "GET", 101)
			child.Info("info message", "method", "GET", "statusCode", 200)
			child.Infof("info %s %d", "GET", 200)
			child.Warn("warn message", "method", "GET", "statusCode", 400)
			child.Warnf("warn %s %d", "GET", 400)
			child.Error("error message", "method", "GET", "statusCode", 500)
			child.Errorf("error %s %d", "GET", 500)

			assert.NoError(t, child.Close())
			assert.NoError(t, child.Close())
		})
	}
}

// -------------------------------------------------- Multi Logger --------------------------------------------------

func TestNewMultiLogger(t *testing.T) {
	tests := []struct {
		name    string
		loggers []Logger
	}{
		{
			name:    "NoLoggers",
			loggers: []Logger{},
		},
		{
			name: "MultipleLoggers",
			loggers: []Logger{
				newNoopLogger(),
				newNoopLogger(),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := newMultiLogger(tc.loggers...).(*multiLogger)

			assert.NotNil(t, l)
			assert.Equal(t, len(tc.loggers), len(l.loggers))

			for i, ll := range l.loggers {
				assert.NotNil(t, ll)
				assert.Same(t, tc.loggers[i], ll.logger)
			}

			assert.NoError(t, l.Close())
			assert.NoError(t, l.Close())
		})
	}
}

func TestMultiLogger(t *testing.T) {
	tests := []struct {
		name  string
		level string
		l     *multiLogger
	}{
		{
			name:  "NoLoggers",
			level: "none",
			l: &multiLogger{
				loggers: []*asyncLogger{},
			},
		},
		{
			name:  "MultipleLoggers",
			level: "none",
			l: &multiLogger{
				loggers: []*asyncLogger{
					newAsyncLogger(newNoopLogger()).(*asyncLogger),
					newAsyncLogger(newNoopLogger()).(*asyncLogger),
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.l.SetLevel(tc.level)
			assert.Equal(t, parseLevel(tc.level), tc.l.Level())

			child := tc.l.With("component", "test")
			assert.NotNil(t, child)

			_, ok := child.(*multiLogger)
			assert.True(t, ok)

			child.Debug("debug message", "method", "GET", "statusCode", 101)
			child.Debugf("debug %s %d", "GET", 101)
			child.Info("info message", "method", "GET", "statusCode", 200)
			child.Infof("info %s %d", "GET", 200)
			child.Warn("warn message", "method", "GET", "statusCode", 400)
			child.Warnf("warn %s %d", "GET", 400)
			child.Error("error message", "method", "GET", "statusCode", 500)
			child.Errorf("error %s %d", "GET", 500)

			assert.NoError(t, child.Close())
			assert.NoError(t, child.Close())
		})
	}
}

// -------------------------------------------------- Stdout Logger --------------------------------------------------

func TestNewStdoutLogger(t *testing.T) {
	tests := []struct {
		name             string
		o                stdoutLoggerOpts
		m                metadataOpts
		expectedLevel    Level
		expectedBaseJSON []byte
	}{
		{
			name:             "WithDefaults",
			o:                stdoutLoggerOpts{},
			m:                metadataOpts{},
			expectedLevel:    LevelInfo,
			expectedBaseJSON: []byte(nil),
		},
		{
			name: "WithOptions",
			o: stdoutLoggerOpts{
				Level: "debug",
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
			expectedLevel:    LevelDebug,
			expectedBaseJSON: []byte(`,"name":"echo-service","version":"v0.1.0","environment":"test","region":"local"`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := newStdoutLogger(tc.o, tc.m).(*fileLogger)

			assert.NotNil(t, l)
			assert.NotNil(t, l.pool)
			assert.Same(t, os.Stdout, l.w)
			assert.Equal(t, tc.expectedLevel, l.Level())
			assert.ElementsMatch(t, tc.expectedBaseJSON, l.baseJSON)

			// The pool should produce reusable byte-slice buffers.
			b, ok := l.pool.Get().(*[]byte)
			assert.True(t, ok)
			assert.NotNil(t, b)
			assert.Empty(t, *b)

			assert.NoError(t, l.Close())
			assert.NoError(t, l.Close())
		})
	}
}

func TestStdoutLogger(t *testing.T) {
	tests := []struct {
		name           string
		level          string
		expectedLevels []string
	}{
		{
			name:           "Debug",
			level:          "debug",
			expectedLevels: []string{"debug", "info", "warn", "error"},
		},
		{
			name:           "Info",
			level:          "info",
			expectedLevels: []string{"info", "warn", "error"},
		},
		{
			name:           "Warn",
			level:          "warn",
			expectedLevels: []string{"warn", "error"},
		},
		{
			name:           "Error",
			level:          "error",
			expectedLevels: []string{"error"},
		},
		{
			name:           "None",
			level:          "none",
			expectedLevels: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The logger must be created and closed inside captureStdout:
			// It binds to the current *os.File value of os.Stdout,
			// so it is built after os.Stdout is swapped for the pipe,
			// and closed before os.Stdout is restored the pipe itself is closed.
			out := captureStdout(t, func() {
				l := newStdoutLogger(
					stdoutLoggerOpts{
						Level: tc.level,
					},
					metadataOpts{
						Name:    "echo-service",
						Version: "v0.1.0",
						Attributes: map[string]any{
							"environment": "test",
							"region":      "local",
						},
					},
				)

				child := l.With("component", "test")
				assert.NotNil(t, child)

				child.Debug("debug message", "method", "GET", "statusCode", 101)
				child.Info("info message", "method", "GET", "statusCode", 200)
				child.Warn("warn message", "method", "GET", "statusCode", 400)
				child.Error("error message", "method", "GET", "statusCode", 500)

				assert.NoError(t, child.Close())
				assert.NoError(t, child.Close())
			})

			lines := strings.Split(strings.TrimSpace(out), "\n")
			if len(tc.expectedLevels) == 0 {
				assert.Equal(t, []string{""}, lines)
				return
			}

			assert.Len(t, lines, len(tc.expectedLevels))

			for i, line := range lines {
				var rec map[string]any
				assert.NoError(t, json.Unmarshal([]byte(line), &rec))

				assert.Equal(t, tc.expectedLevels[i], rec["level"])
				assert.NotEmpty(t, rec["timestamp"])
				assert.Contains(t, rec["message"], "")
				assert.Equal(t, "echo-service", rec["name"])
				assert.Equal(t, "v0.1.0", rec["version"])
				assert.Equal(t, "test", rec["environment"])
				assert.Equal(t, "local", rec["region"])
				assert.Equal(t, "test", rec["component"])
				assert.Equal(t, "GET", rec["method"])
				assert.NotEmpty(t, rec["statusCode"])
			}
		})
	}
}

// -------------------------------------------------- Stdout/File Logger --------------------------------------------------

func TestNewFileLogger(t *testing.T) {
	// Default log file
	defer func() {
		_ = os.Remove("app.log")
	}()

	// Temporary log file
	f, err := os.CreateTemp("", "echo-service-*.log")
	assert.NoError(t, err)

	defer func() {
		_ = os.Remove(f.Name())
	}()

	tests := []struct {
		name             string
		o                fileLoggerOpts
		m                metadataOpts
		expectedLevel    Level
		expectedBaseJSON []byte
	}{
		{
			name:             "WithDefaults",
			o:                fileLoggerOpts{},
			m:                metadataOpts{},
			expectedLevel:    LevelInfo,
			expectedBaseJSON: []byte(nil),
		},
		{
			name: "WithOptions",
			o: fileLoggerOpts{
				Level:      "debug",
				Filepath:   f.Name(),
				MaxSize:    200,
				MaxBackups: 10,
				MaxAge:     14,
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
			expectedLevel:    LevelDebug,
			expectedBaseJSON: []byte(`,"name":"echo-service","version":"v0.1.0","environment":"test","region":"local"`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, err := newFileLogger(tc.o, tc.m)
			assert.NoError(t, err)

			ll := l.(*fileLogger)

			assert.NotNil(t, ll)
			assert.NotNil(t, ll.pool)
			assert.NotNil(t, ll.w)
			assert.Equal(t, tc.expectedLevel, ll.Level())
			assert.ElementsMatch(t, tc.expectedBaseJSON, ll.baseJSON)

			// The pool should produce reusable byte-slice buffers.
			b, ok := ll.pool.Get().(*[]byte)
			assert.True(t, ok)
			assert.NotNil(t, b)
			assert.Empty(t, *b)

			assert.NoError(t, ll.Close())
			assert.NoError(t, ll.Close())
		})
	}
}

func TestFileLogger(t *testing.T) {
	newPool := func() *sync.Pool {
		return &sync.Pool{
			New: func() any {
				b := make([]byte, 0, 32)
				return &b
			},
		}
	}

	tests := []struct {
		name  string
		level string
		l     *fileLogger
	}{
		{
			name:  "Debug",
			level: "debug",
			l: &fileLogger{
				pool: newPool(),
				w:    openDevNull(),
			},
		},
		{
			name:  "Info",
			level: "info",
			l: &fileLogger{
				pool: newPool(),
				w:    openDevNull(),
			},
		},
		{
			name:  "Warn",
			level: "warn",
			l: &fileLogger{
				pool: newPool(),
				w:    openDevNull(),
			},
		},
		{
			name:  "Error",
			level: "error",
			l: &fileLogger{
				pool: newPool(),
				w:    openDevNull(),
			},
		},
		{
			name:  "None",
			level: "none",
			l: &fileLogger{
				pool: newPool(),
				w:    openDevNull(),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.l.SetLevel(tc.level)
			assert.Equal(t, parseLevel(tc.level), tc.l.Level())

			child := tc.l.With("component", "test")
			assert.NotNil(t, child)

			child.Debug("debug message", "method", "GET", "statusCode", 101)
			child.Debugf("debug %s %d", "GET", 101)
			child.Info("info message", "method", "GET", "statusCode", 200)
			child.Infof("info %s %d", "GET", 200)
			child.Warn("warn message", "method", "GET", "statusCode", 400)
			child.Warnf("warn %s %d", "GET", 400)
			child.Error("error message", "method", "GET", "statusCode", 500)
			child.Errorf("error %s %d", "GET", 500)

			assert.NoError(t, child.Close())
		})
	}
}

// -------------------------------------------------- Loki Logger --------------------------------------------------

func TestNewLokiLogger(t *testing.T) {
	tests := []struct {
		name                 string
		o                    lokiLoggerOpts
		m                    metadataOpts
		expectedLabels       map[string]struct{}
		expectedLevel        Level
		expectedBaseLabels   []loki.Pair
		expectedBaseMetadata []loki.Pair
	}{
		{
			name: "WithDefaults",
			o:    lokiLoggerOpts{},
			m:    metadataOpts{},
			expectedLabels: map[string]struct{}{
				"level": {},
				"name":  {},
			},
			expectedLevel:        LevelInfo,
			expectedBaseLabels:   nil,
			expectedBaseMetadata: nil,
		},
		{
			name: "WithOptions",
			o: lokiLoggerOpts{
				Tenant:   "internal",
				Level:    "debug",
				Labels:   []string{"environment", "region"},
				Endpoint: "http://alloy:3100/loki/api/v1/push",
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
			expectedLabels: map[string]struct{}{
				"level":       {},
				"name":        {},
				"environment": {},
				"region":      {},
			},
			expectedLevel: LevelDebug,
			// Attribute iteration order is not guaranteed, so this is checked with ElementsMatch below.
			expectedBaseLabels: []loki.Pair{
				{Name: "name", Value: "echo-service"},
				{Name: "environment", Value: "test"},
				{Name: "region", Value: "local"},
			},
			// name and version are appended in a fixed order, unlike attributes, so order is deterministic here.
			expectedBaseMetadata: []loki.Pair{
				{Name: "version", Value: "v0.1.0"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := newLokiLogger(tc.o, tc.m).(*lokiLogger)

			assert.NotNil(t, l)
			assert.NotNil(t, l.client)
			assert.NotNil(t, l.labels)
			assert.NotNil(t, l.pool)

			assert.Equal(t, tc.expectedLabels, l.labels)
			assert.Equal(t, tc.expectedLevel, l.Level())
			assert.ElementsMatch(t, tc.expectedBaseLabels, l.baseLabels)
			assert.ElementsMatch(t, tc.expectedBaseMetadata, l.baseMetadata)

			// The pool should produce reusable byte-slice buffers.
			b, ok := l.pool.Get().(*lokiPairs)
			assert.True(t, ok)
			assert.NotNil(t, b)
			assert.Empty(t, b.labels)
			assert.Empty(t, b.metadata)

			assert.NoError(t, l.Close())
			assert.NoError(t, l.Close())
		})
	}
}

func TestLokiLogger(t *testing.T) {
	newPool := func() *sync.Pool {
		return &sync.Pool{
			New: func() any {
				return &lokiPairs{
					labels:   make([]loki.Pair, 0, 4),
					metadata: make([]loki.Pair, 0, 4),
				}
			},
		}
	}

	tests := []struct {
		name  string
		level string
		l     *lokiLogger
	}{
		{
			name:  "Debug",
			level: "debug",
			l: &lokiLogger{
				client: &MockLokiClient{
					CloseMocks:  make([]MockLokiClient_CloseMock, 2),
					SendAtMocks: make([]MockLokiClient_SendAtMock, 8),
				},
				labels: map[string]struct{}{},
				pool:   newPool(),
			},
		},
		{
			name:  "Info",
			level: "info",
			l: &lokiLogger{
				client: &MockLokiClient{
					CloseMocks:  make([]MockLokiClient_CloseMock, 2),
					SendAtMocks: make([]MockLokiClient_SendAtMock, 8),
				},
				labels: map[string]struct{}{},
				pool:   newPool(),
			},
		},
		{
			name:  "Warn",
			level: "warn",
			l: &lokiLogger{
				client: &MockLokiClient{
					CloseMocks:  make([]MockLokiClient_CloseMock, 2),
					SendAtMocks: make([]MockLokiClient_SendAtMock, 8),
				},
				labels: map[string]struct{}{},
				pool:   newPool(),
			},
		},
		{
			name:  "Error",
			level: "error",
			l: &lokiLogger{
				client: &MockLokiClient{
					CloseMocks:  make([]MockLokiClient_CloseMock, 2),
					SendAtMocks: make([]MockLokiClient_SendAtMock, 8),
				},
				labels: map[string]struct{}{},
				pool:   newPool(),
			},
		},
		{
			name:  "None",
			level: "none",
			l: &lokiLogger{
				client: &MockLokiClient{
					CloseMocks:  make([]MockLokiClient_CloseMock, 2),
					SendAtMocks: make([]MockLokiClient_SendAtMock, 8),
				},
				labels: map[string]struct{}{},
				pool:   newPool(),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.l.SetLevel(tc.level)
			assert.Equal(t, parseLevel(tc.level), tc.l.Level())

			child := tc.l.With("component", "test")
			assert.NotNil(t, child)

			child.Debug("debug message", "method", "GET", "statusCode", 101)
			child.Debugf("debug %s %d", "GET", 101)
			child.Info("info message", "method", "GET", "statusCode", 200)
			child.Infof("info %s %d", "GET", 200)
			child.Warn("warn message", "method", "GET", "statusCode", 400)
			child.Warnf("warn %s %d", "GET", 400)
			child.Error("error message", "method", "GET", "statusCode", 500)
			child.Errorf("error %s %d", "GET", 500)

			assert.NoError(t, child.Close())
			assert.NoError(t, child.Close())
		})
	}
}

// -------------------------------------------------- Forward Logger --------------------------------------------------

func TestNewForwardLogger(t *testing.T) {
	srv, err := newTestTCPServer()
	assert.NoError(t, err)

	defer func() {
		_ = srv.Close()
	}()

	tlsSrv, err := newTestTLSTCPServer()
	assert.NoError(t, err)

	defer func() {
		_ = tlsSrv.Close()
	}()

	tests := []struct {
		name           string
		o              forwardLoggerOpts
		m              metadataOpts
		expectedLevel  Level
		expectedBaseKV []any
	}{
		{
			name: "WithDefaults",
			o: forwardLoggerOpts{
				Endpoint: srv.Addr,
			},
			m:              metadataOpts{},
			expectedLevel:  LevelInfo,
			expectedBaseKV: nil,
		},
		{
			name: "WithOptions",
			o: forwardLoggerOpts{
				Level:    "debug",
				Tag:      "echo-service",
				Endpoint: srv.Addr,
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
			expectedLevel: LevelDebug,
			expectedBaseKV: []any{
				nameKey, "echo-service",
				versionKey, "v0.1.0",
				"environment", "test",
				"region", "local",
			},
		},
		{
			name: "WithOptions_TLS",
			o: forwardLoggerOpts{
				Tag:      "echo-service",
				Endpoint: tlsSrv.Addr,
				TLS: tlsOpts{
					Enabled: true,
					Config: &tls.Config{
						InsecureSkipVerify: true,
					},
				},
			},
			m:              metadataOpts{},
			expectedLevel:  LevelInfo,
			expectedBaseKV: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, err := newForwardLogger(tc.o, tc.m)
			assert.NoError(t, err)

			ll := l.(*forwardLogger)

			assert.NotNil(t, ll)
			assert.NotNil(t, ll.client)
			assert.Equal(t, tc.expectedLevel, ll.Level())
			assert.ElementsMatch(t, tc.expectedBaseKV, ll.baseKV)

			assert.NoError(t, ll.Close())
			assert.NoError(t, ll.Close())
		})
	}
}

func TestForwardLogger(t *testing.T) {
	tests := []struct {
		name  string
		level string
		l     *forwardLogger
	}{
		{
			name:  "Debug",
			level: "debug",
			l: &forwardLogger{
				client: &MockForwardClient{
					CloseMocks:  make([]MockForwardClient_CloseMock, 2),
					SendAtMocks: make([]MockForwardClient_SendAtMock, 8),
				},
			},
		},
		{
			name:  "Info",
			level: "info",
			l: &forwardLogger{
				client: &MockForwardClient{
					CloseMocks:  make([]MockForwardClient_CloseMock, 2),
					SendAtMocks: make([]MockForwardClient_SendAtMock, 8),
				},
			},
		},
		{
			name:  "Warn",
			level: "warn",
			l: &forwardLogger{
				client: &MockForwardClient{
					CloseMocks:  make([]MockForwardClient_CloseMock, 2),
					SendAtMocks: make([]MockForwardClient_SendAtMock, 8),
				},
			},
		},
		{
			name:  "Error",
			level: "error",
			l: &forwardLogger{
				client: &MockForwardClient{
					CloseMocks:  make([]MockForwardClient_CloseMock, 2),
					SendAtMocks: make([]MockForwardClient_SendAtMock, 8),
				},
			},
		},
		{
			name:  "None",
			level: "none",
			l: &forwardLogger{
				client: &MockForwardClient{
					CloseMocks:  make([]MockForwardClient_CloseMock, 2),
					SendAtMocks: make([]MockForwardClient_SendAtMock, 8),
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.l.SetLevel(tc.level)
			assert.Equal(t, parseLevel(tc.level), tc.l.Level())

			child := tc.l.With("component", "test")
			assert.NotNil(t, child)

			child.Debug("debug message", "method", "GET", "statusCode", 101)
			child.Debugf("debug %s %d", "GET", 101)
			child.Info("info message", "method", "GET", "statusCode", 200)
			child.Infof("info %s %d", "GET", 200)
			child.Warn("warn message", "method", "GET", "statusCode", 400)
			child.Warnf("warn %s %d", "GET", 400)
			child.Error("error message", "method", "GET", "statusCode", 500)
			child.Errorf("error %s %d", "GET", 500)

			assert.NoError(t, child.Close())
			assert.NoError(t, child.Close())
		})
	}
}

func TestAppendNormalizedKV(t *testing.T) {
	tests := []struct {
		name       string
		base       []any
		kv         []any
		expectedKV []any
	}{
		{
			name:       "NoBase",
			base:       nil,
			kv:         []any{"environment", "test"},
			expectedKV: []any{"environment", "test"},
		},
		{
			name:       "StringKey",
			base:       []any{"name", "echo-service"},
			kv:         []any{"environment", "test"},
			expectedKV: []any{"name", "echo-service", "environment", "test"},
		},
		{
			name:       "BytesKey",
			base:       []any{"name", "echo-service"},
			kv:         []any{[]byte("environment"), "test"},
			expectedKV: []any{"name", "echo-service", "environment", "test"},
		},
		{
			name:       "NonStandardKey",
			base:       []any{"name", "echo-service"},
			kv:         []any{true, "yes"},
			expectedKV: []any{"name", "echo-service", "true", "yes"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := appendNormalizedKV(tc.base, tc.kv)

			assert.Equal(t, tc.expectedKV, base)
		})
	}
}

func TestSetForwardKV(t *testing.T) {
	tests := []struct {
		name           string
		r              forward.Record
		kv             []any
		expectedRecord forward.Record
	}{
		{
			name:           "StringKey",
			r:              forward.Record{},
			kv:             []any{"environment", "test"},
			expectedRecord: forward.Record{"environment": "test"},
		},
		{
			name:           "BytesKey",
			r:              forward.Record{},
			kv:             []any{[]byte("environment"), "test"},
			expectedRecord: forward.Record{"environment": "test"},
		},
		{
			name:           "NonStandardKey",
			r:              forward.Record{},
			kv:             []any{true, "yes"},
			expectedRecord: forward.Record{"true": "yes"},
		},
		{
			name:           "OverwriteExistingKey",
			r:              forward.Record{"environment": "old"},
			kv:             []any{"environment", "new"},
			expectedRecord: forward.Record{"environment": "new"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setForwardKV(tc.r, tc.kv)

			assert.Equal(t, tc.expectedRecord, tc.r)
		})
	}
}

// -------------------------------------------------- OpenTelemetry Logger --------------------------------------------------

func TestNewOpenTelemetryLogger(t *testing.T) {
	tests := []struct {
		name           string
		o              opentelemetryLoggerOpts
		m              metadataOpts
		expectedLevel  Level
		expectedBaseKV []attribute.KeyValue
	}{
		{
			name:           "WithDefaults",
			o:              opentelemetryLoggerOpts{},
			m:              metadataOpts{},
			expectedLevel:  LevelInfo,
			expectedBaseKV: nil,
		},
		{
			name: "WithHTTPDefaults",
			o: opentelemetryLoggerOpts{
				OTLPHTTP: &otlpHTTPOpts{},
			},
			m:              metadataOpts{},
			expectedLevel:  LevelInfo,
			expectedBaseKV: nil,
		},
		{
			name: "WithGRPCDefaults",
			o: opentelemetryLoggerOpts{
				OTLPGRPC: &otlpGRPCOpts{},
			},
			m:              metadataOpts{},
			expectedLevel:  LevelInfo,
			expectedBaseKV: nil,
		},
		{
			name: "WithHTTP",
			o: opentelemetryLoggerOpts{
				Level: "debug",
				OTLPHTTP: &otlpHTTPOpts{
					Endpoint: "collector:4318",
				},
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
			expectedLevel:  LevelDebug,
			expectedBaseKV: nil,
		},
		{
			name: "WithGRPC",
			o: opentelemetryLoggerOpts{
				Level: "debug",
				OTLPGRPC: &otlpGRPCOpts{
					Endpoint: "collector:4317",
				},
			},
			m: metadataOpts{
				Name:    "echo-service",
				Version: "v0.1.0",
				Attributes: map[string]any{
					"environment": "test",
					"region":      "local",
				},
			},
			expectedLevel:  LevelDebug,
			expectedBaseKV: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := newOpenTelemetryLogger(tc.o, tc.m).(*opentelemetryLogger)

			assert.NotNil(t, l)
			assert.NotNil(t, l.otel)
			assert.NotNil(t, l.close)
			assert.NotNil(t, l.pool)

			assert.Equal(t, tc.expectedLevel, l.Level())
			assert.ElementsMatch(t, tc.expectedBaseKV, l.baseKV)

			b, ok := l.pool.Get().(*[]attribute.KeyValue)
			assert.True(t, ok)
			assert.NotNil(t, b)
			assert.Empty(t, *b)

			assert.NoError(t, l.Close())
			assert.NoError(t, l.Close())
		})
	}
}

func TestOpenTelemetryLogger(t *testing.T) {
	newPool := func() *sync.Pool {
		return &sync.Pool{
			New: func() any {
				b := make([]attribute.KeyValue, 0, 4)
				return &b
			},
		}
	}

	tests := []struct {
		name  string
		level string
		l     *opentelemetryLogger
	}{
		{
			name:  "Debug",
			level: "debug",
			l: &opentelemetryLogger{
				otel: &MockOTelLogger{
					EmitMocks: make([]MockOTelLogger_EmitMock, 8),
				},
				close: noopCloseFunc,
				pool:  newPool(),
			},
		},
		{
			name:  "Info",
			level: "info",
			l: &opentelemetryLogger{
				otel: &MockOTelLogger{
					EmitMocks: make([]MockOTelLogger_EmitMock, 8),
				},
				close: noopCloseFunc,
				pool:  newPool(),
			},
		},
		{
			name:  "Warn",
			level: "warn",
			l: &opentelemetryLogger{
				otel: &MockOTelLogger{
					EmitMocks: make([]MockOTelLogger_EmitMock, 8),
				},
				close: noopCloseFunc,
				pool:  newPool(),
			},
		},
		{
			name:  "Error",
			level: "error",
			l: &opentelemetryLogger{
				otel: &MockOTelLogger{
					EmitMocks: make([]MockOTelLogger_EmitMock, 8),
				},
				close: noopCloseFunc,
				pool:  newPool(),
			},
		},
		{
			name:  "None",
			level: "none",
			l: &opentelemetryLogger{
				otel: &MockOTelLogger{
					EmitMocks: make([]MockOTelLogger_EmitMock, 8),
				},
				close: noopCloseFunc,
				pool:  newPool(),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.l.SetLevel(tc.level)
			assert.Equal(t, parseLevel(tc.level), tc.l.Level())

			child := tc.l.With("environment", "test")
			assert.NotNil(t, child)

			child.Debug("debug message", "method", "GET", "statusCode", 101)
			child.Debugf("debug %s %d", "GET", 101)
			child.Info("info message", "method", "GET", "statusCode", 200)
			child.Infof("info %s %d", "GET", 200)
			child.Warn("warn message", "method", "GET", "statusCode", 400)
			child.Warnf("warn %s %d", "GET", 400)
			child.Error("error message", "method", "GET", "statusCode", 500)
			child.Errorf("error %s %d", "GET", 500)

			assert.NoError(t, child.Close())
			assert.NoError(t, child.Close())
		})
	}
}

// -------------------------------------------------- Test Helpers --------------------------------------------------

var noopCloseFunc = func(context.Context) error {
	return nil
}

func openDevNull() *os.File {
	f, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	return f
}

// captureStdout redirects os.Stdout for the duration of fn and returns everything written to it.
// fn must not spawn goroutines that write to stdout after it returns.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	assert.NoError(t, err)

	orig := os.Stdout
	os.Stdout = w

	fn()

	assert.NoError(t, w.Close())
	os.Stdout = orig

	out, err := io.ReadAll(r)
	assert.NoError(t, err)

	return string(out)
}

// captureStderr redirects os.Stderr for the duration of fn and returns everything written to it.
// fn must not spawn goroutines that write to stderr after it returns.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	assert.NoError(t, err)

	orig := os.Stderr
	os.Stderr = w

	fn()

	assert.NoError(t, w.Close())
	os.Stderr = orig

	out, err := io.ReadAll(r)
	assert.NoError(t, err)

	return string(out)
}

type (
	MockLokiClient struct {
		CloseIndex int
		CloseMocks []MockLokiClient_CloseMock

		SendAtIndex int
		SendAtMocks []MockLokiClient_SendAtMock
	}

	MockLokiClient_CloseMock struct{}

	MockLokiClient_SendAtMock struct {
		InTime     time.Time
		InLine     string
		InLabels   []loki.Pair
		InMetadata []loki.Pair
	}
)

func (m *MockLokiClient) Close() {
	if m.CloseIndex >= len(m.CloseMocks) {
		panic("Close called more times than expected")
	}

	m.CloseIndex++
}

func (m *MockLokiClient) SendAt(t time.Time, line string, labels, metadata []loki.Pair) {
	if m.SendAtIndex >= len(m.SendAtMocks) {
		panic("SendAt called more times than expected")
	}

	i := m.SendAtIndex
	m.SendAtIndex++

	m.SendAtMocks[i].InTime = t
	m.SendAtMocks[i].InLine = line
	m.SendAtMocks[i].InLabels = labels
	m.SendAtMocks[i].InMetadata = metadata
}

type (
	MockForwardClient struct {
		CloseIndex int
		CloseMocks []MockForwardClient_CloseMock

		SendAtIndex int
		SendAtMocks []MockForwardClient_SendAtMock
	}

	MockForwardClient_CloseMock struct {
		OutError error
	}

	MockForwardClient_SendAtMock struct {
		InTime   time.Time
		InRecord forward.Record
	}
)

func (m *MockForwardClient) Close() error {
	if m.CloseIndex >= len(m.CloseMocks) {
		panic("Close called more times than expected")
	}

	i := m.CloseIndex
	m.CloseIndex++

	return m.CloseMocks[i].OutError
}

func (m *MockForwardClient) SendAt(t time.Time, r forward.Record) {
	if m.SendAtIndex >= len(m.SendAtMocks) {
		panic("SendAt called more times than expected")
	}

	i := m.SendAtIndex
	m.SendAtIndex++

	m.SendAtMocks[i].InTime = t
	m.SendAtMocks[i].InRecord = r
}

type (
	MockOTelLogger struct {
		EmitIndex int
		EmitMocks []MockOTelLogger_EmitMock
	}

	MockOTelLogger_EmitMock struct {
		InContext context.Context
		InRecord  log.Record
	}
)

func (m *MockOTelLogger) Emit(ctx context.Context, r log.Record) {
	if m.EmitIndex >= len(m.EmitMocks) {
		panic("Emit called more times than expected")
	}

	i := m.EmitIndex
	m.EmitIndex++

	m.EmitMocks[i].InContext = ctx
	m.EmitMocks[i].InRecord = r
}
