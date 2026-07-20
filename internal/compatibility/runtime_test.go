package compatibility_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
)

type fakeRunner struct {
	version []byte
	help    []byte
	err     error
}

func (runner fakeRunner) CombinedOutput(_ context.Context, _ string, args ...string) ([]byte, error) {
	if runner.err != nil {
		return nil, runner.err
	}
	if reflect.DeepEqual(args, []string{"--version"}) {
		return runner.version, nil
	}
	return runner.help, nil
}

func mustContract(t *testing.T) compatibility.Contract {
	t.Helper()
	contract, err := compatibility.Parse(repositoryContract(t))
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestInspectSupportedRuntime(t *testing.T) {
	capabilities, err := compatibility.Inspect(context.Background(), mustContract(t), "openvpn", fakeRunner{
		version: []byte("OpenVPN 2.7.5 test-build\n"),
		help:    []byte("--tls-crypt key\n--data-ciphers list\n--crl-verify crl\n--topology t: 'subnet'\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.Supported() || capabilities.Adapter == nil || *capabilities.Adapter != "openvpn-2.7" {
		t.Fatalf("unexpected capabilities: %+v", capabilities)
	}
}

func TestInspectReportsUnsupportedVersionWithoutAdapter(t *testing.T) {
	capabilities, err := compatibility.Inspect(context.Background(), mustContract(t), "openvpn", fakeRunner{
		version: []byte("OpenVPN 2.7.6 test-build\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Supported() || capabilities.SupportedVersion || capabilities.Adapter != nil || capabilities.TemplateFamily != nil {
		t.Fatalf("unsupported runtime selected an adapter: %+v", capabilities)
	}
	for feature, supported := range capabilities.Features {
		if supported {
			t.Fatalf("unsupported runtime reports feature %s", feature)
		}
	}
}

func TestInspectReportsMissingFeature(t *testing.T) {
	capabilities, err := compatibility.Inspect(context.Background(), mustContract(t), "openvpn", fakeRunner{
		version: []byte("OpenVPN 2.7.5 test-build\n"),
		help:    []byte("--data-ciphers list\n--crl-verify crl\n--topology t: 'subnet'\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Supported() || capabilities.Features["tls_crypt"] {
		t.Fatalf("missing feature was accepted: %+v", capabilities)
	}
}

func TestInspectRejectsUnavailableAndMalformedRuntime(t *testing.T) {
	contract := mustContract(t)
	if _, err := compatibility.Inspect(context.Background(), contract, "openvpn", fakeRunner{err: errors.New("missing")}); err == nil {
		t.Fatal("unavailable runtime was accepted")
	}
	if _, err := compatibility.Inspect(context.Background(), contract, "openvpn", fakeRunner{version: []byte("not OpenVPN\n")}); err == nil {
		t.Fatal("malformed runtime version was accepted")
	}
}
