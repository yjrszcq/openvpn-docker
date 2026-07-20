package domain_test

import (
	"net/netip"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

func TestAddressFamiliesAndUnmapping(t *testing.T) {
	if family := (domain.Address{}).Family(); family != domain.FamilyUnknown {
		t.Fatalf("zero address family = %d, want unknown", family)
	}
	if family := (domain.Network{}).Family(); family != domain.FamilyUnknown {
		t.Fatalf("zero network family = %d, want unknown", family)
	}
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

func TestAddressConstructorsRejectInvalidValues(t *testing.T) {
	if _, err := domain.NewAddress(netip.Addr{}); err == nil {
		t.Fatal("invalid netip address was accepted")
	}
	if _, err := domain.NewAddress(netip.MustParseAddr("fe80::1%eth0")); err == nil {
		t.Fatal("zoned address was accepted")
	}
	address := domain.AddressFrom4([4]byte{10, 42, 0, 2})
	if address.Family() != domain.FamilyIPv4 || address.String() != "10.42.0.2" {
		t.Fatalf("unexpected four-byte address: family=%d value=%s", address.Family(), address)
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

func TestNetworkConstructorNormalizesMappedIPv4(t *testing.T) {
	if _, err := domain.NewNetwork(netip.Prefix{}); err == nil {
		t.Fatal("invalid netip prefix was accepted")
	}
	mapped, err := domain.NewNetwork(netip.MustParsePrefix("::ffff:10.42.0.0/120"))
	if err != nil {
		t.Fatalf("normalize mapped IPv4 prefix: %v", err)
	}
	if mapped.Family() != domain.FamilyIPv4 || mapped.String() != "10.42.0.0/24" {
		t.Fatalf("unexpected mapped prefix: family=%d value=%s", mapped.Family(), mapped)
	}
	if _, err := domain.NewNetwork(netip.MustParsePrefix("::ffff:0:0/80")); err == nil {
		t.Fatal("mapped IPv4 prefix shorter than /96 was accepted")
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

func TestGenerateUUID(t *testing.T) {
	first, err := domain.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.GenerateUUID()
	if err != nil {
		t.Fatal(err)
	}
	if !domain.ValidUUID(first) || !domain.ValidUUID(second) || first == second {
		t.Fatalf("invalid generated UUIDs: %q %q", first, second)
	}
}
