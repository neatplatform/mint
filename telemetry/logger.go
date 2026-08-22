package telemetry

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"

	"github.com/neatplatform/mint/file"
	"github.com/neatplatform/mint/internal/forward"
	"github.com/neatplatform/mint/internal/loki"
)

const (
	timeFormat = time.RFC3339Nano
	timeKey    = "timestamp"
	levelKey   = "level"
	nameKey    = "name"
	versionKey = "version"
	messageKey = "message"
)

var (
	timeKeyBytes    = []byte(`{"` + timeKey + `":"`)
	levelKeyBytes   = []byte(`","` + levelKey + `":"`)
	messageKeyBytes = []byte(`","` + messageKey + `":`)
)

// Logger is a levelled, structured logger safe for concurrent use.
//
// It provides a unified interface for logging across underlying protocols and storage backends.
type Logger interface {
	// Level returns the current logging level.
	Level() Level

	// SetLevel sets the logging level.
	// Supported levels are "debug", "info", "warn", "error", and "none".
	SetLevel(level string)

	// With returns a new logger with the given key-value pairs on all logs.
	// The kv argument should contain an even number of elements, alternating between keys and values.
	With(kv ...any) Logger

	// Debug logs a message at the debug level with optional key-value pairs.
	// The kv argument should contain an even number of elements, alternating between keys and values.
	Debug(message string, kv ...any)

	// Debugf logs a message at the debug level.
	// If format contains no format verbs and args is empty, format is logged as-is.
	// If format is empty and args is not empty, args are formatted like [fmt.Sprint].
	// If format contains format verbs and args are provided, output is formatted like [fmt.Sprintf].
	Debugf(format string, args ...any)

	// Info logs a message at the info level with optional key-value pairs.
	// The kv argument should contain an even number of elements, alternating between keys and values.
	Info(message string, kv ...any)

	// Infof logs a message at the info level.
	// If format contains no format verbs and args is empty, format is logged as-is.
	// If format is empty and args is not empty, args are formatted like [fmt.Sprint].
	// If format contains format verbs and args are provided, output is formatted like [fmt.Sprintf].
	Infof(format string, args ...any)

	// Warn logs a message at the warn level with optional key-value pairs.
	// The kv argument should contain an even number of elements, alternating between keys and values.
	Warn(message string, kv ...any)

	// Warnf logs a message at the warn level.
	// If format contains no format verbs and args is empty, format is logged as-is.
	// If format is empty and args is not empty, args are formatted like [fmt.Sprint].
	// If format contains format verbs and args are provided, output is formatted like [fmt.Sprintf].
	Warnf(format string, args ...any)

	// Error logs a message at the error level with optional key-value pairs.
	// The kv argument should contain an even number of elements, alternating between keys and values.
	Error(message string, kv ...any)

	// Errorf logs a message at the error level.
	// If format contains no format verbs and args is empty, format is logged as-is.
	// If format is empty and args is not empty, args are formatted like [fmt.Sprint].
	// If format contains format verbs and args are provided, output is formatted like [fmt.Sprintf].
	Errorf(format string, args ...any)

	// Close flushes any buffered log entries and finalizes the logger.
	Close() error
}

// Level is the logging level.
type Level int

// Logging levels in ascending order of verbosity.
const (
	LevelNone Level = iota
	LevelError
	LevelWarn
	LevelInfo
	LevelDebug
)

// parseLevel converts a string representation of a logging level to the corresponding Level constant.
func parseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn":
		return LevelWarn
	case "error":
		return LevelError
	case "none":
		return LevelNone
	default:
		return Level(99)
	}
}

// levelString converts a Level constant to its string representation.
func levelString(l Level) string {
	if 0 <= l && int(l) < len(levelStrings) {
		return levelStrings[l]
	}

	return "unknown"
}

var levelStrings = [...]string{
	LevelNone:  "none",
	LevelError: "error",
	LevelWarn:  "warn",
	LevelInfo:  "info",
	LevelDebug: "debug",
}

// -------------------------------------------------- Reusable Functions --------------------------------------------------

// handleError handles an error that cannot be returned to a caller,
// such as one occurring on the hot path or inside a goroutine.
func handleError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// formatMessage formats a log message based on the provided format string and arguments.
func formatMessage(format string, args []any) string {
	if len(args) == 0 {
		return format
	}

	// len(args) > 0
	if format == "" {
		s := fmt.Sprint(args)
		return s[1 : len(s)-1] // remove the surrounding brackets
	}

	// format != "" && len(args) > 0
	return fmt.Sprintf(format, args...)
}

// -------------------------------------------------- Noop Logger --------------------------------------------------

// noopLogger is a no-op [Logger] that silently discards all log calls.
type noopLogger struct{}

// newNoopLogger creates a new no-op [Logger] that discards all logs, useful for testing.
func newNoopLogger() Logger {
	return &noopLogger{}
}

func (l *noopLogger) Level() Level                      { return LevelNone }
func (l *noopLogger) SetLevel(level string)             {}
func (l *noopLogger) With(kv ...any) Logger             { return l }
func (l *noopLogger) Debug(message string, kv ...any)   {}
func (l *noopLogger) Debugf(format string, args ...any) {}
func (l *noopLogger) Info(message string, kv ...any)    {}
func (l *noopLogger) Infof(format string, args ...any)  {}
func (l *noopLogger) Warn(message string, kv ...any)    {}
func (l *noopLogger) Warnf(format string, args ...any)  {}
func (l *noopLogger) Error(message string, kv ...any)   {}
func (l *noopLogger) Errorf(format string, args ...any) {}
func (l *noopLogger) Close() error                      { return nil }

// -------------------------------------------------- Async Logger --------------------------------------------------

const queueSize = 100

// task represents a single logging operation.
type task func()

// SafeRun executes the task and recovers from any panic.
func (t task) SafeRun() {
	defer func() {
		if r := recover(); r != nil {
			handleError("recovered from panic: %s", r)
		}
	}()

	t()
}

// asyncLogger is a thread-safe [Logger] implementation that logs asynchronously through an underlying logger.
//
// Logging tasks are queued and processed asynchronously using a bounded queue.
// When the queue is full, additional tasks are intentionally dropped to protect the hot path from blocking.
type asyncLogger struct {
	logger    Logger
	queue     chan task
	done      chan struct{}
	closed    atomic.Bool
	closeOnce sync.Once
}

// newAsyncLogger creates a new thread-safe [Logger] that logs asynchronously through an underlying logger.
func newAsyncLogger(logger Logger) Logger {
	l := &asyncLogger{
		logger: logger,
		queue:  make(chan task, queueSize),
		done:   make(chan struct{}),
	}

	go l.run()

	return l
}

// run executes in a separate goroutine, consuming logging tasks from the queue.
func (l *asyncLogger) run() {
	defer close(l.done)

	for t := range l.queue {
		t.SafeRun()
	}
}

// submit enqueues a logging task.
// It returns false when the queue is full and the task is dropped.
func (l *asyncLogger) submit(t task) bool {
	if l.closed.Load() {
		handleError("logger is closed")
		return false
	}

	select {
	case l.queue <- t:
		return true
	default:
		handleError("logger queue is full")
		return false
	}
}

func (l *asyncLogger) Level() Level {
	return l.logger.Level()
}

func (l *asyncLogger) SetLevel(level string) {
	l.logger.SetLevel(level)
}

func (l *asyncLogger) With(kv ...any) Logger {
	child := &asyncLogger{
		logger: l.logger.With(kv...),
		queue:  make(chan task, queueSize),
		done:   make(chan struct{}),
	}

	go child.run()

	return child
}

func (l *asyncLogger) Debug(message string, kv ...any) {
	l.submit(func() {
		l.logger.Debug(message, kv...)
	})
}

func (l *asyncLogger) Debugf(format string, args ...any) {
	l.submit(func() {
		l.logger.Debugf(format, args...)
	})
}

func (l *asyncLogger) Info(message string, kv ...any) {
	l.submit(func() {
		l.logger.Info(message, kv...)
	})
}

func (l *asyncLogger) Infof(format string, args ...any) {
	l.submit(func() {
		l.logger.Infof(format, args...)
	})
}

func (l *asyncLogger) Warn(message string, kv ...any) {
	l.submit(func() {
		l.logger.Warn(message, kv...)
	})
}

func (l *asyncLogger) Warnf(format string, args ...any) {
	l.submit(func() {
		l.logger.Warnf(format, args...)
	})
}

func (l *asyncLogger) Error(message string, kv ...any) {
	l.submit(func() {
		l.logger.Error(message, kv...)
	})
}

func (l *asyncLogger) Errorf(format string, args ...any) {
	l.submit(func() {
		l.logger.Errorf(format, args...)
	})
}

func (l *asyncLogger) Close() error {
	var err error

	// Ensure that Close is only executed once.
	l.closeOnce.Do(func() {
		l.closed.Store(true)   // Stop accepting new tasks.
		close(l.queue)         // Signal the runner to stop accepting new tasks.
		<-l.done               // Wait for the runner to finish processing all tasks.
		err = l.logger.Close() // Close the underlying logger.
	})

	return err
}

// -------------------------------------------------- Multi Logger --------------------------------------------------

// multiLogger is a thread-safe [Logger] implementation that logs through multiple loggers.
type multiLogger struct {
	loggers []*asyncLogger
}

// newMultiLogger creates a new thread-safe [Logger] that logs through multiple loggers.
func newMultiLogger(loggers ...Logger) Logger {
	l := &multiLogger{
		loggers: make([]*asyncLogger, len(loggers)),
	}

	for i, ll := range loggers {
		l.loggers[i] = newAsyncLogger(ll).(*asyncLogger)
	}

	return l
}

func (l *multiLogger) Level() Level {
	// Determine the most verbose log level among all loggers.
	maxLevel := LevelNone
	for _, ll := range l.loggers {
		if level := ll.Level(); level > maxLevel {
			maxLevel = level
		}
	}

	return maxLevel
}

func (l *multiLogger) SetLevel(level string) {
	// Set the level on all loggers.
	for _, ll := range l.loggers {
		ll.SetLevel(level)
	}
}

func (l *multiLogger) With(kv ...any) Logger {
	child := &multiLogger{
		loggers: make([]*asyncLogger, len(l.loggers)),
	}

	for i, ll := range l.loggers {
		child.loggers[i] = ll.With(kv...).(*asyncLogger)
	}

	return child
}

func (l *multiLogger) Debug(message string, kv ...any) {
	for _, ll := range l.loggers {
		ll.Debug(message, kv...)
	}
}

func (l *multiLogger) Debugf(format string, args ...any) {
	for _, ll := range l.loggers {
		ll.Debugf(format, args...)
	}
}

func (l *multiLogger) Info(message string, kv ...any) {
	for _, ll := range l.loggers {
		ll.Info(message, kv...)
	}
}

func (l *multiLogger) Infof(format string, args ...any) {
	for _, ll := range l.loggers {
		ll.Infof(format, args...)
	}
}

func (l *multiLogger) Warn(message string, kv ...any) {
	for _, ll := range l.loggers {
		ll.Warn(message, kv...)
	}
}

func (l *multiLogger) Warnf(format string, args ...any) {
	for _, ll := range l.loggers {
		ll.Warnf(format, args...)
	}
}

func (l *multiLogger) Error(message string, kv ...any) {
	for _, ll := range l.loggers {
		ll.Error(message, kv...)
	}
}

func (l *multiLogger) Errorf(format string, args ...any) {
	for _, ll := range l.loggers {
		ll.Errorf(format, args...)
	}
}

func (l *multiLogger) Close() error {
	g := new(errgroup.Group)

	for _, ll := range l.loggers {
		ll := ll // capture loop variable to prevent loop variable shadowing
		g.Go(ll.Close)
	}

	return g.Wait()
}

// -------------------------------------------------- Stdout/File Logger --------------------------------------------------

// fileLogger is a thread-safe [Logger] implementation that writes JSON logs to a file.
//
// Design priorities in order:
//
//  1. Zero reflection
//     Every primitive Go type is covered with a static type switch.
//     The compiler resolves each case at compile time with no reflect calls,
//     keeping the CPU branch predictor happy on typical log traffic that is dominated by strings and ints.
//
//  2. One allocation per logger lifetime, zero allocation per record in the steady state
//     Every log call borrows a buffer from the pool, appends into it, writes, and returns it.
//     If the buffer grew during a previous call its backing array stays in the pool, so subsequent records of similar size need no make at all.
//     Buffers are recycled through a sync.Pool.
//
//  3. Pre-encoded base key-value pairs
//     `With` encodes its key-value pairs exactly once and stores the resulting bytes on the child logger.
//     Log splices them with a single append: an O(n) byte copy with no JSON encoding work.
//
//  4. Bulk-copy string escaping
//     A start cursor is kept and escaped bytes are materialised only when a character needs it.
//     Clean ASCII runs are copied with a single append slice, which compiles to a memmove intrinsic.
//
//  5. Single syscall per record
//     The entire record is assembled in memory before the mutex is acquired, so the critical section is as short as a single Write call.
//     This keeps lock contention low under concurrent load while guaranteeing that records from different goroutines never interleave in the output.
type fileLogger struct {
	mu sync.Mutex

	// Shared state
	pool *sync.Pool
	w    io.WriteCloser

	level    atomic.Int32
	baseJSON []byte // pre-encoded JSON fragment containing the fixed key-value pairs
}

// newStdoutLogger creates a new thread-safe [Logger] that writes JSON logs to the standard output.
func newStdoutLogger(o stdoutLoggerOpts, m metadataOpts) Logger {
	if o.Level == "" {
		o.Level = "info"
	}

	pool := &sync.Pool{
		New: func() any {
			// Return a pointer, so it can be put into the return interface value without an allocation.
			b := make([]byte, 0, 1024)
			return &b
		},
	}

	l := &fileLogger{
		pool: pool,
		w:    os.Stdout,
	}

	l.SetLevel(o.Level)

	if m.Name != "" {
		l.baseJSON = appendJSONPairs(l.baseJSON, []any{nameKey, m.Name})
	}

	if m.Version != "" {
		l.baseJSON = appendJSONPairs(l.baseJSON, []any{versionKey, m.Version})
	}

	for n, v := range m.Attributes {
		l.baseJSON = appendJSONPairs(l.baseJSON, []any{n, v})
	}

	return l
}

// newFileLogger creates a new thread-safe [Logger] that writes JSON logs to a file.
func newFileLogger(o fileLoggerOpts, m metadataOpts) (Logger, error) {
	if o.Level == "" {
		o.Level = "info"
	}

	if o.Filepath == "" {
		o.Filepath = "app.log"
	}

	if o.MaxSize <= 0 {
		o.MaxSize = 100 // Default to 100 MB
	}

	if o.MaxBackups <= 0 {
		o.MaxBackups = 5 // Default to 5 backups
	}

	if o.MaxAge <= 0 {
		o.MaxAge = 7 // Default to 7 days
	}

	pool := &sync.Pool{
		New: func() any {
			// Return a pointer, so it can be put into the return interface value without an allocation.
			b := make([]byte, 0, 1024)
			return &b
		},
	}

	f, err := file.NewRotating(
		o.Filepath,
		int64(o.MaxSize)*1024*1024, // MB to bytes
		o.MaxBackups,
		time.Duration(o.MaxAge)*24*time.Hour, // Days to duration
	)

	if err != nil {
		return nil, err
	}

	l := &fileLogger{
		pool: pool,
		w:    f,
	}

	l.SetLevel(o.Level)

	if m.Name != "" {
		l.baseJSON = appendJSONPairs(l.baseJSON, []any{nameKey, m.Name})
	}

	if m.Version != "" {
		l.baseJSON = appendJSONPairs(l.baseJSON, []any{versionKey, m.Version})
	}

	for n, v := range m.Attributes {
		l.baseJSON = appendJSONPairs(l.baseJSON, []any{n, v})
	}

	return l, nil
}

func (l *fileLogger) Level() Level {
	return Level(l.level.Load())
}

func (l *fileLogger) SetLevel(level string) {
	l.level.Store(int32(parseLevel(level)))
}

func (l *fileLogger) With(kv ...any) Logger {
	// Pre-encode the current and new base key-value pairs into a new byte slice.
	merged := make([]byte, len(l.baseJSON), len(l.baseJSON)+len(kv)*20)
	copy(merged, l.baseJSON)
	merged = appendJSONPairs(merged, kv)

	child := &fileLogger{
		pool:     l.pool,
		w:        l.w, // File handle is shared among all loggers.
		baseJSON: merged,
	}

	child.level.Store(l.level.Load())

	return child
}

func (l *fileLogger) Debug(message string, kv ...any) {
	l.log(LevelDebug, message, nil, kv)
}

func (l *fileLogger) Debugf(format string, args ...any) {
	l.log(LevelDebug, format, args, nil)
}

func (l *fileLogger) Info(message string, kv ...any) {
	l.log(LevelInfo, message, nil, kv)
}

func (l *fileLogger) Infof(format string, args ...any) {
	l.log(LevelInfo, format, args, nil)
}

func (l *fileLogger) Warn(message string, kv ...any) {
	l.log(LevelWarn, message, nil, kv)
}

func (l *fileLogger) Warnf(format string, args ...any) {
	l.log(LevelWarn, format, args, nil)
}

func (l *fileLogger) Error(message string, kv ...any) {
	l.log(LevelError, message, nil, kv)
}

func (l *fileLogger) Errorf(format string, args ...any) {
	l.log(LevelError, format, args, nil)
}

func (l *fileLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Closing the standard output returns an error.
	if l.w == os.Stdout {
		return nil
	}

	return l.w.Close()
}

func (l *fileLogger) log(level Level, format string, args []any, kv []any) {
	if l.Level() < level {
		return
	}

	l.emit(
		time.Now(),
		levelString(level),
		formatMessage(format, args),
		kv,
	)
}

func (l *fileLogger) emit(t time.Time, level string, message string, kv []any) {
	// Recycle the buffer from the pool.
	bp := l.pool.Get().(*[]byte)
	b := (*bp)[:0]

	// Encode the fixed fields: timestamp, level, message
	b = append(b, timeKeyBytes...)
	b = t.AppendFormat(b, timeFormat)
	b = append(b, levelKeyBytes...)
	b = append(b, level...)
	b = append(b, messageKeyBytes...)
	b = appendEncodedString(b, []byte(message))

	// Add the pre-encoded base key-value pairs.
	b = append(b, l.baseJSON...)

	// Add per-call key-value pairs.
	b = appendJSONPairs(b, kv)

	b = append(b, "}\n"...)

	l.mu.Lock()
	if _, err := l.w.Write(b); err != nil {
		handleError("failed to write log record: %s", err)
	}
	l.mu.Unlock()

	// Save the buffer to be reused.
	*bp = b
	l.pool.Put(bp)
}

// -------------------------------------------------- Loki Logger --------------------------------------------------

const (
	lokiMaxLabels     = 32
	lokiMaxMetadata   = 64
	lokiBatchMaxSize  = 100
	lokiQueueCapacity = 200
	lokiBatchTimeout  = 5 * time.Second
	lokiMaxRetries    = 5
	lokiBaseBackoff   = 100 * time.Millisecond
	lokiMaxBackoff    = 30 * time.Second
)

// lokiLogger is a thread-safe [Logger] implementation that sends logs to a Loki Push API endpoint.
//
// Sends are async and best-effort: the underlying loki.Client queues and batches streams by size or timeout,
// dropping new entries if the queue is full, and retries failed pushes with exponential backoff.
type lokiLogger struct {
	// Shared state
	client lokiClient
	labels map[string]struct{}
	pool   *sync.Pool // *lokiPairs

	level        atomic.Int32
	baseLabels   []loki.Pair // pre-partitioned base labels
	baseMetadata []loki.Pair // pre-partitioned base structured metadata
}

type lokiClient interface {
	Close()
	SendAt(t time.Time, line string, labels, metadata []loki.Pair)
}

type lokiPairs struct {
	labels   []loki.Pair
	metadata []loki.Pair
}

// newLokiLogger creates a new thread-safe [Logger] that sends logs to a Loki Push API endpoint.
func newLokiLogger(o lokiLoggerOpts, m metadataOpts) Logger {
	if o.Level == "" {
		o.Level = "info"
	}

	if o.Endpoint == "" {
		o.Endpoint = "http://localhost:3100/loki/api/v1/push"
	}

	client := loki.NewClient(o.Tenant, o.Endpoint, loki.Options{
		TLSEnabled:    o.TLS.Enabled,
		TLSConfig:     o.TLS.Config,
		MaxLabels:     lokiMaxLabels,
		MaxMetadata:   lokiMaxMetadata,
		BatchMaxSize:  lokiBatchMaxSize,
		QueueCapacity: lokiQueueCapacity,
		BatchTimeout:  lokiBatchTimeout,
		MaxRetries:    lokiMaxRetries,
		BaseBackoff:   lokiBaseBackoff,
		MaxBackoff:    lokiMaxBackoff,
	})

	// Build a map of labels for efficient lookup.
	labels := make(map[string]struct{}, len(o.Labels)+2)
	labels[levelKey] = struct{}{}
	labels[nameKey] = struct{}{}
	for _, l := range o.Labels {
		labels[l] = struct{}{}
	}

	pool := &sync.Pool{
		New: func() any {
			// Return a pointer, so it can be put into the return interface value without an allocation.
			return &lokiPairs{
				labels:   make([]loki.Pair, 0, lokiMaxLabels),
				metadata: make([]loki.Pair, 0, lokiMaxMetadata),
			}
		},
	}

	l := &lokiLogger{
		client: client,
		labels: labels,
		pool:   pool,
	}

	l.SetLevel(o.Level)

	if m.Name != "" {
		l.addBaseKV(nameKey, m.Name)
	}

	if m.Version != "" {
		l.addBaseKV(versionKey, m.Version)
	}

	for n, v := range m.Attributes {
		l.addBaseKV(n, v)
	}

	return l
}

// addBaseKV designates a fixed name-value pair as a label or as structured metadata,
// and appends it to the logger's pre-partitioned base labels or metadata.
func (l *lokiLogger) addBaseKV(name string, value any) {
	p := loki.Pair{
		Name:  name,
		Value: getJSONValue(value),
	}

	if _, ok := l.labels[name]; ok {
		l.baseLabels = append(l.baseLabels, p)
	} else {
		l.baseMetadata = append(l.baseMetadata, p)
	}
}

func (l *lokiLogger) Level() Level {
	return Level(l.level.Load())
}

func (l *lokiLogger) SetLevel(level string) {
	l.level.Store(int32(parseLevel(level)))
}

func (l *lokiLogger) With(kv ...any) Logger {
	// Partition key-value pairs into labels and structured metadata based on the configured label set.
	var newLabels, newMetadata []loki.Pair
	for i := 0; i+1 < len(kv); i += 2 {
		p := loki.Pair{
			Name:  getJSONKey(kv[i]),
			Value: getJSONValue(kv[i+1]),
		}

		if _, ok := l.labels[p.Name]; ok {
			newLabels = append(newLabels, p)
		} else {
			newMetadata = append(newMetadata, p)
		}
	}

	baseLabels := make([]loki.Pair, len(l.baseLabels)+len(newLabels))
	copy(baseLabels, l.baseLabels)
	copy(baseLabels[len(l.baseLabels):], newLabels)

	baseMetadata := make([]loki.Pair, len(l.baseMetadata)+len(newMetadata))
	copy(baseMetadata, l.baseMetadata)
	copy(baseMetadata[len(l.baseMetadata):], newMetadata)

	child := &lokiLogger{
		client:       l.client,
		labels:       l.labels,
		pool:         l.pool,
		baseLabels:   baseLabels,
		baseMetadata: baseMetadata,
	}

	child.level.Store(l.level.Load())

	return child
}

func (l *lokiLogger) Debug(message string, kv ...any) {
	l.log(LevelDebug, message, nil, kv)
}

func (l *lokiLogger) Debugf(format string, args ...any) {
	l.log(LevelDebug, format, args, nil)
}

func (l *lokiLogger) Info(message string, kv ...any) {
	l.log(LevelInfo, message, nil, kv)
}

func (l *lokiLogger) Infof(format string, args ...any) {
	l.log(LevelInfo, format, args, nil)
}

func (l *lokiLogger) Warn(message string, kv ...any) {
	l.log(LevelWarn, message, nil, kv)
}

func (l *lokiLogger) Warnf(format string, args ...any) {
	l.log(LevelWarn, format, args, nil)
}

func (l *lokiLogger) Error(message string, kv ...any) {
	l.log(LevelError, message, nil, kv)
}

func (l *lokiLogger) Errorf(format string, args ...any) {
	l.log(LevelError, format, args, nil)
}

func (l *lokiLogger) Close() error {
	l.client.Close()
	return nil
}

func (l *lokiLogger) log(level Level, format string, args []any, kv []any) {
	if l.Level() < level {
		return
	}

	l.emit(
		time.Now(),
		levelString(level),
		formatMessage(format, args),
		kv,
	)
}

func (l *lokiLogger) emit(t time.Time, level string, message string, kv []any) {
	// Recycle the label/metadata slices from the pool.
	bp := l.pool.Get().(*lokiPairs)

	labels := append(bp.labels[:0], l.baseLabels...)
	labels = append(labels, loki.Pair{Name: levelKey, Value: level})
	metadata := append(bp.metadata[:0], l.baseMetadata...)

	for i := 0; i+1 < len(kv); i += 2 {
		p := loki.Pair{
			Name:  getJSONKey(kv[i]),
			Value: getJSONValue(kv[i+1]),
		}

		// Add the configured labels to the labels and other pairs as structured metadata.
		if _, ok := l.labels[p.Name]; ok {
			labels = append(labels, p)
		} else {
			metadata = append(metadata, p)
		}
	}

	// SendAt copies everything it needs out of labels and metadata before returning,
	// so it is safe to save and reuse these slices as soon as it returns.
	l.client.SendAt(t, message, labels, metadata)

	// Save the slices to be reused.
	bp.labels, bp.metadata = labels, metadata
	l.pool.Put(bp)
}

// -------------------------------------------------- Forward Logger --------------------------------------------------

// forwardLogger is a thread-safe [Logger] implementation that sends logs to a Fluentd Forward protocol endpoint.
type forwardLogger struct {
	// Shared state
	client forwardClient

	level  atomic.Int32
	baseKV []any
}

type forwardClient interface {
	Close() error
	SendAt(t time.Time, r forward.Record)
}

// newForwardLogger creates a new thread-safe [Logger] that sends logs to a Fluentd Forward Protocol endpoint.
func newForwardLogger(o forwardLoggerOpts, m metadataOpts) (Logger, error) {
	if o.Level == "" {
		o.Level = "info"
	}

	if o.Endpoint == "" {
		o.Endpoint = "localhost:24224"
	}

	client, err := forward.NewClient(o.Tag, o.Endpoint, forward.Options{
		TLSEnabled: o.TLS.Enabled,
		TLSConfig:  o.TLS.Config,
	})

	if err != nil {
		return nil, err
	}

	l := &forwardLogger{
		client: client,
	}

	l.SetLevel(o.Level)

	if m.Name != "" {
		l.baseKV = append(l.baseKV, nameKey, m.Name)
	}

	if m.Version != "" {
		l.baseKV = append(l.baseKV, versionKey, m.Version)
	}

	for n, v := range m.Attributes {
		l.baseKV = append(l.baseKV, n, v)
	}

	return l, nil
}

func (l *forwardLogger) Level() Level {
	return Level(l.level.Load())
}

func (l *forwardLogger) SetLevel(level string) {
	l.level.Store(int32(parseLevel(level)))
}

func (l *forwardLogger) With(kv ...any) Logger {
	merged := make([]any, len(l.baseKV), len(l.baseKV)+len(kv))
	copy(merged, l.baseKV)
	merged = appendNormalizedKV(merged, kv)

	child := &forwardLogger{
		client: l.client,
		baseKV: merged,
	}

	child.level.Store(l.level.Load())

	return child
}

func (l *forwardLogger) Debug(message string, kv ...any) {
	l.log(LevelDebug, message, nil, kv)
}

func (l *forwardLogger) Debugf(format string, args ...any) {
	l.log(LevelDebug, format, args, nil)
}

func (l *forwardLogger) Info(message string, kv ...any) {
	l.log(LevelInfo, message, nil, kv)
}

func (l *forwardLogger) Infof(format string, args ...any) {
	l.log(LevelInfo, format, args, nil)
}

func (l *forwardLogger) Warn(message string, kv ...any) {
	l.log(LevelWarn, message, nil, kv)
}

func (l *forwardLogger) Warnf(format string, args ...any) {
	l.log(LevelWarn, format, args, nil)
}

func (l *forwardLogger) Error(message string, kv ...any) {
	l.log(LevelError, message, nil, kv)
}

func (l *forwardLogger) Errorf(format string, args ...any) {
	l.log(LevelError, format, args, nil)
}

func (l *forwardLogger) Close() error {
	return l.client.Close()
}

func (l *forwardLogger) log(level Level, format string, args []any, kv []any) {
	if l.Level() < level {
		return
	}

	l.emit(
		time.Now(),
		levelString(level),
		formatMessage(format, args),
		kv,
	)
}

func (l *forwardLogger) emit(t time.Time, level string, message string, kv []any) {
	r := make(forward.Record, 3+len(l.baseKV)/2+len(kv)/2)

	// Add the fixed fields: timestamp, level, message
	r[timeKey] = t.Format(timeFormat)
	r[levelKey] = level
	r[messageKey] = message

	// Add the base and per-call fields.
	setForwardKV(r, l.baseKV)
	setForwardKV(r, kv)

	// A record cannot be pooled and reused across calls:
	// SendAt hands it to an async batching queue instead of consuming it before returning,
	// so the record must remain valid until some later point in time.
	l.client.SendAt(t, r)
}

// appendNormalizedKV appends a variadic list of key-value pairs to base with keys normalized to string.
func appendNormalizedKV(base []any, kv []any) []any {
	for i := 0; i+1 < len(kv); i += 2 {
		base = append(base,
			getJSONKey(kv[i]),
			getJSONValue(kv[i+1]),
		)
	}

	return base
}

// setForwardKV sets a variadic list of key-value pairs on a record.
func setForwardKV(r forward.Record, kv []any) {
	for i := 0; i+1 < len(kv); i += 2 {
		ks := getJSONKey(kv[i])
		r[ks] = getJSONValue(kv[i+1])
	}
}

// -------------------------------------------------- OpenTelemetry Logger --------------------------------------------------

// opentelemetryLogger is a thread-safe [Logger] implementation that sends logs to an OpenTelemetry Collector endpoint.
type opentelemetryLogger struct {
	// Shared state
	otel  otelLogger
	close closeFunc
	pool  *sync.Pool

	level  atomic.Int32
	baseKV []attribute.KeyValue
}

type otelLogger interface {
	Emit(ctx context.Context, record log.Record)
}

// newOpenTelemetryLogger creates a new thread-safe [Logger] that sends logs to an OpenTelemetry Collector endpoint.
func newOpenTelemetryLogger(o opentelemetryLoggerOpts, m metadataOpts) Logger {
	if o.Level == "" {
		o.Level = "info"
	}

	if o.OTLPHTTP == nil && o.OTLPGRPC == nil {
		o.OTLPHTTP = &otlpHTTPOpts{
			Endpoint: "localhost:4318",
		}
	}

	if o.OTLPHTTP != nil && o.OTLPHTTP.Endpoint == "" {
		o.OTLPHTTP.Endpoint = "localhost:4318"
	}

	if o.OTLPGRPC != nil && o.OTLPGRPC.Endpoint == "" {
		o.OTLPGRPC.Endpoint = "localhost:4317"
	}

	pool := &sync.Pool{
		New: func() any {
			// Return a pointer, so it can be put into the return interface value without an allocation.
			b := make([]attribute.KeyValue, 0, 16)
			return &b
		},
	}

	logger, close := createOTelLogger(o, m)

	l := &opentelemetryLogger{
		otel:  logger,
		close: close,
		pool:  pool,
	}

	l.SetLevel(o.Level)

	return l
}

func (l *opentelemetryLogger) Level() Level {
	return Level(l.level.Load())
}

func (l *opentelemetryLogger) SetLevel(level string) {
	l.level.Store(int32(parseLevel(level)))
}

func (l *opentelemetryLogger) With(kv ...any) Logger {
	merged := make([]attribute.KeyValue, len(l.baseKV), len(l.baseKV)+len(kv)/2)
	copy(merged, l.baseKV)
	merged = appendOTelKV(merged, kv)

	child := &opentelemetryLogger{
		otel:   l.otel,
		close:  l.close,
		pool:   l.pool,
		baseKV: merged,
	}

	child.level.Store(l.level.Load())

	return child
}

func (l *opentelemetryLogger) Debug(message string, kv ...any) {
	l.log(LevelDebug, message, nil, kv)
}

func (l *opentelemetryLogger) Debugf(format string, args ...any) {
	l.log(LevelDebug, format, args, nil)
}

func (l *opentelemetryLogger) Info(message string, kv ...any) {
	l.log(LevelInfo, message, nil, kv)
}

func (l *opentelemetryLogger) Infof(format string, args ...any) {
	l.log(LevelInfo, format, args, nil)
}

func (l *opentelemetryLogger) Warn(message string, kv ...any) {
	l.log(LevelWarn, message, nil, kv)
}

func (l *opentelemetryLogger) Warnf(format string, args ...any) {
	l.log(LevelWarn, format, args, nil)
}

func (l *opentelemetryLogger) Error(message string, kv ...any) {
	l.log(LevelError, message, nil, kv)
}

func (l *opentelemetryLogger) Errorf(format string, args ...any) {
	l.log(LevelError, format, args, nil)
}

func (l *opentelemetryLogger) Close() error {
	return l.close(context.Background())
}

func (l *opentelemetryLogger) log(level Level, format string, args []any, kv []any) {
	if l.Level() < level {
		return
	}

	l.emit(
		time.Now(),
		levelString(level),
		formatMessage(format, args),
		kv,
	)
}

func (l *opentelemetryLogger) emit(t time.Time, level string, message string, kv []any) {
	var r log.Record

	// Add the fixed fields: timestamp, severity, body
	r.SetTimestamp(t)
	r.SetObservedTimestamp(t)
	r.SetSeverity(levelToSeverity(level))
	r.SetSeverityText(level)
	r.SetBody(attribute.StringValue(message))

	// Add the base and per-call key-value pairs in a single call,
	// so that any overflow past the record's inline attribute array is grown at most once.
	if len(l.baseKV) > 0 || len(kv) > 0 {
		// Recycle the buffer from the pool.
		bp := l.pool.Get().(*[]attribute.KeyValue)
		b := append((*bp)[:0], l.baseKV...)
		b = appendOTelKV(b, kv)

		r.AddAttributes(b...)

		// Save the buffer to be reused.
		*bp = b
		l.pool.Put(bp)
	}

	// Emit the log record through the OpenTelemetry logger.
	// The OpenTelemetry logger takes ownership of the record from this point, so no lock is required.
	l.otel.Emit(context.Background(), r)
}
