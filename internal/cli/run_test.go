package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/buildinfo"
	"github.com/yjrszcq/openvpn-docker/internal/cli"
)

func run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestHelpShowsPlannedGroups(t *testing.T) {
	code, stdout, stderr := run("--help")
	if code != 0 || stderr != "" {
		t.Fatalf("help code=%d stderr=%q", code, stderr)
	}
	for _, group := range []string{"server", "config", "client", "state", "repair", "migrate", "runtime", "version"} {
		if !strings.Contains(stdout, group) {
			t.Errorf("help is missing %q", group)
		}
	}
}

func TestNestedHelp(t *testing.T) {
	code, stdout, _ := run("client", "address", "--help")
	if code != 0 || !strings.Contains(stdout, "set") || !strings.Contains(stdout, "release") {
		t.Fatalf("nested help code=%d output=%q", code, stdout)
	}
}

func TestVersionJSON(t *testing.T) {
	code, stdout, stderr := run("version", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("version code=%d stderr=%q", code, stderr)
	}
	var info buildinfo.Info
	if err := json.Unmarshal([]byte(stdout), &info); err != nil {
		t.Fatalf("decode version JSON: %v", err)
	}
	if info.Version != "4.0.0-dev" || info.DataSchema != 4 || info.GoVersion == "" {
		t.Fatalf("unexpected version info: %+v", info)
	}
}

func TestUnimplementedCommandFailsExplicitly(t *testing.T) {
	code, _, stderr := run("server", "init")
	if code != 1 || !strings.Contains(stderr, "not implemented") {
		t.Fatalf("foundation command code=%d stderr=%q", code, stderr)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	code, _, stderr := run("unknown")
	if code != 64 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("unknown command code=%d stderr=%q", code, stderr)
	}
}

func TestUnknownNestedCommandIsUsageError(t *testing.T) {
	code, _, stderr := run("server", "unknown")
	if code != 64 || !strings.Contains(stderr, "unknown command server unknown") {
		t.Fatalf("unknown nested command code=%d stderr=%q", code, stderr)
	}
}
