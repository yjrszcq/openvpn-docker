package main

import (
	"os"

	"github.com/yjrszcq/openvpn-docker/internal/cli"
)

func main() {
	os.Exit(cli.RunBroker(os.Args[1:], os.Stdout, os.Stderr))
}
