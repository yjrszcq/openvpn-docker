package domain_test

import (
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

func TestAddressFamiliesAndUnmapping(t *testing.T) {
	tests := []struct {
		input  string
		family domain.AddressFamily
		want   string
	}{
		{"10.42.0.2", domain.FamilyIPv4, "10.42.0.2"},
		{"::ffff:192.0.2.1", domain.FamilyIPv4, "192.0.2.1"},
		{"2001:db8::1", domain.FamilyIPv6, "2001:db8::1"},
	}
	for _, test := range tests {
		address, err := domain.ParseAddress(test.input)
		if err != nil {
			t.Fatalf("ParseAddress(%q): %v", test.input, err)
		}
		if address.Family() != test.family || address.String() != test.want {
			t.Errorf("ParseAddress(%q) = family %d, %q", test.input, address.Family(), address.String())
		}
	}
}

func TestNetworkRequiresCanonicalPrefix(t *testing.T) {
	if _, err := domain.ParseNetwork("10.42.0.1/24"); err == nil {
		t.Fatal("non-canonical network was accepted")
	}
	network, err := domain.ParseNetwork("10.42.0.0/24")
	if err != nil {
		t.Fatalf("parse canonical network: %v", err)
	}
	if network.Family() != domain.FamilyIPv4 || network.String() != "10.42.0.0/24" {
		t.Fatalf("unexpected network: family=%d value=%s", network.Family(), network.String())
	}
}

func TestClientIdentity(t *testing.T) {
	client, err := domain.NewClient("11111111-1111-4111-8111-111111111111", "laptop", domain.ClientActive)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if client.Name != "laptop" || client.Status != domain.ClientActive {
		t.Fatalf("unexpected client: %+v", client)
	}
	if _, err := domain.NewClient(client.ID, client.ID, domain.ClientActive); err == nil {
		t.Fatal("UUID-shaped client name was accepted")
	}
}
