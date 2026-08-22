package grace

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSetLogger(t *testing.T) {
	l := new(voidLogger)
	SetLogger(l)

	assert.Equal(t, l, logger)
}

func TestRegisterClient(t *testing.T) {
	c1 := new(MockClient)
	c2 := new(MockClient)
	RegisterClient(c1, c2)

	assert.Equal(t, []Client{c1, c2}, clients)
}

func TestRegisterServer(t *testing.T) {
	s1 := new(MockServer)
	s2 := new(MockServer)
	RegisterServer(s1, s2)

	assert.Equal(t, []Server{s1, s2}, servers)
}

func TestSetMaxRetry(t *testing.T) {
	SetMaxRetry(10)

	assert.Equal(t, 10, maxRetry)
}

func TestSetGracePeriod(t *testing.T) {
	d := 10 * time.Second
	SetGracePeriod(d)

	assert.Equal(t, d, gracePeriod)
}

func TestConnectWithRetry(t *testing.T) {
	tests := []struct {
		name          string
		maxRetry      int
		client        Client
		retries       int
		expectedError error
	}{
		{
			name:     "Successful",
			maxRetry: 2,
			client: &MockClient{
				ConnectMocks: []MockClient_ConnectMock{
					{OutError: nil},
				},
			},
			retries:       1,
			expectedError: nil,
		},
		{
			name:     "NoRetryLeft",
			maxRetry: 2,
			client: &MockClient{
				ConnectMocks: []MockClient_ConnectMock{
					{OutError: errors.New("failed to connect")},
				},
			},
			retries:       0,
			expectedError: errors.New("failed to connect"),
		},
		{
			name:     "SuccessfulAfterRetury",
			maxRetry: 2,
			client: &MockClient{
				ConnectMocks: []MockClient_ConnectMock{
					{OutError: errors.New("failed to connect")},
					{OutError: errors.New("failed to connect")},
					{OutError: nil},
				},
			},
			retries:       2,
			expectedError: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger = new(voidLogger)
			maxRetry = tc.maxRetry

			err := connectWithRetry(tc.client, tc.retries)

			assert.Equal(t, tc.expectedError, err)
		})
	}
}

func TestTerminateGracefully(t *testing.T) {
	tests := []struct {
		name          string
		clients       []Client
		servers       []Server
		termServers   bool
		expectedError error
	}{
		{
			name: "ClientError",
			clients: []Client{
				&MockClient{
					DisconnectMocks: []MockClient_DisconnectMock{
						{OutError: errors.New("failed to disconnect")},
					},
				},
			},
			servers: []Server{
				&MockServer{
					ShutdownMocks: []MockServer_ShutdownMock{
						{OutError: nil},
					},
				},
			},
			termServers:   true,
			expectedError: errors.New("failed to disconnect"),
		},
		{
			name: "ServerError",
			clients: []Client{
				&MockClient{
					DisconnectMocks: []MockClient_DisconnectMock{
						{OutError: nil},
					},
				},
			},
			servers: []Server{
				&MockServer{
					ShutdownMocks: []MockServer_ShutdownMock{
						{OutError: errors.New("failed to shutdown")},
					},
				},
			},
			termServers:   true,
			expectedError: errors.New("failed to shutdown"),
		},
		{
			name: "ClientAndServerSuccessful",
			clients: []Client{
				&MockClient{
					DisconnectMocks: []MockClient_DisconnectMock{
						{OutError: nil},
					},
				},
			},
			servers: []Server{
				&MockServer{
					ShutdownMocks: []MockServer_ShutdownMock{
						{OutError: nil},
					},
				},
			},
			termServers:   true,
			expectedError: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger = new(voidLogger)
			clients = tc.clients
			servers = tc.servers
			maxRetry = 1
			gracePeriod = time.Second

			err := terminateGracefully(tc.termServers)

			assert.Equal(t, tc.expectedError, err)
		})
	}
}

func TestStartAndWait(t *testing.T) {
	tests := []struct {
		name         string
		clients      []Client
		servers      []Server
		signal       os.Signal
		expectedCode int
	}{
		{
			name: "ClientFailsToConnect",
			clients: []Client{
				&MockClient{
					ConnectMocks: []MockClient_ConnectMock{
						{OutError: errors.New("failed to connect")},
					},
					DisconnectMocks: []MockClient_DisconnectMock{
						{OutError: nil},
					},
				},
			},
			servers:      []Server{},
			expectedCode: errorCode,
		},
		{
			name: "ServerFailsToListen",
			clients: []Client{
				&MockClient{
					ConnectMocks: []MockClient_ConnectMock{
						{OutError: nil},
					},
					DisconnectMocks: []MockClient_DisconnectMock{
						{OutError: nil},
					},
				},
			},
			servers: []Server{
				&MockServer{
					ListenAndServeMocks: []MockServer_ListenAndServeMock{
						{OutError: errors.New("failed to listen")},
					},
					ShutdownMocks: []MockServer_ShutdownMock{
						{OutError: nil},
					},
				},
			},
			expectedCode: errorCode,
		},
		{
			name: "SuccessfulGracefulTermination",
			clients: []Client{
				&MockClient{
					ConnectMocks: []MockClient_ConnectMock{
						{OutError: nil},
					},
					DisconnectMocks: []MockClient_DisconnectMock{
						{OutError: nil},
					},
				},
			},
			servers: []Server{
				&MockServer{
					ListenAndServeMocks: []MockServer_ListenAndServeMock{
						{OutError: nil},
					},
					ShutdownMocks: []MockServer_ShutdownMock{
						{OutError: nil},
					},
				},
			},
			signal:       syscall.SIGTERM,
			expectedCode: successCode,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger = new(voidLogger)
			clients = tc.clients
			servers = tc.servers
			maxRetry = 0
			gracePeriod = time.Second

			// Get current process
			proc, err := os.FindProcess(os.Getpid())
			assert.NoError(t, err)

			if tc.signal != nil {
				go func() {
					time.Sleep(50 * time.Millisecond)
					_ = proc.Signal(tc.signal)
				}()
			}

			code := StartAndWait()

			assert.Equal(t, tc.expectedCode, code)
		})
	}
}

// -------------------------------------------------- Mock Types --------------------------------------------------

type (
	MockClient struct {
		StringOutString string

		ConnectIndex int
		ConnectMocks []MockClient_ConnectMock

		DisconnectIndex int
		DisconnectMocks []MockClient_DisconnectMock
	}

	MockClient_ConnectMock struct {
		OutError error
	}

	MockClient_DisconnectMock struct {
		InCtx    context.Context
		OutError error
	}
)

func (m *MockClient) String() string {
	return m.StringOutString
}

func (m *MockClient) Connect() error {
	if m.ConnectIndex >= len(m.ConnectMocks) {
		panic("Connect called more times than expected")
	}

	i := m.ConnectIndex
	m.ConnectIndex++

	return m.ConnectMocks[i].OutError
}

func (m *MockClient) Disconnect(ctx context.Context) error {
	if m.DisconnectIndex >= len(m.DisconnectMocks) {
		panic("Disconnect called more times than expected")
	}

	i := m.DisconnectIndex
	m.DisconnectIndex++

	m.DisconnectMocks[i].InCtx = ctx

	return m.DisconnectMocks[i].OutError
}

type (
	MockServer struct {
		StringOutString string

		ListenAndServeIndex int
		ListenAndServeMocks []MockServer_ListenAndServeMock

		ShutdownIndex int
		ShutdownMocks []MockServer_ShutdownMock
	}

	MockServer_ListenAndServeMock struct {
		OutError error
	}

	MockServer_ShutdownMock struct {
		InCtx    context.Context
		OutError error
	}
)

func (m *MockServer) String() string {
	return m.StringOutString
}

func (m *MockServer) ListenAndServe() error {
	if m.ListenAndServeIndex >= len(m.ListenAndServeMocks) {
		panic("ListenAndServe called more times than expected")
	}

	i := m.ListenAndServeIndex
	m.ListenAndServeIndex++

	return m.ListenAndServeMocks[i].OutError
}

func (m *MockServer) Shutdown(ctx context.Context) error {
	if m.ShutdownIndex >= len(m.ShutdownMocks) {
		panic("Shutdown called more times than expected")
	}

	i := m.ShutdownIndex
	m.ShutdownIndex++

	m.ShutdownMocks[i].InCtx = ctx

	return m.ShutdownMocks[i].OutError
}
