package telemetry

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
)

// TestTCPServer is a test TCP server that accepts connections and discards any data written to them.
type TestTCPServer struct {
	Addr     string // Addr is the address the server is listening on.
	listener net.Listener
}

// newTestTCPServer starts a TestTCPServer listening on a plaintext TCP socket.
func newTestTCPServer() (*TestTCPServer, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	s := &TestTCPServer{
		Addr:     l.Addr().String(),
		listener: l,
	}

	go s.acceptConnections()

	return s, nil
}

// newTestTLSTCPServer starts a TestTCPServer listening on a TLS socket. It
// reuses the certificate from a throwaway httptest.NewTLSServer rather than
// generating one directly, since httptest already knows how to mint a valid
// self-signed cert.
func newTestTLSTCPServer() (*TestTCPServer, error) {
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

	s := &TestTCPServer{
		Addr:     l.Addr().String(),
		listener: l,
	}

	go s.acceptConnections()

	return s, nil
}

// acceptConnections accepts connections on the listener until it is closed, discarding any data received.
// It returns once Accept starts erroring, which is how a closed listener signals shutdown.
func (s *TestTCPServer) acceptConnections() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}

		go func() {
			_, _ = io.Copy(io.Discard, conn)
			_ = conn.Close()
		}()
	}
}

// Close shuts down the server's listener.
func (s *TestTCPServer) Close() error {
	return s.listener.Close()
}
