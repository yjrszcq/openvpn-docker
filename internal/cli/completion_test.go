package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionCommandGeneratesSupportedShells(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run([]string{"completion", shell}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
				t.Fatalf("completion %s code=%d stderr=%q", shell, code, stderr.String())
			}
			output := stdout.String()
			for _, value := range []string{"client", "address", "disconnect", "name", "id", "ipv4", "full-id"} {
				if !strings.Contains(output, value) {
					t.Errorf("completion %s is missing %q", shell, value)
				}
			}
			if strings.Contains(output, "BEGIN PRIVATE KEY") || strings.Contains(output, "tls-crypt") {
				t.Fatalf("completion %s contains private material", shell)
			}
		})
	}
}

func TestCompletionRejectsUnsupportedOrMissingShell(t *testing.T) {
	for _, args := range [][]string{{"completion"}, {"completion", "powershell"}, {"completion", "bash", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code != 64 || stdout.Len() != 0 || stderr.Len() == 0 || !strings.Contains(stderr.String(), "run the command with -h") {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestCompletionContractCoversEveryTreeOption(t *testing.T) {
	var bash bytes.Buffer
	if err := writeBashCompletion(&bash); err != nil {
		t.Fatal(err)
	}
	output := bash.String()
	for _, spec := range completionSpecs() {
		for _, child := range spec.command.children {
			if !strings.Contains(output, child.name) {
				t.Errorf("bash completion is missing command %s", strings.Join(append(spec.path, child.name), " "))
			}
		}
		for _, option := range completionOptions(spec.command.usage) {
			if !strings.Contains(output, option.long) {
				t.Errorf("bash completion is missing %s for %s", option.long, strings.Join(spec.path, " "))
			}
			if option.short != "" && !strings.Contains(output, option.short) {
				t.Errorf("bash completion is missing %s for %s", option.short, strings.Join(spec.path, " "))
			}
		}
	}
}

func TestGeneratedCompletionSyntaxWhenShellIsAvailable(t *testing.T) {
	checks := []struct {
		shell string
		args  []string
		write func(io.Writer) error
	}{
		{shell: "bash", args: []string{"-n"}, write: writeBashCompletion},
		{shell: "zsh", args: []string{"-n"}, write: writeZshCompletion},
		{shell: "fish", args: []string{"-n"}, write: writeFishCompletion},
	}
	for _, check := range checks {
		t.Run(check.shell, func(t *testing.T) {
			binary, err := exec.LookPath(check.shell)
			if err != nil {
				t.Skip(check.shell + " is not installed")
			}
			var script bytes.Buffer
			if err := check.write(&script); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "ovpn."+check.shell)
			if err := os.WriteFile(path, script.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(binary, append(check.args, path)...)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s syntax: %v\n%s", check.shell, err, output)
			}
		})
	}
}

func TestCompletionOptionsPreserveAliases(t *testing.T) {
	options := completionOptions("ovpn client create NAME [--ipv4|-4 [auto|dynamic|ADDRESS]] [--output|-o FILE|-] [--full-id|-u] [--json|-j]")
	wanted := []completionOption{
		{long: "--ipv4", short: "-4"},
		{long: "--output", short: "-o"},
		{long: "--full-id", short: "-u"},
		{long: "--json", short: "-j"},
	}
	if len(options) != len(wanted) {
		t.Fatalf("options=%+v", options)
	}
	for index := range wanted {
		if options[index] != wanted[index] {
			t.Fatalf("options[%d]=%+v want %+v", index, options[index], wanted[index])
		}
	}
	revoke := completionOptions("ovpn client revoke NAME [--release-ipv4|-4] [--json|-j]")
	if len(revoke) != 2 || revoke[0] != (completionOption{long: "--release-ipv4", short: "-4"}) || revoke[1] != (completionOption{long: "--json", short: "-j"}) {
		t.Fatalf("revoke options=%+v", revoke)
	}
}
