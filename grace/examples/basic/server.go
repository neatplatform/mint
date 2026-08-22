package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

// Server is a simple http server implementing grace.Server interface.
type Server struct {
	name string
	*http.Server
}

func NewServer(name string, port uint16) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprintln(w, "Hello, World!")
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	// RegisterOnShutdown cancels the base context when the server begins shutting down.
	//
	// This is necessary to signal long-lived connections that bypass the normal shutdown path,
	// specifically those that have been hijacked (e.g. WebSocket) or upgraded via ALPN (e.g. HTTP/2).
	// Unlike regular connections, these are not closed automatically by Shutdown;
	// their handlers must observe context cancellation and exit on their own.
	server.RegisterOnShutdown(cancel)

	return &Server{
		name:   name,
		Server: server,
	}
}

func (s *Server) String() string {
	return s.name
}
