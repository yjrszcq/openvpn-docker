// Package configuration coordinates declarative configuration workflows over
// authoritative state without exposing SQLite details to the CLI.
package configuration

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sort"

	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

var ErrPlanConflict = errors.New("configuration cannot be applied to current state")

type StateStore interface {
	LoadOnlyInstance(context.Context) (storesqlite.InstanceState, error)
	ListClients(context.Context, string) ([]storesqlite.ClientState, error)
}

type Service struct {
	state StateStore
}

func NewService(state StateStore) (*Service, error) {
	if state == nil {
		return nil, fmt.Errorf("configuration state store is required")
	}
	return &Service{state: state}, nil
}

type ClientRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AddressIntent struct {
	Mode    string  `json:"mode"`
	Address *string `json:"address"`
	State   string  `json:"state"`
}

type AddressChange struct {
	Client ClientRef     `json:"client"`
	Before AddressIntent `json:"before"`
	After  AddressIntent `json:"after"`
}

type ArtifactImpact struct {
	OwnerKind string `json:"owner_kind"`
	OwnerID   string `json:"owner_id"`
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	Action    string `json:"action"`
}

type FirewallState struct {
	Network      string   `json:"network"`
	NATEnabled   bool     `json:"nat_enabled"`
	NATInterface string   `json:"nat_interface"`
	Routes       []string `json:"routes"`
}

type FirewallImpact struct {
	Reconcile bool           `json:"reconcile"`
	Before    *FirewallState `json:"before"`
	After     *FirewallState `json:"after"`
}

// Plan is the complete, stable operator view of one offline apply. The base
// configuration comparison is augmented with concrete clients and artifacts.
type Plan struct {
	Version               int                `json:"version"`
	InstanceID            string             `json:"instance_id"`
	Configuration         configservice.Plan `json:"configuration"`
	AddressChanges        []AddressChange    `json:"address_changes"`
	Artifacts             []ArtifactImpact   `json:"artifacts"`
	ProfileRedistribution []ClientRef        `json:"profile_redistribution"`
	Firewall              FirewallImpact     `json:"firewall"`
}

func (service *Service) Plan(ctx context.Context, desired domain.Config) (Plan, error) {
	instance, err := service.state.LoadOnlyInstance(ctx)
	if err != nil {
		return Plan{}, err
	}
	clients, err := service.state.ListClients(ctx, instance.ID)
	if err != nil {
		return Plan{}, err
	}
	return BuildPlan(instance, clients, desired)
}

func BuildPlan(instance storesqlite.InstanceState, clients []storesqlite.ClientState, desired domain.Config) (Plan, error) {
	base, err := configservice.BuildPlan(&instance.Applied, desired)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Version:               1,
		InstanceID:            instance.ID,
		Configuration:         base,
		AddressChanges:        make([]AddressChange, 0),
		Artifacts:             make([]ArtifactImpact, 0),
		ProfileRedistribution: make([]ClientRef, 0),
		Firewall:              FirewallImpact{Reconcile: base.Impact.FirewallReconcile},
	}
	if base.Impact.FirewallReconcile {
		before := firewallState(instance.Applied.Config)
		after := firewallState(desired)
		plan.Firewall.Before, plan.Firewall.After = &before, &after
	}
	if base.InSync {
		return plan, nil
	}

	ordered := append([]storesqlite.ClientState(nil), clients...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Client.Name != ordered[j].Client.Name {
			return ordered[i].Client.Name < ordered[j].Client.Name
		}
		return ordered[i].Client.ID < ordered[j].Client.ID
	})

	if base.Impact.AddressRemap {
		plan.AddressChanges, err = remapAddresses(ordered, desired)
		if err != nil {
			return Plan{}, err
		}
	}
	for _, group := range base.Impact.DerivedArtifacts {
		switch group {
		case "server_config":
			plan.Artifacts = append(plan.Artifacts, ArtifactImpact{OwnerKind: "instance", OwnerID: instance.ID, Kind: "server-config", Key: "server/server.conf", Action: "regenerate"})
		case "client_profiles":
			for _, client := range ordered {
				directory := "active"
				if client.Client.Status == domain.ClientRevoked {
					directory = "revoked"
				}
				plan.Artifacts = append(plan.Artifacts, ArtifactImpact{OwnerKind: "client", OwnerID: client.Client.ID, Kind: "profile", Key: "clients/" + directory + "/" + client.Client.Name + ".ovpn", Action: "regenerate"})
				if client.Client.Status == domain.ClientActive {
					plan.ProfileRedistribution = append(plan.ProfileRedistribution, ClientRef{ID: client.Client.ID, Name: client.Client.Name})
				}
			}
		case "ccd":
			for _, client := range ordered {
				action := "delete"
				if client.Client.Status == domain.ClientActive && client.Assignment != nil && client.Assignment.Kind == "static" {
					action = "regenerate"
				}
				plan.Artifacts = append(plan.Artifacts, ArtifactImpact{OwnerKind: "client", OwnerID: client.Client.ID, Kind: "ccd", Key: "ccd/" + client.Client.ID, Action: action})
			}
		default:
			return Plan{}, fmt.Errorf("unsupported derived artifact impact %q", group)
		}
	}
	return plan, nil
}

func remapAddresses(clients []storesqlite.ClientState, desired domain.Config) ([]AddressChange, error) {
	layout, err := ipam.NewIPv4Layout(desired.IPv4.Network, desired.IPv4.DynamicPoolSize)
	if err != nil {
		return nil, err
	}
	type pending struct {
		client storesqlite.ClientState
		value  domain.Address
		set    bool
	}
	static := make([]pending, 0)
	changes := make([]AddressChange, 0)
	for _, client := range clients {
		assignment := client.Assignment
		if assignment == nil {
			continue
		}
		if assignment.Kind == "dynamic" {
			if layout.Dynamic.Empty() {
				return nil, fmt.Errorf("%w: dynamic client %s requires a non-empty dynamic pool", ErrPlanConflict, client.Client.Name)
			}
			intent := addressIntent(*assignment)
			changes = append(changes, AddressChange{Client: ClientRef{ID: client.Client.ID, Name: client.Client.Name}, Before: intent, After: intent})
			continue
		}
		if assignment.Kind != "static" || assignment.Address == nil {
			return nil, fmt.Errorf("%w: client %s has invalid IPv4 assignment", ErrPlanConflict, client.Client.Name)
		}
		static = append(static, pending{client: client})
	}

	used := make(map[netip.Addr]struct{}, len(static))
	// Preserve already-valid absolute addresses before allocating remaps so one
	// client can never steal another client's valid address.
	for index := range static {
		address := *static[index].client.Assignment.Address
		if layout.Static.Contains(address) {
			if _, exists := used[address.Netip()]; !exists {
				static[index].value, static[index].set = address, true
				used[address.Netip()] = struct{}{}
			}
		}
	}
	// Next preserve host bits relative to the new prefix where possible.
	for index := range static {
		if static[index].set {
			continue
		}
		candidate := mapHostBits(*static[index].client.Assignment.Address, desired.IPv4.Network.Prefix())
		if layout.Static.Contains(candidate) {
			if _, exists := used[candidate.Netip()]; !exists {
				static[index].value, static[index].set = candidate, true
				used[candidate.Netip()] = struct{}{}
			}
		}
	}
	for index := range static {
		if static[index].set {
			continue
		}
		existing := make([]domain.Address, 0, len(used))
		for address := range used {
			value, convertErr := domain.NewAddress(address)
			if convertErr != nil {
				return nil, convertErr
			}
			existing = append(existing, value)
		}
		candidate, allocateErr := layout.NextStatic(existing)
		if allocateErr != nil {
			return nil, fmt.Errorf("%w: no static IPv4 address remains for client %s", ErrPlanConflict, static[index].client.Client.Name)
		}
		static[index].value, static[index].set = candidate, true
		used[candidate.Netip()] = struct{}{}
	}
	for _, item := range static {
		before := addressIntent(*item.client.Assignment)
		afterAddress := item.value.String()
		after := AddressIntent{Mode: "static", Address: &afterAddress, State: string(item.client.Assignment.Status)}
		changes = append(changes, AddressChange{Client: ClientRef{ID: item.client.Client.ID, Name: item.client.Client.Name}, Before: before, After: after})
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Client.Name != changes[j].Client.Name {
			return changes[i].Client.Name < changes[j].Client.Name
		}
		return changes[i].Client.ID < changes[j].Client.ID
	})
	return changes, nil
}

func addressIntent(assignment storesqlite.AddressAssignment) AddressIntent {
	intent := AddressIntent{Mode: assignment.Kind, State: string(assignment.Status)}
	if assignment.Address != nil {
		value := assignment.Address.String()
		intent.Address = &value
	}
	return intent
}

func mapHostBits(address domain.Address, target netip.Prefix) domain.Address {
	packed := address.Netip().As4()
	value := binary.BigEndian.Uint32(packed[:])
	networkPacked := target.Addr().As4()
	network := binary.BigEndian.Uint32(networkPacked[:])
	hostMask := ^uint32(0)
	if target.Bits() > 0 {
		hostMask = ^(^uint32(0) << (32 - target.Bits()))
	}
	var result [4]byte
	binary.BigEndian.PutUint32(result[:], network|(value&hostMask))
	return domain.AddressFrom4(result)
}

func firewallState(value domain.Config) FirewallState {
	routes := make([]string, len(value.IPv4.Routes))
	for index, route := range value.IPv4.Routes {
		routes[index] = route.String()
	}
	return FirewallState{Network: value.IPv4.Network.String(), NATEnabled: value.IPv4.NATEnabled, NATInterface: value.IPv4.NATInterface, Routes: routes}
}
