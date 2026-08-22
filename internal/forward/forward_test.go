package forward

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tinylib/msgp/msgp"
)

func TestEventType(t *testing.T) {
	tests := []struct {
		name                  string
		t                     eventTime
		expectedExtensionType int8
		expectedLen           int
	}{
		{
			name:                  "OK",
			t:                     eventTime(time.Now()),
			expectedExtensionType: 0,
			expectedLen:           8,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := make([]byte, eventTimeLen)

			assert.Equal(t, tc.expectedExtensionType, tc.t.ExtensionType())
			assert.Equal(t, tc.expectedLen, tc.t.Len())
			assert.NoError(t, tc.t.MarshalBinaryTo(b))
			assert.NoError(t, tc.t.UnmarshalBinary(b))
		})
	}
}

func TestOptions(t *testing.T) {
	tests := []struct {
		name                 string
		opts                 Options
		expectedWithDefaults Options
	}{
		{
			name: "WithDefaults",
			opts: Options{},
			expectedWithDefaults: Options{
				Timeout:       5 * time.Second,
				BatchMaxSize:  100,
				QueueCapacity: 200,
				BatchTimeout:  5 * time.Second,
				MaxRetries:    5,
				BaseBackoff:   100 * time.Millisecond,
				MaxBackoff:    time.Minute,
			},
		},
		{
			name: "WithoutDefaults",
			opts: Options{
				Timeout:       10 * time.Second,
				BatchMaxSize:  50,
				QueueCapacity: 100,
				BatchTimeout:  10 * time.Second,
				MaxRetries:    3,
				BaseBackoff:   50 * time.Millisecond,
				MaxBackoff:    5 * time.Second,
			},
			expectedWithDefaults: Options{
				Timeout:       10 * time.Second,
				BatchMaxSize:  50,
				QueueCapacity: 100,
				BatchTimeout:  10 * time.Second,
				MaxRetries:    3,
				BaseBackoff:   50 * time.Millisecond,
				MaxBackoff:    5 * time.Second,
			},
		},
		{
			name: "MaxBackoffLessThanBaseBackoff",
			opts: Options{
				BaseBackoff: time.Minute,
				MaxBackoff:  time.Second,
			},
			expectedWithDefaults: Options{
				Timeout:       5 * time.Second,
				BatchMaxSize:  100,
				QueueCapacity: 200,
				BatchTimeout:  5 * time.Second,
				MaxRetries:    5,
				BaseBackoff:   100 * time.Millisecond,
				MaxBackoff:    time.Minute,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedWithDefaults, tc.opts.withDefaults())
		})
	}
}

func TestNewClient(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		srv, err := newForwardTestServer(func(string, []forwardEntry) {})
		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		opts := Options{}

		c, err := NewClient("echo-service", srv.Addr, opts)
		assert.NoError(t, err)

		assert.NotNil(t, c)
		assert.Equal(t, "echo-service", c.tag)
		assert.Equal(t, srv.Addr, c.endpoint)
		assert.Equal(t, opts.withDefaults(), c.opts)
		assert.NotNil(t, c.conn)
		assert.NotNil(t, c.queue)
		assert.NoError(t, c.Close())
	})

	t.Run("Success_WithOptions", func(t *testing.T) {
		srv, err := newForwardTestServer(func(string, []forwardEntry) {})
		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		opts := Options{
			Timeout:       time.Second,
			BatchMaxSize:  50,
			QueueCapacity: 100,
			BatchTimeout:  10 * time.Second,
			MaxRetries:    3,
			BaseBackoff:   50 * time.Millisecond,
			MaxBackoff:    5 * time.Second,
		}

		c, err := NewClient("echo-service", srv.Addr, opts)
		assert.NoError(t, err)

		assert.NotNil(t, c)
		assert.Equal(t, "echo-service", c.tag)
		assert.Equal(t, srv.Addr, c.endpoint)
		assert.Equal(t, opts, c.opts)
		assert.NotNil(t, c.conn)
		assert.NotNil(t, c.queue)
		assert.NoError(t, c.Close())
	})

	t.Run("Success_WithTLS", func(t *testing.T) {
		srv, err := newTLSForwardTestServer(func(string, []forwardEntry) {})
		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		opts := Options{
			TLSEnabled: true,
			TLSConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}

		c, err := NewClient("echo-service", srv.Addr, opts)
		assert.NoError(t, err)

		assert.NotNil(t, c)
		assert.Equal(t, "echo-service", c.tag)
		assert.Equal(t, srv.Addr, c.endpoint)
		assert.Equal(t, opts.withDefaults(), c.opts)
		assert.NotNil(t, c.conn)
		assert.NotNil(t, c.queue)
		assert.NoError(t, c.Close())
	})

	t.Run("DialFails", func(t *testing.T) {
		c, err := NewClient("echo-service", "127.0.0.1:0", Options{
			Timeout: 200 * time.Millisecond,
		})

		assert.Nil(t, c)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error dialing connection: dial tcp 127.0.0.1:0: connect:")
	})
}

func TestClient_connect(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		srv, err := newForwardTestServer(func(string, []forwardEntry) {})
		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		c := &Client{
			endpoint: srv.Addr,
			opts:     Options{}.withDefaults(),
		}

		err = c.connect()

		assert.NoError(t, err)
		assert.NotNil(t, c.conn)
		assert.NoError(t, c.conn.Close())
	})

	t.Run("Success_WithTLS", func(t *testing.T) {
		srv, err := newTLSForwardTestServer(func(string, []forwardEntry) {})
		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		c := &Client{
			endpoint: srv.Addr,
			opts: Options{
				TLSEnabled: true,
				TLSConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			}.withDefaults(),
		}

		err = c.connect()

		assert.NoError(t, err)
		assert.NotNil(t, c.conn)
		assert.NoError(t, c.conn.Close())
	})

	t.Run("DialFails", func(t *testing.T) {
		c := &Client{
			endpoint: "127.0.0.1:0",
			opts: Options{
				Timeout: 200 * time.Millisecond,
			}.withDefaults(),
		}

		err := c.connect()

		assert.Nil(t, c.conn)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error dialing connection: dial tcp 127.0.0.1:0: connect:")
	})

	t.Run("TLSHandshakeFails", func(t *testing.T) {
		srv, err := newForwardTestServer(func(string, []forwardEntry) {})
		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		c := &Client{
			endpoint: srv.Addr,
			opts: Options{
				TLSEnabled: true,
				TLSConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			}.withDefaults(),
		}

		err = c.connect()

		assert.Error(t, err)
		assert.EqualError(t, err, "error on TLS handshake: EOF")
	})
}

func TestClient_Flush(t *testing.T) {
	t.Run("OK", func(t *testing.T) {
		var mu sync.Mutex
		var counter atomic.Int64
		var gotTag string
		var gotEntries []forwardEntry

		srv, err := newForwardTestServer(func(tag string, entries []forwardEntry) {
			mu.Lock()
			defer mu.Unlock()

			counter.Add(1)
			gotTag, gotEntries = tag, entries
		})

		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		c, err := NewClient("echo-service", srv.Addr, Options{
			BatchMaxSize:  100,
			QueueCapacity: 200,
			BatchTimeout:  time.Minute,
			MaxRetries:    1,
			BaseBackoff:   time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})

		assert.NoError(t, err)

		defer func() {
			_ = c.Close()
		}()

		c.Send(Record{"message": "foo"})
		c.Send(Record{"message": "bar"})

		// Wait for the enqueued entries to be pulled into the current batch.
		time.Sleep(50 * time.Millisecond)

		assert.Zero(t, counter.Load())

		c.Flush()

		// Close only guarantees the batch was written to the socket,
		// not that the server goroutine on the other end has receive it yet.
		assert.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()

			return counter.Load() == 1 &&
				gotTag == "echo-service" &&
				len(gotEntries) == 2 &&
				gotEntries[0].Record["message"] == "foo" &&
				gotEntries[1].Record["message"] == "bar"
		}, 200*time.Millisecond, 10*time.Millisecond)
	})
}

func TestClient_Close(t *testing.T) {
	t.Run("Idempotent", func(t *testing.T) {
		srv, err := newForwardTestServer(func(string, []forwardEntry) {})
		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		c, err := NewClient("echo-service", srv.Addr, Options{})
		assert.NoError(t, err)

		assert.NoError(t, c.Close())
		assert.NoError(t, c.Close())
		assert.NoError(t, c.Close())
	})

	t.Run("DropsSendsAfterClose", func(t *testing.T) {
		srv, err := newForwardTestServer(func(string, []forwardEntry) {})
		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		c, err := NewClient("echo-service", srv.Addr, Options{})
		assert.NoError(t, err)

		assert.NoError(t, c.Close())
		assert.Zero(t, c.queue.Dropped())

		c.Send(Record{"message": "foo"})
		c.Send(Record{"message": "bar"})
		assert.Equal(t, 2, c.queue.Dropped())
	})

	t.Run("BlocksUntilQueueFlushed", func(t *testing.T) {
		var mu sync.Mutex
		var counter atomic.Int64
		var gotTag string
		var gotEntries []forwardEntry

		srv, err := newForwardTestServer(func(tag string, entries []forwardEntry) {
			mu.Lock()
			defer mu.Unlock()

			counter.Add(1)
			gotTag, gotEntries = tag, entries
		})

		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		c, err := NewClient("echo-service", srv.Addr, Options{
			BatchMaxSize:  100,
			QueueCapacity: 200,
			BatchTimeout:  time.Minute,
			MaxRetries:    1,
			BaseBackoff:   time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})

		assert.NoError(t, err)

		c.Send(Record{"message": "foo"})
		c.Send(Record{"message": "bar"})

		// Wait for the enqueued entries to be pulled into the current batch.
		time.Sleep(50 * time.Millisecond)

		assert.NoError(t, c.Close())

		// Close only guarantees the batch was written to the socket,
		// not that the server goroutine on the other end has receive it yet.
		assert.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()

			return counter.Load() == 1 &&
				gotTag == "echo-service" &&
				len(gotEntries) == 2 &&
				gotEntries[0].Record["message"] == "foo" &&
				gotEntries[1].Record["message"] == "bar"
		}, 200*time.Millisecond, 10*time.Millisecond)
	})
}

func TestClient_Send(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		var mu sync.Mutex
		var gotTag string
		var gotEntries []forwardEntry

		srv, err := newForwardTestServer(func(tag string, entries []forwardEntry) {
			mu.Lock()
			defer mu.Unlock()

			gotTag, gotEntries = tag, entries
		})

		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		c, err := NewClient("echo-service", srv.Addr, Options{
			BatchMaxSize:  10,
			QueueCapacity: 20,
			BatchTimeout:  20 * time.Millisecond,
			MaxRetries:    1,
			BaseBackoff:   time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})

		assert.NoError(t, err)

		defer func() {
			_ = c.Close()
		}()

		now := time.Now()
		c.Send(Record{"message": "hello"})

		// Close only guarantees the batch was written to the socket,
		// not that the server goroutine on the other end has receive it yet.
		assert.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()

			return gotTag == "echo-service" &&
				len(gotEntries) == 1 &&
				gotEntries[0].Time.After(now.Add(-time.Second)) &&
				gotEntries[0].Time.Before(now.Add(time.Second)) &&
				gotEntries[0].Record["message"] == "hello"
		}, 200*time.Millisecond, 10*time.Millisecond)
	})
}

func TestClient_SendAt(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		var mu sync.Mutex
		var gotTag string
		var gotEntries []forwardEntry

		srv, err := newForwardTestServer(func(tag string, entries []forwardEntry) {
			mu.Lock()
			defer mu.Unlock()

			gotTag, gotEntries = tag, entries
		})

		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		c, err := NewClient("echo-service", srv.Addr, Options{
			BatchMaxSize:  10,
			QueueCapacity: 20,
			BatchTimeout:  20 * time.Millisecond,
			MaxRetries:    1,
			BaseBackoff:   time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})

		assert.NoError(t, err)

		defer func() {
			_ = c.Close()
		}()

		now := time.Now()
		c.SendAt(now, Record{"message": "hello"})

		// Close only guarantees the batch was written to the socket,
		// not that the server goroutine on the other end has receive it yet.
		assert.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()

			return gotTag == "echo-service" &&
				len(gotEntries) == 1 &&
				gotEntries[0].Time.Equal(now) &&
				gotEntries[0].Record["message"] == "hello"
		}, 200*time.Millisecond, 10*time.Millisecond)
	})
}

func TestClient_sendEntries(t *testing.T) {
	entries := []entry{
		{
			Time:   eventTime(time.Now()),
			Record: Record{"message": "foo"},
		},
		{
			Time:   eventTime(time.Now().Add(-time.Hour)),
			Record: Record{"message": "bar"},
		},
	}

	t.Run("Success", func(t *testing.T) {
		var mu sync.Mutex
		var gotTag string
		var gotEntries []forwardEntry

		srv, err := newForwardTestServer(func(tag string, entries []forwardEntry) {
			mu.Lock()
			defer mu.Unlock()

			gotTag, gotEntries = tag, entries
		})

		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		c, err := NewClient("echo-service", srv.Addr, Options{
			MaxRetries:  1,
			BaseBackoff: time.Millisecond,
			MaxBackoff:  time.Millisecond,
		})

		assert.NoError(t, err)

		defer func() {
			_ = c.Close()
		}()

		c.sendEntries(entries)

		// Close only guarantees the batch was written to the socket,
		// not that the server goroutine on the other end has receive it yet.
		assert.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()

			return gotTag == "echo-service" &&
				len(gotEntries) == 2 &&
				gotEntries[0].Record["message"] == "foo" &&
				gotEntries[1].Record["message"] == "bar"
		}, 200*time.Millisecond, 10*time.Millisecond)
	})

	t.Run("SuccessOnRetry", func(t *testing.T) {
		var mu sync.Mutex
		var counter atomic.Int64
		var gotTag string
		var gotEntries []forwardEntry

		srv, err := newForwardTestServer(func(tag string, entries []forwardEntry) {
			mu.Lock()
			defer mu.Unlock()

			counter.Add(1)
			gotTag, gotEntries = tag, entries
		})

		assert.NoError(t, err)

		defer func() {
			_ = srv.Close()
		}()

		c, err := NewClient("echo-service", srv.Addr, Options{
			MaxRetries:  2,
			BaseBackoff: time.Millisecond,
			MaxBackoff:  time.Millisecond,
		})

		assert.NoError(t, err)

		defer func() {
			_ = c.Close()
		}()

		// Break the connection without clearing c.conn, so the first write attempt fails.
		assert.NoError(t, c.conn.Close())

		c.sendEntries(entries)

		// Close only guarantees the batch was written to the socket,
		// not that the server goroutine on the other end has receive it yet.
		assert.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()

			return counter.Load() == 1 &&
				gotTag == "echo-service" &&
				len(gotEntries) == 2 &&
				gotEntries[0].Record["message"] == "foo" &&
				gotEntries[1].Record["message"] == "bar"
		}, 200*time.Millisecond, 10*time.Millisecond)
	})

	t.Run("AllRetriesFail", func(t *testing.T) {
		c := &Client{
			endpoint: "127.0.0.1:0",
			opts: Options{
				Timeout:     200 * time.Millisecond,
				MaxRetries:  1,
				BaseBackoff: time.Millisecond,
				MaxBackoff:  time.Millisecond,
			},
		}

		out := captureStderr(t, func() {
			c.sendEntries(entries)
		})

		assert.Contains(t, out, "[forward] error sending entries: error reconnecting: error dialing connection")
	})
}

func TestClient_writeRecord(t *testing.T) {
	tests := []struct {
		name     string
		record   Record
		expected map[string]any
	}{
		{
			name:     "Empty",
			record:   Record{},
			expected: map[string]any{},
		},
		{
			name: "OK",
			record: Record{
				"message": "hello",
				"count":   int64(10),
				"active":  true,
				"nested": map[string]any{
					"key": "value",
				},
			},
			expected: map[string]any{
				"message": "hello",
				"count":   int64(10),
				"active":  true,
				"nested": map[string]any{
					"key": "value",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := msgp.NewWriter(&buf)

			assert.NoError(t, writeRecord(w, tc.record))
			assert.NoError(t, w.Flush())

			r := msgp.NewReader(&buf)
			v, err := r.ReadIntf()
			assert.NoError(t, err)

			assert.Equal(t, tc.expected, v)
		})
	}
}

func TestClient_writeValue(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected any
	}{
		{name: "Nil", value: nil, expected: nil},
		{name: "Bool", value: true, expected: true},
		{name: "Int8", value: int8(-3), expected: int64(-3)},
		{name: "Int16", value: int16(-500), expected: int64(-500)},
		{name: "Int32", value: int32(-100000), expected: int64(-100000)},
		{name: "Int64", value: int64(-3000000000), expected: int64(-3000000000)},
		{name: "Int", value: -4000000000, expected: int64(-4000000000)},
		{name: "Uint8", value: uint8(200), expected: uint64(200)},
		{name: "Uint16", value: uint16(50000), expected: uint64(50000)},
		{name: "Uint32", value: uint32(4000000000), expected: uint64(4000000000)},
		{name: "Uint64", value: uint64(16000000000000000000), expected: uint64(16000000000000000000)},
		{name: "Uint", value: uint(18000000000000000000), expected: uint64(18000000000000000000)},
		{name: "Float32", value: float32(2.71828), expected: float32(2.71828)},
		{name: "Float64", value: float64(3.1415926535), expected: float64(3.1415926535)},
		{name: "Complex64", value: complex64(1 + 2i), expected: complex64(1 + 2i)},
		{name: "Complex128", value: complex128(3 + 4i), expected: complex128(3 + 4i)},
		{name: "String", value: "hello, world!", expected: "hello, world!"},
		{name: "Bytes", value: []byte("Lorem ipsum dolor sit amet"), expected: []byte("Lorem ipsum dolor sit amet")},
		{name: "Duration", value: time.Second, expected: int64(time.Second)}, // WriteDuration encodes the duration as a plain int64
		{name: "Slice", value: []any{"a", 1}, expected: []any{"a", int64(1)}},
		{name: "Map", value: map[string]any{"count": 10}, expected: map[string]any{"count": int64(10)}},
		{name: "Default", value: struct{ X, Y int }{1, 2}, expected: "{1 2}"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := msgp.NewWriter(&buf)

			assert.NoError(t, writeValue(w, tc.value))
			assert.NoError(t, w.Flush())

			r := msgp.NewReader(&buf)
			v, err := r.ReadIntf()
			assert.NoError(t, err)

			assert.Equal(t, tc.expected, v)
		})
	}

	t.Run("Time", func(t *testing.T) {
		now := time.Now()

		var buf bytes.Buffer
		w := msgp.NewWriter(&buf)

		assert.NoError(t, writeValue(w, now))
		assert.NoError(t, w.Flush())

		r := msgp.NewReader(&buf)
		v, err := r.ReadIntf()
		assert.NoError(t, err)

		tm, ok := v.(time.Time)
		assert.True(t, ok)
		assert.True(t, tm.Equal(now))
	})
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

// -------------------------------------------------- Forward Test Server --------------------------------------------------

// forwardTestServer is a test server that mimics a Fluentd Forward endpoint,
// invoking a handler for every Forward message.
type forwardTestServer struct {
	Addr     string
	listener net.Listener
}

// handleForwardMessageFunc is the function type for handling a Forward message.
type handleForwardMessageFunc func(tag string, entries []forwardEntry)

// newForwardTestServer starts a plain TCP forwardTestServer.
func newForwardTestServer(handle handleForwardMessageFunc) (*forwardTestServer, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	s := &forwardTestServer{
		Addr:     l.Addr().String(),
		listener: l,
	}

	go s.acceptConnections(handle)

	return s, nil
}

// newTLSForwardTestServer starts a TLS-wrapped forwardTestServer.
func newTLSForwardTestServer(handle handleForwardMessageFunc) (*forwardTestServer, error) {
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	cert := ts.TLS.Certificates[0]
	ts.Close()

	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	l, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		return nil, err
	}

	s := &forwardTestServer{
		Addr:     l.Addr().String(),
		listener: l,
	}

	go s.acceptConnections(handle)

	return s, nil
}

// acceptConnections accepts connections on server listener until it is closed,
// decoding Forward messages from each and invoking handle for every message received.
func (s *forwardTestServer) acceptConnections(handle handleForwardMessageFunc) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}

		go func() {
			defer func() {
				_ = conn.Close()
			}()

			r := msgp.NewReader(conn)
			for {
				tag, entries, err := decodeForwardMessage(r)
				if err != nil {
					return
				}

				if handle != nil {
					handle(tag, entries)
				}
			}
		}()
	}
}

// Close shuts down the server's listener.
func (s *forwardTestServer) Close() error {
	return s.listener.Close()
}

// forwardEntry is a [time, record] entry within a Forward Mode message.
type forwardEntry struct {
	Time   time.Time
	Record Record
}

// decodeForwardMessage decodes a single Forward Mode payload ([tag, entries, options]).
func decodeForwardMessage(r *msgp.Reader) (string, []forwardEntry, error) {
	n, err := r.ReadArrayHeader()
	if err != nil {
		return "", nil, err
	}

	if n != 3 {
		return "", nil, fmt.Errorf("unexpected outer array length: %d", n)
	}

	tag, err := r.ReadString()
	if err != nil {
		return "", nil, err
	}

	count, err := r.ReadArrayHeader()
	if err != nil {
		return "", nil, err
	}

	entries := make([]forwardEntry, 0, count)

	for range count {
		if _, err := r.ReadArrayHeader(); err != nil {
			return "", nil, err
		}

		var t eventTime
		if err := r.ReadExtension(&t); err != nil {
			return "", nil, err
		}

		v, err := r.ReadIntf()
		if err != nil {
			return "", nil, err
		}

		m, ok := v.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("unexpected record type: %T", v)
		}

		entries = append(entries, forwardEntry{
			Time:   time.Time(t),
			Record: Record(m),
		})
	}

	if _, err := r.ReadMapHeader(); err != nil {
		return "", nil, err
	}

	return tag, entries, nil
}
