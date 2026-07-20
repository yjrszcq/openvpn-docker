// Package recovery restores file artifacts only from cryptographically verified evidence.
package recovery

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
	"sort"
	"strings"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

type Candidate struct {
	Action  string `json:"action"`
	OwnerID string `json:"ownerId,omitempty"`
	Source  string `json:"source"`
}

type Assessment struct {
	Version int         `json:"version"`
	Ready   []Candidate `json:"ready"`
}

type profileEvidence struct {
	clientID   string
	clientName string
	path       string
	ca         []byte
	cert       []byte
	key        []byte
	tlsCrypt   []byte
	certInfo   pki.CertificateInfo
}

type material struct {
	mode     os.FileMode
	owner    string
	ownerID  string
	kind     string
	key      string
	data     []byte
	certInfo *pki.CertificateInfo
}

type collected struct {
	instance storesqlite.InstanceState
	profiles map[string]profileEvidence
	ca       *profileEvidence
	tls      *profileEvidence
}

type Service struct {
	state     *storesqlite.Store
	artifacts *artifact.LocalStore
	dataDir   string
	now       func() time.Time
}

func NewService(state *storesqlite.Store, artifacts *artifact.LocalStore, dataDir string) (*Service, error) {
	if state == nil || artifacts == nil || artifacts.Root() != dataDir || dataDir == "" || !filepath.IsAbs(dataDir) || filepath.Clean(dataDir) != dataDir {
		return nil, fmt.Errorf("matching state, artifact, and data directory are required")
	}
	return &Service{state: state, artifacts: artifacts, dataDir: dataDir, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (service *Service) Assess(ctx context.Context, report statecontrol.Report) (Assessment, error) {
	values, err := service.collect(ctx)
	if err != nil {
		return Assessment{}, err
	}
	assessment := Assessment{Version: 1, Ready: []Candidate{}}
	seen := map[string]struct{}{}
	for _, issue := range report.Issues {
		if issue.Severity != statecontrol.SeverityRecoverable {
			continue
		}
		ready := false
		source := ""
		switch issue.Action {
		case "RECOVER_CA_CERT":
			ready = values.ca != nil
			if ready {
				source = values.ca.path
			}
		case "RECOVER_TLS_CRYPT":
			ready = values.tls != nil
			if ready {
				source = values.tls.path
			}
		case "RECOVER_CLIENT_IDENTITY":
			evidence, exists := values.profiles[issue.OwnerID]
			ready = exists
			if ready {
				source = evidence.path
			}
		}
		ownerID := issue.OwnerID
		if issue.Action != "RECOVER_CLIENT_IDENTITY" {
			ownerID = ""
		}
		identity := issue.Action + "\x00" + ownerID
		if !ready {
			continue
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		assessment.Ready = append(assessment.Ready, Candidate{Action: issue.Action, OwnerID: ownerID, Source: source})
	}
	sort.Slice(assessment.Ready, func(i, j int) bool {
		if assessment.Ready[i].Action != assessment.Ready[j].Action {
			return assessment.Ready[i].Action < assessment.Ready[j].Action
		}
		return assessment.Ready[i].OwnerID < assessment.Ready[j].OwnerID
	})
	return assessment, nil
}

func (service *Service) Recover(ctx context.Context, instanceID, action, ownerID string) (string, error) {
	if !domain.ValidUUID(instanceID) {
		return "", fmt.Errorf("invalid instance UUID")
	}
	lock, err := artifact.AcquireLock(ctx, filepath.Join(service.dataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return "", err
	}
	defer lock.Release()
	values, err := service.collect(ctx)
	if err != nil {
		return "", err
	}
	if values.instance.ID != instanceID {
		return "", fmt.Errorf("recovery instance mismatch")
	}
	var materials []material
	switch action {
	case "RECOVER_CA_CERT":
		if values.ca == nil {
			return "", fmt.Errorf("no mutually consistent CA profile evidence")
		}
		info, err := pki.ValidateCACertificate(values.ca.ca, service.now())
		if err != nil || info.Fingerprint != values.instance.CAFingerprint {
			return "", fmt.Errorf("CA recovery evidence is no longer valid: %w", err)
		}
		materials = []material{{mode: 0o644, owner: "instance", ownerID: instanceID, kind: "ca-cert", key: "pki/ca.crt", data: values.ca.ca, certInfo: &info}}
	case "RECOVER_TLS_CRYPT":
		if values.tls == nil {
			return "", fmt.Errorf("no mutually consistent tls-crypt profile evidence")
		}
		materials = []material{{mode: 0o600, owner: "instance", ownerID: instanceID, kind: "tls-crypt", key: "secrets/tls-crypt.key", data: values.tls.tlsCrypt}}
	case "RECOVER_CLIENT_IDENTITY":
		evidence, exists := values.profiles[ownerID]
		if !exists || !domain.ValidUUID(ownerID) {
			return "", fmt.Errorf("no trusted profile evidence for client %s", ownerID)
		}
		materials = []material{
			{mode: 0o644, owner: "client", ownerID: ownerID, kind: "client-cert", key: "pki/issued/" + ownerID + ".crt", data: evidence.cert, certInfo: &evidence.certInfo},
			{mode: 0o600, owner: "client", ownerID: ownerID, kind: "client-key", key: "pki/private/" + ownerID + ".key", data: evidence.key},
		}
	default:
		return "", fmt.Errorf("unsupported recovery action %s", action)
	}
	return service.install(ctx, instanceID, action, materials)
}

func (service *Service) collect(ctx context.Context) (collected, error) {
	instance, err := service.state.LoadOnlyInstance(ctx)
	if err != nil {
		return collected{}, err
	}
	instanceArtifacts, err := service.state.LoadInstanceArtifacts(ctx, instance.ID)
	if err != nil {
		return collected{}, err
	}
	instanceMetadata := activeByKind(instanceArtifacts)
	clients, err := service.state.ListClients(ctx, instance.ID)
	if err != nil {
		return collected{}, err
	}
	values := collected{instance: instance, profiles: map[string]profileEvidence{}}
	valid := make([]profileEvidence, 0, len(clients))
	for _, client := range clients {
		directory := "active"
		if client.Client.Status == domain.ClientRevoked {
			directory = "revoked"
		}
		key := "clients/" + directory + "/" + client.Client.Name + ".ovpn"
		data, _, err := service.artifacts.Read(ctx, key)
		if err != nil {
			continue
		}
		metadata := activeByKind(client.Artifacts)
		profileMetadata := metadata["profile"]
		if profileMetadata == nil || profileMetadata.Key != key || profileMetadata.Digest != sha256.Sum256(data) {
			continue
		}
		evidence, err := parseProfile(data, key, client.Client.ID, client.Client.Name, service.now())
		if err != nil || evidence.certInfo.Fingerprint == ([32]byte{}) {
			continue
		}
		caInfo, err := pki.ValidateCACertificate(evidence.ca, service.now())
		if err != nil || caInfo.Fingerprint != instance.CAFingerprint {
			continue
		}
		if !matchesMetadata(metadata["client-cert"], evidence.cert, &evidence.certInfo) || !matchesMetadata(metadata["client-key"], evidence.key, nil) {
			continue
		}
		values.profiles[client.Client.ID] = evidence
		valid = append(valid, evidence)
	}
	if evidence, ok := consensus(valid, func(value profileEvidence) []byte { return value.ca }, instanceMetadata["ca-cert"]); ok {
		values.ca = &evidence
	}
	if evidence, ok := consensus(valid, func(value profileEvidence) []byte { return value.tlsCrypt }, instanceMetadata["tls-crypt"]); ok {
		values.tls = &evidence
	}
	return values, nil
}

func parseProfile(data []byte, key, wantedID, wantedName string, now time.Time) (profileEvidence, error) {
	if len(data) == 0 || len(data) > artifact.MaxArtifactSize {
		return profileEvidence{}, fmt.Errorf("profile size is invalid")
	}
	text := string(data)
	if !hasExactComment(text, "# ovpn-client-id: ", wantedID) || !hasExactComment(text, "# ovpn-client-name: ", wantedName) {
		return profileEvidence{}, fmt.Errorf("profile identity comments do not match SQLite")
	}
	evidence := profileEvidence{clientID: wantedID, clientName: wantedName, path: key}
	var err error
	if evidence.ca, err = extractBlock(text, "ca"); err != nil {
		return profileEvidence{}, err
	}
	if evidence.cert, err = extractBlock(text, "cert"); err != nil {
		return profileEvidence{}, err
	}
	if evidence.key, err = extractBlock(text, "key"); err != nil {
		return profileEvidence{}, err
	}
	if evidence.tlsCrypt, err = extractBlock(text, "tls-crypt"); err != nil {
		return profileEvidence{}, err
	}
	if err := pki.ValidateTLSCryptData(evidence.tlsCrypt); err != nil {
		return profileEvidence{}, err
	}
	evidence.certInfo, err = pki.ValidateClientMaterial(evidence.ca, evidence.cert, evidence.key, wantedID, now)
	if err != nil {
		return profileEvidence{}, err
	}
	return evidence, nil
}

func extractBlock(profile, name string) ([]byte, error) {
	open, close := "<"+name+">", "</"+name+">"
	if strings.Count(profile, open) != 1 || strings.Count(profile, close) != 1 {
		return nil, fmt.Errorf("profile must contain one %s block", name)
	}
	start := strings.Index(profile, open) + len(open)
	end := strings.Index(profile[start:], close)
	if end < 0 {
		return nil, fmt.Errorf("profile %s block is not closed", name)
	}
	value := strings.TrimSpace(profile[start : start+end])
	if value == "" {
		return nil, fmt.Errorf("profile %s block is empty", name)
	}
	return []byte(value + "\n"), nil
}

func hasExactComment(profile, prefix, wanted string) bool {
	count := 0
	for _, line := range strings.Split(profile, "\n") {
		if strings.HasPrefix(line, prefix) {
			count++
			if strings.TrimSpace(strings.TrimPrefix(line, prefix)) != wanted {
				return false
			}
		}
	}
	return count == 1
}

func activeByKind(values []storesqlite.ArtifactMetadata) map[string]*storesqlite.ArtifactMetadata {
	result := map[string]*storesqlite.ArtifactMetadata{}
	for index := range values {
		if values[index].Status == storesqlite.ArtifactActive {
			result[values[index].Kind] = &values[index]
		}
	}
	return result
}

func matchesMetadata(metadata *storesqlite.ArtifactMetadata, data []byte, certificate *pki.CertificateInfo) bool {
	if metadata == nil {
		return true
	}
	if metadata.Digest != sha256.Sum256(data) {
		return false
	}
	return certificate == nil || (metadata.CertificateSerial == certificate.Serial && bytes.Equal(metadata.CertificateFingerprint, certificate.Fingerprint[:]))
}

func consensus(values []profileEvidence, selectData func(profileEvidence) []byte, metadata *storesqlite.ArtifactMetadata) (profileEvidence, bool) {
	var selected profileEvidence
	var digest [32]byte
	found := false
	for _, value := range values {
		data := selectData(value)
		if !matchesMetadata(metadata, data, nil) {
			continue
		}
		current := sha256.Sum256(data)
		if found && current != digest {
			return profileEvidence{}, false
		}
		if !found {
			selected, digest, found = value, current, true
		}
	}
	return selected, found
}

func (service *Service) install(ctx context.Context, instanceID, action string, materials []material) (string, error) {
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return "", err
	}
	type payloadEntry struct {
		Key    string `json:"key"`
		Digest string `json:"digest"`
	}
	payloadView := struct {
		Version int            `json:"version"`
		Action  string         `json:"action"`
		Entries []payloadEntry `json:"entries"`
	}{Version: 1, Action: action, Entries: []payloadEntry{}}
	metadata := make([]storesqlite.ArtifactMetadata, 0, len(materials))
	for _, item := range materials {
		digest := sha256.Sum256(item.data)
		payloadView.Entries = append(payloadView.Entries, payloadEntry{Key: item.key, Digest: hex.EncodeToString(digest[:])})
		id, err := domain.GenerateUUID()
		if err != nil {
			return "", err
		}
		value := storesqlite.ArtifactMetadata{ID: id, OwnerKind: item.owner, OwnerID: item.ownerID, Kind: item.kind, Key: item.key, Digest: digest, Status: storesqlite.ArtifactActive}
		if item.certInfo != nil {
			value.CertificateSerial = item.certInfo.Serial
			value.CertificateFingerprint = append([]byte(nil), item.certInfo.Fingerprint[:]...)
		}
		metadata = append(metadata, value)
	}
	payload, err := json.Marshal(payloadView)
	if err != nil {
		return "", err
	}
	now := service.now().UTC().Truncate(time.Second)
	if err := service.state.PrepareOperation(ctx, storesqlite.Operation{ID: operationID, InstanceID: instanceID, Kind: "artifacts.recovery", State: storesqlite.OperationPrepared, PayloadVersion: 1, RecoveryPayload: payload, CreatedAt: now, UpdatedAt: now}); err != nil {
		return "", err
	}
	operation, err := service.artifacts.BeginOperation(operationID)
	if err != nil {
		_ = service.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, payload, "", service.now())
		return "", err
	}
	rollback := func(cause error) error {
		return errors.Join(cause, operation.Rollback(), service.state.AdvanceOperation(ctx, operationID, storesqlite.OperationRolledBack, payload, "", service.now()))
	}
	for _, item := range materials {
		if _, err := operation.Stage(ctx, item.key, item.mode, bytes.NewReader(item.data)); err != nil {
			return "", rollback(err)
		}
	}
	if err := operation.Install(ctx, nil); err != nil {
		return "", rollback(err)
	}
	if err := service.state.AdvanceOperation(ctx, operationID, storesqlite.OperationFilesInstalled, payload, "", service.now()); err != nil {
		return "", rollback(err)
	}
	if err := service.state.CommitArtifactOperation(ctx, operationID, metadata, nil, payload, service.now()); err != nil {
		return "", rollback(err)
	}
	if err := operation.Commit(nil); err != nil {
		return "", err
	}
	return operationID, nil
}
