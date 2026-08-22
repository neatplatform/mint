package loki

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/golang/snappy"
	"github.com/grafana/loki/pkg/push"
)

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
				MaxLabels:     16,
				MaxMetadata:   16,
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
				MaxLabels:     32,
				MaxMetadata:   32,
				BatchMaxSize:  50,
				QueueCapacity: 100,
				BatchTimeout:  10 * time.Second,
				MaxRetries:    3,
				BaseBackoff:   200 * time.Millisecond,
				MaxBackoff:    30 * time.Second,
			},
			expectedWithDefaults: Options{
				MaxLabels:     32,
				MaxMetadata:   32,
				BatchMaxSize:  50,
				QueueCapacity: 100,
				BatchTimeout:  10 * time.Second,
				MaxRetries:    3,
				BaseBackoff:   200 * time.Millisecond,
				MaxBackoff:    30 * time.Second,
			},
		},
		{
			name: "MaxBackoffLessThanBaseBackoff",
			opts: Options{
				BaseBackoff: time.Minute,
				MaxBackoff:  time.Second,
			},
			expectedWithDefaults: Options{
				MaxLabels:     16,
				MaxMetadata:   16,
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
	t.Run("WithDefaults", func(t *testing.T) {
		opts := Options{}
		expectedOpts := Options{}.withDefaults()

		c := NewClient("internal", "http://localhost:3100/loki/api/v1/push", opts)
		defer c.Close()

		assert.NotNil(t, c)
		assert.Equal(t, "internal", c.tenant)
		assert.Equal(t, "http://localhost:3100/loki/api/v1/push", c.endpoint)
		assert.Equal(t, expectedOpts, c.opts)
		assert.NotNil(t, c.client)
		assert.NotNil(t, c.queue)

		transport, ok := c.client.Transport.(*http.Transport)
		assert.True(t, ok)
		assert.Nil(t, transport.TLSClientConfig)
	})

	t.Run("WithOptions", func(t *testing.T) {
		opts := Options{
			MaxLabels:     32,
			MaxMetadata:   32,
			BatchMaxSize:  50,
			QueueCapacity: 100,
			BatchTimeout:  10 * time.Second,
			MaxRetries:    3,
			BaseBackoff:   200 * time.Millisecond,
			MaxBackoff:    30 * time.Second,
		}

		c := NewClient("internal", "http://localhost:3100/loki/api/v1/push", opts)
		defer c.Close()

		assert.NotNil(t, c)
		assert.Equal(t, "internal", c.tenant)
		assert.Equal(t, "http://localhost:3100/loki/api/v1/push", c.endpoint)
		assert.Equal(t, opts, c.opts)
		assert.NotNil(t, c.client)
		assert.NotNil(t, c.queue)

		transport, ok := c.client.Transport.(*http.Transport)
		assert.True(t, ok)
		assert.Nil(t, transport.TLSClientConfig)
	})

	t.Run("WithTLS", func(t *testing.T) {
		opts := Options{
			TLSEnabled: true,
			TLSConfig: &tls.Config{
				ServerName: "loki.example.com",
			},
		}

		c := NewClient("internal", "https://loki.example.com/loki/api/v1/push", opts)
		defer c.Close()

		assert.NotNil(t, c)
		assert.Equal(t, "internal", c.tenant)
		assert.Equal(t, "https://loki.example.com/loki/api/v1/push", c.endpoint)
		assert.NotNil(t, c.client)
		assert.NotNil(t, c.queue)

		transport, ok := c.client.Transport.(*http.Transport)
		assert.True(t, ok)
		assert.Same(t, opts.TLSConfig, transport.TLSClientConfig)
		assert.NotZero(t, transport.TLSHandshakeTimeout)
	})
}

func TestClient_Flush(t *testing.T) {
	tenant := "internal"

	t.Run("OK", func(t *testing.T) {
		var counter atomic.Int64

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer s.Close()

		c := NewClient(tenant, s.URL, Options{
			BatchMaxSize:  100,
			QueueCapacity: 200,
			BatchTimeout:  time.Minute,
			MaxRetries:    1,
			BaseBackoff:   time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})
		defer c.Close()

		c.Send("foo", []Pair{{"name", "echo-service"}}, nil)
		c.Send("bar", []Pair{{"name", "echo-service"}}, nil)

		// Wait for the enqueued streams to be pulled into the current batch.
		time.Sleep(50 * time.Millisecond)

		assert.Zero(t, counter.Load())

		c.Flush()

		assert.Eventually(t, func() bool {
			return counter.Load() == 1
		}, 100*time.Millisecond, 10*time.Millisecond)
	})
}

func TestClient_Close(t *testing.T) {
	tenant := "internal"

	t.Run("Idempotent", func(t *testing.T) {
		c := NewClient(tenant, "http://localhost:3100/loki/api/v1/push", Options{})

		c.Close()
		c.Close()
		c.Close()
	})

	t.Run("DropsSendsAfterClose", func(t *testing.T) {
		c := NewClient(tenant, "http://localhost:3100/loki/api/v1/push", Options{})

		c.Close()
		assert.Zero(t, c.queue.Dropped())

		c.Send("foo", []Pair{{"name", "echo-service"}}, nil)
		c.Send("bar", []Pair{{"name", "echo-service"}}, nil)
		assert.Equal(t, 2, c.queue.Dropped())
	})

	t.Run("BlocksUntilQueueFlushed", func(t *testing.T) {
		expectedStreams := []*push.Stream{
			{
				Labels: `{name="echo-service"}`,
				Entries: []push.Entry{
					{
						Line: "foo",
					},
				},
			},
			{
				Labels: `{name="echo-service"}`,
				Entries: []push.Entry{
					{
						Line: "bar",
					},
				},
			},
		}

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertServerRequest(t, r, tenant, expectedStreams)
			w.WriteHeader(http.StatusOK)
		}))
		defer s.Close()

		c := NewClient(tenant, s.URL, Options{
			BatchMaxSize:  100,
			QueueCapacity: 200,
			BatchTimeout:  time.Minute,
			MaxRetries:    1,
			BaseBackoff:   time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})

		c.Send("foo", []Pair{{"name", "echo-service"}}, nil)
		c.Send("bar", []Pair{{"name", "echo-service"}}, nil)

		// Wait for the enqueued streams to be pulled into the current batch.
		time.Sleep(50 * time.Millisecond)

		c.Close()
	})
}

func TestClient_Send(t *testing.T) {
	tenant := "internal"

	t.Run("NoLabels", func(t *testing.T) {
		c := NewClient(tenant, "http://localhost:3100/loki/api/v1/push", Options{})
		defer c.Close()

		out := captureStderr(t, func() {
			c.Send("foo bar", nil, nil)
		})

		assert.Contains(t, out, "[loki] invalid labels: at least one label is required")
		assert.Zero(t, c.queue.Len())
	})

	t.Run("TooManyLabels", func(t *testing.T) {
		c := NewClient(tenant, "http://localhost:3100/loki/api/v1/push", Options{
			MaxLabels: 2,
		})
		defer c.Close()

		out := captureStderr(t, func() {
			c.Send("foo bar",
				[]Pair{
					{"name", "echo-service"},
					{"environment", "test"},
					{"region", "local"},
				},
				nil,
			)
		})

		assert.Contains(t, out, "[loki] invalid labels: the maximum number of labels is 2")
		assert.Zero(t, c.queue.Len())
	})

	t.Run("TooManyMetadata", func(t *testing.T) {
		c := NewClient(tenant, "http://localhost:3100/loki/api/v1/push", Options{
			MaxMetadata: 2,
		})
		defer c.Close()

		out := captureStderr(t, func() {
			c.Send("foo bar",
				[]Pair{
					{"name", "echo-service"},
					{"environment", "test"},
					{"region", "local"},
				},
				[]Pair{
					{Name: "traceID", Value: "2e775a16-1039-4969-aedb-e17a647f5f6c"},
					{Name: "requestID", Value: "81861a5a-0c90-4b5d-bfec-9d5ac1061023"},
					{Name: "userID", Value: "acc3acc1-cee1-4b67-bfc2-baa9e30644d5"},
				},
			)
		})

		assert.Contains(t, out, "[loki] invalid metadata: the maximum number of metadata is 2")
		assert.Zero(t, c.queue.Len())
	})

	t.Run("Success", func(t *testing.T) {
		expectedStreams := []*push.Stream{
			{
				Labels: `{name="echo-service",environment="test",region="local"}`,
				Entries: []push.Entry{
					{
						Line: "ready",
					},
				},
			},
			{
				Labels: `{name="echo-service",environment="test",region="local",method="GetUser"}`,
				Entries: []push.Entry{
					{
						Line: "request handled",
						StructuredMetadata: push.LabelsAdapter{
							{Name: "traceID", Value: "8bb2adaa-6fa6-4c6f-adf2-d022b1dd9995"},
							{Name: "requestID", Value: "ec9d9742-b9d6-4f7b-94bf-19b9e3c06b5f"},
						},
					},
				},
			},
		}

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertServerRequest(t, r, tenant, expectedStreams)
			w.WriteHeader(http.StatusOK)
		}))
		defer s.Close()

		c := NewClient(tenant, s.URL, Options{
			BatchMaxSize:  2,
			QueueCapacity: 4,
			BatchTimeout:  time.Second,
			MaxRetries:    1,
			BaseBackoff:   time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})
		defer c.Close()

		c.Send("ready",
			[]Pair{
				{"name", "echo-service"},
				{"environment", "test"},
				{"region", "local"},
			},
			nil,
		)

		c.Send("request handled",
			[]Pair{
				{"name", "echo-service"},
				{"environment", "test"},
				{"region", "local"},
				{"method", "GetUser"},
			},
			[]Pair{
				{Name: "traceID", Value: "8bb2adaa-6fa6-4c6f-adf2-d022b1dd9995"},
				{Name: "requestID", Value: "ec9d9742-b9d6-4f7b-94bf-19b9e3c06b5f"},
			},
		)
	})
}

func TestClient_SendAt(t *testing.T) {
	tenant := "internal"

	t.Run("NoLabels", func(t *testing.T) {
		c := NewClient(tenant, "http://localhost:3100/loki/api/v1/push", Options{})
		defer c.Close()

		out := captureStderr(t, func() {
			c.SendAt(time.Now(), "foo bar", nil, nil)
		})

		assert.Contains(t, out, "[loki] invalid labels: at least one label is required")
		assert.Zero(t, c.queue.Len())
	})

	t.Run("TooManyLabels", func(t *testing.T) {
		c := NewClient(tenant, "http://localhost:3100/loki/api/v1/push", Options{
			MaxLabels: 2,
		})
		defer c.Close()

		out := captureStderr(t, func() {
			c.SendAt(time.Now(), "foo bar",
				[]Pair{
					{"name", "echo-service"},
					{"environment", "test"},
					{"region", "local"},
				},
				nil,
			)
		})

		assert.Contains(t, out, "[loki] invalid labels: the maximum number of labels is 2")
		assert.Zero(t, c.queue.Len())
	})

	t.Run("TooManyMetadata", func(t *testing.T) {
		c := NewClient(tenant, "http://localhost:3100/loki/api/v1/push", Options{
			MaxMetadata: 2,
		})
		defer c.Close()

		out := captureStderr(t, func() {
			c.SendAt(time.Now(), "foo bar",
				[]Pair{
					{"name", "echo-service"},
					{"environment", "test"},
					{"region", "local"},
				},
				[]Pair{
					{Name: "traceID", Value: "2e775a16-1039-4969-aedb-e17a647f5f6c"},
					{Name: "requestID", Value: "81861a5a-0c90-4b5d-bfec-9d5ac1061023"},
					{Name: "userID", Value: "acc3acc1-cee1-4b67-bfc2-baa9e30644d5"},
				},
			)
		})

		assert.Contains(t, out, "[loki] invalid metadata: the maximum number of metadata is 2")
		assert.Zero(t, c.queue.Len())
	})

	t.Run("Success", func(t *testing.T) {
		expectedStreams := []*push.Stream{
			{
				Labels: `{name="echo-service",environment="test",region="local"}`,
				Entries: []push.Entry{
					{
						Line: "ready",
					},
				},
			},
			{
				Labels: `{name="echo-service",environment="test",region="local",method="GetUser"}`,
				Entries: []push.Entry{
					{
						Line: "request handled",
						StructuredMetadata: push.LabelsAdapter{
							{Name: "traceID", Value: "8bb2adaa-6fa6-4c6f-adf2-d022b1dd9995"},
							{Name: "requestID", Value: "ec9d9742-b9d6-4f7b-94bf-19b9e3c06b5f"},
						},
					},
				},
			},
		}

		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertServerRequest(t, r, tenant, expectedStreams)
			w.WriteHeader(http.StatusOK)
		}))
		defer s.Close()

		c := NewClient(tenant, s.URL, Options{
			BatchMaxSize:  2,
			QueueCapacity: 4,
			BatchTimeout:  time.Second,
			MaxRetries:    1,
			BaseBackoff:   time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})
		defer c.Close()

		c.SendAt(time.Now(), "ready",
			[]Pair{
				{"name", "echo-service"},
				{"environment", "test"},
				{"region", "local"},
			},
			nil,
		)

		c.SendAt(time.Now(), "request handled",
			[]Pair{
				{"name", "echo-service"},
				{"environment", "test"},
				{"region", "local"},
				{"method", "GetUser"},
			},
			[]Pair{
				{Name: "traceID", Value: "8bb2adaa-6fa6-4c6f-adf2-d022b1dd9995"},
				{Name: "requestID", Value: "ec9d9742-b9d6-4f7b-94bf-19b9e3c06b5f"},
			},
		)
	})
}

func TestClient_buildStream(t *testing.T) {
	tests := []struct {
		name             string
		t                time.Time
		line             string
		labels           []Pair
		metadata         []Pair
		expectedLabels   string
		expectedMetadata push.LabelsAdapter
	}{
		{
			name: "WithSingleLabel",
			t:    time.Now(),
			line: "Hello, World!",
			labels: []Pair{
				{"name", "echo-service"},
			},
			metadata:         nil,
			expectedLabels:   `{name="echo-service"}`,
			expectedMetadata: push.LabelsAdapter{},
		},
		{
			name: "WithMultipleLabels",
			t:    time.Now(),
			line: "Hello, World!",
			labels: []Pair{
				{"name", "echo-service"},
				{"environment", "test"},
				{"region", "local"},
			},
			metadata:         nil,
			expectedLabels:   `{name="echo-service",environment="test",region="local"}`,
			expectedMetadata: push.LabelsAdapter{},
		},
		{
			name: "WithMetadata",
			t:    time.Now(),
			line: "Hello, World!",
			labels: []Pair{
				{"name", "echo-service"},
				{"environment", "test"},
				{"region", "local"},
			},
			metadata: []Pair{
				{"traceID", "2e775a16-1039-4969-aedb-e17a647f5f6c"},
				{"requestID", "81861a5a-0c90-4b5d-bfec-9d5ac1061023"},
			},
			expectedLabels: `{name="echo-service",environment="test",region="local"}`,
			expectedMetadata: push.LabelsAdapter{
				{Name: "traceID", Value: "2e775a16-1039-4969-aedb-e17a647f5f6c"},
				{Name: "requestID", Value: "81861a5a-0c90-4b5d-bfec-9d5ac1061023"},
			},
		},
		{
			name: "LabelValueHasQuotes",
			t:    time.Now(),
			line: "Hello, World!",
			labels: []Pair{
				{"name", "echo-service"},
				{"environment", "test"},
				{"region", "local"},
				{"partition", `"test-local-01"`},
			},
			metadata:         nil,
			expectedLabels:   `{name="echo-service",environment="test",region="local",partition="\"test-local-01\""}`,
			expectedMetadata: push.LabelsAdapter{},
		},
	}

	c := NewClient("internal", "http://localhost:3100/loki/api/v1/push", Options{})
	defer c.Close()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := c.buildStream(tc.t, tc.line, tc.labels, tc.metadata)

			assert.NotNil(t, s)
			assert.Equal(t, tc.expectedLabels, s.Labels)
			assert.Len(t, s.Entries, 1)
			assert.True(t, tc.t.Equal(s.Entries[0].Timestamp))
			assert.Equal(t, tc.line, s.Entries[0].Line)
			assert.Equal(t, tc.expectedMetadata, s.Entries[0].StructuredMetadata)

			// Return the stream to the pool as sendStreams would,
			// so the next test can exercise reuse.
			c.stmPool.Put(s)
		})
	}
}

func TestClient_sendStreams(t *testing.T) {
	tenant := "internal"
	batch := []*push.Stream{
		{
			Labels: `{name="echo-service",environment="test",region="local"}`,
			Entries: []push.Entry{
				{
					Timestamp: time.Now(),
					Line:      "ready",
				},
			},
		},
		{
			Labels: `{name="echo-service",environment="test",region="local",method="GetUser"}`,
			Entries: []push.Entry{
				{
					Timestamp: time.Now(),
					Line:      "request handled",
					StructuredMetadata: push.LabelsAdapter{
						{Name: "traceID", Value: "e335dc42-8fbc-4bea-b108-bd35034e94ae"},
						{Name: "requestID", Value: "4d4ab9ce-c100-4fc4-92d2-4e950cea5e61"},
					},
				},
			},
		},
	}

	t.Run("Success_WithoutTenant", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertServerRequest(t, r, "", batch)
			w.WriteHeader(http.StatusOK)
		}))
		defer s.Close()

		c := NewClient("", s.URL, Options{
			MaxRetries:  1,
			BaseBackoff: time.Millisecond,
			MaxBackoff:  time.Millisecond,
		})
		defer c.Close()

		c.sendStreams(batch)
	})

	t.Run("Success_WithTenant", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertServerRequest(t, r, tenant, batch)
			w.WriteHeader(http.StatusOK)
		}))
		defer s.Close()

		c := NewClient(tenant, s.URL, Options{
			MaxRetries:  1,
			BaseBackoff: time.Millisecond,
			MaxBackoff:  time.Millisecond,
		})
		defer c.Close()

		c.sendStreams(batch)
	})

	t.Run("Failure_InvalidEndpoint", func(t *testing.T) {
		c := NewClient(tenant, "http://\x7f", Options{
			MaxRetries:  1,
			BaseBackoff: time.Millisecond,
			MaxBackoff:  time.Millisecond,
		})
		defer c.Close()

		out := captureStderr(t, func() {
			c.sendStreams(batch)
		})

		assert.Contains(t, out, "[loki] error creating http request: parse")
	})

	t.Run("Failure_ServerNotResponding", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertServerRequest(t, r, tenant, batch)
			w.WriteHeader(http.StatusOK)
		}))
		s.Close()

		client := NewClient(tenant, s.URL, Options{
			MaxRetries:  1,
			BaseBackoff: time.Millisecond,
			MaxBackoff:  time.Millisecond,
		})
		defer client.Close()

		out := captureStderr(t, func() {
			client.sendStreams(batch)
		})

		assert.Contains(t, out, "[loki] error sending http request: Post")
	})

	t.Run("Failure_ServerRespondingWithError", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assertServerRequest(t, r, tenant, batch)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid request"))
		}))
		defer s.Close()

		c := NewClient(tenant, s.URL, Options{
			MaxRetries:  1,
			BaseBackoff: time.Millisecond,
			MaxBackoff:  time.Millisecond,
		})
		defer c.Close()

		out := captureStderr(t, func() {
			c.sendStreams(batch)
		})

		assert.Contains(t, out, "[loki] unexpected http status: [400] invalid request")
	})
}

// assertServerRequest asserts headers and decodes the snappy-compressed protobuf body
func assertServerRequest(t *testing.T, r *http.Request, expectedTenant string, expectedStreams []*push.Stream) {
	t.Helper()

	assert.Equal(t, http.MethodPost, r.Method)
	assert.Equal(t, "snappy", r.Header.Get("Content-Encoding"))
	assert.Equal(t, "application/x-protobuf", r.Header.Get("Content-Type"))
	assert.Equal(t, expectedTenant, r.Header.Get("X-Scope-OrgID"))

	b, err := io.ReadAll(r.Body)
	assert.NoError(t, err)
	assert.NoError(t, r.Body.Close())

	bb, err := snappy.Decode(nil, b)
	assert.NoError(t, err)

	var req push.PushRequest
	assert.NoError(t, req.Unmarshal(bb))

	assert.Len(t, req.Streams, len(expectedStreams))
	for i := range req.Streams {
		assert.Equal(t, expectedStreams[i].Labels, req.Streams[i].Labels)
		assert.Len(t, req.Streams[i].Entries, len(expectedStreams[i].Entries))
		for j := range req.Streams[i].Entries {
			assert.Equal(t, req.Streams[i].Entries[j].Line, expectedStreams[i].Entries[j].Line)
			assert.Equal(t, req.Streams[i].Entries[j].StructuredMetadata, expectedStreams[i].Entries[j].StructuredMetadata)
		}
	}
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
