package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalOptionContract(t *testing.T) {
	aliases := map[string]string{
		"-h": "--help",
		"-j": "--json",
		"-o": "--output",
		"-y": "--yes",
		"-n": "--name",
		"-i": "--id",
		"-4": "--ipv4",
		"-a": "--all",
		"-d": "--detail",
		"-u": "--full-id",
		"-l": "--lines",
		"-f": "--follow",
		"-r": "--raw",
		"-s": "--short",
	}
	for short, long := range aliases {
		if got := canonicalOption(short); got != long {
			t.Errorf("canonicalOption(%q)=%q, want %q", short, got, long)
		}
		if got := canonicalOption(long); got != long {
			t.Errorf("canonicalOption(%q)=%q, want unchanged", long, got)
		}
	}
	if got := canonicalOption("-6"); got != "-6" {
		t.Fatalf("reserved IPv6 alias was consumed as %q", got)
	}
}

func TestShortClientSelectorsMatchLongForms(t *testing.T) {
	short, shortPositionals, err := parseMutationSelector([]string{"-n", "laptop", "office"})
	if err != nil {
		t.Fatal(err)
	}
	long, longPositionals, err := parseMutationSelector([]string{"--name", "laptop", "office"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(short, long) || !reflect.DeepEqual(shortPositionals, longPositionals) {
		t.Fatalf("short=%+v/%v long=%+v/%v", short, shortPositionals, long, longPositionals)
	}

	short, shortPositionals, err = parseMutationSelector([]string{"-i", "11111111"})
	if err != nil {
		t.Fatal(err)
	}
	long, longPositionals, err = parseMutationSelector([]string{"--id", "11111111"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(short, long) || !reflect.DeepEqual(shortPositionals, longPositionals) {
		t.Fatalf("short=%+v/%v long=%+v/%v", short, shortPositionals, long, longPositionals)
	}
}

func TestShortExportAndStreamOptionsMatchLongForms(t *testing.T) {
	shortSelector, shortOutput, err := parseClientExport([]string{"-n", "laptop", "-o", "-"})
	if err != nil {
		t.Fatal(err)
	}
	longSelector, longOutput, err := parseClientExport([]string{"--name", "laptop", "--output", "-"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(shortSelector, longSelector) || shortOutput != longOutput {
		t.Fatalf("short=%+v/%q long=%+v/%q", shortSelector, shortOutput, longSelector, longOutput)
	}

	shortStream, err := parseStreamOptions([]string{"-l", "12", "-f", "-r", "-u"}, false)
	if err != nil {
		t.Fatal(err)
	}
	longStream, err := parseStreamOptions([]string{"--lines", "12", "--follow", "--raw", "--full-id"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if shortStream != longStream {
		t.Fatalf("short=%+v long=%+v", shortStream, longStream)
	}
}

func TestShortJSONAndVersionOptions(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runVersion([]string{"-s"}, &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) == "" || stderr.Len() != 0 {
		t.Fatalf("short version code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code, ok := parseJSONOnly([]string{"-j"}, &stdout, &stderr, "ovpn config show"); !ok || code != 1 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("short JSON code=%d ok=%t stdout=%q stderr=%q", code, ok, stdout.String(), stderr.String())
	}
}

func TestLongAndShortDuplicateOptionsAreRejected(t *testing.T) {
	if _, _, err := parseClientExport([]string{"-n", "laptop", "--name", "phone"}); err == nil {
		t.Fatal("mixed long and short name selectors were accepted")
	}
	if _, err := parseStreamOptions([]string{"-f", "--follow"}, false); err == nil {
		t.Fatal("mixed long and short follow options were accepted")
	}
	var stdout, stderr bytes.Buffer
	if code, ok := parseJSONOnly([]string{"-j", "--json"}, &stdout, &stderr, "ovpn config show"); ok || code != 64 {
		t.Fatalf("mixed JSON duplicate code=%d ok=%t stderr=%q", code, ok, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runVersion([]string{"-s", "--short"}, &stdout, &stderr); code != 64 {
		t.Fatalf("mixed short-version duplicate code=%d stderr=%q", code, stderr.String())
	}
}
