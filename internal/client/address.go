package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

type AddressResult struct {
	Version      int      `json:"version"`
	OperationID  string   `json:"operation_id"`
	Clients      []View   `json:"clients"`
	KickRequired []string `json:"kick_required"`
}

type AddressEditRequest struct {
	All       bool
	Selectors []Selector
	Edit      func(string) error
}

type addressRecovery struct {
	Version int               `json:"version"`
	Kind    string            `json:"kind"`
	Changes map[string]string `json:"changes"`
	Written []string          `json:"written"`
	Deleted []string          `json:"deleted"`
}

func (manager *Manager) AddressSet(ctx context.Context, selector Selector, ipv4 string) (AddressResult, error) {
	if ipv4 == "" {
		return AddressResult{}, fmt.Errorf("%w: --ipv4 is required", ErrInvalidRequest)
	}
	lock, err := artifact.AcquireLock(ctx, filepath.Join(manager.paths.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return AddressResult{}, err
	}
	defer lock.Release()
	instance, state, err := manager.query.Select(ctx, selector)
	if err != nil {
		return AddressResult{}, err
	}
	clients, err := manager.state.ListClients(ctx, instance.ID)
	if err != nil {
		return AddressResult{}, err
	}
	return manager.applyAddressesLocked(ctx, instance, clients, []storesqlite.ClientState{state}, map[string]string{state.Client.ID: ipv4}, "client.address.set")
}

func (manager *Manager) AddressRelease(ctx context.Context, selector Selector) (AddressResult, error) {
	lock, err := artifact.AcquireLock(ctx, filepath.Join(manager.paths.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return AddressResult{}, err
	}
	defer lock.Release()
	instance, state, err := manager.query.Select(ctx, selector)
	if err != nil {
		return AddressResult{}, err
	}
	if state.Client.Status != domain.ClientRevoked || state.Assignment == nil {
		return AddressResult{}, fmt.Errorf("%w: only a revoked client with a retained assignment may release IPv4", ErrInvalidRequest)
	}
	return manager.commitAddressChanges(ctx, instance, []storesqlite.ClientState{state}, map[string]*storesqlite.AddressAssignment{state.Client.ID: nil}, map[string]string{state.Client.ID: "released"}, "client.address.release")
}

func (manager *Manager) AddressEdit(ctx context.Context, request AddressEditRequest) (AddressResult, error) {
	if request.Edit == nil || request.All == (len(request.Selectors) > 0) {
		return AddressResult{}, fmt.Errorf("%w: select --all or one or more clients", ErrInvalidRequest)
	}
	lock, err := artifact.AcquireLock(ctx, filepath.Join(manager.paths.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return AddressResult{}, err
	}
	defer lock.Release()
	instance, clients, targets, err := manager.query.selectActive(ctx, request.All, request.Selectors)
	if err != nil {
		return AddressResult{}, err
	}
	temporaryID, err := domain.GenerateUUID()
	if err != nil {
		return AddressResult{}, err
	}
	temporary := filepath.Join(manager.paths.DataDir, "meta", ".address-edit-"+temporaryID+".csv")
	if err := writeAddressEditFile(temporary, targets); err != nil {
		return AddressResult{}, err
	}
	defer os.Remove(temporary)
	if err := request.Edit(temporary); err != nil {
		return AddressResult{}, fmt.Errorf("address editor failed: %w", err)
	}
	selections, err := parseAddressEditFile(temporary, targets)
	if err != nil {
		return AddressResult{}, err
	}
	return manager.applyAddressesLocked(ctx, instance, clients, targets, selections, "client.address.edit")
}

func (manager *Manager) applyAddressesLocked(ctx context.Context, instance storesqlite.InstanceState, clients, targets []storesqlite.ClientState, selections map[string]string, kind string) (AddressResult, error) {
	now := manager.now().UTC().Truncate(time.Second)
	layout, err := ipam.NewIPv4Layout(instance.Applied.Config.IPv4.Network, instance.Applied.Config.IPv4.DynamicPoolSize)
	if err != nil {
		return AddressResult{}, err
	}
	selected := make(map[string]struct{}, len(targets))
	for _, state := range targets {
		selected[state.Client.ID] = struct{}{}
	}
	used := make([]domain.Address, 0)
	for _, state := range clients {
		if _, changing := selected[state.Client.ID]; changing || state.Assignment == nil || state.Assignment.Address == nil {
			continue
		}
		used = append(used, *state.Assignment.Address)
	}
	sort.Slice(targets, func(left, right int) bool {
		if targets[left].Client.Name == targets[right].Client.Name {
			return targets[left].Client.ID < targets[right].Client.ID
		}
		return targets[left].Client.Name < targets[right].Client.Name
	})
	assignments := make(map[string]*storesqlite.AddressAssignment, len(targets))
	normalized := make(map[string]string, len(targets))
	for _, state := range targets {
		selection := selections[state.Client.ID]
		lower := strings.ToLower(selection)
		assignment := &storesqlite.AddressAssignment{NetworkID: instance.NetworkID, Status: storesqlite.AssignmentActive, CreatedAt: now, UpdatedAt: now}
		assignment.ID, err = domain.GenerateUUID()
		if err != nil {
			return AddressResult{}, err
		}
		switch lower {
		case "dynamic":
			if layout.Dynamic.Empty() {
				return AddressResult{}, fmt.Errorf("%w: dynamic IPv4 pool has no capacity", ErrInvalidRequest)
			}
			assignment.Kind = "dynamic"
			normalized[state.Client.ID] = "dynamic"
		case "auto":
			address, err := layout.NextStatic(used)
			if err != nil {
				return AddressResult{}, fmt.Errorf("%w: %v", ErrConflict, err)
			}
			assignment.Kind = "static"
			assignment.Address = &address
			used = append(used, address)
			normalized[state.Client.ID] = address.String()
		default:
			address, err := domain.ParseAddress(selection)
			if err != nil || address.Family() != domain.FamilyIPv4 || layout.ValidateStatic(address) != nil {
				return AddressResult{}, fmt.Errorf("%w: invalid static IPv4 for %s", ErrInvalidRequest, state.Client.Name)
			}
			for _, current := range used {
				if current.Netip() == address.Netip() {
					return AddressResult{}, fmt.Errorf("%w: static IPv4 %s is already assigned", ErrConflict, address)
				}
			}
			assignment.Kind = "static"
			assignment.Address = &address
			used = append(used, address)
			normalized[state.Client.ID] = address.String()
		}
		assignments[state.Client.ID] = assignment
	}
	return manager.commitAddressChanges(ctx, instance, targets, assignments, normalized, kind)
}

func (manager *Manager) commitAddressChanges(ctx context.Context, instance storesqlite.InstanceState, targets []storesqlite.ClientState, assignments map[string]*storesqlite.AddressAssignment, selections map[string]string, kind string) (AddressResult, error) {
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return AddressResult{}, err
	}
	now := manager.now().UTC().Truncate(time.Second)
	recovery := addressRecovery{Version: 1, Kind: kind, Changes: selections, Written: []string{}, Deleted: []string{}}
	payload, _ := json.Marshal(recovery)
	workspaceKey := ".client-address-" + operationID
	fileOperation, workspace, rollback, err := manager.startLifecycle(ctx, operationID, instance.ID, workspaceKey, kind, payload, now)
	if err != nil {
		return AddressResult{}, err
	}
	active := make([]storesqlite.ArtifactMetadata, 0, len(targets))
	deleted := make([]storesqlite.ArtifactDeletion, 0, len(targets))
	changes := make([]storesqlite.ClientAddressChange, 0, len(targets))
	layout, _ := ipam.NewIPv4Layout(instance.Applied.Config.IPv4.Network, instance.Applied.Config.IPv4.DynamicPoolSize)
	for _, state := range targets {
		assignment := assignments[state.Client.ID]
		changes = append(changes, storesqlite.ClientAddressChange{ClientID: state.Client.ID, Name: state.Client.Name, ClientStatus: state.Client.Status, Assignment: assignment})
		ccdKey := "ccd/" + state.Client.ID
		if state.Client.Status == domain.ClientActive && assignment != nil && assignment.Kind == "static" {
			content := []byte(fmt.Sprintf("ifconfig-push %s %s\n", assignment.Address, layout.Netmask))
			reference, err := fileOperation.Stage(ctx, ccdKey, 0o600, bytes.NewReader(content))
			if err != nil {
				return AddressResult{}, rollback(err, payload)
			}
			metadata, err := newReferenceMetadata(state.Client.ID, "ccd", ccdKey, reference, nil)
			if err != nil {
				return AddressResult{}, rollback(err, payload)
			}
			active = append(active, metadata)
			recovery.Written = append(recovery.Written, ccdKey)
		} else if _, ok := activeArtifact(state.Artifacts, "ccd"); ok {
			if err := fileOperation.Remove(ccdKey); err != nil {
				return AddressResult{}, rollback(err, payload)
			}
			deleted = append(deleted, storesqlite.ArtifactDeletion{OwnerKind: "client", OwnerID: state.Client.ID, Key: ccdKey})
			recovery.Deleted = append(recovery.Deleted, ccdKey)
		}
	}
	payload, _ = json.Marshal(recovery)
	commit := func(at time.Time) error {
		return manager.state.CommitClientAddressOperation(ctx, operationID, instance.ID, kind, changes, active, deleted, payload, at)
	}
	if err := manager.finishLifecycle(ctx, operationID, fileOperation, workspace, payload, rollback, commit); err != nil {
		return AddressResult{}, err
	}
	result := AddressResult{Version: 1, OperationID: operationID, Clients: make([]View, 0, len(targets)), KickRequired: []string{}}
	for _, state := range targets {
		loaded, err := manager.state.LoadClient(ctx, instance.ID, state.Client.ID)
		if err != nil {
			return AddressResult{}, err
		}
		result.Clients = append(result.Clients, newView(loaded))
		if state.Client.Status == domain.ClientActive {
			result.KickRequired = append(result.KickRequired, state.Client.ID)
		}
	}
	return result, nil
}

func writeAddressEditFile(path string, targets []storesqlite.ClientState) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	var resultErr error
	if _, resultErr = io.WriteString(file, "# client,ipv4\n"); resultErr == nil {
		for _, state := range targets {
			value := "dynamic"
			if state.Assignment != nil && state.Assignment.Address != nil {
				value = state.Assignment.Address.String()
			}
			if _, resultErr = fmt.Fprintf(file, "%s,%s\n", state.Client.Name, value); resultErr != nil {
				break
			}
		}
	}
	if resultErr == nil {
		resultErr = file.Sync()
	}
	return errors.Join(resultErr, file.Close())
}

func parseAddressEditFile(path string, targets []storesqlite.ClientState) (map[string]string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%w: address edit file is not a private regular file", ErrInvalidRequest)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if len(content) > 1<<20 {
		return nil, fmt.Errorf("%w: address edit file is too large", ErrInvalidRequest)
	}
	byName := make(map[string]string, len(targets))
	for _, state := range targets {
		byName[state.Client.Name] = state.Client.ID
	}
	values := make(map[string]string, len(targets))
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if len(lines) == 0 || lines[0] != "# client,ipv4" {
		return nil, fmt.Errorf("%w: address edit header is invalid", ErrInvalidRequest)
	}
	for _, line := range lines[1:] {
		if strings.ContainsAny(line, " \t\r") || strings.Count(line, ",") != 1 {
			return nil, fmt.Errorf("%w: address edit rows must be client,ipv4 without whitespace", ErrInvalidRequest)
		}
		parts := strings.SplitN(line, ",", 2)
		id, selected := byName[parts[0]]
		if !selected || parts[1] == "" {
			return nil, fmt.Errorf("%w: address edit contains an unknown client or empty value", ErrInvalidRequest)
		}
		if _, duplicate := values[id]; duplicate {
			return nil, fmt.Errorf("%w: address edit duplicates client %s", ErrInvalidRequest, parts[0])
		}
		values[id] = parts[1]
	}
	if len(values) != len(targets) {
		return nil, fmt.Errorf("%w: address edit must contain every selected client exactly once", ErrInvalidRequest)
	}
	return values, nil
}
