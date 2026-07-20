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
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

type MutationStore interface {
	QueryStore
	LoadClient(context.Context, string, string) (storesqlite.ClientState, error)
	PrepareOperation(context.Context, storesqlite.Operation) error
	AdvanceOperation(context.Context, string, storesqlite.OperationState, json.RawMessage, string, time.Time) error
	CommitCreateClientOperation(context.Context, string, string, storesqlite.ClientState, json.RawMessage, time.Time) error
	CommitRenameClientOperation(context.Context, string, string, string, string, string, storesqlite.ArtifactMetadata, storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error
	CommitRevokeClientOperation(context.Context, string, string, string, string, bool, []storesqlite.ArtifactMetadata, []storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error
	CommitReissueClientOperation(context.Context, string, string, string, string, domain.ClientStatus, storesqlite.AddressAssignment, []storesqlite.ArtifactMetadata, []storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error
	CommitDeleteClientOperation(context.Context, string, string, string, string, domain.ClientStatus, []storesqlite.ArtifactMetadata, []storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error
}

type Manager struct {
	state     MutationStore
	artifacts *artifact.LocalStore
	pki       *pki.Runner
	renderer  render.Renderer
	paths     render.Paths
	query     *Service
	now       func() time.Time
}

type CreateRequest struct {
	Name string
	IPv4 string
}

type MutationResult struct {
	OperationID  string `json:"operation_id"`
	Client       View   `json:"client"`
	KickRequired bool   `json:"kick_required"`
}

type mutationRecovery struct {
	Version   int      `json:"version"`
	Kind      string   `json:"kind"`
	ClientID  string   `json:"client_id"`
	Name      string   `json:"name"`
	OldName   string   `json:"old_name,omitempty"`
	IPv4      string   `json:"ipv4,omitempty"`
	Workspace string   `json:"workspace,omitempty"`
	Written   []string `json:"written"`
	Deleted   []string `json:"deleted"`
}

func NewManager(state MutationStore, artifacts *artifact.LocalStore, pkiRunner *pki.Runner, renderer render.Renderer, paths render.Paths) (*Manager, error) {
	if state == nil || artifacts == nil || pkiRunner == nil {
		return nil, fmt.Errorf("client mutation dependencies are required")
	}
	if artifacts.Root() != paths.DataDir {
		return nil, fmt.Errorf("artifact root must match client data directory")
	}
	query, err := NewService(state, artifacts)
	if err != nil {
		return nil, err
	}
	return &Manager{state: state, artifacts: artifacts, pki: pkiRunner, renderer: renderer, paths: paths, query: query, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (manager *Manager) Create(ctx context.Context, request CreateRequest) (MutationResult, error) {
	if !domain.ValidClientName(request.Name) {
		return MutationResult{}, fmt.Errorf("%w: invalid client name", ErrInvalidRequest)
	}
	selection := strings.ToLower(request.IPv4)
	if selection == "" {
		selection = "auto"
	}
	lock, err := artifact.AcquireLock(ctx, filepath.Join(manager.paths.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return MutationResult{}, err
	}
	defer lock.Release()
	instance, clients, err := manager.query.load(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	for _, state := range clients {
		if state.Client.Name == request.Name {
			return MutationResult{}, fmt.Errorf("%w: client name already exists", ErrConflict)
		}
	}
	clientID, err := domain.GenerateUUID()
	if err != nil {
		return MutationResult{}, err
	}
	now := manager.now().UTC().Truncate(time.Second)
	assignment, err := planCreateAssignment(instance, clients, selection, now)
	if err != nil {
		return MutationResult{}, err
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return MutationResult{}, err
	}
	workspaceKey := ".client-work-" + operationID
	recovery := mutationRecovery{Version: 1, Kind: "client.create", ClientID: clientID, Name: request.Name, IPv4: selection, Workspace: workspaceKey, Written: []string{}, Deleted: []string{}}
	payload, err := json.Marshal(recovery)
	if err != nil {
		return MutationResult{}, err
	}
	if err := manager.prepare(ctx, operationID, instance.ID, "client.create", payload, now); err != nil {
		return MutationResult{}, err
	}
	fileOperation, err := manager.artifacts.BeginOperation(operationID)
	if err != nil {
		_ = manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, payload, "", manager.now())
		return MutationResult{}, err
	}
	workspace := filepath.Join(manager.paths.DataDir, workspaceKey)
	rollback := func(cause error) error {
		workspaceErr := os.RemoveAll(workspace)
		artifactErr := fileOperation.Rollback()
		journalErr := manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, payload, "", manager.now())
		return errors.Join(cause, workspaceErr, artifactErr, journalErr)
	}
	if err := cloneTree(filepath.Join(manager.paths.DataDir, "pki"), filepath.Join(workspace, "pki")); err != nil {
		return MutationResult{}, rollback(err)
	}
	certificateInfo, err := manager.pki.IssueClient(ctx, filepath.Join(workspace, "pki"), clientID)
	if err != nil {
		return MutationResult{}, rollback(err)
	}
	pkiReferences, pkiKeys, err := stageTree(ctx, fileOperation, filepath.Join(workspace, "pki"), "pki")
	if err != nil {
		return MutationResult{}, rollback(err)
	}
	recovery.Written = append(recovery.Written, pkiKeys...)
	profile, err := manager.renderProfile(ctx, instance, filepath.Join(workspace, "pki"), clientID, request.Name)
	if err != nil {
		return MutationResult{}, rollback(err)
	}
	profileKey := "clients/active/" + request.Name + ".ovpn"
	profileReference, err := fileOperation.Stage(ctx, profileKey, 0o600, bytes.NewReader(profile))
	if err != nil {
		return MutationResult{}, rollback(err)
	}
	recovery.Written = append(recovery.Written, profileKey)
	artifacts := make([]storesqlite.ArtifactMetadata, 0, 4)
	certificateKey := "pki/issued/" + clientID + ".crt"
	privateKey := "pki/private/" + clientID + ".key"
	for _, description := range []struct {
		kind        string
		key         string
		reference   artifact.Reference
		certificate *pki.CertificateInfo
	}{
		{kind: "client-cert", key: certificateKey, reference: pkiReferences[certificateKey], certificate: &certificateInfo},
		{kind: "client-key", key: privateKey, reference: pkiReferences[privateKey]},
		{kind: "profile", key: profileKey, reference: profileReference},
	} {
		if description.reference.Key != description.key {
			return MutationResult{}, rollback(fmt.Errorf("staged PKI output %s is missing", description.key))
		}
		metadata, err := newReferenceMetadata(clientID, description.kind, description.key, description.reference, description.certificate)
		if err != nil {
			return MutationResult{}, rollback(err)
		}
		artifacts = append(artifacts, metadata)
	}
	if assignment.Kind == "static" {
		layout, _ := ipam.NewIPv4Layout(instance.Applied.Config.IPv4.Network, instance.Applied.Config.IPv4.DynamicPoolSize)
		ccd := []byte(fmt.Sprintf("ifconfig-push %s %s\n", assignment.Address, layout.Netmask))
		ccdKey := "ccd/" + clientID
		reference, err := fileOperation.Stage(ctx, ccdKey, 0o600, bytes.NewReader(ccd))
		if err != nil {
			return MutationResult{}, rollback(err)
		}
		recovery.Written = append(recovery.Written, ccdKey)
		metadata, err := newReferenceMetadata(clientID, "ccd", ccdKey, reference, nil)
		if err != nil {
			return MutationResult{}, rollback(err)
		}
		artifacts = append(artifacts, metadata)
	}
	payload, err = json.Marshal(recovery)
	if err != nil {
		return MutationResult{}, rollback(err)
	}
	state := storesqlite.ClientState{Client: domain.Client{ID: clientID, Name: request.Name, Status: domain.ClientActive}, CreatedAt: now, Assignment: assignment, Artifacts: artifacts}
	if err := fileOperation.Install(ctx, nil); err != nil {
		return MutationResult{}, rollback(err)
	}
	if err := manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationFilesInstalled, payload, "", manager.now()); err != nil {
		return MutationResult{}, rollback(err)
	}
	if err := manager.state.CommitCreateClientOperation(ctx, operationID, instance.ID, state, payload, manager.now()); err != nil {
		return MutationResult{}, rollback(err)
	}
	if err := fileOperation.Commit(nil); err != nil {
		return MutationResult{}, err
	}
	if err := os.RemoveAll(workspace); err != nil {
		return MutationResult{}, err
	}
	loaded, err := manager.state.LoadClient(ctx, instance.ID, clientID)
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{OperationID: operationID, Client: newView(loaded)}, nil
}

func (manager *Manager) Rename(ctx context.Context, selector Selector, newName string) (MutationResult, error) {
	if !domain.ValidClientName(newName) {
		return MutationResult{}, fmt.Errorf("%w: invalid new client name", ErrInvalidRequest)
	}
	lock, err := artifact.AcquireLock(ctx, filepath.Join(manager.paths.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return MutationResult{}, err
	}
	defer lock.Release()
	instance, state, err := manager.query.Select(ctx, selector)
	if err != nil {
		return MutationResult{}, err
	}
	if state.Client.Name == newName {
		return MutationResult{Client: newView(state)}, nil
	}
	clients, err := manager.state.ListClients(ctx, instance.ID)
	if err != nil {
		return MutationResult{}, err
	}
	for _, candidate := range clients {
		if candidate.Client.Name == newName {
			return MutationResult{}, fmt.Errorf("%w: client name already exists", ErrConflict)
		}
	}
	profile, err := manager.renderProfile(ctx, instance, filepath.Join(manager.paths.DataDir, "pki"), state.Client.ID, newName)
	if err != nil {
		return MutationResult{}, err
	}
	directory := "active"
	if state.Client.Status == domain.ClientRevoked {
		directory = "revoked"
	}
	oldKey := "clients/" + directory + "/" + state.Client.Name + ".ovpn"
	newKey := "clients/" + directory + "/" + newName + ".ovpn"
	if err := verifyCurrentProfile(ctx, manager.artifacts, state.Artifacts, oldKey); err != nil {
		return MutationResult{}, err
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return MutationResult{}, err
	}
	recovery := mutationRecovery{Version: 1, Kind: "client.rename", ClientID: state.Client.ID, Name: newName, OldName: state.Client.Name, Written: []string{newKey}, Deleted: []string{oldKey}}
	payload, err := json.Marshal(recovery)
	if err != nil {
		return MutationResult{}, err
	}
	now := manager.now().UTC().Truncate(time.Second)
	if err := manager.prepare(ctx, operationID, instance.ID, "client.rename", payload, now); err != nil {
		return MutationResult{}, err
	}
	fileOperation, err := manager.artifacts.BeginOperation(operationID)
	if err != nil {
		_ = manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, payload, "", manager.now())
		return MutationResult{}, err
	}
	rollback := func(cause error) error {
		return errors.Join(cause, fileOperation.Rollback(), manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, payload, "", manager.now()))
	}
	profileReference, err := fileOperation.Stage(ctx, newKey, 0o600, bytes.NewReader(profile))
	if err != nil {
		return MutationResult{}, rollback(err)
	}
	if err := fileOperation.Remove(oldKey); err != nil {
		return MutationResult{}, rollback(err)
	}
	if err := fileOperation.Install(ctx, nil); err != nil {
		return MutationResult{}, rollback(err)
	}
	if err := manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationFilesInstalled, payload, "", manager.now()); err != nil {
		return MutationResult{}, rollback(err)
	}
	metadata, err := newReferenceMetadata(state.Client.ID, "profile", newKey, profileReference, nil)
	if err != nil {
		return MutationResult{}, rollback(err)
	}
	deletion := storesqlite.ArtifactDeletion{OwnerKind: "client", OwnerID: state.Client.ID, Key: oldKey}
	if err := manager.state.CommitRenameClientOperation(ctx, operationID, instance.ID, state.Client.ID, state.Client.Name, newName, metadata, deletion, payload, manager.now()); err != nil {
		return MutationResult{}, rollback(err)
	}
	if err := fileOperation.Commit(nil); err != nil {
		return MutationResult{}, err
	}
	loaded, err := manager.state.LoadClient(ctx, instance.ID, state.Client.ID)
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{OperationID: operationID, Client: newView(loaded)}, nil
}

func (manager *Manager) prepare(ctx context.Context, operationID, instanceID, kind string, payload json.RawMessage, now time.Time) error {
	return manager.state.PrepareOperation(ctx, storesqlite.Operation{ID: operationID, InstanceID: instanceID, Kind: kind, State: storesqlite.OperationPrepared, PayloadVersion: 1, RecoveryPayload: payload, CreatedAt: now, UpdatedAt: now})
}

func (manager *Manager) renderProfile(ctx context.Context, instance storesqlite.InstanceState, pkiDirectory, clientID, name string) ([]byte, error) {
	caInfo, err := pki.ValidateCA(pkiDirectory, manager.now())
	if err != nil {
		return nil, err
	}
	if caInfo.Fingerprint != instance.CAFingerprint {
		return nil, fmt.Errorf("PKI CA fingerprint does not match SQLite instance state")
	}
	if _, err := pki.ValidateClient(pkiDirectory, clientID, manager.now()); err != nil {
		return nil, err
	}
	if err := pki.ValidateTLSCryptKey(filepath.Join(manager.paths.DataDir, "secrets", "tls-crypt.key")); err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(filepath.Join(pkiDirectory, "ca.crt"))
	if err != nil {
		return nil, err
	}
	certificate, err := os.ReadFile(filepath.Join(pkiDirectory, "issued", clientID+".crt"))
	if err != nil {
		return nil, err
	}
	privateKey, err := os.ReadFile(filepath.Join(pkiDirectory, "private", clientID+".key"))
	if err != nil {
		return nil, err
	}
	tlsCrypt, _, err := manager.artifacts.Read(ctx, "secrets/tls-crypt.key")
	if err != nil {
		return nil, err
	}
	return manager.renderer.Client(instance.Applied.Config, manager.paths, render.ClientMaterial{ID: clientID, Name: name, CACert: string(ca), Certificate: string(certificate), PrivateKey: string(privateKey), TLSCryptKey: string(tlsCrypt)})
}

func planCreateAssignment(instance storesqlite.InstanceState, clients []storesqlite.ClientState, selection string, now time.Time) (*storesqlite.AddressAssignment, error) {
	layout, err := ipam.NewIPv4Layout(instance.Applied.Config.IPv4.Network, instance.Applied.Config.IPv4.DynamicPoolSize)
	if err != nil {
		return nil, err
	}
	assignment := &storesqlite.AddressAssignment{NetworkID: instance.NetworkID, Status: storesqlite.AssignmentActive, CreatedAt: now, UpdatedAt: now}
	assignment.ID, err = domain.GenerateUUID()
	if err != nil {
		return nil, err
	}
	switch selection {
	case "dynamic":
		if layout.Dynamic.Empty() {
			return nil, fmt.Errorf("%w: dynamic IPv4 pool has no capacity", ErrInvalidRequest)
		}
		assignment.Kind = "dynamic"
	case "auto":
		used := make([]domain.Address, 0)
		for _, state := range clients {
			if state.Assignment != nil && state.Assignment.Kind == "static" && state.Assignment.Address != nil {
				used = append(used, *state.Assignment.Address)
			}
		}
		address, err := layout.NextStatic(used)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrConflict, err)
		}
		assignment.Kind = "static"
		assignment.Address = &address
	default:
		address, err := domain.ParseAddress(selection)
		if err != nil || address.Family() != domain.FamilyIPv4 {
			return nil, fmt.Errorf("%w: invalid client IPv4 address", ErrInvalidRequest)
		}
		if err := layout.ValidateStatic(address); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
		for _, state := range clients {
			if state.Assignment != nil && state.Assignment.Address != nil && state.Assignment.Address.Netip() == address.Netip() {
				return nil, fmt.Errorf("%w: static IPv4 address is already assigned", ErrConflict)
			}
		}
		assignment.Kind = "static"
		assignment.Address = &address
	}
	return assignment, nil
}

func cloneTree(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create PKI workspace parent: %w", err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return fmt.Errorf("create PKI workspace: %w", err)
	}
	return filepath.WalkDir(source, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, filePath)
		if err != nil || relative == "." {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("unsafe PKI workspace source %s", relative)
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.Mkdir(target, 0o700)
		}
		if !info.Mode().IsRegular() || (info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o644) {
			return fmt.Errorf("unsupported PKI file mode for %s", relative)
		}
		return copyFile(filePath, target, info.Mode().Perm())
	})
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, artifact.MaxArtifactSize+1))
	if copyErr == nil && written > artifact.MaxArtifactSize {
		copyErr = fmt.Errorf("PKI file exceeds %d bytes", artifact.MaxArtifactSize)
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	return errors.Join(copyErr, closeErr)
}

func stageTree(ctx context.Context, operation *artifact.Operation, root, prefix string) (map[string]artifact.Reference, []string, error) {
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || (info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o644) {
			return fmt.Errorf("unsafe staged PKI file %s", filePath)
		}
		paths = append(paths, filePath)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths)
	references := make(map[string]artifact.Reference, len(paths))
	keys := make([]string, 0, len(paths))
	for _, filePath := range paths {
		relative, _ := filepath.Rel(root, filePath)
		key := filepath.ToSlash(filepath.Join(prefix, relative))
		file, err := os.Open(filePath)
		if err != nil {
			return nil, nil, err
		}
		info, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return nil, nil, statErr
		}
		reference, stageErr := operation.Stage(ctx, key, info.Mode().Perm(), file)
		closeErr := file.Close()
		if stageErr != nil || closeErr != nil {
			return nil, nil, errors.Join(stageErr, closeErr)
		}
		references[key] = reference
		keys = append(keys, key)
	}
	return references, keys, nil
}

func newReferenceMetadata(clientID, kind, key string, reference artifact.Reference, certificate *pki.CertificateInfo) (storesqlite.ArtifactMetadata, error) {
	return newOwnerReferenceMetadata("client", clientID, kind, key, reference, certificate)
}

func newOwnerReferenceMetadata(ownerKind, ownerID, kind, key string, reference artifact.Reference, certificate *pki.CertificateInfo) (storesqlite.ArtifactMetadata, error) {
	id, err := domain.GenerateUUID()
	if err != nil {
		return storesqlite.ArtifactMetadata{}, err
	}
	value := storesqlite.ArtifactMetadata{ID: id, OwnerKind: ownerKind, OwnerID: ownerID, Kind: kind, Key: key, Digest: reference.Digest, Status: storesqlite.ArtifactActive}
	if certificate != nil {
		value.CertificateSerial = certificate.Serial
		value.CertificateFingerprint = append([]byte(nil), certificate.Fingerprint[:]...)
	}
	return value, nil
}

func verifyCurrentProfile(ctx context.Context, store *artifact.LocalStore, metadata []storesqlite.ArtifactMetadata, key string) error {
	var expected *storesqlite.ArtifactMetadata
	for index := range metadata {
		if metadata[index].Kind == "profile" && metadata[index].Status == storesqlite.ArtifactActive && metadata[index].Key == key {
			expected = &metadata[index]
		}
	}
	if expected == nil {
		return fmt.Errorf("%w: current profile metadata is missing", ErrArtifactMismatch)
	}
	_, reference, err := store.Read(ctx, key)
	if err != nil || reference.Digest != expected.Digest || reference.Mode != 0o600 {
		return fmt.Errorf("%w: current profile differs from state", ErrArtifactMismatch)
	}
	return nil
}
