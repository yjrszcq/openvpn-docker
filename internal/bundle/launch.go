// Package bundle configures the relocatable, self-contained Linux distribution.
package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Invocation is an exec-ready bundle process. The launcher itself has no CGO.
type Invocation struct {
	Path      string
	Args, Env []string
}

// Prepare resolves the launcher through symlinks, independent of the caller's cwd.
func Prepare(executable, invoked string, args, environment []string) (Invocation, error) {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return Invocation{}, err
	}
	root := filepath.Dir(resolved)
	if _, err := os.Stat(filepath.Join(root, "runtime", "manifest.json")); err != nil {
		return Invocation{}, fmt.Errorf("bundle runtime is missing: %w", err)
	}
	env := map[string]string{}
	for _, value := range environment {
		if k, v, ok := strings.Cut(value, "="); ok {
			env[k] = v
		}
	}
	defaults := map[string]string{
		"OVPN_BUNDLE_ROOT": root, "OVPN_DATA_DIR": filepath.Join(root, "data"), "OVPN_RUNTIME_DIR": filepath.Join(root, "run"),
		"OVPN_CONFIG_FILE": filepath.Join(root, "config", "config.yaml"), "OVPN_COMPATIBILITY_FILE": filepath.Join(root, "runtime", "share", "compatibility", "contract.json"),
		"OVPN_TEMPLATE_ROOT": filepath.Join(root, "runtime", "share", "templates"), "OVPN_OPENVPN_BIN": filepath.Join(root, "bin", "openvpn"),
		"OVPN_BROKER_BIN": filepath.Join(root, "bin", "ovpn-broker"), "OVPN_API_BIN": filepath.Join(root, "bin", "ovpn-api"),
		"OVPN_HOOK_BIN": filepath.Join(root, "bin", "ovpn-hook"), "OVPN_EASYRSA_BIN": filepath.Join(root, "bin", "easyrsa"),
		"OVPN_IP_BIN": filepath.Join(root, "bin", "ip"), "OVPN_IPTABLES_BIN": filepath.Join(root, "bin", "iptables"),
		"EASYRSA": filepath.Join(root, "runtime", "share", "easy-rsa"), "EASYRSA_OPENSSL": filepath.Join(root, "bin", "openssl"),
		"OPENSSL_MODULES": filepath.Join(root, "runtime", "lib", "ossl-modules"), "OPENSSL_CONF": filepath.Join(root, "runtime", "etc", "ssl", "openssl.cnf"),
		"SSL_CERT_FILE": filepath.Join(root, "runtime", "etc", "ssl", "cert.pem"), "XTABLES_LIBDIR": filepath.Join(root, "runtime", "lib", "xtables"),
	}
	for k, v := range defaults {
		if env[k] == "" {
			env[k] = v
		}
	}
	delete(env, "OVPN_EXEC_MODE")
	env["PATH"] = filepath.Join(root, "bin") + ":" + env["PATH"]
	// Never export LD_LIBRARY_PATH into the host environment. Each dynamic program
	// is executed through the bundled musl loader with its own search path.
	name := filepath.Base(invoked)
	binary := filepath.Join(root, "runtime", "bin", name)
	argv := append([]string{name}, args...)
	switch name {
	case "ovpn", "ovpn-hook", "ovpn-broker", "ovpn-api":
		if name == "ovpn" {
			binary = filepath.Join(root, "runtime", "bin", "ovpn-core")
			if len(args) == 0 {
				env["OVPN_EXEC_MODE"] = "entrypoint"
			}
		} else if name == "ovpn-hook" {
			binary = filepath.Join(root, "runtime", "bin", "ovpn-core")
			env["OVPN_EXEC_MODE"] = "hook"
		}
	case "easyrsa":
		binary = filepath.Join(root, "runtime", "bin", "busybox")
		argv = append([]string{"busybox", "sh", filepath.Join(env["EASYRSA"], "easyrsa")}, args...)
	default:
		if _, err := os.Lstat(binary); err != nil {
			binary = filepath.Join(root, "runtime", "bin", "busybox")
			argv = append([]string{"busybox", name}, args...)
		} else {
			loader := filepath.Join(root, "runtime", "lib", "ld-musl.so.1")
			argv = append([]string{loader, "--library-path", filepath.Join(root, "runtime", "lib"), binary}, args...)
			binary = loader
		}
	}
	result := Invocation{Path: binary, Args: argv}
	for k, v := range env {
		result.Env = append(result.Env, k+"="+v)
	}
	return result, nil
}

func (inv Invocation) Exec() error { return syscall.Exec(inv.Path, inv.Args, inv.Env) }
