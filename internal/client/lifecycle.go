package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func (manager *Manager) Revoke(ctx context.Context, selector Selector, releaseAddress bool) (MutationResult, error) {
	lock, err := artifact.AcquireLock(ctx, filepath.Join(manager.paths.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return MutationResult{}, err
	}
	defer lock.Release()
	instance, state, err := manager.query.Select(ctx, selector)
	if err != nil {
		return MutationResult{}, err
	}
	if state.Client.Status != domain.ClientActive {
		return MutationResult{}, fmt.Errorf("%w: client is not active", ErrInvalidRequest)
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return MutationResult{}, err
	}
	now := manager.now().UTC().Truncate(time.Second)
	workspaceKey := ".client-work-" + operationID
	recovery := mutationRecovery{Version: 1, Kind: "client.revoke", ClientID: state.Client.ID, Name: state.Client.Name, IPv4: fmt.Sprintf("release=%t", releaseAddress), Workspace: workspaceKey, Written: []string{}, Deleted: []string{}}
	payload, _ := json.Marshal(recovery)
	fileOperation, workspace, rollback, err := manager.startLifecycle(ctx, operationID, instance.ID, workspaceKey, "client.revoke", payload, now)
	if err != nil {
		return MutationResult{}, err
	}
	if err := cloneTree(filepath.Join(manager.paths.DataDir, "pki"), filepath.Join(workspace, "pki")); err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	if err := manager.pki.RevokeClient(ctx, filepath.Join(workspace, "pki"), state.Client.ID); err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	pkiReferences, pkiKeys, err := stageTree(ctx, fileOperation, filepath.Join(workspace, "pki"), "pki")
	if err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	recovery.Written = append(recovery.Written, pkiKeys...)
	oldProfileKey := "clients/active/" + state.Client.Name + ".ovpn"
	if err := verifyCurrentProfile(ctx, manager.artifacts, state.Artifacts, oldProfileKey); err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	profile, _, err := manager.artifacts.Read(ctx, oldProfileKey)
	if err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	newProfileKey := "clients/revoked/" + state.Client.Name + ".ovpn"
	profileReference, err := fileOperation.Stage(ctx, newProfileKey, 0o600, bytes.NewReader(profile))
	if err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	if err := fileOperation.Remove(oldProfileKey); err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	recovery.Written = append(recovery.Written, newProfileKey)
	recovery.Deleted = append(recovery.Deleted, oldProfileKey)
	active := make([]storesqlite.ArtifactMetadata, 0, 2)
	profileMetadata, err := newReferenceMetadata(state.Client.ID, "profile", newProfileKey, profileReference, nil)
	if err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	active = append(active, profileMetadata)
	crlMetadata, err := manager.crlMetadata(instance.ID, pkiReferences)
	if err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	active = append(active, crlMetadata)
	deleted := []storesqlite.ArtifactDeletion{{OwnerKind: "client", OwnerID: state.Client.ID, Key: oldProfileKey}}
	if ccd, ok := activeArtifact(state.Artifacts, "ccd"); ok {
		if err := fileOperation.Remove(ccd.Key); err != nil {
			return MutationResult{}, rollback(err, payload)
		}
		recovery.Deleted = append(recovery.Deleted, ccd.Key)
		deleted = append(deleted, storesqlite.ArtifactDeletion{OwnerKind: "client", OwnerID: state.Client.ID, Key: ccd.Key})
	}
	payload, _ = json.Marshal(recovery)
	commit := func(at time.Time) error {
		return manager.state.CommitRevokeClientOperation(ctx, operationID, instance.ID, state.Client.ID, state.Client.Name, releaseAddress, active, deleted, payload, at)
	}
	if err := manager.finishLifecycle(ctx, operationID, fileOperation, workspace, payload, rollback, commit); err != nil {
		return MutationResult{}, err
	}
	loaded, err := manager.state.LoadClient(ctx, instance.ID, state.Client.ID)
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{Version: 1, OperationID: operationID, Client: newView(loaded), KickRequired: true}, nil
}

func (manager *Manager) Reissue(ctx context.Context, selector Selector, ipv4 string) (MutationResult, error) {
	lock, err := artifact.AcquireLock(ctx, filepath.Join(manager.paths.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return MutationResult{}, err
	}
	defer lock.Release()
	instance, state, err := manager.query.Select(ctx, selector)
	if err != nil {
		return MutationResult{}, err
	}
	clients, err := manager.state.ListClients(ctx, instance.ID)
	if err != nil {
		return MutationResult{}, err
	}
	now := manager.now().UTC().Truncate(time.Second)
	assignment, err := planReissueAssignment(instance, clients, state, ipv4, now)
	if err != nil {
		return MutationResult{}, err
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return MutationResult{}, err
	}
	workspaceKey := ".client-work-" + operationID
	recovery := mutationRecovery{Version: 1, Kind: "client.reissue", ClientID: state.Client.ID, Name: state.Client.Name, IPv4: ipv4, Workspace: workspaceKey, Written: []string{}, Deleted: []string{}}
	payload, _ := json.Marshal(recovery)
	fileOperation, workspace, rollback, err := manager.startLifecycle(ctx, operationID, instance.ID, workspaceKey, "client.reissue", payload, now)
	if err != nil {
		return MutationResult{}, err
	}
	if err := cloneTree(filepath.Join(manager.paths.DataDir, "pki"), filepath.Join(workspace, "pki")); err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	if state.Client.Status == domain.ClientActive {
		if err := manager.pki.RevokeClient(ctx, filepath.Join(workspace, "pki"), state.Client.ID); err != nil {
			return MutationResult{}, rollback(err, payload)
		}
	}
	certificateInfo, err := manager.pki.ReissueClient(ctx, filepath.Join(workspace, "pki"), state.Client.ID)
	if err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	pkiReferences, pkiKeys, err := stageTree(ctx, fileOperation, filepath.Join(workspace, "pki"), "pki")
	if err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	recovery.Written = append(recovery.Written, pkiKeys...)
	profile, err := manager.renderProfile(ctx, instance, filepath.Join(workspace, "pki"), state.Client.ID, state.Client.Name)
	if err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	profileKey := "clients/active/" + state.Client.Name + ".ovpn"
	profileReference, err := fileOperation.Stage(ctx, profileKey, 0o600, bytes.NewReader(profile))
	if err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	recovery.Written = append(recovery.Written, profileKey)
	active := make([]storesqlite.ArtifactMetadata, 0, 5)
	for _, description := range []struct {
		kind        string
		key         string
		reference   artifact.Reference
		certificate *pki.CertificateInfo
	}{
		{kind: "client-cert", key: "pki/issued/" + state.Client.ID + ".crt", reference: pkiReferences["pki/issued/"+state.Client.ID+".crt"], certificate: &certificateInfo},
		{kind: "client-key", key: "pki/private/" + state.Client.ID + ".key", reference: pkiReferences["pki/private/"+state.Client.ID+".key"]},
		{kind: "profile", key: profileKey, reference: profileReference},
	} {
		if description.reference.Key != description.key {
			return MutationResult{}, rollback(fmt.Errorf("staged PKI output %s is missing", description.key), payload)
		}
		metadata, err := newReferenceMetadata(state.Client.ID, description.kind, description.key, description.reference, description.certificate)
		if err != nil {
			return MutationResult{}, rollback(err, payload)
		}
		active = append(active, metadata)
	}
	deleted := make([]storesqlite.ArtifactDeletion, 0, 2)
	if state.Client.Status == domain.ClientRevoked {
		oldProfile := "clients/revoked/" + state.Client.Name + ".ovpn"
		if err := fileOperation.Remove(oldProfile); err != nil {
			return MutationResult{}, rollback(err, payload)
		}
		recovery.Deleted = append(recovery.Deleted, oldProfile)
		deleted = append(deleted, storesqlite.ArtifactDeletion{OwnerKind: "client", OwnerID: state.Client.ID, Key: oldProfile})
	}
	if state.Client.Status == domain.ClientActive {
		crlMetadata, err := manager.crlMetadata(instance.ID, pkiReferences)
		if err != nil {
			return MutationResult{}, rollback(err, payload)
		}
		active = append(active, crlMetadata)
	}
	ccdKey := "ccd/" + state.Client.ID
	if assignment.Kind == "static" {
		layout, _ := ipam.NewIPv4Layout(instance.Applied.Config.IPv4.Network, instance.Applied.Config.IPv4.DynamicPoolSize)
		ccd := []byte(fmt.Sprintf("ifconfig-push %s %s\n", assignment.Address, layout.Netmask))
		reference, err := fileOperation.Stage(ctx, ccdKey, 0o600, bytes.NewReader(ccd))
		if err != nil {
			return MutationResult{}, rollback(err, payload)
		}
		metadata, err := newReferenceMetadata(state.Client.ID, "ccd", ccdKey, reference, nil)
		if err != nil {
			return MutationResult{}, rollback(err, payload)
		}
		active = append(active, metadata)
		recovery.Written = append(recovery.Written, ccdKey)
	} else if _, ok := activeArtifact(state.Artifacts, "ccd"); ok {
		if err := fileOperation.Remove(ccdKey); err != nil {
			return MutationResult{}, rollback(err, payload)
		}
		deleted = append(deleted, storesqlite.ArtifactDeletion{OwnerKind: "client", OwnerID: state.Client.ID, Key: ccdKey})
		recovery.Deleted = append(recovery.Deleted, ccdKey)
	}
	payload, _ = json.Marshal(recovery)
	commit := func(at time.Time) error {
		return manager.state.CommitReissueClientOperation(ctx, operationID, instance.ID, state.Client.ID, state.Client.Name, state.Client.Status, *assignment, active, deleted, payload, at)
	}
	if err := manager.finishLifecycle(ctx, operationID, fileOperation, workspace, payload, rollback, commit); err != nil {
		return MutationResult{}, err
	}
	loaded, err := manager.state.LoadClient(ctx, instance.ID, state.Client.ID)
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{Version: 1, OperationID: operationID, Client: newView(loaded), KickRequired: true, ProfileRedistributionRequired: true}, nil
}

func (manager *Manager) Delete(ctx context.Context, selector Selector) (MutationResult, error) {
	lock, err := artifact.AcquireLock(ctx, filepath.Join(manager.paths.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return MutationResult{}, err
	}
	defer lock.Release()
	instance, state, err := manager.query.Select(ctx, selector)
	if err != nil {
		return MutationResult{}, err
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return MutationResult{}, err
	}
	now := manager.now().UTC().Truncate(time.Second)
	workspaceKey := ".client-work-" + operationID
	recovery := mutationRecovery{Version: 1, Kind: "client.delete", ClientID: state.Client.ID, Name: state.Client.Name, Workspace: workspaceKey, Written: []string{}, Deleted: []string{}}
	payload, _ := json.Marshal(recovery)
	fileOperation, workspace, rollback, err := manager.startLifecycle(ctx, operationID, instance.ID, workspaceKey, "client.delete", payload, now)
	if err != nil {
		return MutationResult{}, err
	}
	sourcePKI := filepath.Join(manager.paths.DataDir, "pki")
	workspacePKI := filepath.Join(workspace, "pki")
	if err := cloneTree(sourcePKI, workspacePKI); err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	if state.Client.Status == domain.ClientActive {
		if err := manager.pki.RevokeClient(ctx, workspacePKI, state.Client.ID); err != nil {
			return MutationResult{}, rollback(err, payload)
		}
	}
	for _, key := range []string{filepath.Join("issued", state.Client.ID+".crt"), filepath.Join("private", state.Client.ID+".key"), filepath.Join("reqs", state.Client.ID+".req")} {
		if err := os.Remove(filepath.Join(workspacePKI, key)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return MutationResult{}, rollback(err, payload)
		}
	}
	pkiReferences, pkiKeys, removedPKI, err := stageTreeDiff(ctx, fileOperation, sourcePKI, workspacePKI, "pki")
	if err != nil {
		return MutationResult{}, rollback(err, payload)
	}
	recovery.Written = append(recovery.Written, pkiKeys...)
	recovery.Deleted = append(recovery.Deleted, removedPKI...)
	active := make([]storesqlite.ArtifactMetadata, 0, 1)
	if state.Client.Status == domain.ClientActive {
		crlMetadata, err := manager.crlMetadata(instance.ID, pkiReferences)
		if err != nil {
			return MutationResult{}, rollback(err, payload)
		}
		active = append(active, crlMetadata)
	}
	deleted := make([]storesqlite.ArtifactDeletion, 0, len(state.Artifacts))
	for _, value := range state.Artifacts {
		if value.Status != storesqlite.ArtifactActive || value.OwnerKind != "client" {
			continue
		}
		if value.Kind == "profile" || value.Kind == "ccd" {
			if err := fileOperation.Remove(value.Key); err != nil {
				return MutationResult{}, rollback(err, payload)
			}
			recovery.Deleted = append(recovery.Deleted, value.Key)
		}
		deleted = append(deleted, storesqlite.ArtifactDeletion{OwnerKind: "client", OwnerID: state.Client.ID, Key: value.Key})
	}
	payload, _ = json.Marshal(recovery)
	commit := func(at time.Time) error {
		return manager.state.CommitDeleteClientOperation(ctx, operationID, instance.ID, state.Client.ID, state.Client.Name, state.Client.Status, active, deleted, payload, at)
	}
	if err := manager.finishLifecycle(ctx, operationID, fileOperation, workspace, payload, rollback, commit); err != nil {
		return MutationResult{}, err
	}
	loaded, err := manager.state.LoadClient(ctx, instance.ID, state.Client.ID)
	if err != nil {
		return MutationResult{}, err
	}
	// A revoked client may still have a stale session when the runtime was
	// unavailable during revoke. Deletion must therefore attempt convergence
	// for every selectable non-deleted client state.
	return MutationResult{Version: 1, OperationID: operationID, Client: newView(loaded), KickRequired: true}, nil
}

type lifecycleRollback func(error, json.RawMessage) error

func (manager *Manager) startLifecycle(ctx context.Context, operationID, instanceID, workspaceKey, kind string, payload json.RawMessage, now time.Time) (*artifact.Operation, string, lifecycleRollback, error) {
	if err := manager.prepare(ctx, operationID, instanceID, kind, payload, now); err != nil {
		return nil, "", nil, err
	}
	fileOperation, err := manager.artifacts.BeginOperation(operationID)
	if err != nil {
		_ = manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, payload, "", manager.now())
		return nil, "", nil, err
	}
	workspace := filepath.Join(manager.paths.DataDir, workspaceKey)
	rollback := func(cause error, currentPayload json.RawMessage) error {
		return errors.Join(cause, os.RemoveAll(workspace), fileOperation.Rollback(), manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, currentPayload, "", manager.now()))
	}
	return fileOperation, workspace, rollback, nil
}

func (manager *Manager) finishLifecycle(ctx context.Context, operationID string, fileOperation *artifact.Operation, workspace string, payload json.RawMessage, rollback lifecycleRollback, commit func(time.Time) error) error {
	if err := fileOperation.Install(ctx, nil); err != nil {
		return rollback(err, payload)
	}
	if err := manager.state.AdvanceOperation(ctx, operationID, storesqlite.OperationFilesInstalled, payload, "", manager.now()); err != nil {
		return rollback(err, payload)
	}
	if err := commit(manager.now()); err != nil {
		return rollback(err, payload)
	}
	if err := fileOperation.Commit(nil); err != nil {
		return err
	}
	return os.RemoveAll(workspace)
}

func (manager *Manager) crlMetadata(instanceID string, references map[string]artifact.Reference) (storesqlite.ArtifactMetadata, error) {
	reference := references["pki/crl.pem"]
	if reference.Key != "pki/crl.pem" {
		return storesqlite.ArtifactMetadata{}, fmt.Errorf("staged CRL is missing")
	}
	return newOwnerReferenceMetadata("instance", instanceID, "crl", reference.Key, reference, nil)
}

func activeArtifact(values []storesqlite.ArtifactMetadata, kind string) (storesqlite.ArtifactMetadata, bool) {
	for _, value := range values {
		if value.Kind == kind && value.Status == storesqlite.ArtifactActive {
			return value, true
		}
	}
	return storesqlite.ArtifactMetadata{}, false
}

func planReissueAssignment(instance storesqlite.InstanceState, clients []storesqlite.ClientState, state storesqlite.ClientState, selection string, now time.Time) (*storesqlite.AddressAssignment, error) {
	if selection == "" && state.Assignment != nil {
		value := *state.Assignment
		value.Status = storesqlite.AssignmentActive
		value.UpdatedAt = now
		value.ReleasedAt = nil
		return &value, nil
	}
	if selection == "" {
		selection = "auto"
	}
	others := make([]storesqlite.ClientState, 0, len(clients)-1)
	for _, candidate := range clients {
		if candidate.Client.ID != state.Client.ID {
			others = append(others, candidate)
		}
	}
	return planCreateAssignment(instance, others, selection, now)
}

func stageTreeDiff(ctx context.Context, operation *artifact.Operation, source, candidate, prefix string) (map[string]artifact.Reference, []string, []string, error) {
	references, written, err := stageTree(ctx, operation, candidate, prefix)
	if err != nil {
		return nil, nil, nil, err
	}
	removed := make([]string, 0)
	err = filepath.WalkDir(source, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(source, filePath)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(candidate, relative)); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		key := filepath.ToSlash(filepath.Join(prefix, relative))
		if err := operation.Remove(key); err != nil {
			return err
		}
		removed = append(removed, key)
		return nil
	})
	return references, written, removed, err
}
