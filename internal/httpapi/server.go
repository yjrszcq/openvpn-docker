package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 10 * time.Second
	maxHeaderBytes    = 16 << 10
)

// NewServer applies fixed transport limits to the API handler.
func NewServer(address string, apiHandler http.Handler) (*http.Server, error) {
	if address == "" || apiHandler == nil {
		return nil, errors.New("API listen address and handler are required")
	}
	return &http.Server{
		Addr:              address,
		Handler:           apiHandler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}, nil
}

// Serve accepts requests until the context is cancelled or serving fails.
func Serve(ctx context.Context, listener net.Listener, server *http.Server) error {
	if ctx == nil || listener == nil || server == nil {
		return errors.New("API context, listener, and server are required")
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(listener)
	}()
	select {
	case err := <-serveDone:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-serveDone
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
