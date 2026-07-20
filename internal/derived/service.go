// Package derived refreshes file artifacts from SQLite authority.
package derived

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
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

type StateStore interface {
	LoadInstance(context.Context, string) (storesqlite.InstanceState, error)
	LoadClient(context.Context, string, string) (storesqlite.ClientState, error)
	PrepareOperation(context.Context, storesqlite.Operation) error
	AdvanceOperation(context.Context, string, storesqlite.OperationState, json.RawMessage, string, time.Time) error
	CommitArtifactOperation(context.Context, string, []storesqlite.ArtifactMetadata, []storesqlite.ArtifactDeletion, json.RawMessage, time.Time) error
}

type Service struct {
	state     StateStore
	artifacts *artifact.LocalStore
	renderer  render.Renderer
	paths     render.Paths
	now       func() time.Time
}

type RefreshResult struct {
	OperationID string
	Written     []string
	Deleted     []string
}

func NewService(state StateStore, artifacts *artifact.LocalStore, renderer render.Renderer, paths render.Paths) (*Service, error) {
	if state == nil || artifacts == nil {
		return nil, fmt.Errorf("state and artifact stores are required")
	}
	if artifacts.Root() != paths.DataDir {
		return nil, fmt.Errorf("artifact root must match render data directory")
	}
	return &Service{state: state, artifacts: artifacts, renderer: renderer, paths: paths, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (service *Service) RefreshServer(ctx context.Context, instanceID string) (RefreshResult, error) {
	if !domain.ValidUUID(instanceID) {
		return RefreshResult{}, fmt.Errorf("invalid instance UUID")
	}
	lock, err := artifact.AcquireLock(ctx, filepath.Join(service.paths.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return RefreshResult{}, err
	}
	defer lock.Release()
	instance, err := service.state.LoadInstance(ctx, instanceID)
	if err != nil {
		return RefreshResult{}, err
	}
	content, err := service.renderer.Server(instance.Applied.Config, service.paths)
	if err != nil {
		return RefreshResult{}, err
	}
	metadata, err := newMetadata("instance", instanceID, "server-config", "server/server.conf", content)
	if err != nil {
		return RefreshResult{}, err
	}
	return service.apply(ctx, instanceID, "artifacts.server.refresh", []writeSpec{{key: metadata.Key, mode: 0o600, data: content, metadata: metadata}}, nil, nil)
}

func (service *Service) RefreshClient(ctx context.Context, instanceID, clientID string) (RefreshResult, error) {
	if !domain.ValidUUID(instanceID) || !domain.ValidUUID(clientID) {
		return RefreshResult{}, fmt.Errorf("invalid instance or client UUID")
	}
	lock, err := artifact.AcquireLock(ctx, filepath.Join(service.paths.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return RefreshResult{}, err
	}
	defer lock.Release()
	instance, err := service.state.LoadInstance(ctx, instanceID)
	if err != nil {
		return RefreshResult{}, err
	}
	client, err := service.state.LoadClient(ctx, instanceID, clientID)
	if err != nil {
		return RefreshResult{}, err
	}
	if client.Client.Status == domain.ClientDeleted {
		return RefreshResult{}, fmt.Errorf("deleted clients do not have derived artifacts")
	}
	pkiDirectory := filepath.Join(service.paths.DataDir, "pki")
	caInfo, err := pki.ValidateCA(pkiDirectory, service.now())
	if err != nil {
		return RefreshResult{}, err
	}
	if caInfo.Fingerprint != instance.CAFingerprint {
		return RefreshResult{}, fmt.Errorf("PKI CA fingerprint does not match SQLite instance state")
	}
	certificateInfo, err := pki.ValidateClient(pkiDirectory, clientID, service.now())
	if err != nil {
		return RefreshResult{}, err
	}
	if err := pki.ValidateTLSCryptKey(filepath.Join(service.paths.DataDir, "secrets", "tls-crypt.key")); err != nil {
		return RefreshResult{}, err
	}
	ca, _, err := service.artifacts.Read(ctx, "pki/ca.crt")
	if err != nil {
		return RefreshResult{}, err
	}
	certificate, certificateReference, err := service.artifacts.Read(ctx, "pki/issued/"+clientID+".crt")
	if err != nil {
		return RefreshResult{}, err
	}
	privateKey, keyReference, err := service.artifacts.Read(ctx, "pki/private/"+clientID+".key")
	if err != nil {
		return RefreshResult{}, err
	}
	tlsCrypt, _, err := service.artifacts.Read(ctx, "secrets/tls-crypt.key")
	if err != nil {
		return RefreshResult{}, err
	}
	profile, err := service.renderer.Client(instance.Applied.Config, service.paths, render.ClientMaterial{
		ID:          clientID,
		Name:        client.Client.Name,
		CACert:      string(ca),
		Certificate: string(certificate),
		PrivateKey:  string(privateKey),
		TLSCryptKey: string(tlsCrypt),
	})
	if err != nil {
		return RefreshResult{}, err
	}
	directory := "active"
	opposite := "revoked"
	if client.Client.Status == domain.ClientRevoked {
		directory, opposite = opposite, directory
	}
	profileKey := "clients/" + directory + "/" + client.Client.Name + ".ovpn"
	profileMetadata, err := newMetadata("client", clientID, "profile", profileKey, profile)
	if err != nil {
		return RefreshResult{}, err
	}
	certificateMetadata, err := referenceMetadata(clientID, "client-cert", "pki/issued/"+clientID+".crt", certificateReference, &certificateInfo)
	if err != nil {
		return RefreshResult{}, err
	}
	keyMetadata, err := referenceMetadata(clientID, "client-key", "pki/private/"+clientID+".key", keyReference, nil)
	if err != nil {
		return RefreshResult{}, err
	}
	writes := []writeSpec{{key: profileKey, mode: 0o600, data: profile, metadata: profileMetadata}}
	active := []storesqlite.ArtifactMetadata{certificateMetadata, keyMetadata}
	deletions := []storesqlite.ArtifactDeletion{{OwnerKind: "client", OwnerID: clientID, Key: "clients/" + opposite + "/" + client.Client.Name + ".ovpn"}}
	ccdKey := "ccd/" + clientID
	if client.Client.Status == domain.ClientActive && client.Assignment != nil && client.Assignment.Kind == "static" && client.Assignment.Address != nil {
		layout, err := ipam.NewIPv4Layout(instance.Applied.Config.IPv4.Network, instance.Applied.Config.IPv4.DynamicPoolSize)
		if err != nil {
			return RefreshResult{}, err
		}
		content := []byte(fmt.Sprintf("ifconfig-push %s %s\n", client.Assignment.Address, layout.Netmask))
		metadata, err := newMetadata("client", clientID, "ccd", ccdKey, content)
		if err != nil {
			return RefreshResult{}, err
		}
		writes = append(writes, writeSpec{key: ccdKey, mode: 0o600, data: content, metadata: metadata})
	} else {
		deletions = append(deletions, storesqlite.ArtifactDeletion{OwnerKind: "client", OwnerID: clientID, Key: ccdKey})
	}
	return service.apply(ctx, instanceID, "artifacts.client.refresh", writes, deletions, append(active, metadataFromWrites(writes)...))
}

type writeSpec struct {
	key      string
	mode     os.FileMode
	data     []byte
	metadata storesqlite.ArtifactMetadata
}

type recoveryView struct {
	Version int      `json:"version"`
	Kind    string   `json:"kind"`
	Written []string `json:"written"`
	Deleted []string `json:"deleted"`
	Digests []string `json:"digests"`
}

func (service *Service) apply(ctx context.Context, instanceID, kind string, writes []writeSpec, deletions []storesqlite.ArtifactDeletion, active []storesqlite.ArtifactMetadata) (RefreshResult, error) {
	if active == nil {
		active = metadataFromWrites(writes)
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return RefreshResult{}, err
	}
	view := recoveryView{Version: 1, Kind: kind, Written: make([]string, 0, len(writes)), Deleted: make([]string, 0, len(deletions)), Digests: make([]string, 0, len(active))}
	for _, item := range writes {
		view.Written = append(view.Written, item.key)
	}
	for _, item := range deletions {
		view.Deleted = append(view.Deleted, item.Key)
	}
	for _, item := range active {
		view.Digests = append(view.Digests, hex.EncodeToString(item.Digest[:]))
	}
	payload, err := json.Marshal(view)
	if err != nil {
		return RefreshResult{}, err
	}
	now := service.now().UTC().Truncate(time.Second)
	if err := service.state.PrepareOperation(ctx, storesqlite.Operation{ID: operationID, InstanceID: instanceID, Kind: kind, State: storesqlite.OperationPrepared, PayloadVersion: 1, RecoveryPayload: payload, CreatedAt: now, UpdatedAt: now}); err != nil {
		return RefreshResult{}, err
	}
	operation, err := service.artifacts.BeginOperation(operationID)
	if err != nil {
		_ = service.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, payload, "", now.Add(time.Second))
		return RefreshResult{}, err
	}
	rollback := func(cause error) error {
		artifactErr := operation.Rollback()
		journalErr := service.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, payload, "", service.now())
		return errors.Join(cause, artifactErr, journalErr)
	}
	for _, item := range writes {
		if _, err := operation.Stage(ctx, item.key, item.mode, bytes.NewReader(item.data)); err != nil {
			return RefreshResult{}, rollback(err)
		}
	}
	for _, item := range deletions {
		if err := operation.Remove(item.Key); err != nil {
			return RefreshResult{}, rollback(err)
		}
	}
	if err := operation.Install(ctx, nil); err != nil {
		return RefreshResult{}, rollback(err)
	}
	if err := service.state.AdvanceOperation(ctx, operationID, storesqlite.OperationFilesInstalled, payload, "", service.now()); err != nil {
		return RefreshResult{}, rollback(err)
	}
	if err := service.state.CommitArtifactOperation(ctx, operationID, active, deletions, payload, service.now()); err != nil {
		return RefreshResult{}, rollback(err)
	}
	if err := operation.Commit(nil); err != nil {
		return RefreshResult{}, err
	}
	deletedKeys := make([]string, 0, len(deletions))
	for _, item := range deletions {
		deletedKeys = append(deletedKeys, item.Key)
	}
	return RefreshResult{OperationID: operationID, Written: view.Written, Deleted: deletedKeys}, nil
}

func metadataFromWrites(writes []writeSpec) []storesqlite.ArtifactMetadata {
	values := make([]storesqlite.ArtifactMetadata, 0, len(writes))
	for _, item := range writes {
		values = append(values, item.metadata)
	}
	return values
}

func newMetadata(ownerKind, ownerID, kind, key string, content []byte) (storesqlite.ArtifactMetadata, error) {
	id, err := domain.GenerateUUID()
	if err != nil {
		return storesqlite.ArtifactMetadata{}, err
	}
	value := storesqlite.ArtifactMetadata{ID: id, OwnerKind: ownerKind, OwnerID: ownerID, Kind: kind, Key: key, Digest: sha256.Sum256(content), Status: storesqlite.ArtifactActive}
	return value, nil
}

func referenceMetadata(clientID, kind, key string, reference artifact.Reference, certificate *pki.CertificateInfo) (storesqlite.ArtifactMetadata, error) {
	id, err := domain.GenerateUUID()
	if err != nil {
		return storesqlite.ArtifactMetadata{}, err
	}
	value := storesqlite.ArtifactMetadata{ID: id, OwnerKind: "client", OwnerID: clientID, Kind: kind, Key: key, Digest: reference.Digest, Status: storesqlite.ArtifactActive}
	if certificate != nil {
		value.CertificateSerial = certificate.Serial
		value.CertificateFingerprint = append([]byte(nil), certificate.Fingerprint[:]...)
	}
	return value, nil
}
