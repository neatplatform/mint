// Package forward implements a client for the Fluentd Forward Protocol,
// sending batched log entries to a Forward endpoint over TCP/TLS.
package forward

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/tinylib/msgp/msgp"

	"github.com/neatplatform/mint/queue"
)

func init() {
	msgp.RegisterExtension(eventTimeExtType, func() msgp.Extension {
		return new(eventTime)
	})
}

// eventTime is a nanosecond-precision timestamp encoded as msgpack extension type 0.
// It provides support for sub-second (nanosecond) precision in serialized timestamps.
//
// See https://github.com/fluent/fluentd/wiki/Forward-Protocol-Specification-v1#eventtime-ext-format
//
// See https://github.com/tinylib/msgp/wiki/Using-Extensions
type eventTime time.Time

const (
	eventTimeExtType = 0
	eventTimeLen     = 8
)

func (t *eventTime) ExtensionType() int8 {
	return eventTimeExtType
}

func (t *eventTime) Len() int {
	return eventTimeLen
}

func (t *eventTime) MarshalBinaryTo(b []byte) error {
	utc := time.Time(*t).UTC()

	sec := uint32(utc.Unix())
	nsec := uint32(utc.Nanosecond())

	b[0], b[1], b[2], b[3] = byte(sec>>24), byte(sec>>16), byte(sec>>8), byte(sec)
	b[4], b[5], b[6], b[7] = byte(nsec>>24), byte(nsec>>16), byte(nsec>>8), byte(nsec)

	return nil
}

func (t *eventTime) UnmarshalBinary(b []byte) error {
	if len(b) != eventTimeLen {
		return fmt.Errorf("EventTime: invalid length %d", len(b))
	}

	sec := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	nsec := uint32(b[4])<<24 | uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7])

	*t = eventTime(time.Unix(int64(sec), int64(nsec)))

	return nil
}

// Options holds the optional configuration for a Forward client.
type Options struct {
	Timeout time.Duration // Timeout is the timeout for connecting to the endpoint as well as writing a batch.

	TLSEnabled bool        // TLSEnabled indicates whether the Forward endpoint is HTTPS.
	TLSConfig  *tls.Config // TLSConfig is the TLS configuration for the Forward endpoint.

	BatchMaxSize  int           // The maximum number of logs to batch before sending to the Forward endpoint.
	QueueCapacity int           // The maximum number of logs to queue before dropping new logs.
	BatchTimeout  time.Duration // The maximum duration to wait before sending a batch of logs to the Forward endpoint.

	MaxRetries  int           // The maximum number of times to retry sending a batch after a connection failure.
	BaseBackoff time.Duration // The initial delay before retrying a failed send.
	MaxBackoff  time.Duration // The maximum delay between retries of a failed send.
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Second
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

// Record represents a key-value pairs of the event record.
type Record map[string]any

// entry represents a timestamped record.
type entry struct {
	Time   eventTime
	Record Record
}

// Client sends log entries to a Fluentd endpoint over TCP using the Forward Protocol.
type Client struct {
	mu sync.Mutex

	tag      string
	endpoint string
	opts     Options

	conn  net.Conn
	queue *queue.Batching[entry]
}

// NewClient creates a new Client for the given tag and Forward endpoint, and connects to it.
func NewClient(tag, endpoint string, opts Options) (*Client, error) {
	c := &Client{
		tag:      tag,
		endpoint: endpoint,
		opts:     opts.withDefaults(),
	}

	if err := c.connect(); err != nil {
		return nil, err
	}

	c.queue = queue.NewBatching(c.opts.BatchMaxSize, c.opts.QueueCapacity, c.opts.BatchTimeout, c.sendEntries)

	return c, nil
}

// connect dials a TCP connection to the endpoint, upgrading to TLS if enabled.
func (c *Client) connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), c.opts.Timeout)
	defer cancel()

	dialer := &net.Dialer{}

	conn, err := dialer.DialContext(ctx, "tcp", c.endpoint)
	if err != nil {
		return fmt.Errorf("error dialing connection: %w", err)
	}

	if c.opts.TLSEnabled {
		// If nil, the default configuration is used.
		config := c.opts.TLSConfig

		tlsconn := tls.Client(conn, config)
		if err := tlsconn.HandshakeContext(ctx); err != nil {
			_ = conn.Close() // avoid leaking the raw conn on handshake failure
			return fmt.Errorf("error on TLS handshake: %w", err)
		}

		conn = tlsconn
	}

	c.conn = conn

	return nil
}

// Flush sends any buffered log records to the Forward endpoint.
func (c *Client) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.queue.Flush()
}

// Close flushes any buffered log records and closes the connection to the Forward endpoint.
func (c *Client) Close() error {
	// Stop blocks until the worker goroutine finishes processing the final batch.
	// It may call connect, which locks mu, while reconnecting on write failures.
	c.queue.Stop()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil

		if err != nil {
			return fmt.Errorf("error closing connection: %w", err)
		}
	}

	return nil
}

// Send sends a log record to the Forward endpoint, timestamped with the current time.
func (c *Client) Send(r Record) {
	c.SendAt(time.Now(), r)
}

// SendAt sends a log record to the Forward endpoint, timestamped with the given time.
func (c *Client) SendAt(t time.Time, r Record) {
	// Best-effort: the item is dropped if the queue is full.
	c.queue.Enqueue(entry{
		Time:   eventTime(t),
		Record: r,
	})
}

// sendEntries encodes entries as a Fluent Forward Protocol Forward Mode
// payload ([tag, entries, options]) and writes it to the connection.
//
// On failure, it closes the connection, reconnects, and retries the same batch,
// with exponential backoff between attempts up to MaxRetries times.
// If every attempt fails, the batch is dropped and the last error is logged.
//
// This method is only called from the queue's single worker goroutine,
// and Close waits for that goroutine to exit before touching c.conn,
// so no lock is needed here.
func (c *Client) sendEntries(entries []entry) {
	var lastErr error

	for attempt := 0; attempt <= c.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			// BaseBackoff << shift can overflow int64 for large combinations of attempt and BaseBackoff,
			// so cap the exponential backoff at MaxBackoff before shifting instead of after.
			delay := c.opts.MaxBackoff
			if shift := attempt - 1; c.opts.BaseBackoff <= c.opts.MaxBackoff>>shift {
				delay = c.opts.BaseBackoff << shift
			}

			// No jitter: each Client retries independently against its own connection,
			// so there is no thundering-herd of many clients to spread out.

			time.Sleep(delay)
		}

		if c.conn == nil {
			if err := c.connect(); err != nil {
				lastErr = fmt.Errorf("error reconnecting: %w", err)
				continue
			}
		}

		if err := c.writeEntries(entries); err != nil {
			lastErr = err

			// Close the connection and reconnect for the next attempt.
			_ = c.conn.Close()
			c.conn = nil

			continue
		}

		return
	}

	handleError("[forward] error sending entries: %s", lastErr)
}

// writeEntries encodes entries as a single Forward Mode payloadand writes it to the connection.
func (c *Client) writeEntries(entries []entry) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(c.opts.Timeout)); err != nil {
		return fmt.Errorf("error setting write deadline: %w", err)
	}

	w := msgp.NewWriter(c.conn)

	// Outer tuple: [tag, entries array, option map]
	if err := w.WriteArrayHeader(3); err != nil {
		return fmt.Errorf("error writing outer array header: %w", err)
	}

	if err := w.WriteString(c.tag); err != nil {
		return fmt.Errorf("error writing tag: %w", err)
	}

	// Entries: array of [EventTime, Record] tuples
	if err := w.WriteArrayHeader(uint32(len(entries))); err != nil {
		return fmt.Errorf("error writing entries array header: %w", err)
	}

	for _, e := range entries {
		if err := w.WriteArrayHeader(2); err != nil {
			return fmt.Errorf("error writing entry array header: %w", err)
		}

		if err := w.WriteExtension(&e.Time); err != nil {
			return fmt.Errorf("error writing entry time: %w", err)
		}

		if err := writeRecord(w, e.Record); err != nil {
			return fmt.Errorf("error writing entry record: %w", err)
		}
	}

	// No options
	if err := w.WriteMapHeader(0); err != nil {
		return fmt.Errorf("error writing option map header: %w", err)
	}

	if err := w.Flush(); err != nil {
		return fmt.Errorf("error flushing: %w", err)
	}

	return nil
}

// writeRecord encodes r as a MessagePack map without relying on reflection,
// unlike msgp's generic encoding of map[string]any (WriteMapStrIntf).
func writeRecord(w *msgp.Writer, r Record) error {
	if err := w.WriteMapHeader(uint32(len(r))); err != nil {
		return err
	}

	for k, v := range r {
		if err := w.WriteString(k); err != nil {
			return err
		}

		if err := writeValue(w, v); err != nil {
			return err
		}
	}

	return nil
}

// writeValue encodes v by type-switching on its concrete type and calling the matching msgp.Writer method,
// avoiding msgp's reflection-based encoding for interface{} values (WriteIntf).
func writeValue(w *msgp.Writer, v any) error {
	switch val := v.(type) {
	case nil:
		return w.WriteNil()

	case bool:
		return w.WriteBool(val)

	case int8:
		return w.WriteInt8(val)
	case int16:
		return w.WriteInt16(val)
	case int32:
		return w.WriteInt32(val)
	case int64:
		return w.WriteInt64(val)
	case int:
		return w.WriteInt64(int64(val))

	case uint8:
		return w.WriteUint8(val)
	case uint16:
		return w.WriteUint16(val)
	case uint32:
		return w.WriteUint32(val)
	case uint64:
		return w.WriteUint64(val)
	case uint:
		return w.WriteUint64(uint64(val))

	case float32:
		return w.WriteFloat32(val)
	case float64:
		return w.WriteFloat64(val)

	case complex64:
		return w.WriteComplex64(val)
	case complex128:
		return w.WriteComplex128(val)

	case string:
		return w.WriteString(val)
	case []byte:
		return w.WriteBytes(val)

	case time.Time:
		return w.WriteTime(val)
	case time.Duration:
		return w.WriteDuration(val)

	case []any:
		if err := w.WriteArrayHeader(uint32(len(val))); err != nil {
			return err
		}

		for _, vv := range val {
			if err := writeValue(w, vv); err != nil {
				return err
			}
		}

		return nil

	case map[string]any:
		if err := w.WriteMapHeader(uint32(len(val))); err != nil {
			return err
		}

		for kk, vv := range val {
			if err := w.WriteString(kk); err != nil {
				return err
			}

			if err := writeValue(w, vv); err != nil {
				return err
			}
		}

		return nil

	default:
		return w.WriteString(fmt.Sprint(val))
	}
}

// handleError handles an error that cannot be returned to a caller,
// such as one occurring on the hot path or inside a goroutine.
func handleError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
