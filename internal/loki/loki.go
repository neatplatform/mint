// Package loki provides a simple, best-effort client for sending logs to a Loki Push API endpoint.
// It batches and sends streams asynchronously with retrying,
// optimized for a lean, bounded-memory logging experience.
// Errors are not returned, only written to stderr.
package loki

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang/snappy"
	"github.com/grafana/loki/pkg/push"

	"github.com/neatplatform/mint/httpx"
	"github.com/neatplatform/mint/queue"
)

const (
	maxBodyDrainSize = 4 * 1024 // 4 KB
)

// Pair is a name-value string pair, used as a Loki label or a structured metadata entry.
type Pair struct {
	Name  string
	Value string
}

// Options holds the optional configuration for a Loki client.
type Options struct {
	TLSEnabled bool        // TLSEnabled indicates whether the Loki Push endpoint is HTTPS.
	TLSConfig  *tls.Config // TLSConfig is the TLS configuration for the Loki Push endpoint.

	MaxLabels   int // The maximum number of labels for logs
	MaxMetadata int // The maximum number of metadata for logs

	BatchMaxSize  int           // The maximum number of logs to batch before sending to Loki.
	QueueCapacity int           // The maximum number of logs to queue before dropping new logs.
	BatchTimeout  time.Duration // The maximum duration to wait before sending a batch of logs to Loki.

	MaxRetries  int           // The maximum number of times to retry a failed request.
	BaseBackoff time.Duration // The initial delay before retrying a failed request.
	MaxBackoff  time.Duration // The maximum delay between retries of a failed request.
}

func (o Options) withDefaults() Options {
	if o.MaxLabels <= 0 {
		o.MaxLabels = 16
	}

	if o.MaxMetadata <= 0 {
		o.MaxMetadata = 16
	}

	if o.BatchMaxSize <= 0 {
		o.BatchMaxSize = 100
	}

	if o.QueueCapacity <= 0 {
		o.QueueCapacity = 200
	}

	if o.BatchTimeout <= 0 {
		o.BatchTimeout = 5 * time.Second
	}

	if o.MaxRetries <= 0 {
		o.MaxRetries = 5
	}

	if o.BaseBackoff <= 0 {
		o.BaseBackoff = 100 * time.Millisecond
	}

	if o.MaxBackoff <= 0 {
		o.MaxBackoff = time.Minute
	}

	if o.MaxBackoff < o.BaseBackoff {
		o.BaseBackoff = 100 * time.Millisecond
		o.MaxBackoff = time.Minute
	}

	return o
}

// Client is a Loki client that batches and pushes log streams to a Loki Push endpoint for a single tenant.
// It is safe for concurrent use by multiple goroutines.
//
// Sends are async and best-effort: streams are queued and flushed in batches by size or timeout,
// dropped if the queue is full, retried with backoff on failure, and never return an error (failures go to stderr).
type Client struct {
	tenant   string
	endpoint string
	opts     Options

	client *http.Client                  // Safe for concurrent use
	queue  *queue.Batching[*push.Stream] // Safe for concurrent use

	closeOnce sync.Once
	bufPool   sync.Pool     // *[]byte
	stmPool   sync.Pool     // *push.Stream
	streams   []push.Stream // A scratch buffer for building []push.Stream from []*push.Stream
}

// NewClient creates a new Loki [Client] for the given tenant and Loki Push endpoint.
func NewClient(tenant, endpoint string, opts Options) *Client {
	opts = opts.withDefaults()

	dialer := &net.Dialer{
		Timeout:   5 * time.Second,  // TCP connect timeout
		KeepAlive: 30 * time.Second, // TCP keep-alive
	}

	transport := &http.Transport{
		DialContext: dialer.DialContext,

		// Connection pooling
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		MaxConnsPerHost:     0,
		IdleConnTimeout:     90 * time.Second,
	}

	if opts.TLSEnabled {
		transport.TLSClientConfig = opts.TLSConfig // If nil, the default configuration is used.
		transport.TLSHandshakeTimeout = 5 * time.Second
	}

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
	}

	c := &Client{
		tenant:   tenant,
		endpoint: endpoint,
		opts:     opts,

		client: client,

		bufPool: sync.Pool{
			New: func() any {
				// Return a pointer, so it can be put into the return interface value without an allocation.
				b := make([]byte, 0, 1024)
				return &b
			},
		},

		stmPool: sync.Pool{
			New: func() any {
				// Return a pointer, so it can be put into the return interface value without an allocation.
				return &push.Stream{
					Entries: []push.Entry{
						{
							StructuredMetadata: make(push.LabelsAdapter, 0, 16),
						},
					},
				}
			},
		},
	}

	c.queue = queue.NewBatching(opts.BatchMaxSize, opts.QueueCapacity, opts.BatchTimeout, c.sendStreams)

	return c
}

// Flush signals the queue to send its pending batch immediately,
// without waiting for it to fill up or time out.
func (c *Client) Flush() {
	c.queue.Flush()
}

// Close stops the queue, blocking until all queued streams have been sent,
// then releases the underlying HTTP client's idle connections.
//
// Close is idempotent and safe for concurrent use.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.queue.Stop()
		c.client.CloseIdleConnections()
	})
}

// Send queues a log line, timestamped with the current time,
// for an async best-effort push to Loki.
//
// labels are indexed by Loki and must remain low-cardinality; at least one is required.
// Metadata is stored unindexed, and can hold higher-cardinality values that are not suitable as labels.
func (c *Client) Send(line string, labels, metadata []Pair) {
	c.SendAt(time.Now(), line, labels, metadata)
}

// SendAt queues a log line, timestamped with t,
// for an async best-effort push to Loki.
//
// labels are indexed by Loki and must remain low-cardinality; at least one is required.
// Metadata is stored unindexed, and can hold higher-cardinality values that are not suitable as labels.
func (c *Client) SendAt(t time.Time, line string, labels, metadata []Pair) {
	if len(labels) == 0 {
		handleError("[loki] invalid labels: at least one label is required")
		return
	}

	if len(labels) > c.opts.MaxLabels {
		handleError("[loki] invalid labels: the maximum number of labels is %d", c.opts.MaxLabels)
		return
	}

	if len(metadata) > c.opts.MaxMetadata {
		handleError("[loki] invalid metadata: the maximum number of metadata is %d", c.opts.MaxMetadata)
		return
	}

	// Best-effort: the item is dropped if the queue is full.
	c.queue.Enqueue(c.buildStream(t, line, labels, metadata))
}

// buildStream assembles a single-entry push.Stream from a log line, its labels, and its metadata.
// labels are indexed by Loki and must remain low-cardinality; metadata is stored per-entry, unindexed,
// and can hold higher-cardinality values that aren't suitable as labels (e.g. request IDs, trace IDs).
//
// The returned *push.Stream is an independently pooled object (stmPool)
// that is only ever reused once it is handed back to the pool (from sendStreams).
// It is never reused while the batching queue still owns it,
// so it must not be copied by value and reused elsewhere while in flight.
func (c *Client) buildStream(t time.Time, line string, labels, metadata []Pair) *push.Stream {
	// Recycle the buffer from the pool.
	bp := c.bufPool.Get().(*[]byte)
	b := (*bp)[:0]

	// Construct the Loki label selector.
	b = append(b, '{')
	for i, p := range labels {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, p.Name...)
		b = append(b, '=')
		b = strconv.AppendQuote(b, p.Value)
	}
	b = append(b, '}')

	// Recycle a Stream from the pool and mutate it in place.
	s := c.stmPool.Get().(*push.Stream)

	s.Labels = string(b)
	s.Entries[0].Timestamp = t
	s.Entries[0].Line = line

	// Reuse the pooled StructuredMetadata slice.
	// append only reallocates if the capacity is exceeded.
	sm := s.Entries[0].StructuredMetadata[:0]
	for _, p := range metadata {
		sm = append(sm, push.LabelAdapter{Name: p.Name, Value: p.Value})
	}
	s.Entries[0].StructuredMetadata = sm

	// Save the buffer to be reused.
	*bp = b
	c.bufPool.Put(bp)

	return s
}

// sendStreams marshals streams into a snappy-compressed protobuf push request and sends it to Loki.
func (c *Client) sendStreams(batch []*push.Stream) {
	// Return each stream to the pool to be reused at the end of this call.
	// batch is owned by the batching queue and it is reused for the next call,
	// but it only overwrites the pointers, not the streams they point to,
	// so putting the pointers themselves back into the pool is safe.
	defer func() {
		for _, s := range batch {
			c.stmPool.Put(s)
		}
	}()

	if cap(c.streams) < len(batch) {
		c.streams = make([]push.Stream, len(batch))
	}

	req := push.PushRequest{
		Streams: c.streams[:len(batch)],
	}

	for i, s := range batch {
		req.Streams[i] = *s
	}

	// Marshal the request to protobuf.
	data, err := req.Marshal()
	if err != nil {
		handleError("[loki] error encoding the request: %s", err)
		return
	}

	// Compress the request body using snappy.
	compressed := snappy.Encode(nil, data)

	ctx := context.Background()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(compressed))
	if err != nil {
		handleError("[loki] error creating http request: %s", err)
		return
	}

	httpReq.Header.Set("Content-Encoding", "snappy")
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	if c.tenant != "" {
		httpReq.Header.Set("X-Scope-OrgID", c.tenant)
	}

	resp, err := httpx.Retry(c.client, httpReq, c.opts.MaxRetries, c.opts.BaseBackoff, c.opts.MaxBackoff)
	if err != nil {
		handleError("[loki] error sending http request: %s", err)
		return
	}

	// Drain and close the body so the transport can reuse the underlying TCP connection.
	// Cap the body size, so a large or slow response body cannot stall the caller.
	// If the body exceeds the cap, the connection will not be reused for the next attempt.
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyDrainSize))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyDrainSize))
		body := strings.Trim(string(b), "\n")
		handleError("[loki] unexpected http status: [%d] %s", resp.StatusCode, body)
		return
	}
}

// handleError handles an error that cannot be returned to a caller,
// such as one occurring on the hot path or inside a goroutine.
func handleError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
