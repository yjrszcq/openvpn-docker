package configuration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

type ApplyStore interface {
	StateStore
	LoadInstanceArtifacts(context.Context, string) ([]storesqlite.ArtifactMetadata, error)
	PrepareOperation(context.Context, storesqlite.Operation) error
	AdvanceOperation(context.Context, string, storesqlite.OperationState, json.RawMessage, string, time.Time) error
	CommitConfigurationOperation(context.Context, storesqlite.ConfigurationCommit) error
}

type Manager struct {
	state     ApplyStore
	artifacts *artifact.LocalStore
	renderer  render.Renderer
	paths     render.Paths
	now       func() time.Time
}

type ApplyResult struct {
	Version     int    `json:"version"`
	Applied     bool   `json:"applied"`
	OperationID string `json:"operation_id,omitempty"`
	Plan        Plan   `json:"plan"`
}

type writeSpec struct {
	key      string
	mode     os.FileMode
	content  []byte
	metadata storesqlite.ArtifactMetadata
}

type applyRecovery struct {
	Version         int      `json:"version"`
	Kind            string   `json:"kind"`
	CurrentRevision uint64   `json:"current_revision"`
	TargetRevision  uint64   `json:"target_revision"`
	CurrentDigest   string   `json:"current_digest"`
	TargetDigest    string   `json:"target_digest"`
	Written         []string `json:"written"`
	Deleted         []string `json:"deleted"`
	ArtifactDigests []string `json:"artifact_digests"`
}

func NewManager(state ApplyStore, artifacts *artifact.LocalStore, renderer render.Renderer, paths render.Paths) (*Manager, error) {
	if state == nil || artifacts == nil {
		return nil, fmt.Errorf("configuration state and artifact stores are required")
	}
	if artifacts.Root() != paths.DataDir || paths.RuntimeDir == "" || !filepath.IsAbs(paths.RuntimeDir) || filepath.Clean(paths.RuntimeDir) != paths.RuntimeDir {
		return nil, fmt.Errorf("configuration data and runtime paths are inconsistent")
	}
	return &Manager{state: state, artifacts: artifacts, renderer: renderer, paths: paths, now: func() time.Time { return time.Now().UTC() }}, nil
}

// Apply obtains the exclusive runtime lock before the data lock. The running
// supervisor holds the same runtime lock shared for its complete lifetime, so
// an apply can never overlap OpenVPN startup or execution.
func (manager *Manager) Apply(ctx context.Context, desired domain.Config) (ApplyResult, error) {
	runtimeLock, err := artifact.AcquireLock(ctx, filepath.Join(manager.paths.RuntimeDir, ".runtime.lock"), artifact.LockExclusive)
	if err != nil {
		return ApplyResult{}, err
	}
	defer runtimeLock.Release()
	dataLock, err := artifact.AcquireLock(ctx, filepath.Join(manager.paths.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return ApplyResult{}, err
	}
	defer dataLock.Release()
	return manager.applyLocked(ctx, desired)
}

func (manager *Manager) applyLocked(ctx context.Context, desired domain.Config) (ApplyResult, error) {
	instance, err := manager.state.LoadOnlyInstance(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	clients, err := manager.state.ListClients(ctx, instance.ID)
	if err != nil {
		return ApplyResult{}, err
	}
	plan, err := BuildPlan(instance, clients, desired)
	if err != nil {
		return ApplyResult{}, err
	}
	result := ApplyResult{Version: 1, Plan: plan}
	if plan.Configuration.InSync {
		return result, nil
	}
	writes, deletions, err := manager.prepareArtifacts(ctx, instance, clients, desired, plan)
	if err != nil {
		return ApplyResult{}, err
	}
	active := make([]storesqlite.ArtifactMetadata, 0, len(writes))
	for _, item := range writes {
		active = append(active, item.metadata)
	}

	now := manager.now().UTC().Truncate(time.Second)
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return ApplyResult{}, err
	}
	payloadView := applyRecovery{
		Version: 1, Kind: "config.apply",
		CurrentRevision: uint64(plan.Configuration.CurrentRevision), TargetRevision: uint64(plan.Configuration.TargetRevision),
		CurrentDigest: plan.Configuration.CurrentDigest, TargetDigest: plan.Configuration.DesiredDigest,
		Written: make([]string, 0, len(writes)), Deleted: make([]string, 0, len(deletions)), ArtifactDigests: make([]string, 0, len(active)),
	}
	for _, item := range writes {
		payloadView.Written = append(payloadView.Written, item.key)
	}
	for _, item := range deletions {
		payloadView.Deleted = append(payloadView.Deleted, item.Key)
	}
	for _, item := range active {
		payloadView.ArtifactDigests = append(payloadView.ArtifactDigests, hex.EncodeToString(item.Digest[:]))
	}
	payload, err := json.Marshal(payloadView)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := manager.state.PrepareOperation(ctx, storesqlite.Operation{ID: operationID, InstanceID: instance.ID, Kind: "config.apply", State: storesqlite.OperationPrepared, PayloadVersion: 1, RecoveryPayload: payload, CreatedAt: now, UpdatedAt: now}); err != nil {
		return ApplyResult{}, err
	}
	operation, err := manager.artifacts.BeginOperation(operationID)
	if err != nil {
		_ = manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, payload, "", manager.now())
		return ApplyResult{}, err
	}
	rollback := func(cause error) error {
		return errors.Join(cause, operation.Rollback(), manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, payload, "", manager.now()))
	}
	for _, item := range writes {
		if _, err := operation.Stage(ctx, item.key, item.mode, bytes.NewReader(item.content)); err != nil {
			return ApplyResult{}, rollback(err)
		}
	}
	for _, item := range deletions {
		if err := operation.Remove(item.Key); err != nil {
			return ApplyResult{}, rollback(err)
		}
	}
	if err := operation.Install(ctx, nil); err != nil {
		return ApplyResult{}, rollback(err)
	}
	if err := manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationFilesInstalled, payload, "", manager.now()); err != nil {
		return ApplyResult{}, rollback(err)
	}
	snapshot, err := configservice.NewAppliedSnapshot(plan.Configuration.TargetRevision, desired)
	if err != nil {
		return ApplyResult{}, rollback(err)
	}
	commit := storesqlite.ConfigurationCommit{
		OperationID: operationID, InstanceID: instance.ID,
		ExpectedRevision: instance.Applied.Revision, ExpectedDigest: instance.Applied.Digest,
		Snapshot: snapshot, ActiveArtifacts: active, DeletedArtifacts: deletions,
		RecoveryPayload: payload, UpdatedAt: manager.now().UTC().Truncate(time.Second),
	}
	if plan.Configuration.Impact.AddressRemap {
		commit.NewNetworkID, err = domain.GenerateUUID()
		if err != nil {
			return ApplyResult{}, rollback(err)
		}
		commit.AddressChanges, err = buildAddressChanges(clients, plan, commit.NewNetworkID, now)
		if err != nil {
			return ApplyResult{}, rollback(err)
		}
	}
	if err := manager.state.CommitConfigurationOperation(ctx, commit); err != nil {
		return ApplyResult{}, rollback(err)
	}
	if err := operation.Commit(nil); err != nil {
		return ApplyResult{}, err
	}
	result.Applied = true
	result.OperationID = operationID
	return result, nil
}

func (manager *Manager) prepareArtifacts(ctx context.Context, instance storesqlite.InstanceState, clients []storesqlite.ClientState, desired domain.Config, plan Plan) ([]writeSpec, []storesqlite.ArtifactDeletion, error) {
	writes := make([]writeSpec, 0, len(plan.Artifacts))
	deletions := make([]storesqlite.ArtifactDeletion, 0, len(plan.Artifacts))
	clientByID := make(map[string]storesqlite.ClientState, len(clients))
	for _, client := range clients {
		clientByID[client.Client.ID] = client
	}
	afterAddress := make(map[string]AddressIntent, len(plan.AddressChanges))
	for _, change := range plan.AddressChanges {
		afterAddress[change.Client.ID] = change.After
	}
	instanceArtifacts, err := manager.state.LoadInstanceArtifacts(ctx, instance.ID)
	if err != nil {
		return nil, nil, err
	}
	var shared *profileMaterial
	layout, err := ipam.NewIPv4Layout(desired.IPv4.Network, desired.IPv4.DynamicPoolSize)
	if err != nil {
		return nil, nil, err
	}
	for _, impact := range plan.Artifacts {
		if impact.Action == "delete" {
			deletions = append(deletions, storesqlite.ArtifactDeletion{OwnerKind: impact.OwnerKind, OwnerID: impact.OwnerID, Key: impact.Key})
			continue
		}
		var content []byte
		switch impact.Kind {
		case "server-config":
			content, err = manager.renderer.Server(desired, manager.paths)
		case "profile":
			client, exists := clientByID[impact.OwnerID]
			if !exists {
				return nil, nil, fmt.Errorf("profile owner %s is unavailable", impact.OwnerID)
			}
			if shared == nil {
				shared, err = manager.loadProfileMaterial(ctx, instance, instanceArtifacts)
			}
			if err == nil {
				content, err = manager.renderProfile(ctx, desired, client, *shared)
			}
		case "ccd":
			intent, exists := afterAddress[impact.OwnerID]
			if !exists || intent.Mode != "static" || intent.Address == nil {
				return nil, nil, fmt.Errorf("static CCD owner %s has no planned address", impact.OwnerID)
			}
			content = []byte(fmt.Sprintf("ifconfig-push %s %s\n", *intent.Address, layout.Netmask))
		default:
			return nil, nil, fmt.Errorf("unsupported configuration artifact kind %s", impact.Kind)
		}
		if err != nil {
			return nil, nil, err
		}
		metadata, err := configurationMetadata(impact.OwnerKind, impact.OwnerID, impact.Kind, impact.Key, content)
		if err != nil {
			return nil, nil, err
		}
		writes = append(writes, writeSpec{key: impact.Key, mode: 0o600, content: content, metadata: metadata})
	}
	return writes, deletions, nil
}

type profileMaterial struct {
	ca  []byte
	tls []byte
}

func (manager *Manager) loadProfileMaterial(ctx context.Context, instance storesqlite.InstanceState, metadata []storesqlite.ArtifactMetadata) (*profileMaterial, error) {
	ca, err := manager.readVerified(ctx, metadata, "ca-cert")
	if err != nil {
		return nil, err
	}
	tls, err := manager.readVerified(ctx, metadata, "tls-crypt")
	if err != nil {
		return nil, err
	}
	info, err := pki.ValidateCACertificate(ca, manager.now())
	if err != nil || info.Fingerprint != instance.CAFingerprint {
		return nil, fmt.Errorf("profile CA does not match SQLite authority")
	}
	if err := pki.ValidateTLSCryptKey(filepath.Join(manager.paths.DataDir, "secrets", "tls-crypt.key")); err != nil {
		return nil, err
	}
	return &profileMaterial{ca: ca, tls: tls}, nil
}

func (manager *Manager) renderProfile(ctx context.Context, desired domain.Config, client storesqlite.ClientState, shared profileMaterial) ([]byte, error) {
	certificate, err := manager.readVerified(ctx, client.Artifacts, "client-cert")
	if err != nil {
		return nil, err
	}
	key, err := manager.readVerified(ctx, client.Artifacts, "client-key")
	if err != nil {
		return nil, err
	}
	if _, err := pki.ValidateClientMaterial(shared.ca, certificate, key, client.Client.ID, manager.now()); err != nil {
		return nil, err
	}
	return manager.renderer.Client(desired, manager.paths, render.ClientMaterial{ID: client.Client.ID, Name: client.Client.Name, CACert: string(shared.ca), Certificate: string(certificate), PrivateKey: string(key), TLSCryptKey: string(shared.tls)})
}

func (manager *Manager) readVerified(ctx context.Context, metadata []storesqlite.ArtifactMetadata, kind string) ([]byte, error) {
	var selected *storesqlite.ArtifactMetadata
	for index := range metadata {
		if metadata[index].Kind == kind && metadata[index].Status == storesqlite.ArtifactActive {
			if selected != nil {
				return nil, fmt.Errorf("multiple active %s artifacts", kind)
			}
			selected = &metadata[index]
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("active %s artifact is missing", kind)
	}
	content, reference, err := manager.artifacts.Read(ctx, selected.Key)
	if err != nil {
		return nil, err
	}
	if reference.Digest != selected.Digest {
		return nil, fmt.Errorf("%s artifact digest differs from SQLite", kind)
	}
	return content, nil
}

func configurationMetadata(ownerKind, ownerID, kind, key string, content []byte) (storesqlite.ArtifactMetadata, error) {
	id, err := domain.GenerateUUID()
	if err != nil {
		return storesqlite.ArtifactMetadata{}, err
	}
	return storesqlite.ArtifactMetadata{ID: id, OwnerKind: ownerKind, OwnerID: ownerID, Kind: kind, Key: key, Digest: sha256.Sum256(content), Status: storesqlite.ArtifactActive}, nil
}

func buildAddressChanges(clients []storesqlite.ClientState, plan Plan, networkID string, now time.Time) ([]storesqlite.ConfigurationAddressChange, error) {
	planned := make(map[string]AddressIntent, len(plan.AddressChanges))
	for _, change := range plan.AddressChanges {
		planned[change.Client.ID] = change.After
	}
	values := make([]storesqlite.ConfigurationAddressChange, 0, len(plan.AddressChanges))
	for _, client := range clients {
		if client.Assignment == nil {
			continue
		}
		intent, exists := planned[client.Client.ID]
		if !exists {
			return nil, fmt.Errorf("client %s is missing from address plan", client.Client.Name)
		}
		assignmentID, err := domain.GenerateUUID()
		if err != nil {
			return nil, err
		}
		assignment := storesqlite.AddressAssignment{ID: assignmentID, NetworkID: networkID, Kind: intent.Mode, CreatedAt: now, UpdatedAt: now}
		if intent.Address != nil {
			address, err := domain.ParseAddress(*intent.Address)
			if err != nil {
				return nil, err
			}
			assignment.Address = &address
		}
		values = append(values, storesqlite.ConfigurationAddressChange{ClientID: client.Client.ID, Name: client.Client.Name, ClientStatus: client.Client.Status, Assignment: assignment})
	}
	return values, nil
}
