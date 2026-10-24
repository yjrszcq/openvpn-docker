// ovpn-launcher is built with CGO_ENABLED=0 and starts only bundled programs.
package main

import (
	"fmt"
	"github.com/yjrszcq/openvpn-docker/internal/bundle"
	"os"
)

func main() {
	executable, err := os.Executable()
	if err == nil {
		var invocation bundle.Invocation
		invocation, err = bundle.Prepare(executable, os.Args[0], os.Args[1:], os.Environ())
		if err == nil {
			err = invocation.Exec()
		}
	}
	fmt.Fprintln(os.Stderr, "ovpn:", err)
	os.Exit(69)
}
