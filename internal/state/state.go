// Package state diagnoses consistency across SQLite authority, PKI, and local artifacts.
package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

type Classification string

const (
	Empty               Classification = "EMPTY"
	Healthy             Classification = "HEALTHY"
	DegradedRepairable  Classification = "DEGRADED_REPAIRABLE"
	DegradedRecoverable Classification = "DEGRADED_RECOVERABLE"
	DegradedReissuable  Classification = "DEGRADED_REISSUABLE"
	Critical            Classification = "CRITICAL"
	Unrecoverable       Classification = "UNRECOVERABLE"
)

type Severity string

const (
	SeverityRepairable  Severity = "repairable"
	SeverityRecoverable Severity = "recoverable"
	SeverityReissuable  Severity = "reissuable"
	SeverityCritical    Severity = "critical"
	SeverityFatal       Severity = "unrecoverable"
)

type Issue struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Action   string   `json:"action"`
	Target   string   `json:"target,omitempty"`
	OwnerID  string   `json:"ownerId,omitempty"`
	Detail   string   `json:"detail"`
}

type Report struct {
	Version      int            `json:"version"`
	State        Classification `json:"state"`
	DataSchema   int            `json:"dataSchema,omitempty"`
	InstanceID   string         `json:"instanceId,omitempty"`
	Revision     uint64         `json:"revision,omitempty"`
	ScannedAt    time.Time      `json:"scannedAt"`
	IssueCount   int            `json:"issueCount"`
	PendingCount int            `json:"pendingOperationCount"`
	Issues       []Issue        `json:"issues"`
}

type Options struct {
	DataDir    string
	ConfigFile string
	ServerName string
	Paths      render.Paths
	Renderer   render.Renderer
	Now        time.Time
}

func Scan(ctx context.Context, options Options) Report {
	now := options.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	report := Report{Version: 1, State: Healthy, DataSchema: storesqlite.DataSchema, ScannedAt: now, Issues: []Issue{}}
	if options.ServerName == "" {
		options.ServerName = initialize.DefaultServerName
	}
	if options.Paths.DataDir == "" {
		options.Paths.DataDir = options.DataDir
	}
	if options.Paths.RuntimeDir == "" {
		options.Paths.RuntimeDir = initialize.DefaultRuntimeDir
	}
	databasePath := filepath.Join(options.DataDir, "meta", "state.db")
	store, err := storesqlite.Open(ctx, databasePath)
	if err != nil {
		if errors.Is(err, storesqlite.ErrMissing) && directoryEmpty(options.DataDir) {
			report.State = Empty
			return report
		}
		report.add(Issue{ID: databaseIssueID(err), Severity: SeverityCritical, Action: "RESTORE_BACKUP", Target: "meta/state.db", Detail: err.Error()})
		return report.finish()
	}
	defer store.Close()
	instance, err := store.LoadOnlyInstance(ctx)
	if err != nil {
		report.add(Issue{ID: "SQLITE_STATE_INVALID", Severity: SeverityCritical, Action: "RESTORE_BACKUP", Target: "meta/state.db", Detail: err.Error()})
		return report.finish()
	}
	report.InstanceID = instance.ID
	report.Revision = uint64(instance.Applied.Revision)
	clients, err := store.ListClients(ctx, instance.ID)
	if err != nil {
		report.add(Issue{ID: "SQLITE_CLIENT_STATE_INVALID", Severity: SeverityCritical, Action: "RESTORE_BACKUP", Target: "meta/state.db", Detail: err.Error()})
		return report.finish()
	}
	metadata, err := store.LoadInstanceArtifacts(ctx, instance.ID)
	if err != nil {
		report.add(Issue{ID: "SQLITE_ARTIFACT_STATE_INVALID", Severity: SeverityCritical, Action: "RESTORE_BACKUP", Target: "meta/state.db", Detail: err.Error()})
		return report.finish()
	}
	pending, err := store.PendingOperations(ctx, instance.ID)
	if err != nil {
		report.add(Issue{ID: "SQLITE_OPERATION_STATE_INVALID", Severity: SeverityCritical, Action: "RESTORE_BACKUP", Target: "meta/state.db", Detail: err.Error()})
	} else {
		report.PendingCount = len(pending)
		for _, operation := range pending {
			report.add(Issue{ID: "OPERATION_INTERRUPTED", Severity: SeverityCritical, Action: "RECOVER_OPERATION", OwnerID: operation.ID, Detail: fmt.Sprintf("operation %s is %s", operation.Kind, operation.State)})
		}
	}
	artifacts, err := artifact.NewLocal(options.DataDir)
	if err != nil {
		report.add(Issue{ID: "ARTIFACT_ROOT_UNSAFE", Severity: SeverityCritical, Action: "RESTORE_BACKUP", Target: options.DataDir, Detail: err.Error()})
		return report.finish()
	}
	scanConfig(&report, options.ConfigFile, instance)
	crlValid := scanAuthority(&report, options.DataDir, options.ServerName, instance, now)
	scanArtifactSet(ctx, &report, artifacts, metadata, instance.ID)
	serverExpected, renderErr := options.Renderer.Server(instance.Applied.Config, options.Paths)
	if renderErr != nil {
		report.add(Issue{ID: "SERVER_CONFIG_RENDER_FAILED", Severity: SeverityCritical, Action: "RESTORE_TEMPLATES", Target: "server/server.conf", Detail: renderErr.Error()})
	} else {
		compareDerived(ctx, &report, artifacts, "SERVER_CONFIG_DRIFT", "server/server.conf", serverExpected, instance.ID)
	}
	for _, client := range clients {
		scanClient(ctx, &report, artifacts, options, instance, client, now, crlValid)
	}
	return report.finish()
}

func scanConfig(report *Report, path string, instance storesqlite.InstanceState) {
	if path == "" {
		return
	}
	desired, err := configservice.LoadFile(path)
	if err != nil {
		report.add(Issue{ID: "DECLARATIVE_CONFIG_UNAVAILABLE", Severity: SeverityRepairable, Action: "EXPORT_CONFIG", Target: path, Detail: err.Error()})
		return
	}
	digest, err := configservice.Digest(desired)
	if err != nil || digest != instance.Applied.Digest {
		detail := fmt.Sprintf("desired configuration differs from applied revision %d", instance.Applied.Revision)
		if err != nil {
			detail = err.Error()
		}
		report.add(Issue{ID: "DECLARATIVE_CONFIG_DRIFT", Severity: SeverityRepairable, Action: "REVIEW_CONFIG_PLAN", Target: path, Detail: detail})
	}
}

func scanAuthority(report *Report, dataDir, serverName string, instance storesqlite.InstanceState, now time.Time) bool {
	pkiDir := filepath.Join(dataDir, "pki")
	ca, err := pki.ValidateCA(pkiDir, now)
	if err != nil {
		report.add(materialIssue("CA_CERT_INVALID", SeverityRecoverable, "RECOVER_CA_CERT", "pki/ca.crt", err))
	} else if ca.Fingerprint != instance.CAFingerprint {
		report.add(Issue{ID: "CA_FINGERPRINT_MISMATCH", Severity: SeverityCritical, Action: "RESTORE_BACKUP", Target: "pki/ca.crt", Detail: "CA fingerprint does not match SQLite authority"})
	}
	if _, err := pki.ValidateCAKeyPair(pkiDir, now); err != nil {
		severity, action := SeverityCritical, "RESTORE_BACKUP"
		if errors.Is(err, os.ErrNotExist) {
			severity, action = SeverityFatal, "RESTORE_CA_PRIVATE_KEY"
		}
		report.add(materialIssue("CA_PRIVATE_KEY_INVALID", severity, action, "pki/private/ca.key", err))
	}
	if _, err := pki.ValidateServer(pkiDir, serverName, now); err != nil {
		report.add(materialIssue("SERVER_IDENTITY_INVALID", SeverityReissuable, "REISSUE_SERVER", "pki/issued/"+serverName+".crt", err))
	}
	crlValid := true
	if err := pki.ValidateCRLForCA(pkiDir, now); err != nil {
		crlValid = false
		report.add(materialIssue("CRL_INVALID", SeverityRepairable, "REBUILD_CRL", "pki/crl.pem", err))
	}
	if err := pki.ValidateTLSCryptKey(filepath.Join(dataDir, "secrets", "tls-crypt.key")); err != nil {
		report.add(materialIssue("TLS_CRYPT_INVALID", SeverityRecoverable, "RECOVER_TLS_CRYPT", "secrets/tls-crypt.key", err))
	}
	for _, target := range []string{"pki/index.txt", "pki/serial"} {
		if err := requireRegular(filepath.Join(dataDir, target)); err != nil {
			report.add(materialIssue("PKI_SIGNING_DATABASE_INVALID", SeverityCritical, "RESTORE_BACKUP", target, err))
		}
	}
	return crlValid
}

func scanArtifactSet(ctx context.Context, report *Report, local *artifact.LocalStore, metadata []storesqlite.ArtifactMetadata, ownerID string) {
	expected := map[string]string{"ca-cert": "pki/ca.crt", "ca-key": "pki/private/ca.key", "server-cert": "pki/issued/" + initialize.DefaultServerName + ".crt", "server-key": "pki/private/" + initialize.DefaultServerName + ".key", "crl": "pki/crl.pem", "tls-crypt": "secrets/tls-crypt.key", "server-config": "server/server.conf"}
	active := map[string]storesqlite.ArtifactMetadata{}
	for _, item := range metadata {
		if item.Status == storesqlite.ArtifactActive {
			active[item.Kind] = item
		}
	}
	for kind, key := range expected {
		item, ok := active[kind]
		if !ok {
			report.add(Issue{ID: "ARTIFACT_METADATA_MISSING", Severity: severityForArtifact(kind), Action: actionForArtifact(kind), Target: key, OwnerID: ownerID, Detail: "active artifact metadata is missing"})
			continue
		}
		if item.Key != key {
			report.add(Issue{ID: "ARTIFACT_KEY_INVALID", Severity: SeverityCritical, Action: "RESTORE_BACKUP", Target: item.Key, OwnerID: ownerID, Detail: fmt.Sprintf("%s must use canonical key %s", kind, key)})
			continue
		}
		_, reference, err := local.Read(ctx, item.Key)
		if err != nil || reference.Digest != item.Digest {
			detail := "artifact digest does not match SQLite metadata"
			if err != nil {
				detail = err.Error()
			}
			report.add(Issue{ID: "ARTIFACT_CONTENT_MISMATCH", Severity: severityForArtifact(kind), Action: actionForArtifact(kind), Target: item.Key, OwnerID: ownerID, Detail: detail})
		}
	}
}

func scanClient(ctx context.Context, report *Report, local *artifact.LocalStore, options Options, instance storesqlite.InstanceState, client storesqlite.ClientState, now time.Time, crlValid bool) {
	id := client.Client.ID
	certificateInfo, identityErr := pki.ValidateClient(filepath.Join(options.DataDir, "pki"), id, now)
	if identityErr != nil {
		report.add(materialIssueForOwner("CLIENT_IDENTITY_INVALID", SeverityRecoverable, "RECOVER_CLIENT_IDENTITY", "pki/issued/"+id+".crt", id, identityErr))
	} else if crlValid {
		if err := pki.ValidateRevocationStatus(filepath.Join(options.DataDir, "pki"), certificateInfo.Serial, client.Client.Status == domain.ClientRevoked, now); err != nil {
			report.add(materialIssueForOwner("CLIENT_REVOCATION_STATUS_INVALID", SeverityCritical, "RESTORE_BACKUP", "pki/crl.pem", id, err))
		}
	}
	active := map[string]storesqlite.ArtifactMetadata{}
	for _, item := range client.Artifacts {
		if item.Status == storesqlite.ArtifactActive {
			active[item.Kind] = item
			_, reference, err := local.Read(ctx, item.Key)
			if err != nil || reference.Digest != item.Digest {
				detail := "artifact digest does not match SQLite metadata"
				if err != nil {
					detail = err.Error()
				}
				report.add(Issue{ID: "CLIENT_ARTIFACT_CONTENT_MISMATCH", Severity: severityForArtifact(item.Kind), Action: actionForArtifact(item.Kind), Target: item.Key, OwnerID: id, Detail: detail})
			}
		}
	}
	for _, kind := range []string{"client-cert", "client-key", "profile"} {
		if _, ok := active[kind]; !ok {
			report.add(Issue{ID: "CLIENT_ARTIFACT_METADATA_MISSING", Severity: severityForArtifact(kind), Action: actionForArtifact(kind), OwnerID: id, Detail: kind + " metadata is missing"})
		}
	}
	if identityErr == nil {
		if item, ok := active["client-cert"]; ok && (item.CertificateSerial != certificateInfo.Serial || !bytes.Equal(item.CertificateFingerprint, certificateInfo.Fingerprint[:])) {
			report.add(Issue{ID: "CLIENT_CERTIFICATE_METADATA_MISMATCH", Severity: SeverityCritical, Action: "RESTORE_BACKUP", Target: item.Key, OwnerID: id, Detail: "certificate identity does not match SQLite metadata"})
		}
		ca, _, caErr := local.Read(ctx, "pki/ca.crt")
		certificate, _, certErr := local.Read(ctx, "pki/issued/"+id+".crt")
		key, _, keyErr := local.Read(ctx, "pki/private/"+id+".key")
		tlsKey, _, tlsErr := local.Read(ctx, "secrets/tls-crypt.key")
		if errors.Join(caErr, certErr, keyErr, tlsErr) == nil {
			profile, err := options.Renderer.Client(instance.Applied.Config, options.Paths, render.ClientMaterial{ID: id, Name: client.Client.Name, CACert: string(ca), Certificate: string(certificate), PrivateKey: string(key), TLSCryptKey: string(tlsKey)})
			if err == nil {
				directory := "active"
				if client.Client.Status == domain.ClientRevoked {
					directory = "revoked"
				}
				compareDerived(ctx, report, local, "CLIENT_PROFILE_DRIFT", "clients/"+directory+"/"+client.Client.Name+".ovpn", profile, id)
			}
		}
	}
	ccdKey := "ccd/" + id
	if client.Client.Status == domain.ClientActive && client.Assignment != nil && client.Assignment.Kind == "static" && client.Assignment.Address != nil {
		layout, err := ipam.NewIPv4Layout(instance.Applied.Config.IPv4.Network, instance.Applied.Config.IPv4.DynamicPoolSize)
		if err == nil {
			compareDerived(ctx, report, local, "CLIENT_CCD_DRIFT", ccdKey, []byte(fmt.Sprintf("ifconfig-push %s %s\n", client.Assignment.Address, layout.Netmask)), id)
		}
	} else if _, _, err := local.Read(ctx, ccdKey); err == nil {
		report.add(Issue{ID: "CLIENT_CCD_UNEXPECTED", Severity: SeverityRepairable, Action: "REFRESH_CLIENT_ARTIFACTS", Target: ccdKey, OwnerID: id, Detail: "CCD exists without an active static assignment"})
	}
}

func compareDerived(ctx context.Context, report *Report, local *artifact.LocalStore, id, key string, expected []byte, owner string) {
	actual, _, err := local.Read(ctx, key)
	if err != nil || !bytes.Equal(actual, expected) {
		detail := "derived artifact differs from SQLite state"
		if err != nil {
			detail = err.Error()
		}
		report.add(Issue{ID: id, Severity: SeverityRepairable, Action: repairAction(id), Target: key, OwnerID: owner, Detail: detail})
	}
}

func (report *Report) add(issue Issue) { report.Issues = append(report.Issues, issue) }

func (report Report) finish() Report {
	sort.SliceStable(report.Issues, func(i, j int) bool {
		if rank(report.Issues[i].Severity) != rank(report.Issues[j].Severity) {
			return rank(report.Issues[i].Severity) > rank(report.Issues[j].Severity)
		}
		if report.Issues[i].ID != report.Issues[j].ID {
			return report.Issues[i].ID < report.Issues[j].ID
		}
		if report.Issues[i].OwnerID != report.Issues[j].OwnerID {
			return report.Issues[i].OwnerID < report.Issues[j].OwnerID
		}
		return report.Issues[i].Target < report.Issues[j].Target
	})
	report.IssueCount = len(report.Issues)
	report.State = Healthy
	for _, issue := range report.Issues {
		if classificationFor(issue.Severity) > report.State.rank() {
			report.State = stateForSeverity(issue.Severity)
		}
	}
	return report
}

func (state Classification) rank() int {
	switch state {
	case Empty, Healthy:
		return 0
	case DegradedRepairable:
		return 10
	case DegradedRecoverable:
		return 20
	case DegradedReissuable:
		return 30
	case Critical:
		return 40
	case Unrecoverable:
		return 50
	default:
		return -1
	}
}

func classificationFor(value Severity) int { return stateForSeverity(value).rank() }
func rank(value Severity) int              { return classificationFor(value) }

func stateForSeverity(value Severity) Classification {
	switch value {
	case SeverityRepairable:
		return DegradedRepairable
	case SeverityRecoverable:
		return DegradedRecoverable
	case SeverityReissuable:
		return DegradedReissuable
	case SeverityCritical:
		return Critical
	case SeverityFatal:
		return Unrecoverable
	default:
		return Critical
	}
}

func materialIssue(id string, severity Severity, action, target string, err error) Issue {
	return Issue{ID: id, Severity: severity, Action: action, Target: target, Detail: err.Error()}
}

func materialIssueForOwner(id string, severity Severity, action, target, owner string, err error) Issue {
	issue := materialIssue(id, severity, action, target, err)
	issue.OwnerID = owner
	return issue
}

func severityForArtifact(kind string) Severity {
	switch kind {
	case "ca-key":
		return SeverityFatal
	case "server-cert", "server-key":
		return SeverityReissuable
	case "ca-cert", "tls-crypt", "client-cert", "client-key":
		return SeverityRecoverable
	default:
		return SeverityRepairable
	}
}

func actionForArtifact(kind string) string {
	switch kind {
	case "ca-key":
		return "RESTORE_CA_PRIVATE_KEY"
	case "server-cert", "server-key":
		return "REISSUE_SERVER"
	case "ca-cert":
		return "RECOVER_CA_CERT"
	case "tls-crypt":
		return "RECOVER_TLS_CRYPT"
	case "client-cert", "client-key":
		return "RECOVER_CLIENT_IDENTITY"
	case "crl":
		return "REBUILD_CRL"
	case "server-config":
		return "REFRESH_SERVER_ARTIFACTS"
	default:
		return "REFRESH_CLIENT_ARTIFACTS"
	}
}

func repairAction(id string) string {
	if id == "SERVER_CONFIG_DRIFT" {
		return "REFRESH_SERVER_ARTIFACTS"
	}
	return "REFRESH_CLIENT_ARTIFACTS"
}

func databaseIssueID(err error) string {
	switch {
	case errors.Is(err, storesqlite.ErrMissing):
		return "SQLITE_MISSING"
	case errors.Is(err, storesqlite.ErrCorrupt):
		return "SQLITE_CORRUPT"
	case errors.Is(err, storesqlite.ErrPermission):
		return "SQLITE_PERMISSION_INVALID"
	case errors.Is(err, storesqlite.ErrUnsupportedSchema), errors.Is(err, storesqlite.ErrUnsupportedRevision):
		return "SQLITE_VERSION_UNSUPPORTED"
	default:
		return "SQLITE_SCHEMA_INVALID"
	}
}

func directoryEmpty(path string) bool {
	entries, err := os.ReadDir(path)
	return errors.Is(err, os.ErrNotExist) || (err == nil && len(entries) == 0)
}

func requireRegular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 {
		return fmt.Errorf("path is not a non-empty regular file")
	}
	return nil
}
