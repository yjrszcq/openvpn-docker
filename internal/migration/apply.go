package migration

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

var (
	ErrMaintenanceRequired = errors.New("migration requires maintenance mode")
	ErrSnapshotExists      = errors.New("migration snapshot already exists")
	ErrRecoveryRequired    = errors.New("migration recovery requires a valid snapshot")
)

type CrashPoint string

const (
	CrashAfterSnapshot CrashPoint = "after-snapshot"
	CrashAfterStage    CrashPoint = "after-stage"
	CrashAfterInstall  CrashPoint = "after-install"
	CrashAfterCommit   CrashPoint = "after-commit"
)

type ApplyOptions struct {
	DataDir     string
	RuntimeDir  string
	Maintenance bool
	Version     string
	Renderer    render.Renderer
	Paths       render.Paths
	Now         time.Time
	Crash       func(CrashPoint) error
}

type ApplyResult struct {
	Version      int    `json:"version"`
	Applied      bool   `json:"applied"`
	Recovered    bool   `json:"recovered"`
	OperationID  string `json:"operation_id,omitempty"`
	InstanceID   string `json:"instance_id,omitempty"`
	SourceSchema int    `json:"source_schema"`
	TargetSchema int    `json:"target_schema"`
	SnapshotPath string `json:"snapshot_path,omitempty"`
	FinalState   string `json:"final_state,omitempty"`
	Clients      int    `json:"clients"`
	AuditEvents  int    `json:"audit_events"`
}

type transactionMarker struct {
	Version        int      `json:"version"`
	OperationID    string   `json:"operation_id"`
	State          string   `json:"state"`
	Stage          string   `json:"stage"`
	Snapshot       string   `json:"snapshot"`
	SnapshotDigest string   `json:"snapshot_digest"`
	Installed      []string `json:"installed"`
}

var backupEntries = []string{"cache", "ccd", "clients", "config", "data", "meta", "pki", "secrets", "server"}
var installEntries = []string{"cache", "ccd", "clients", "config", "meta", "server"}

// Apply performs the complete offline schema 3 to 4 transaction.
func Apply(ctx context.Context, options ApplyOptions) (ApplyResult, error) {
	if !options.Maintenance {
		return ApplyResult{}, ErrMaintenanceRequired
	}
	if err := validateApplyOptions(options); err != nil {
		return ApplyResult{}, err
	}
	lockCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	runtimeLock, err := artifact.AcquireLock(lockCtx, filepath.Join(options.RuntimeDir, ".runtime.lock"), artifact.LockExclusive)
	if err != nil {
		return ApplyResult{}, err
	}
	defer runtimeLock.Release()
	dataLock, err := artifact.AcquireLock(lockCtx, filepath.Join(options.DataDir, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		return ApplyResult{}, err
	}
	defer dataLock.Release()
	recovered, err := recoverInterrupted(options.DataDir)
	if err != nil {
		return ApplyResult{}, err
	}
	plan, err := BuildPlan(ctx, options.DataDir, options.Now)
	if err != nil {
		return ApplyResult{}, err
	}
	if plan.Status == "current" {
		return ApplyResult{Version: 1, Applied: false, Recovered: recovered, InstanceID: plan.InstanceID, SourceSchema: 4, TargetSchema: 4, FinalState: string(statecontrol.Healthy)}, nil
	}
	source, err := ReadSchema3(ctx, options.DataDir, options.Now)
	if err != nil {
		return ApplyResult{}, err
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return ApplyResult{}, err
	}
	migrationDir := filepath.Join(options.DataDir, "repair", "migrations")
	if err := os.MkdirAll(migrationDir, 0o700); err != nil {
		return ApplyResult{}, fmt.Errorf("create migration directory: %w", err)
	}
	if err := os.Chmod(migrationDir, 0o700); err != nil {
		return ApplyResult{}, err
	}
	snapshotPath := filepath.Join(options.DataDir, filepath.FromSlash(SnapshotRelativePath))
	if !recovered {
		if _, err := os.Lstat(snapshotPath); err == nil {
			return ApplyResult{}, ErrSnapshotExists
		} else if !os.IsNotExist(err) {
			return ApplyResult{}, err
		}
		if err := createSnapshot(ctx, options.DataDir, snapshotPath); err != nil {
			return ApplyResult{}, err
		}
	}
	snapshotDigest, err := fileDigest(snapshotPath)
	if err != nil {
		return ApplyResult{}, err
	}
	stageRelative := "repair/migrations/stage-" + operationID
	stage := filepath.Join(options.DataDir, filepath.FromSlash(stageRelative))
	marker := transactionMarker{Version: 1, OperationID: operationID, State: "snapshot-ready", Stage: stageRelative, Snapshot: SnapshotRelativePath, SnapshotDigest: snapshotDigest, Installed: []string{}}
	if err := writeMarker(options.DataDir, marker); err != nil {
		return ApplyResult{}, err
	}
	if err := inject(options.Crash, CrashAfterSnapshot); err != nil {
		return ApplyResult{}, err
	}
	if err := copyBusinessTree(ctx, options.DataDir, stage); err != nil {
		return ApplyResult{}, err
	}
	if err := buildStage(ctx, stage, source, options); err != nil {
		return ApplyResult{}, err
	}
	marker.State = "staged"
	if err := writeMarker(options.DataDir, marker); err != nil {
		return ApplyResult{}, err
	}
	if err := inject(options.Crash, CrashAfterStage); err != nil {
		return ApplyResult{}, err
	}
	marker.State = "installing"
	if err := writeMarker(options.DataDir, marker); err != nil {
		return ApplyResult{}, err
	}
	for _, name := range installEntries {
		live := filepath.Join(options.DataDir, name)
		staged := filepath.Join(stage, name)
		if err := os.RemoveAll(live); err != nil {
			return ApplyResult{}, fmt.Errorf("remove legacy %s: %w", name, err)
		}
		if err := os.Rename(staged, live); err != nil {
			return ApplyResult{}, fmt.Errorf("install schema 4 %s: %w", name, err)
		}
		if err := syncDirectory(options.DataDir); err != nil {
			return ApplyResult{}, fmt.Errorf("sync installed schema 4 %s: %w", name, err)
		}
		marker.Installed = append(marker.Installed, name)
		if err := writeMarker(options.DataDir, marker); err != nil {
			return ApplyResult{}, err
		}
		if err := inject(options.Crash, CrashAfterInstall); err != nil {
			return ApplyResult{}, err
		}
	}
	marker.State = "committed"
	if err := writeMarker(options.DataDir, marker); err != nil {
		return ApplyResult{}, err
	}
	if err := inject(options.Crash, CrashAfterCommit); err != nil {
		return ApplyResult{}, err
	}
	if err := os.RemoveAll(stage); err != nil {
		return ApplyResult{}, err
	}
	if err := os.Remove(filepath.Join(options.DataDir, filepath.FromSlash(ManifestRelativePath))); err != nil {
		return ApplyResult{}, err
	}
	if err := syncDirectory(migrationDir); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Version: 1, Applied: true, Recovered: recovered, OperationID: operationID, InstanceID: source.Instance.ID, SourceSchema: 3, TargetSchema: 4, SnapshotPath: snapshotPath, FinalState: string(statecontrol.Healthy), Clients: len(source.Clients), AuditEvents: len(source.Audit)}, nil
}

func validateApplyOptions(options ApplyOptions) error {
	for _, value := range []string{options.DataDir, options.RuntimeDir, options.Paths.DataDir, options.Paths.RuntimeDir} {
		if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return fmt.Errorf("migration paths must be clean and absolute")
		}
	}
	if options.Paths.DataDir != options.DataDir {
		return fmt.Errorf("render data path must match migration data directory")
	}
	if options.Version == "" {
		return fmt.Errorf("migration version is required")
	}
	if options.Now.IsZero() {
		return fmt.Errorf("migration time is required")
	}
	return nil
}

func buildStage(ctx context.Context, stage string, source Source, options ApplyOptions) error {
	for _, key := range []string{"config/project.env", "config/schema-version", "meta/instance.json", "meta/instance-id", "meta/client-state.csv", "meta/client-ip.csv", "meta/audit.jsonl", "meta/client-ip.applied.csv", "data/client-ip.csv"} {
		_ = os.Remove(filepath.Join(stage, filepath.FromSlash(key)))
	}
	_ = os.RemoveAll(filepath.Join(stage, "cache", "client-leases"))
	_ = os.RemoveAll(filepath.Join(stage, "clients", "active"))
	_ = os.RemoveAll(filepath.Join(stage, "clients", "revoked"))
	_ = os.RemoveAll(filepath.Join(stage, "ccd"))
	for _, directory := range []string{"config", "meta", "server", "clients/active", "clients/revoked", "ccd", "cache"} {
		if err := os.MkdirAll(filepath.Join(stage, filepath.FromSlash(directory)), 0o700); err != nil {
			return err
		}
	}
	server, err := options.Renderer.Server(source.Config, options.Paths)
	if err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(stage, "server", "server.conf"), server, 0o600); err != nil {
		return err
	}
	ca, err := os.ReadFile(filepath.Join(stage, "pki", "ca.crt"))
	if err != nil {
		return err
	}
	tls, err := os.ReadFile(filepath.Join(stage, "secrets", "tls-crypt.key"))
	if err != nil {
		return err
	}
	for _, client := range source.Clients {
		if client.Client.Status == domain.ClientDeleted {
			continue
		}
		cert, err := os.ReadFile(filepath.Join(stage, "pki", "issued", client.Client.ID+".crt"))
		if err != nil {
			return err
		}
		key, err := os.ReadFile(filepath.Join(stage, "pki", "private", client.Client.ID+".key"))
		if err != nil {
			return err
		}
		profile, err := options.Renderer.Client(source.Config, options.Paths, render.ClientMaterial{ID: client.Client.ID, Name: client.Client.Name, CACert: string(ca), Certificate: string(cert), PrivateKey: string(key), TLSCryptKey: string(tls)})
		if err != nil {
			return err
		}
		directory := "active"
		if client.Client.Status == domain.ClientRevoked {
			directory = "revoked"
		}
		if err := writePrivate(filepath.Join(stage, "clients", directory, client.Client.Name+".ovpn"), profile, 0o600); err != nil {
			return err
		}
	}
	layout, err := ipam.NewIPv4Layout(source.Config.IPv4.Network, source.Config.IPv4.DynamicPoolSize)
	if err != nil {
		return err
	}
	for _, client := range source.Clients {
		if client.Client.Status == domain.ClientActive && client.Address != nil {
			content := []byte(fmt.Sprintf("ifconfig-push %s %s\n", client.Address.String(), layout.Netmask.String()))
			if err := writePrivate(filepath.Join(stage, "ccd", client.Client.ID), content, 0o600); err != nil {
				return err
			}
		}
	}
	database, err := storesqlite.Create(ctx, filepath.Join(stage, "meta", "state.db"), options.Version)
	if err != nil {
		return err
	}
	defer database.Close()
	applied, err := configservice.NewAppliedSnapshot(1, source.Config)
	if err != nil {
		return err
	}
	instanceState := storesqlite.InstanceState{ID: source.Instance.ID, CreatedAt: source.Instance.InitializedAt, CAFingerprint: source.Instance.CAFingerprint, Applied: applied}
	if err := database.CreateInstance(ctx, instanceState); err != nil {
		return err
	}
	instanceState, err = database.LoadOnlyInstance(ctx)
	if err != nil {
		return err
	}
	instanceArtifacts, err := stageInstanceArtifacts(stage, source, options.Now)
	if err != nil {
		return err
	}
	if err := database.RegisterInstanceArtifacts(ctx, source.Instance.ID, instanceArtifacts); err != nil {
		return err
	}
	leaseByID := map[string]Lease{}
	for _, lease := range source.Leases {
		if lease.Import {
			leaseByID[lease.ClientID] = lease
		}
	}
	for _, client := range source.Clients {
		state, err := stageClientState(stage, source, client, instanceState.NetworkID, leaseByID[client.Client.ID])
		if err != nil {
			return err
		}
		if err := database.CreateClient(ctx, source.Instance.ID, state); err != nil {
			return err
		}
	}
	imported := make([]storesqlite.ImportedAuditEvent, 0, len(source.Audit))
	for _, event := range source.Audit {
		payload := json.RawMessage(append([]byte(nil), event.Raw...))
		imported = append(imported, storesqlite.ImportedAuditEvent{Type: "legacy." + event.Event, Payload: payload, CreatedAt: event.Timestamp})
	}
	if err := database.ImportAuditEvents(ctx, source.Instance.ID, imported, options.Now); err != nil {
		return err
	}
	if err := database.IntegrityCheck(ctx); err != nil {
		return err
	}
	if err := database.Close(); err != nil {
		return err
	}
	report := statecontrol.Scan(ctx, statecontrol.Options{DataDir: stage, ConfigFile: "", ServerName: source.Instance.ServerName, Renderer: options.Renderer, Paths: options.Paths, Now: options.Now})
	if report.State != statecontrol.Healthy {
		return fmt.Errorf("staged schema 4 state doctor returned %s: %+v", report.State, report.Issues)
	}
	return nil
}

func stageInstanceArtifacts(stage string, source Source, now time.Time) ([]storesqlite.ArtifactMetadata, error) {
	ca, err := pki.ValidateCA(filepath.Join(stage, "pki"), now)
	if err != nil {
		return nil, err
	}
	server, err := pki.ValidateServer(filepath.Join(stage, "pki"), source.Instance.ServerName, now)
	if err != nil {
		return nil, err
	}
	specs := []struct {
		kind, key string
		cert      *pki.CertificateInfo
	}{{"ca-cert", "pki/ca.crt", &ca}, {"ca-key", "pki/private/ca.key", nil}, {"server-cert", "pki/issued/" + source.Instance.ServerName + ".crt", &server}, {"server-key", "pki/private/" + source.Instance.ServerName + ".key", nil}, {"crl", "pki/crl.pem", nil}, {"tls-crypt", "secrets/tls-crypt.key", nil}, {"server-config", "server/server.conf", nil}}
	values := make([]storesqlite.ArtifactMetadata, 0, len(specs))
	for _, spec := range specs {
		value, err := newStageMetadata(stage, "instance", source.Instance.ID, spec.kind, spec.key, spec.cert)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func stageClientState(stage string, source Source, client Client, networkID string, lease Lease) (storesqlite.ClientState, error) {
	created := source.Instance.InitializedAt
	state := storesqlite.ClientState{Client: client.Client, CreatedAt: created, Artifacts: []storesqlite.ArtifactMetadata{}}
	terminal := terminalTime(source, client.Client.ID, client.Client.Status)
	if client.Client.Status == domain.ClientRevoked {
		state.RevokedAt = &terminal
	}
	if client.Client.Status == domain.ClientDeleted {
		state.DeletedAt = &terminal
		return state, nil
	}
	assignmentID, err := domain.GenerateUUID()
	if err != nil {
		return state, err
	}
	kind := "dynamic"
	if client.Address != nil {
		kind = "static"
	}
	status := storesqlite.AssignmentActive
	if client.Client.Status == domain.ClientRevoked {
		status = storesqlite.AssignmentRetained
	}
	state.Assignment = &storesqlite.AddressAssignment{ID: assignmentID, NetworkID: networkID, Kind: kind, Address: client.Address, Status: status, CreatedAt: created, UpdatedAt: terminal}
	if lease.Import && client.Client.Status == domain.ClientActive && kind == "dynamic" {
		state.Lease = &storesqlite.ClientLease{NetworkID: networkID, Address: lease.Address, UpdatedAt: lease.UpdatedAt}
	}
	directory := "active"
	if client.Client.Status == domain.ClientRevoked {
		directory = "revoked"
	}
	specs := []struct {
		kind, key string
		cert      *pki.CertificateInfo
	}{{"client-cert", "pki/issued/" + client.Client.ID + ".crt", client.Certificate}, {"client-key", "pki/private/" + client.Client.ID + ".key", nil}, {"profile", "clients/" + directory + "/" + client.Client.Name + ".ovpn", nil}}
	if client.Client.Status == domain.ClientActive && client.Address != nil {
		specs = append(specs, struct {
			kind, key string
			cert      *pki.CertificateInfo
		}{"ccd", "ccd/" + client.Client.ID, nil})
	}
	for _, spec := range specs {
		value, err := newStageMetadata(stage, "client", client.Client.ID, spec.kind, spec.key, spec.cert)
		if err != nil {
			return state, err
		}
		state.Artifacts = append(state.Artifacts, value)
	}
	return state, nil
}

func terminalTime(source Source, clientID string, status domain.ClientStatus) time.Time {
	value := source.Instance.InitializedAt
	operation := "revoke"
	if status == domain.ClientDeleted {
		operation = "delete"
	}
	for _, event := range source.Audit {
		if event.Event == "client_lifecycle" && event.Operation == operation && event.Outcome == "applied" && event.ClientID != nil && *event.ClientID == clientID && event.Timestamp.After(value) {
			value = event.Timestamp
		}
	}
	return value
}

func newStageMetadata(stage, ownerKind, ownerID, kind, key string, certificate *pki.CertificateInfo) (storesqlite.ArtifactMetadata, error) {
	data, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(key)))
	if err != nil {
		return storesqlite.ArtifactMetadata{}, err
	}
	id, err := domain.GenerateUUID()
	if err != nil {
		return storesqlite.ArtifactMetadata{}, err
	}
	value := storesqlite.ArtifactMetadata{ID: id, OwnerKind: ownerKind, OwnerID: ownerID, Kind: kind, Key: key, Digest: sha256.Sum256(data), Status: storesqlite.ArtifactActive}
	if certificate != nil {
		value.CertificateSerial = certificate.Serial
		value.CertificateFingerprint = append([]byte(nil), certificate.Fingerprint[:]...)
	}
	return value, nil
}

func recoverInterrupted(root string) (bool, error) {
	marker, err := readMarker(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrRecoveryRequired, err)
	}
	stage := filepath.Join(root, filepath.FromSlash(marker.Stage))
	if marker.State == "committed" {
		_ = os.RemoveAll(stage)
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(ManifestRelativePath))); err != nil && !os.IsNotExist(err) {
			return false, err
		}
		return true, nil
	}
	snapshot := filepath.Join(root, filepath.FromSlash(marker.Snapshot))
	actual, err := fileDigest(snapshot)
	if err != nil || actual != marker.SnapshotDigest {
		return false, fmt.Errorf("%w: migration snapshot is missing or has the wrong digest", ErrRecoveryRequired)
	}
	if err := restoreSnapshot(root, snapshot); err != nil {
		return false, fmt.Errorf("%w: %v", ErrRecoveryRequired, err)
	}
	_ = os.RemoveAll(stage)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(ManifestRelativePath))); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}

func createSnapshot(ctx context.Context, root, destination string) error {
	temporary := destination + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	failed := true
	defer func() {
		file.Close()
		if failed {
			_ = os.Remove(temporary)
		}
	}()
	gzipWriter := gzip.NewWriter(file)
	archive := tar.NewWriter(gzipWriter)
	for _, name := range backupEntries {
		source := filepath.Join(root, name)
		if _, err := os.Lstat(source); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := appendTree(ctx, archive, root, source); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := gzipWriter.Close(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return err
	}
	failed = false
	return syncDirectory(filepath.Dir(destination))
}

func appendTree(ctx context.Context, archive *tar.Writer, root, current string) error {
	return filepath.Walk(current, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("snapshot path %s is not regular", path)
		}
		if info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("snapshot path %s has unsafe permissions", path)
		}
		if info.Mode().IsRegular() && info.Size() > artifact.MaxArtifactSize {
			return fmt.Errorf("snapshot path %s exceeds the per-file limit", path)
		}
		relative, _ := filepath.Rel(root, path)
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(archive, file)
			closeErr := file.Close()
			return errors.Join(copyErr, closeErr)
		}
		return nil
	})
}

func restoreSnapshot(root, snapshot string) error {
	for _, name := range backupEntries {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	file, err := os.Open(snapshot)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	archive := tar.NewReader(gzipReader)
	var total int64
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		if name == "." || filepath.IsAbs(name) || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("snapshot contains unsafe path")
		}
		top := strings.Split(filepath.ToSlash(name), "/")[0]
		if !contains(backupEntries, top) {
			return fmt.Errorf("snapshot contains unsupported root %s", top)
		}
		target := filepath.Join(root, name)
		mode := os.FileMode(header.Mode).Perm()
		if mode&0o022 != 0 {
			return fmt.Errorf("snapshot contains unsafe permissions")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode); err != nil {
				return err
			}
		case tar.TypeReg:
			total += header.Size
			if header.Size < 0 || header.Size > artifact.MaxArtifactSize || total > (1<<30) {
				return fmt.Errorf("snapshot file exceeds restore limits")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			written, copyErr := io.CopyN(output, archive, header.Size)
			closeErr := output.Close()
			if copyErr != nil || closeErr != nil || written != header.Size {
				return errors.Join(copyErr, closeErr)
			}
		default:
			return fmt.Errorf("snapshot contains unsupported entry")
		}
	}
	return syncDirectory(root)
}

func copyBusinessTree(ctx context.Context, root, stage string) error {
	if err := os.Mkdir(stage, 0o700); err != nil {
		return err
	}
	for _, name := range backupEntries {
		source := filepath.Join(root, name)
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := copyTree(ctx, source, filepath.Join(stage, name), info); err != nil {
			return err
		}
	}
	return nil
}
func copyTree(ctx context.Context, source, destination string, info os.FileInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("unsafe source path %s", source)
	}
	if info.IsDir() {
		if err := os.Mkdir(destination, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			child, err := entry.Info()
			if err != nil {
				return err
			}
			if err := copyTree(ctx, filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name()), child); err != nil {
				return err
			}
		}
		return nil
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}

func writeMarker(root string, marker transactionMarker) error {
	if err := validateMarker(marker); err != nil {
		return err
	}
	marker.Installed = append([]string(nil), marker.Installed...)
	sort.Strings(marker.Installed)
	encoded, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	path := filepath.Join(root, filepath.FromSlash(ManifestRelativePath))
	return writeAtomic(path, encoded, 0o600)
}
func readMarker(root string) (transactionMarker, error) {
	path := filepath.Join(root, filepath.FromSlash(ManifestRelativePath))
	data, err := readMigrationFile(path)
	if err != nil {
		return transactionMarker{}, err
	}
	if err := rejectDuplicateJSON(data); err != nil {
		return transactionMarker{}, err
	}
	var marker transactionMarker
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return marker, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return marker, fmt.Errorf("migration marker has trailing JSON")
	}
	if err := validateMarker(marker); err != nil {
		return marker, err
	}
	return marker, nil
}

func validateMarker(marker transactionMarker) error {
	if marker.Version != 1 || !domain.ValidUUID(marker.OperationID) || marker.Stage != "repair/migrations/stage-"+marker.OperationID || marker.Snapshot != SnapshotRelativePath {
		return fmt.Errorf("invalid migration marker identity")
	}
	if marker.State != "snapshot-ready" && marker.State != "staged" && marker.State != "installing" && marker.State != "committed" {
		return fmt.Errorf("invalid migration marker state")
	}
	decoded, err := hex.DecodeString(marker.SnapshotDigest)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("invalid migration snapshot digest")
	}
	seen := map[string]bool{}
	for _, name := range marker.Installed {
		if !contains(installEntries, name) || seen[name] {
			return fmt.Errorf("invalid installed migration entry")
		}
		seen[name] = true
	}
	return nil
}
func writePrivate(path string, data []byte, mode os.FileMode) error {
	return writeAtomic(path, data, mode)
}
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
func fileDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("migration snapshot must be a regular 0600 file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readMigrationFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("migration metadata must be a regular 0600 file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLegacyMetadata+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLegacyMetadata {
		return nil, fmt.Errorf("migration metadata exceeds size limit")
	}
	return data, nil
}
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func inject(crash func(CrashPoint) error, point CrashPoint) error {
	if crash != nil {
		return crash(point)
	}
	return nil
}
