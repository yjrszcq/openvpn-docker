package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	"github.com/yjrszcq/openvpn-docker/internal/httpapi"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func main() {
	if err := run(); err != nil {
		log.Printf("ovpn-api: %v", err)
		os.Exit(1)
	}
}

func run() error {
	listenAddress := os.Getenv("OVPN_API_LISTEN")
	if listenAddress == "" {
		return fmt.Errorf("OVPN_API_LISTEN is required")
	}
	dataDir := os.Getenv("OVPN_DATA_DIR")
	if dataDir == "" {
		dataDir = initialize.DefaultDataDir
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	database, err := storesqlite.Open(ctx, filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		return fmt.Errorf("open authoritative state: %w", err)
	}
	defer database.Close()
	instance, err := database.LoadOnlyInstance(ctx)
	if err != nil {
		return fmt.Errorf("load instance: %w", err)
	}
	authenticator, err := apikey.NewService(database, instance.ID)
	if err != nil {
		return err
	}
	origins, err := parseOrigins(os.Getenv("OVPN_API_CORS_ORIGINS"))
	if err != nil {
		return err
	}
	handler, err := httpapi.NewHandler(authenticator, origins)
	if err != nil {
		return err
	}
	server, err := httpapi.NewServer(listenAddress, handler)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen on API address: %w", err)
	}
	log.Printf("ovpn-api: listening on %s", listener.Addr())
	return httpapi.Serve(ctx, listener, server)
}

func parseOrigins(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" {
			return nil, fmt.Errorf("OVPN_API_CORS_ORIGINS contains an empty origin")
		}
	}
	return parts, nil
}
