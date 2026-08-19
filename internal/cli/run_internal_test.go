package cli

import "testing"

func TestRuntimeDependencyBinariesGateOptionalAPI(t *testing.T) {
	t.Setenv("OVPN_OPENVPN_BIN", "test-openvpn")
	t.Setenv("OVPN_BROKER_BIN", "test-broker")
	t.Setenv("OVPN_API_BIN", "test-api")

	openvpn, broker, api, dependencies := runtimeDependencyBinaries()
	if openvpn != "test-openvpn" || broker != "test-broker" || api != "" || len(dependencies) != 2 {
		t.Fatalf("disabled binaries=(%q, %q, %q) dependencies=%v", openvpn, broker, api, dependencies)
	}

	t.Setenv("OVPN_API_LISTEN", "127.0.0.1:11940")
	openvpn, broker, api, dependencies = runtimeDependencyBinaries()
	if openvpn != "test-openvpn" || broker != "test-broker" || api != "test-api" || len(dependencies) != 3 || dependencies[2] != "test-api" {
		t.Fatalf("enabled binaries=(%q, %q, %q) dependencies=%v", openvpn, broker, api, dependencies)
	}
}
