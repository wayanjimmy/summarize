// Package events provides embedded NATS server and event pub/sub.
package events

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// Server wraps an embedded NATS server and its client connection.
type Server struct {
	ns  *server.Server
	nc  *nats.Conn
	log *slog.Logger
}

// Start creates and starts an embedded NATS server, then connects a client.
// port < 0 means in-process only (no TCP listener).
func Start(host string, port int) (*Server, error) {
	log := slog.With("component", "nats")

	opts := &server.Options{
		ServerName: "summarize-nats",
		JetStream:  false,
	}

	if port < 0 {
		opts.DontListen = true
	} else if port > 0 {
		opts.Host = host
		opts.Port = port
	}

	ns, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("nats: create server: %w", err)
	}

	ns.Start()

	if !ns.ReadyForConnections(5 * time.Second) {
		ns.Shutdown()
		return nil, fmt.Errorf("nats: server not ready within 5s")
	}

	clientOpts := []nats.Option{
		nats.Name("summarize-nats-client"),
		nats.MaxReconnects(0),
		nats.Timeout(5 * time.Second),
	}

	var nc *nats.Conn
	if port < 0 {
		nc, err = nats.Connect(nats.DefaultURL,
			append(clientOpts, nats.InProcessServer(ns))...,
		)
	} else {
		nc, err = nats.Connect(ns.ClientURL(), clientOpts...)
	}
	if err != nil {
		ns.Shutdown()
		return nil, fmt.Errorf("nats: connect: %w", err)
	}

	log.Info("Embedded NATS started",
		"in_process", port < 0,
	)

	return &Server{ns: ns, nc: nc, log: log}, nil
}

// Conn returns the NATS client connection.
func (s *Server) Conn() *nats.Conn {
	return s.nc
}

// Shutdown gracefully stops the NATS server and client.
func (s *Server) Shutdown() {
	s.log.Info("Shutting down NATS")
	if s.nc != nil {
		s.nc.Drain()
		s.nc.Close()
	}
	if s.ns != nil {
		s.ns.Shutdown()
	}
}
