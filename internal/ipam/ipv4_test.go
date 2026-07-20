package ipam_test

import (
	"errors"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
)

func mustNetwork(t *testing.T, value string) domain.Network {
	t.Helper()
	network, err := domain.ParseNetwork(value)
	if err != nil {
		t.Fatalf("parse network %s: %v", value, err)
	}
	return network
}

func mustAddress(t *testing.T, value string) domain.Address {
	t.Helper()
	address, err := domain.ParseAddress(value)
	if err != nil {
		t.Fatalf("parse address %s: %v", value, err)
	}
	return address
}

func TestIPv4LayoutBoundaries(t *testing.T) {
	tests := []struct {
		name         string
		network      string
		dynamic      uint64
		server       string
		netmask      string
		clientFirst  string
		clientLast   string
		staticFirst  string
		staticLast   string
		staticCount  uint64
		dynamicFirst string
		dynamicLast  string
		dynamicCount uint64
	}{
		{"slash30-static", "10.42.0.0/30", 0, "10.42.0.1", "255.255.255.252", "10.42.0.2", "10.42.0.2", "10.42.0.2", "10.42.0.2", 1, "", "", 0},
		{"slash24-half", "10.42.0.0/24", 126, "10.42.0.1", "255.255.255.0", "10.42.0.2", "10.42.0.254", "10.42.0.2", "10.42.0.128", 127, "10.42.0.129", "10.42.0.254", 126},
		{"slash24-dynamic", "10.42.0.0/24", 253, "10.42.0.1", "255.255.255.0", "10.42.0.2", "10.42.0.254", "", "", 0, "10.42.0.2", "10.42.0.254", 253},
		{"slash0", "0.0.0.0/0", 2147483646, "0.0.0.1", "0.0.0.0", "0.0.0.2", "255.255.255.254", "0.0.0.2", "128.0.0.0", 2147483647, "128.0.0.1", "255.255.255.254", 2147483646},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout, err := ipam.NewIPv4Layout(mustNetwork(t, test.network), test.dynamic)
			if err != nil {
				t.Fatal(err)
			}
			if layout.Server.String() != test.server || layout.Netmask.String() != test.netmask || layout.Clients.First.String() != test.clientFirst || layout.Clients.Last.String() != test.clientLast {
				t.Fatalf("unexpected base layout: %+v", layout)
			}
			assertRange(t, layout.Static, test.staticFirst, test.staticLast, test.staticCount)
			assertRange(t, layout.Dynamic, test.dynamicFirst, test.dynamicLast, test.dynamicCount)
		})
	}
}

func assertRange(t *testing.T, value ipam.AddressRange, first, last string, capacity uint64) {
	t.Helper()
	if value.Capacity != capacity {
		t.Fatalf("range capacity=%d, want %d", value.Capacity, capacity)
	}
	if capacity == 0 {
		if !value.Empty() || value.First.Family() != domain.FamilyUnknown || value.Last.Family() != domain.FamilyUnknown {
			t.Fatalf("empty range contains addresses: %+v", value)
		}
		return
	}
	if value.Empty() || value.First.String() != first || value.Last.String() != last {
		t.Fatalf("range=%s-%s, want %s-%s", value.First, value.Last, first, last)
	}
}

func TestLayoutRejectsUnsupportedNetworkAndPool(t *testing.T) {
	for _, test := range []struct {
		network string
		pool    uint64
	}{
		{"10.42.0.0/31", 0},
		{"2001:db8::/64", 0},
		{"10.42.0.0/30", 2},
	} {
		if _, err := ipam.NewIPv4Layout(mustNetwork(t, test.network), test.pool); err == nil {
			t.Fatalf("accepted network=%s pool=%d", test.network, test.pool)
		}
	}
}

func TestStaticAllocationAndValidation(t *testing.T) {
	layout, err := ipam.NewIPv4Layout(mustNetwork(t, "10.42.0.0/29"), 2)
	if err != nil {
		t.Fatal(err)
	}
	used := []domain.Address{mustAddress(t, "10.42.0.2"), mustAddress(t, "10.42.0.4")}
	next, err := layout.NextStatic(used)
	if err != nil || next.String() != "10.42.0.3" {
		t.Fatalf("next static=%s err=%v", next, err)
	}
	if err := layout.ValidateStatic(mustAddress(t, "10.42.0.4")); err != nil {
		t.Fatalf("valid static address rejected: %v", err)
	}
	if err := layout.ValidateDynamicLease(mustAddress(t, "10.42.0.6")); err != nil {
		t.Fatalf("valid dynamic lease rejected: %v", err)
	}
	if err := layout.ValidateStatic(mustAddress(t, "10.42.0.6")); err == nil {
		t.Fatal("dynamic address accepted as static")
	}
	if err := layout.ValidateDynamicLease(mustAddress(t, "10.42.0.4")); err == nil {
		t.Fatal("static address accepted as dynamic lease")
	}
	if err := layout.ValidateStatic(mustAddress(t, "2001:db8::1")); err == nil {
		t.Fatal("IPv6 address accepted as static")
	}
}

func TestStaticSetRejectsDuplicatesAndInvalidEvidence(t *testing.T) {
	layout, err := ipam.NewIPv4Layout(mustNetwork(t, "10.42.0.0/29"), 2)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := mustAddress(t, "10.42.0.2")
	if err := layout.ValidateStaticSet([]domain.Address{duplicate, duplicate}); err == nil {
		t.Fatal("duplicate static assignments were accepted")
	}
	if _, err := layout.NextStatic([]domain.Address{mustAddress(t, "10.42.0.6")}); err == nil {
		t.Fatal("invalid existing static evidence was ignored")
	}
}

func TestStaticExhaustion(t *testing.T) {
	layout, err := ipam.NewIPv4Layout(mustNetwork(t, "10.42.0.0/30"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := layout.NextStatic([]domain.Address{mustAddress(t, "10.42.0.2")}); !errors.Is(err, ipam.ErrStaticExhausted) {
		t.Fatalf("exhaustion error=%v", err)
	}
	allDynamic, err := ipam.NewIPv4Layout(mustNetwork(t, "10.42.0.0/30"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := allDynamic.NextStatic(nil); !errors.Is(err, ipam.ErrStaticExhausted) {
		t.Fatalf("all-dynamic exhaustion error=%v", err)
	}
}
