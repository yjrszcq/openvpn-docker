package cli

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestMutationSelectorDefaultsFirstPositionalToName(t *testing.T) {
	selector, positionals, err := parseMutationSelector([]string{"laptop", "office-laptop"})
	if err != nil || selector.Name != "laptop" || selector.IDPrefix != "" || len(positionals) != 1 || positionals[0] != "office-laptop" {
		t.Fatalf("selector=%+v positionals=%v err=%v", selector, positionals, err)
	}

	selector, positionals, err = parseMutationSelector([]string{"--id", "11111111", "office-laptop"})
	if err != nil || selector.Name != "" || selector.IDPrefix != "11111111" || len(positionals) != 1 || positionals[0] != "office-laptop" {
		t.Fatalf("explicit selector=%+v positionals=%v err=%v", selector, positionals, err)
	}
}

func TestClientExportDefaultsPositionalToName(t *testing.T) {
	selector, output, err := parseClientExport([]string{"laptop", "--output", "-"})
	if err != nil || selector.Name != "laptop" || selector.IDPrefix != "" || output != "-" {
		t.Fatalf("selector=%+v output=%q err=%v", selector, output, err)
	}

	for _, args := range [][]string{{"laptop", "phone"}, {"laptop", "--id", "11111111"}} {
		if _, _, err := parseClientExport(args); err == nil {
			t.Fatalf("args=%v unexpectedly accepted", args)
		}
	}
}

func TestAddressEditDefaultsPositionalsToNames(t *testing.T) {
	t.Setenv("OVPN_DATA_DIR", filepath.Join(t.TempDir(), "missing"))
	var stdout, stderr bytes.Buffer
	if code := runClientAddressEdit([]string{"laptop", "phone", "--yes"}, &stdout, &stderr); code == 64 {
		t.Fatalf("positional names rejected as usage: %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runClientAddressEdit([]string{"laptop", "--id", "11111111", "--yes"}, &stdout, &stderr); code != 64 {
		t.Fatalf("mixed positional and ID code=%d stderr=%q", code, stderr.String())
	}
}

func TestIPv4OptionDefaultsMissingValueToAuto(t *testing.T) {
	for _, args := range [][]string{{"--ipv4"}, {"--ipv4", "--name", "laptop"}} {
		value, index := optionalIPv4Value(args, 0)
		if value != "auto" || index != 0 {
			t.Fatalf("args=%v value=%q index=%d", args, value, index)
		}
	}

	value, index := optionalIPv4Value([]string{"--ipv4", "dynamic"}, 0)
	if value != "dynamic" || index != 1 {
		t.Fatalf("explicit value=%q index=%d", value, index)
	}
}
