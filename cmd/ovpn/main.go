package main

import (
	"os"
	"path/filepath"

	"github.com/yjrszcq/openvpn-docker/internal/cli"
)

func main() {
	if filepath.Base(os.Args[0]) == "docker-entrypoint" {
		os.Exit(cli.RunEntrypoint(os.Args[1:], os.Stdout, os.Stderr))
	}
	if filepath.Base(os.Args[0]) == "ovpn-hook" {
		os.Exit(cli.RunHook(os.Args[1:], os.Stderr))
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
