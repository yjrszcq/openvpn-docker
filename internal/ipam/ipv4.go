// Package ipam implements deterministic tunnel address layout and allocation.
package ipam

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

var ErrStaticExhausted = errors.New("static address region is exhausted")

// AddressRange is an inclusive address interval. Capacity zero is empty.
type AddressRange struct {
	First    domain.Address
	Last     domain.Address
	Capacity uint64
}

// Empty reports whether the range contains no addresses.
func (r AddressRange) Empty() bool { return r.Capacity == 0 }

// Contains reports whether address belongs to the inclusive range.
func (r AddressRange) Contains(address domain.Address) bool {
	if r.Empty() || address.Family() != domain.FamilyIPv4 {
		return false
	}
	value := ipv4Uint32(address.Netip())
	return value >= ipv4Uint32(r.First.Netip()) && value <= ipv4Uint32(r.Last.Netip())
}

// IPv4Layout is the complete subnet-topology server/client pool partition.
type IPv4Layout struct {
	Network domain.Network
	Netmask domain.Address
	Server  domain.Address
	Clients AddressRange
	Static  AddressRange
	Dynamic AddressRange
}

// ClientCapacity returns usable client addresses after reserving network,
// server (network+1), and broadcast addresses.
func ClientCapacity(network domain.Network) (uint64, error) {
	if network.Family() != domain.FamilyIPv4 {
		return 0, fmt.Errorf("IPv4 IPAM does not support address family %d", network.Family())
	}
	prefix := network.Prefix().Bits()
	if prefix > 30 {
		return 0, fmt.Errorf("IPv4 network must be /30 or larger")
	}
	return (uint64(1) << (32 - prefix)) - 3, nil
}

// NewIPv4Layout partitions usable client addresses into a static prefix and a
// dynamic tail. Both zero and full dynamic capacity are valid.
func NewIPv4Layout(network domain.Network, dynamicPoolSize uint64) (IPv4Layout, error) {
	capacity, err := ClientCapacity(network)
	if err != nil {
		return IPv4Layout{}, err
	}
	if dynamicPoolSize > capacity {
		return IPv4Layout{}, fmt.Errorf("dynamic pool size must be between 0 and %d", capacity)
	}
	prefix := network.Prefix()
	networkValue := uint64(ipv4Uint32(prefix.Addr()))
	total := uint64(1) << (32 - prefix.Bits())
	clientFirst := networkValue + 2
	clientLast := networkValue + total - 2
	staticCapacity := capacity - dynamicPoolSize

	layout := IPv4Layout{
		Network: network,
		Netmask: addressFromUint32(uint32Mask(prefix.Bits())),
		Server:  addressFromUint32(uint32(networkValue + 1)),
		Clients: newRange(clientFirst, clientLast, capacity),
	}
	if staticCapacity > 0 {
		layout.Static = newRange(clientFirst, clientFirst+staticCapacity-1, staticCapacity)
	}
	if dynamicPoolSize > 0 {
		layout.Dynamic = newRange(clientLast-dynamicPoolSize+1, clientLast, dynamicPoolSize)
	}
	return layout, nil
}

// ValidateStatic rejects addresses outside the configured static region.
func (layout IPv4Layout) ValidateStatic(address domain.Address) error {
	if address.Family() != domain.FamilyIPv4 {
		return fmt.Errorf("static assignment must use IPv4")
	}
	if !layout.Static.Contains(address) {
		return fmt.Errorf("static address %s is outside the static region", address)
	}
	return nil
}

// ValidateDynamicLease rejects leases outside the configured dynamic pool.
func (layout IPv4Layout) ValidateDynamicLease(address domain.Address) error {
	if address.Family() != domain.FamilyIPv4 {
		return fmt.Errorf("dynamic lease must use IPv4")
	}
	if !layout.Dynamic.Contains(address) {
		return fmt.Errorf("dynamic lease %s is outside the dynamic pool", address)
	}
	return nil
}

// ValidateStaticSet checks range membership and uniqueness of authoritative
// static assignments.
func (layout IPv4Layout) ValidateStaticSet(addresses []domain.Address) error {
	seen := make(map[uint32]struct{}, len(addresses))
	for _, address := range addresses {
		if err := layout.ValidateStatic(address); err != nil {
			return err
		}
		value := ipv4Uint32(address.Netip())
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate static address %s", address)
		}
		seen[value] = struct{}{}
	}
	return nil
}

// NextStatic returns the lowest unused static address. Existing assignments
// must themselves be valid and unique.
func (layout IPv4Layout) NextStatic(addresses []domain.Address) (domain.Address, error) {
	if err := layout.ValidateStaticSet(addresses); err != nil {
		return domain.Address{}, err
	}
	if layout.Static.Empty() {
		return domain.Address{}, ErrStaticExhausted
	}
	used := make(map[uint32]struct{}, len(addresses))
	for _, address := range addresses {
		used[ipv4Uint32(address.Netip())] = struct{}{}
	}
	first := uint64(ipv4Uint32(layout.Static.First.Netip()))
	last := uint64(ipv4Uint32(layout.Static.Last.Netip()))
	for candidate := first; candidate <= last; candidate++ {
		if _, exists := used[uint32(candidate)]; !exists {
			return addressFromUint32(uint32(candidate)), nil
		}
	}
	return domain.Address{}, ErrStaticExhausted
}

func newRange(first, last, capacity uint64) AddressRange {
	return AddressRange{
		First:    addressFromUint32(uint32(first)),
		Last:     addressFromUint32(uint32(last)),
		Capacity: capacity,
	}
}

func addressFromUint32(value uint32) domain.Address {
	var packed [4]byte
	binary.BigEndian.PutUint32(packed[:], value)
	return domain.AddressFrom4(packed)
}

func ipv4Uint32(address netip.Addr) uint32 {
	packed := address.As4()
	return binary.BigEndian.Uint32(packed[:])
}

func uint32Mask(prefix int) uint32 {
	if prefix == 0 {
		return 0
	}
	return ^uint32(0) << (32 - prefix)
}
