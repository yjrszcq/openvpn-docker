package httpapi

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNewServerUsesFixedSecurityLimits(t *testing.T) {
	server, err := NewServer("127.0.0.1:11940", http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	if server.ReadHeaderTimeout != readHeaderTimeout || server.ReadTimeout != readTimeout || server.WriteTimeout != writeTimeout || server.IdleTimeout != idleTimeout || server.MaxHeaderBytes != maxHeaderBytes {
		t.Fatalf("unexpected server limits: %+v", server)
	}
}

func TestServeStopsCleanlyWithContext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(listener.Addr().String(), http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listener, server) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
}
