// Package domain contains implementation-independent OpenVPN business values.
package domain

import (
	"fmt"
	"net/netip"
)

// AddressFamily identifies an IP address family in persisted and domain values.
type AddressFamily uint8

const (
	FamilyUnknown AddressFamily = 0
	FamilyIPv4    AddressFamily = 4
	FamilyIPv6    AddressFamily = 6
)

// Address wraps a canonical netip address with an explicit family.
type Address struct {
	value netip.Addr
}

// ParseAddress parses an IPv4 or IPv6 address and removes IPv4-in-IPv6 form.
func ParseAddress(input string) (Address, error) {
	value, err := netip.ParseAddr(input)
	if err != nil {
		return Address{}, fmt.Errorf("parse IP address %q: %w", input, err)
	}
	if value.Zone() != "" {
		return Address{}, fmt.Errorf("IP address %q must not contain a zone", input)
	}
	return Address{value: value.Unmap()}, nil
}

// Family returns the address family.
func (a Address) Family() AddressFamily {
	if !a.value.IsValid() {
		return FamilyUnknown
	}
	if a.value.Is4() {
		return FamilyIPv4
	}
	return FamilyIPv6
}

func (a Address) String() string { return a.value.String() }

// Netip returns the immutable standard-library address value.
func (a Address) Netip() netip.Addr { return a.value }

// Network wraps a canonical network prefix.
type Network struct {
	value netip.Prefix
}

// ParseNetwork accepts only a canonical IPv4 or IPv6 prefix.
func ParseNetwork(input string) (Network, error) {
	value, err := netip.ParsePrefix(input)
	if err != nil {
		return Network{}, fmt.Errorf("parse network %q: %w", input, err)
	}
	address := value.Addr().Unmap()
	bits := value.Bits()
	if address.Is4() && value.Addr().Is6() {
		bits -= 96
	}
	value = netip.PrefixFrom(address, bits)
	if value != value.Masked() {
		return Network{}, fmt.Errorf("network %q is not canonical", input)
	}
	return Network{value: value}, nil
}

// Family returns the network family.
func (n Network) Family() AddressFamily {
	if !n.value.IsValid() {
		return FamilyUnknown
	}
	if n.value.Addr().Is4() {
		return FamilyIPv4
	}
	return FamilyIPv6
}

func (n Network) String() string { return n.value.String() }

// Prefix returns the immutable standard-library prefix value.
func (n Network) Prefix() netip.Prefix { return n.value }
