// Package initialize creates and recovers complete schema 4 instances.
package initialize

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

const (
	DefaultDataDir    = "/etc/openvpn"
	DefaultRuntimeDir = "/run/openvpn-container"
	DefaultServerName = "openvpn-server"
	markerName        = ".init-transaction.json"
)

var (
	ErrNotEmpty       = errors.New("data directory is not empty")
	ErrRecoveryNeeded = errors.New("initialization recovery failed")
	ErrInvalidConfig  = errors.New("initialization configuration is invalid")
)

type CrashPoint string

const (
	CrashAfterStaged         CrashPoint = "after-staged"
	CrashAfterMarkerPrepared CrashPoint = "after-marker-prepared"
	CrashAfterEntryMoved     CrashPoint = "after-entry-moved"
	CrashAfterFilesInstalled CrashPoint = "after-files-installed"
	CrashAfterCommitted      CrashPoint = "after-committed"
)

type CrashInjector func(CrashPoint) error

type Options struct {
	DataDir    string
	RuntimeDir string
	ConfigFile string
	ServerName string
	Version    string
}

type Result struct {
	InstanceID  string
	OperationID string
	Recovered   bool
}

type Service struct {
	pki      *pki.Runner
	renderer render.Renderer
	now      func() time.Time
}

func NewService(pkiRunner *pki.Runner, renderer render.Renderer) (*Service, error) {
	if pkiRunner == nil {
		return nil, fmt.Errorf("PKI runner is required")
	}
	return &Service{pki: pkiRunner, renderer: renderer, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (service *Service) Initialize(ctx context.Context, options Options, inject CrashInjector) (result Result, resultErr error) {
	if err := validateOptions(options, true); err != nil {
		return Result{}, err
	}
	desired, err := configservice.LoadFile(options.ConfigFile)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	lock, err := acquireDataLock(ctx, options.DataDir)
	if err != nil {
		return Result{}, err
	}
	defer lock.Release()
	if _, err := os.Lstat(filepath.Join(options.DataDir, markerName)); err == nil {
		recovered, recoverErr := service.recoverLocked(ctx, options)
		if recoverErr != nil {
			return Result{}, recoverErr
		}
		if recovered.InstanceID != "" {
			return recovered, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	if err := requireEmptyDataDir(options.DataDir); err != nil {
		return Result{}, err
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return Result{}, err
	}
	instanceID, err := domain.GenerateUUID()
	if err != nil {
		return Result{}, err
	}
	marker := initMarker{
		Version: 1, OperationID: operationID, InstanceID: instanceID,
		Stage: ".init-" + operationID, State: markerStaging,
		Entries: defaultEntries(), CreatedAt: service.now().UTC().Truncate(time.Second),
	}
	markerPath := filepath.Join(options.DataDir, markerName)
	if err := writeMarker(markerPath, marker); err != nil {
		return Result{}, err
	}
	markerCreated := true
	preserveOnError := false
	stage := filepath.Join(options.DataDir, marker.Stage)
	defer func() {
		if resultErr != nil && markerCreated && !preserveOnError {
			_ = os.RemoveAll(stage)
			_ = os.Remove(markerPath)
		}
	}()
	if err := os.Mkdir(stage, 0o700); err != nil {
		return Result{}, fmt.Errorf("create initialization stage: %w", err)
	}
	if err := createLayout(stage); err != nil {
		return Result{}, err
	}
	authority, err := service.pki.Initialize(ctx, filepath.Join(stage, "pki"), options.ServerName)
	if err != nil {
		return Result{}, err
	}
	if err := service.pki.GenerateTLSCrypt(ctx, filepath.Join(stage, "secrets", "tls-crypt.key")); err != nil {
		return Result{}, err
	}
	serverConfig, err := service.renderer.Server(desired, render.Paths{DataDir: options.DataDir, RuntimeDir: options.RuntimeDir})
	if err != nil {
		return Result{}, err
	}
	if err := writeFile(filepath.Join(stage, "server", "server.conf"), 0o600, serverConfig); err != nil {
		return Result{}, err
	}
	database, err := storesqlite.Create(ctx, filepath.Join(stage, "meta", "state.db"), options.Version)
	if err != nil {
		return Result{}, err
	}
	databaseOpen := true
	defer func() {
		if databaseOpen {
			_ = database.Close()
		}
	}()
	snapshot, err := configservice.NewAppliedSnapshot(1, desired)
	if err != nil {
		return Result{}, err
	}
	createdAt := service.now().UTC().Truncate(time.Second)
	if err := database.CreateInstance(ctx, storesqlite.InstanceState{ID: instanceID, CreatedAt: createdAt, CAFingerprint: authority.CA.Fingerprint, Applied: snapshot}); err != nil {
		return Result{}, err
	}
	metadata, err := buildArtifactMetadata(ctx, stage, instanceID, options.ServerName, authority)
	if err != nil {
		return Result{}, err
	}
	if err := database.RegisterInstanceArtifacts(ctx, instanceID, metadata); err != nil {
		return Result{}, err
	}
	recoveryPayload, err := json.Marshal(marker)
	if err != nil {
		return Result{}, err
	}
	operationTime := service.now().UTC().Truncate(time.Second)
	if err := database.PrepareOperation(ctx, storesqlite.Operation{
		ID: operationID, InstanceID: instanceID, Kind: "instance.initialize",
		State: storesqlite.OperationPrepared, PayloadVersion: 1,
		RecoveryPayload: recoveryPayload, CreatedAt: operationTime, UpdatedAt: operationTime,
	}); err != nil {
		return Result{}, err
	}
	if err := database.Close(); err != nil {
		return Result{}, err
	}
	databaseOpen = false
	if err := service.validateInstance(ctx, stage, options, instanceID); err != nil {
		return Result{}, err
	}
	if err := syncTree(stage); err != nil {
		return Result{}, fmt.Errorf("sync initialization stage: %w", err)
	}
	if err := callCrash(inject, CrashAfterStaged); err != nil {
		preserveOnError = true
		return Result{}, err
	}
	marker.State = markerPrepared
	if err := writeMarker(markerPath, marker); err != nil {
		return Result{}, err
	}
	if err := callCrash(inject, CrashAfterMarkerPrepared); err != nil {
		preserveOnError = true
		return Result{}, err
	}
	markerCreated = false
	result, err = service.installPrepared(ctx, options, marker, inject, false)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func (service *Service) Recover(ctx context.Context, options Options) (Result, error) {
	if err := validateOptions(options, false); err != nil {
		return Result{}, err
	}
	lock, err := acquireDataLock(ctx, options.DataDir)
	if err != nil {
		return Result{}, err
	}
	defer lock.Release()
	return service.recoverLocked(ctx, options)
}

func (service *Service) recoverLocked(ctx context.Context, options Options) (Result, error) {
	markerPath := filepath.Join(options.DataDir, markerName)
	marker, err := readMarker(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrRecoveryNeeded, err)
	}
	stage := filepath.Join(options.DataDir, marker.Stage)
	if marker.State == markerStaging {
		if err := os.RemoveAll(stage); err != nil {
			return Result{}, err
		}
		if err := os.Remove(markerPath); err != nil {
			return Result{}, err
		}
		return Result{OperationID: marker.OperationID, Recovered: true}, nil
	}
	return service.installPrepared(ctx, options, marker, nil, true)
}

func (service *Service) installPrepared(ctx context.Context, options Options, marker initMarker, inject CrashInjector, recovered bool) (Result, error) {
	markerPath := filepath.Join(options.DataDir, markerName)
	stage := filepath.Join(options.DataDir, marker.Stage)
	marker.State = markerInstalling
	if err := writeMarker(markerPath, marker); err != nil {
		return Result{}, err
	}
	for index := range marker.Entries {
		entry := &marker.Entries[index]
		staged := filepath.Join(stage, entry.Name)
		final := filepath.Join(options.DataDir, entry.Name)
		stageInfo, stageErr := os.Lstat(staged)
		finalInfo, finalErr := os.Lstat(final)
		switch {
		case stageErr == nil && errors.Is(finalErr, os.ErrNotExist):
			if !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
				return Result{}, fmt.Errorf("%w: staged entry %s is unsafe", ErrRecoveryNeeded, entry.Name)
			}
			if err := os.Rename(staged, final); err != nil {
				return Result{}, fmt.Errorf("install initialization entry %s: %w", entry.Name, err)
			}
			if err := syncDirectory(options.DataDir); err != nil {
				return Result{}, err
			}
			if err := syncDirectory(stage); err != nil {
				return Result{}, err
			}
			entry.Moved = true
		case errors.Is(stageErr, os.ErrNotExist) && finalErr == nil:
			if !finalInfo.IsDir() || finalInfo.Mode()&os.ModeSymlink != 0 {
				return Result{}, fmt.Errorf("%w: installed entry %s is unsafe", ErrRecoveryNeeded, entry.Name)
			}
			entry.Moved = true
		default:
			return Result{}, fmt.Errorf("%w: ambiguous initialization entry %s", ErrRecoveryNeeded, entry.Name)
		}
		if err := writeMarker(markerPath, marker); err != nil {
			return Result{}, err
		}
		if err := callCrash(inject, CrashAfterEntryMoved); err != nil {
			return Result{}, err
		}
	}
	if err := service.validateInstance(ctx, options.DataDir, options, marker.InstanceID); err != nil {
		return Result{}, fmt.Errorf("%w: installed instance validation: %v", ErrRecoveryNeeded, err)
	}
	database, err := storesqlite.Open(ctx, filepath.Join(options.DataDir, "meta", "state.db"))
	if err != nil {
		return Result{}, err
	}
	defer database.Close()
	operation, err := database.LoadOperation(ctx, marker.OperationID)
	if err != nil {
		return Result{}, err
	}
	payload, err := json.Marshal(marker)
	if err != nil {
		return Result{}, err
	}
	now := service.now().UTC().Truncate(time.Second)
	if operation.State == storesqlite.OperationPrepared {
		if err := database.AdvanceOperation(ctx, marker.OperationID, storesqlite.OperationFilesInstalled, payload, "", now); err != nil {
			return Result{}, err
		}
		operation.State = storesqlite.OperationFilesInstalled
	}
	marker.State = markerFilesInstalled
	if err := writeMarker(markerPath, marker); err != nil {
		return Result{}, err
	}
	if err := callCrash(inject, CrashAfterFilesInstalled); err != nil {
		return Result{}, err
	}
	if operation.State == storesqlite.OperationFilesInstalled {
		if err := database.AdvanceOperation(ctx, marker.OperationID, storesqlite.OperationCommitted, payload, "", now.Add(time.Second)); err != nil {
			return Result{}, err
		}
		operation.State = storesqlite.OperationCommitted
	}
	if operation.State != storesqlite.OperationCommitted {
		return Result{}, fmt.Errorf("%w: initialization operation is %s", ErrRecoveryNeeded, operation.State)
	}
	marker.State = markerCommitted
	if err := writeMarker(markerPath, marker); err != nil {
		return Result{}, err
	}
	if err := callCrash(inject, CrashAfterCommitted); err != nil {
		return Result{}, err
	}
	if err := os.RemoveAll(stage); err != nil {
		return Result{}, err
	}
	if err := os.Remove(markerPath); err != nil {
		return Result{}, err
	}
	if err := syncDirectory(options.DataDir); err != nil {
		return Result{}, err
	}
	return Result{InstanceID: marker.InstanceID, OperationID: marker.OperationID, Recovered: recovered}, nil
}

func (service *Service) validateInstance(ctx context.Context, root string, options Options, instanceID string) error {
	database, err := storesqlite.Open(ctx, filepath.Join(root, "meta", "state.db"))
	if err != nil {
		return err
	}
	defer database.Close()
	state, err := database.LoadInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	authority, err := pki.ValidateAuthority(filepath.Join(root, "pki"), options.ServerName, service.now())
	if err != nil {
		return err
	}
	if authority.CA.Fingerprint != state.CAFingerprint {
		return fmt.Errorf("CA fingerprint does not match SQLite instance")
	}
	if err := pki.ValidateTLSCryptKey(filepath.Join(root, "secrets", "tls-crypt.key")); err != nil {
		return err
	}
	wantServer, err := service.renderer.Server(state.Applied.Config, render.Paths{DataDir: options.DataDir, RuntimeDir: options.RuntimeDir})
	if err != nil {
		return err
	}
	gotServer, err := os.ReadFile(filepath.Join(root, "server", "server.conf"))
	if err != nil || !bytes.Equal(gotServer, wantServer) {
		return fmt.Errorf("server configuration does not match applied state")
	}
	artifacts, err := database.LoadInstanceArtifacts(ctx, instanceID)
	if err != nil || len(artifacts) != 7 {
		return fmt.Errorf("instance artifact metadata is incomplete: %v", err)
	}
	local, err := artifact.NewLocal(root)
	if err != nil {
		return err
	}
	for _, metadata := range artifacts {
		_, reference, err := local.Read(ctx, metadata.Key)
		if err != nil || reference.Digest != metadata.Digest {
			return fmt.Errorf("artifact %s does not match SQLite metadata", metadata.Key)
		}
	}
	return nil
}

func buildArtifactMetadata(ctx context.Context, root, instanceID, serverName string, authority pki.Authority) ([]storesqlite.ArtifactMetadata, error) {
	local, err := artifact.NewLocal(root)
	if err != nil {
		return nil, err
	}
	type description struct {
		kind string
		key  string
		cert *pki.CertificateInfo
	}
	ca, server := authority.CA, authority.Server
	descriptions := []description{
		{kind: "ca-cert", key: "pki/ca.crt", cert: &ca},
		{kind: "ca-key", key: "pki/private/ca.key"},
		{kind: "server-cert", key: "pki/issued/" + serverName + ".crt", cert: &server},
		{kind: "server-key", key: "pki/private/" + serverName + ".key"},
		{kind: "crl", key: "pki/crl.pem"},
		{kind: "tls-crypt", key: "secrets/tls-crypt.key"},
		{kind: "server-config", key: "server/server.conf"},
	}
	values := make([]storesqlite.ArtifactMetadata, 0, len(descriptions))
	for _, item := range descriptions {
		_, reference, err := local.Read(ctx, item.key)
		if err != nil {
			return nil, err
		}
		id, err := domain.GenerateUUID()
		if err != nil {
			return nil, err
		}
		metadata := storesqlite.ArtifactMetadata{ID: id, OwnerKind: "instance", OwnerID: instanceID, Kind: item.kind, Key: item.key, Digest: reference.Digest, Status: storesqlite.ArtifactActive}
		if item.cert != nil {
			metadata.CertificateSerial = item.cert.Serial
			metadata.CertificateFingerprint = append([]byte(nil), item.cert.Fingerprint[:]...)
		}
		values = append(values, metadata)
	}
	return values, nil
}

type markerState string

const (
	markerStaging        markerState = "staging"
	markerPrepared       markerState = "prepared"
	markerInstalling     markerState = "installing"
	markerFilesInstalled markerState = "files-installed"
	markerCommitted      markerState = "committed"
)

type markerEntry struct {
	Name  string `json:"name"`
	Moved bool   `json:"moved"`
}

type initMarker struct {
	Version     int           `json:"version"`
	OperationID string        `json:"operation_id"`
	InstanceID  string        `json:"instance_id"`
	Stage       string        `json:"stage"`
	State       markerState   `json:"state"`
	Entries     []markerEntry `json:"entries"`
	CreatedAt   time.Time     `json:"created_at"`
}

func defaultEntries() []markerEntry {
	return []markerEntry{{Name: "pki"}, {Name: "secrets"}, {Name: "server"}, {Name: "clients"}, {Name: "ccd"}, {Name: "meta"}}
}

func writeMarker(filePath string, marker initMarker) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	temporary := filePath + ".tmp"
	if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, filePath); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(filepath.Dir(filePath))
}

func readMarker(filePath string) (initMarker, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return initMarker{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return initMarker{}, fmt.Errorf("initialization marker is unsafe")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return initMarker{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var marker initMarker
	if err := decoder.Decode(&marker); err != nil {
		return initMarker{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return initMarker{}, fmt.Errorf("initialization marker has trailing data")
	}
	if err := validateMarker(marker); err != nil {
		return initMarker{}, err
	}
	return marker, nil
}

func validateMarker(marker initMarker) error {
	if marker.Version != 1 || !domain.ValidUUID(marker.OperationID) || !domain.ValidUUID(marker.InstanceID) || marker.Stage != ".init-"+marker.OperationID || marker.CreatedAt.IsZero() {
		return fmt.Errorf("invalid initialization marker identity")
	}
	switch marker.State {
	case markerStaging, markerPrepared, markerInstalling, markerFilesInstalled, markerCommitted:
	default:
		return fmt.Errorf("invalid initialization marker state")
	}
	expected := defaultEntries()
	if len(marker.Entries) != len(expected) {
		return fmt.Errorf("invalid initialization marker entries")
	}
	for index := range expected {
		if marker.Entries[index].Name != expected[index].Name {
			return fmt.Errorf("invalid initialization marker entry")
		}
	}
	return nil
}

func createLayout(root string) error {
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{
		{"meta", 0o750}, {"pki", 0o750}, {"secrets", 0o750}, {"server", 0o750},
		{"clients/active", 0o750}, {"clients/revoked", 0o750}, {"clients/archive", 0o750}, {"ccd", 0o750},
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(item.path)), item.mode); err != nil {
			return err
		}
	}
	return nil
}

func requireEmptyDataDir(dataDir string) error {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".ovpn-data.lock" || entry.Name() == ".ovpn-runtime.lock" {
			continue
		}
		return fmt.Errorf("%w: found %s", ErrNotEmpty, entry.Name())
	}
	return nil
}

func acquireDataLock(ctx context.Context, dataDir string) (*artifact.FileLock, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dataDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("data directory is unsafe")
	}
	return artifact.AcquireLock(ctx, filepath.Join(dataDir, ".ovpn-data.lock"), artifact.LockExclusive)
}

func validateOptions(options Options, requireConfig bool) error {
	values := []string{options.DataDir, options.RuntimeDir}
	if requireConfig {
		values = append(values, options.ConfigFile)
	}
	for _, value := range values {
		if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("initialization paths must be clean and absolute")
		}
	}
	if options.ServerName == "" || options.Version == "" || strings.ContainsAny(options.ServerName+options.Version, "\r\n") {
		return fmt.Errorf("initialization identity/version is invalid")
	}
	return nil
}

func writeFile(filePath string, mode os.FileMode, data []byte) error {
	if err := os.WriteFile(filePath, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(filePath, mode); err != nil {
		return err
	}
	file, err := os.OpenFile(filePath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncTree(root string) error {
	directories := make([]string, 0, 16)
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("staged path %s is a symlink", filePath)
		}
		if info.IsDir() {
			directories = append(directories, filePath)
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("staged path %s is not a regular file", filePath)
		}
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		return errors.Join(syncErr, closeErr)
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := syncDirectory(directories[index]); err != nil {
			return err
		}
	}
	return nil
}

func callCrash(inject CrashInjector, point CrashPoint) error {
	if inject == nil {
		return nil
	}
	return inject(point)
}
