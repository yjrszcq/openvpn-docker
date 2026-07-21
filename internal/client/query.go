// Package client implements client lifecycle use cases over authoritative state.
package client

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

const (
	MinimumIDPrefix = 8
	DisplayIDLength = 12
)

var (
	ErrNotFound         = errors.New("client was not found")
	ErrAmbiguous        = errors.New("client ID prefix is ambiguous")
	ErrInactive         = errors.New("client is not active")
	ErrArtifactMismatch = errors.New("client artifact does not match state")
	ErrInvalidRequest   = errors.New("client request is invalid")
	ErrConflict         = errors.New("client state conflicts with the request")
)

type QueryStore interface {
	LoadOnlyInstance(context.Context) (storesqlite.InstanceState, error)
	ListClients(context.Context, string) ([]storesqlite.ClientState, error)
	ClientIdentities(context.Context, string) (map[string]string, error)
}

type Selector struct {
	Name     string
	IDPrefix string
}

type Identity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type IPv4View struct {
	Mode    string  `json:"mode"`
	Address *string `json:"address"`
	State   string  `json:"state"`
}

type View struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	Status domain.ClientStatus `json:"status"`
	IPv4   IPv4View            `json:"ipv4"`
}

type ListResult struct {
	Version int    `json:"version"`
	Clients []View `json:"clients"`
}

type Service struct {
	state     QueryStore
	artifacts *artifact.LocalStore
}

func NewService(state QueryStore, artifacts *artifact.LocalStore) (*Service, error) {
	if state == nil || artifacts == nil {
		return nil, fmt.Errorf("client state and artifact stores are required")
	}
	return &Service{state: state, artifacts: artifacts}, nil
}

func (service *Service) List(ctx context.Context) (ListResult, error) {
	_, clients, err := service.load(ctx)
	if err != nil {
		return ListResult{}, err
	}
	views := make([]View, 0, len(clients))
	for _, state := range clients {
		views = append(views, newView(state))
	}
	return ListResult{Version: 1, Clients: views}, nil
}

func (service *Service) Select(ctx context.Context, selector Selector) (storesqlite.InstanceState, storesqlite.ClientState, error) {
	instance, clients, err := service.load(ctx)
	if err != nil {
		return storesqlite.InstanceState{}, storesqlite.ClientState{}, err
	}
	state, err := selectClient(clients, selector)
	return instance, state, err
}

// ResolveIdentity selects current clients by name and includes deleted
// tombstones when an immutable UUID prefix is used.
func (service *Service) ResolveIdentity(ctx context.Context, selector Selector) (Identity, error) {
	if err := validateSelector(selector); err != nil {
		return Identity{}, err
	}
	if selector.Name != "" {
		_, state, err := service.Select(ctx, selector)
		if err != nil {
			return Identity{}, err
		}
		return Identity{ID: state.Client.ID, Name: state.Client.Name}, nil
	}
	instance, err := service.state.LoadOnlyInstance(ctx)
	if err != nil {
		return Identity{}, err
	}
	identities, err := service.state.ClientIdentities(ctx, instance.ID)
	if err != nil {
		return Identity{}, err
	}
	prefix, err := normalizeIDPrefix(selector.IDPrefix)
	if err != nil {
		return Identity{}, err
	}
	matches := make([]Identity, 0, 1)
	for id, name := range identities {
		if strings.HasPrefix(strings.ReplaceAll(id, "-", ""), prefix) {
			matches = append(matches, Identity{ID: id, Name: name})
		}
	}
	switch len(matches) {
	case 0:
		return Identity{}, ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return Identity{}, ErrAmbiguous
	}
}

// SelectActive returns the concrete batch edit targets without changing state.
func (service *Service) SelectActive(ctx context.Context, all bool, selectors []Selector) ([]View, error) {
	_, _, targets, err := service.selectActive(ctx, all, selectors)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(targets))
	for _, target := range targets {
		views = append(views, newView(target))
	}
	return views, nil
}

func (service *Service) selectActive(ctx context.Context, all bool, selectors []Selector) (storesqlite.InstanceState, []storesqlite.ClientState, []storesqlite.ClientState, error) {
	if all == (len(selectors) > 0) {
		return storesqlite.InstanceState{}, nil, nil, fmt.Errorf("%w: select --all or one or more clients", ErrInvalidRequest)
	}
	instance, clients, err := service.load(ctx)
	if err != nil {
		return storesqlite.InstanceState{}, nil, nil, err
	}
	targets := make([]storesqlite.ClientState, 0, len(selectors))
	if all {
		for _, state := range clients {
			if state.Client.Status == domain.ClientActive {
				targets = append(targets, state)
			}
		}
	} else {
		seen := make(map[string]struct{}, len(selectors))
		for _, selector := range selectors {
			state, err := selectClient(clients, selector)
			if err != nil {
				return storesqlite.InstanceState{}, nil, nil, err
			}
			if state.Client.Status != domain.ClientActive {
				return storesqlite.InstanceState{}, nil, nil, fmt.Errorf("%w: batch address edit only accepts active clients", ErrInvalidRequest)
			}
			if _, duplicate := seen[state.Client.ID]; duplicate {
				return storesqlite.InstanceState{}, nil, nil, fmt.Errorf("%w: client selected more than once", ErrInvalidRequest)
			}
			seen[state.Client.ID] = struct{}{}
			targets = append(targets, state)
		}
	}
	if len(targets) == 0 {
		return storesqlite.InstanceState{}, nil, nil, fmt.Errorf("%w: no active clients selected", ErrInvalidRequest)
	}
	sort.Slice(targets, func(left, right int) bool {
		if targets[left].Client.Name == targets[right].Client.Name {
			return targets[left].Client.ID < targets[right].Client.ID
		}
		return targets[left].Client.Name < targets[right].Client.Name
	})
	return instance, clients, targets, nil
}

func selectClient(clients []storesqlite.ClientState, selector Selector) (storesqlite.ClientState, error) {
	if err := validateSelector(selector); err != nil {
		return storesqlite.ClientState{}, err
	}
	matches := make([]storesqlite.ClientState, 0, 1)
	if selector.Name != "" {
		for _, state := range clients {
			if state.Client.Name == selector.Name {
				matches = append(matches, state)
			}
		}
	} else {
		prefix, err := normalizeIDPrefix(selector.IDPrefix)
		if err != nil {
			return storesqlite.ClientState{}, err
		}
		for _, state := range clients {
			if strings.HasPrefix(strings.ReplaceAll(state.Client.ID, "-", ""), prefix) {
				matches = append(matches, state)
			}
		}
	}
	switch len(matches) {
	case 0:
		return storesqlite.ClientState{}, ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return storesqlite.ClientState{}, ErrAmbiguous
	}
}

func (service *Service) Export(ctx context.Context, selector Selector) ([]byte, View, error) {
	_, state, err := service.Select(ctx, selector)
	if err != nil {
		return nil, View{}, err
	}
	if state.Client.Status != domain.ClientActive {
		return nil, View{}, ErrInactive
	}
	var profile *storesqlite.ArtifactMetadata
	for index := range state.Artifacts {
		candidate := &state.Artifacts[index]
		if candidate.Kind == "profile" && candidate.Status == storesqlite.ArtifactActive {
			if profile != nil {
				return nil, View{}, fmt.Errorf("%w: multiple active profiles", ErrArtifactMismatch)
			}
			profile = candidate
		}
	}
	if profile == nil {
		return nil, View{}, fmt.Errorf("%w: active profile is missing", ErrArtifactMismatch)
	}
	content, reference, err := service.artifacts.Read(ctx, profile.Key)
	if err != nil {
		return nil, View{}, fmt.Errorf("%w: %v", ErrArtifactMismatch, err)
	}
	if reference.Digest != profile.Digest || reference.Mode != 0o600 {
		return nil, View{}, fmt.Errorf("%w: profile digest or permissions differ", ErrArtifactMismatch)
	}
	return content, newView(state), nil
}

func (service *Service) load(ctx context.Context) (storesqlite.InstanceState, []storesqlite.ClientState, error) {
	instance, err := service.state.LoadOnlyInstance(ctx)
	if err != nil {
		return storesqlite.InstanceState{}, nil, err
	}
	clients, err := service.state.ListClients(ctx, instance.ID)
	return instance, clients, err
}

func validateSelector(selector Selector) error {
	if (selector.Name == "") == (selector.IDPrefix == "") {
		return fmt.Errorf("%w: exactly one of client name or ID is required", ErrInvalidRequest)
	}
	if selector.Name != "" && !domain.ValidClientName(selector.Name) {
		return fmt.Errorf("%w: invalid client name", ErrInvalidRequest)
	}
	if selector.IDPrefix != "" {
		_, err := normalizeIDPrefix(selector.IDPrefix)
		return err
	}
	return nil
}

func normalizeIDPrefix(value string) (string, error) {
	lower := strings.ToLower(value)
	if strings.ContainsRune(lower, '-') && !domain.ValidUUID(lower) {
		return "", fmt.Errorf("%w: hyphenated client ID must be a complete canonical UUID", ErrInvalidRequest)
	}
	compact := strings.ReplaceAll(lower, "-", "")
	if len(compact) < MinimumIDPrefix || len(compact) > 32 {
		return "", fmt.Errorf("%w: client ID prefix must contain 8 to 32 hexadecimal characters", ErrInvalidRequest)
	}
	for _, character := range compact {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return "", fmt.Errorf("%w: client ID prefix must be hexadecimal", ErrInvalidRequest)
		}
	}
	return compact, nil
}

func newView(state storesqlite.ClientState) View {
	view := View{ID: state.Client.ID, Name: state.Client.Name, Status: state.Client.Status, IPv4: IPv4View{Mode: "none", State: "none"}}
	if state.Assignment == nil {
		return view
	}
	view.IPv4.Mode = state.Assignment.Kind
	view.IPv4.State = string(state.Assignment.Status)
	if state.Assignment.Address != nil {
		value := state.Assignment.Address.String()
		view.IPv4.Address = &value
	} else if state.Lease != nil {
		value := state.Lease.Address.String()
		view.IPv4.Address = &value
	}
	return view
}

func ShortID(id string) string {
	compact := strings.ReplaceAll(id, "-", "")
	if len(compact) <= DisplayIDLength {
		return compact
	}
	return compact[:DisplayIDLength]
}
