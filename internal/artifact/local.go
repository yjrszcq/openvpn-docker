// Package artifact implements bounded local storage for PKI and derived files.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

const (
	BackendLocal    = "local"
	MaxArtifactSize = 16 << 20
	operationsDir   = ".operations"
)

var (
	ErrUnsafePath      = errors.New("artifact path is unsafe")
	ErrOperationExists = errors.New("artifact operation already exists")
	ErrOperationState  = errors.New("artifact operation state is invalid")
)

type Reference struct {
	Backend string   `json:"backend"`
	Key     string   `json:"key"`
	Digest  [32]byte `json:"digest"`
	Mode    uint32   `json:"mode"`
}

type LocalStore struct {
	root string
}

func NewLocal(root string) (*LocalStore, error) {
	if err := validateAbsolutePath(root); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create artifact root: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect artifact root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("%w: artifact root must be a non-writable-by-group/other directory", ErrUnsafePath)
	}
	return &LocalStore{root: root}, nil
}

func (store *LocalStore) Root() string { return store.root }

func ValidateKey(key string) error {
	if key == "" || key == "." || key == ".." || strings.ContainsRune(key, '\x00') || strings.Contains(key, "\\") || strings.HasPrefix(key, "/") || path.Clean(key) != key || strings.HasPrefix(key, "../") {
		return fmt.Errorf("%w: artifact key must be a canonical relative path", ErrUnsafePath)
	}
	if key == operationsDir || strings.HasPrefix(key, operationsDir+"/") {
		return fmt.Errorf("%w: artifact key uses a reserved path", ErrUnsafePath)
	}
	return nil
}

func (store *LocalStore) Read(ctx context.Context, key string) ([]byte, Reference, error) {
	if err := ctx.Err(); err != nil {
		return nil, Reference{}, err
	}
	filePath, info, err := store.existingFile(key)
	if err != nil {
		return nil, Reference{}, err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, Reference{}, fmt.Errorf("open artifact %s: %w", key, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxArtifactSize+1))
	if err != nil {
		return nil, Reference{}, fmt.Errorf("read artifact %s: %w", key, err)
	}
	if len(data) > MaxArtifactSize {
		return nil, Reference{}, fmt.Errorf("artifact %s exceeds %d bytes", key, MaxArtifactSize)
	}
	if err := ctx.Err(); err != nil {
		return nil, Reference{}, err
	}
	digest := sha256.Sum256(data)
	return data, Reference{Backend: BackendLocal, Key: key, Digest: digest, Mode: uint32(info.Mode().Perm())}, nil
}

type OperationState string

const (
	OperationPrepared       OperationState = "prepared"
	OperationFilesInstalled OperationState = "files-installed"
	OperationCommitted      OperationState = "committed"
	OperationRolledBack     OperationState = "rolled-back"
)

type CrashPoint string

const (
	CrashAfterBackup         CrashPoint = "after-backup"
	CrashAfterInstall        CrashPoint = "after-install"
	CrashAfterFilesInstalled CrashPoint = "after-files-installed"
	CrashAfterCommitMarker   CrashPoint = "after-commit-marker"
)

type CrashInjector func(CrashPoint) error

type operationEntry struct {
	Action         string `json:"action"`
	Key            string `json:"key"`
	Mode           uint32 `json:"mode"`
	Digest         string `json:"digest"`
	HadOriginal    bool   `json:"had_original"`
	OriginalMode   uint32 `json:"original_mode,omitempty"`
	OriginalDigest string `json:"original_digest,omitempty"`
	BackupReady    bool   `json:"backup_ready"`
	Installed      bool   `json:"installed"`
}

type operationManifest struct {
	Version int              `json:"version"`
	ID      string           `json:"id"`
	State   OperationState   `json:"state"`
	Entries []operationEntry `json:"entries"`
}

type Operation struct {
	store    *LocalStore
	dir      string
	manifest operationManifest
}

func (store *LocalStore) BeginOperation(operationID string) (*Operation, error) {
	if !domain.ValidUUID(operationID) {
		return nil, fmt.Errorf("invalid artifact operation UUID")
	}
	base, err := store.internalOperationsDir()
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(base, operationID)
	if err := os.Mkdir(directory, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s", ErrOperationExists, operationID)
		}
		return nil, fmt.Errorf("create artifact operation: %w", err)
	}
	operation := &Operation{store: store, dir: directory, manifest: operationManifest{Version: 1, ID: operationID, State: OperationPrepared, Entries: make([]operationEntry, 0)}}
	if err := operation.saveManifest(); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	return operation, nil
}

func (store *LocalStore) OpenOperation(operationID string) (*Operation, error) {
	if !domain.ValidUUID(operationID) {
		return nil, fmt.Errorf("invalid artifact operation UUID")
	}
	directory := filepath.Join(store.root, operationsDir, operationID)
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("open artifact operation: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: operation directory is unsafe", ErrUnsafePath)
	}
	manifest, err := readManifest(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return nil, err
	}
	if manifest.ID != operationID {
		return nil, fmt.Errorf("%w: operation identity mismatch", ErrOperationState)
	}
	return &Operation{store: store, dir: directory, manifest: manifest}, nil
}

func (store *LocalStore) PendingOperations() ([]string, error) {
	base := filepath.Join(store.root, operationsDir)
	info, statErr := os.Lstat(base)
	if errors.Is(statErr, os.ErrNotExist) {
		return []string{}, nil
	}
	if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: operation root is unsafe", ErrUnsafePath)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, fmt.Errorf("list artifact operations: %w", err)
	}
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() || !domain.ValidUUID(entry.Name()) {
			return nil, fmt.Errorf("%w: invalid operation entry %s", ErrUnsafePath, entry.Name())
		}
		values = append(values, entry.Name())
	}
	sort.Strings(values)
	return values, nil
}

func (operation *Operation) ID() string { return operation.manifest.ID }

func (operation *Operation) State() OperationState { return operation.manifest.State }

func (operation *Operation) Stage(ctx context.Context, key string, mode os.FileMode, source io.Reader) (Reference, error) {
	if operation.manifest.State != OperationPrepared {
		return Reference{}, fmt.Errorf("%w: cannot stage in %s", ErrOperationState, operation.manifest.State)
	}
	if mode != 0o600 && mode != 0o644 {
		return Reference{}, fmt.Errorf("artifact mode must be 0600 or 0644")
	}
	if err := ValidateKey(key); err != nil {
		return Reference{}, err
	}
	for _, entry := range operation.manifest.Entries {
		if entry.Key == key {
			return Reference{}, fmt.Errorf("artifact %s is already staged", key)
		}
	}
	if err := ctx.Err(); err != nil {
		return Reference{}, err
	}
	staged := filepath.Join(operation.dir, "staged", filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(staged), 0o700); err != nil {
		return Reference{}, fmt.Errorf("create staging parent: %w", err)
	}
	temporary := staged + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return Reference{}, fmt.Errorf("create staged artifact: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(source, MaxArtifactSize+1))
	if copyErr == nil && written > MaxArtifactSize {
		copyErr = fmt.Errorf("artifact exceeds %d bytes", MaxArtifactSize)
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		if copyErr != nil {
			return Reference{}, fmt.Errorf("write staged artifact: %w", copyErr)
		}
		return Reference{}, fmt.Errorf("close staged artifact: %w", closeErr)
	}
	if err := os.Rename(temporary, staged); err != nil {
		_ = os.Remove(temporary)
		return Reference{}, fmt.Errorf("install staged artifact: %w", err)
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	operation.manifest.Entries = append(operation.manifest.Entries, operationEntry{Action: "write", Key: key, Mode: uint32(mode.Perm()), Digest: hex.EncodeToString(digest[:])})
	if err := operation.saveManifest(); err != nil {
		return Reference{}, err
	}
	return Reference{Backend: BackendLocal, Key: key, Digest: digest, Mode: uint32(mode.Perm())}, nil
}

// Remove stages an idempotent artifact deletion in the same operation journal.
func (operation *Operation) Remove(key string) error {
	if operation.manifest.State != OperationPrepared {
		return fmt.Errorf("%w: cannot remove in %s", ErrOperationState, operation.manifest.State)
	}
	if err := ValidateKey(key); err != nil {
		return err
	}
	for _, entry := range operation.manifest.Entries {
		if entry.Key == key {
			return fmt.Errorf("artifact %s is already staged", key)
		}
	}
	if err := operation.store.preflightTarget(key); err != nil {
		return err
	}
	operation.manifest.Entries = append(operation.manifest.Entries, operationEntry{Action: "delete", Key: key})
	return operation.saveManifest()
}

func (operation *Operation) Install(ctx context.Context, inject CrashInjector) error {
	if operation.manifest.State != OperationPrepared || len(operation.manifest.Entries) == 0 {
		return fmt.Errorf("%w: operation is not installable", ErrOperationState)
	}
	for _, entry := range operation.manifest.Entries {
		if entry.Action == "write" {
			staged := filepath.Join(operation.dir, "staged", filepath.FromSlash(entry.Key))
			if err := verifyStaged(staged, entry); err != nil {
				return err
			}
		}
		if err := operation.store.preflightTarget(entry.Key); err != nil {
			return err
		}
	}
	for index := range operation.manifest.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := &operation.manifest.Entries[index]
		staged := filepath.Join(operation.dir, "staged", filepath.FromSlash(entry.Key))
		target, err := operation.store.prepareTarget(entry.Key)
		if err != nil {
			return err
		}
		backup := filepath.Join(operation.dir, "backup", filepath.FromSlash(entry.Key))
		info, statErr := os.Lstat(target)
		switch {
		case statErr == nil:
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: target %s is not a regular file", ErrUnsafePath, entry.Key)
			}
			if info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o644 {
				return fmt.Errorf("%w: target %s has unsafe permissions", ErrUnsafePath, entry.Key)
			}
			originalDigest, err := digestFile(target)
			if err != nil {
				return err
			}
			entry.HadOriginal = true
			entry.OriginalMode = uint32(info.Mode().Perm())
			entry.OriginalDigest = originalDigest
			if err := operation.saveManifest(); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
				return fmt.Errorf("create operation backup parent: %w", err)
			}
			if err := os.Rename(target, backup); err != nil {
				return fmt.Errorf("backup artifact %s: %w", entry.Key, err)
			}
			if err := syncDirectory(filepath.Dir(target)); err != nil {
				return err
			}
			if err := syncDirectory(filepath.Dir(backup)); err != nil {
				return err
			}
			entry.BackupReady = true
			if err := operation.saveManifest(); err != nil {
				return err
			}
			if err := callCrash(inject, CrashAfterBackup); err != nil {
				return err
			}
		case errors.Is(statErr, os.ErrNotExist):
		case statErr != nil:
			return fmt.Errorf("inspect target artifact %s: %w", entry.Key, statErr)
		}
		if entry.Action == "write" {
			if err := os.Rename(staged, target); err != nil {
				return fmt.Errorf("install artifact %s: %w", entry.Key, err)
			}
		}
		entry.Installed = true
		if err := syncDirectory(filepath.Dir(target)); err != nil {
			return err
		}
		if err := operation.saveManifest(); err != nil {
			return err
		}
		if err := callCrash(inject, CrashAfterInstall); err != nil {
			return err
		}
	}
	operation.manifest.State = OperationFilesInstalled
	if err := operation.saveManifest(); err != nil {
		return err
	}
	return callCrash(inject, CrashAfterFilesInstalled)
}

func (operation *Operation) Commit(inject CrashInjector) error {
	if operation.manifest.State == OperationCommitted {
		return operation.cleanup()
	}
	if operation.manifest.State != OperationFilesInstalled {
		return fmt.Errorf("%w: operation cannot commit from %s", ErrOperationState, operation.manifest.State)
	}
	operation.manifest.State = OperationCommitted
	if err := operation.saveManifest(); err != nil {
		return err
	}
	if err := callCrash(inject, CrashAfterCommitMarker); err != nil {
		return err
	}
	return operation.cleanup()
}

func (operation *Operation) Rollback() error {
	if operation.manifest.State == OperationRolledBack {
		return operation.cleanup()
	}
	if operation.manifest.State == OperationCommitted {
		return fmt.Errorf("%w: operation cannot roll back from %s", ErrOperationState, operation.manifest.State)
	}
	for index := len(operation.manifest.Entries) - 1; index >= 0; index-- {
		entry := operation.manifest.Entries[index]
		target, err := operation.store.prepareTarget(entry.Key)
		if err != nil {
			return err
		}
		backup := filepath.Join(operation.dir, "backup", filepath.FromSlash(entry.Key))
		_, backupErr := os.Lstat(backup)
		if backupErr == nil {
			if err := removeRegularIfPresent(target); err != nil {
				return err
			}
			if err := os.Rename(backup, target); err != nil {
				return fmt.Errorf("restore artifact %s: %w", entry.Key, err)
			}
		} else if !errors.Is(backupErr, os.ErrNotExist) {
			return fmt.Errorf("inspect artifact backup: %w", backupErr)
		} else if entry.HadOriginal {
			currentDigest, digestErr := digestFile(target)
			if digestErr != nil || entry.OriginalDigest == "" || currentDigest != entry.OriginalDigest {
				return fmt.Errorf("%w: original artifact %s cannot be restored", ErrOperationState, entry.Key)
			}
		} else if entry.Action == "write" {
			staged := filepath.Join(operation.dir, "staged", filepath.FromSlash(entry.Key))
			_, stagedErr := os.Lstat(staged)
			if entry.Installed || errors.Is(stagedErr, os.ErrNotExist) {
				if _, targetErr := os.Lstat(target); targetErr == nil {
					currentDigest, digestErr := digestFile(target)
					if digestErr != nil || currentDigest != entry.Digest {
						return fmt.Errorf("%w: new artifact %s cannot be identified", ErrOperationState, entry.Key)
					}
				}
				if err := removeRegularIfPresent(target); err != nil {
					return err
				}
			}
		}
		if err := syncDirectory(filepath.Dir(target)); err != nil {
			return err
		}
	}
	operation.manifest.State = OperationRolledBack
	if err := operation.saveManifest(); err != nil {
		return err
	}
	return operation.cleanup()
}

type SnapshotEntry struct {
	Key    string `json:"key"`
	Digest string `json:"digest"`
	Mode   uint32 `json:"mode"`
}

func (store *LocalStore) Snapshot(ctx context.Context, keys []string, destination string) (resultErr error) {
	if len(keys) == 0 {
		return fmt.Errorf("snapshot requires at least one artifact")
	}
	if err := validateAbsolutePath(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create snapshot parent: %w", err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return fmt.Errorf("create snapshot: %w", err)
	}
	complete := false
	defer func() {
		if resultErr != nil && !complete {
			_ = os.RemoveAll(destination)
		}
	}()
	ordered := append([]string(nil), keys...)
	sort.Strings(ordered)
	entries := make([]SnapshotEntry, 0, len(ordered))
	seen := make(map[string]struct{}, len(ordered))
	for _, key := range ordered {
		if _, exists := seen[key]; exists {
			return fmt.Errorf("snapshot artifact %s is duplicated", key)
		}
		seen[key] = struct{}{}
		data, reference, err := store.Read(ctx, key)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("create snapshot artifact parent: %w", err)
		}
		if err := writeExclusiveFile(target, os.FileMode(reference.Mode), data); err != nil {
			return err
		}
		entries = append(entries, SnapshotEntry{Key: key, Digest: hex.EncodeToString(reference.Digest[:]), Mode: reference.Mode})
	}
	manifest, err := json.Marshal(struct {
		Version int             `json:"version"`
		Entries []SnapshotEntry `json:"entries"`
	}{Version: 1, Entries: entries})
	if err != nil {
		return err
	}
	if err := writeExclusiveFile(filepath.Join(destination, ".ovpn-artifact-snapshot.json"), 0o600, append(manifest, '\n')); err != nil {
		return err
	}
	if err := syncDirectory(destination); err != nil {
		return err
	}
	complete = true
	return nil
}

func (store *LocalStore) internalOperationsDir() (string, error) {
	directory := filepath.Join(store.root, operationsDir)
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("create operation root: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return "", fmt.Errorf("%w: operation root is unsafe", ErrUnsafePath)
	}
	return directory, nil
}

func (store *LocalStore) existingFile(key string) (string, os.FileInfo, error) {
	if err := ValidateKey(key); err != nil {
		return "", nil, err
	}
	value := store.root
	parts := strings.Split(filepath.FromSlash(key), string(filepath.Separator))
	for index, part := range parts {
		value = filepath.Join(value, part)
		info, err := os.Lstat(value)
		if err != nil {
			return "", nil, fmt.Errorf("inspect artifact %s: %w", key, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("%w: artifact %s crosses a symlink", ErrUnsafePath, key)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", nil, fmt.Errorf("%w: artifact parent is not a directory", ErrUnsafePath)
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return "", nil, fmt.Errorf("%w: artifact is not a regular file", ErrUnsafePath)
		}
		if index == len(parts)-1 {
			if info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o644 {
				return "", nil, fmt.Errorf("%w: artifact has unsafe permissions", ErrUnsafePath)
			}
			return value, info, nil
		}
	}
	return "", nil, fmt.Errorf("%w: invalid artifact key", ErrUnsafePath)
}

func (store *LocalStore) prepareTarget(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	current := store.root
	parts := strings.Split(filepath.FromSlash(path.Dir(key)), string(filepath.Separator))
	if path.Dir(key) != "." {
		for _, part := range parts {
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if errors.Is(err, os.ErrNotExist) {
				if err := os.Mkdir(current, 0o750); err != nil {
					return "", fmt.Errorf("create artifact parent: %w", err)
				}
				continue
			}
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
				return "", fmt.Errorf("%w: artifact parent is unsafe", ErrUnsafePath)
			}
		}
	}
	return filepath.Join(store.root, filepath.FromSlash(key)), nil
}

func (store *LocalStore) preflightTarget(key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	current := store.root
	parts := strings.Split(filepath.FromSlash(key), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: artifact target is unsafe", ErrUnsafePath)
		}
		if index < len(parts)-1 {
			if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
				return fmt.Errorf("%w: artifact parent is unsafe", ErrUnsafePath)
			}
		} else if !info.Mode().IsRegular() || (info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o644) {
			return fmt.Errorf("%w: artifact target is not a safely permissioned regular file", ErrUnsafePath)
		}
	}
	return nil
}

func (operation *Operation) saveManifest() error {
	data, err := json.Marshal(operation.manifest)
	if err != nil {
		return err
	}
	return writeAtomicFile(filepath.Join(operation.dir, "manifest.json"), 0o600, append(data, '\n'))
}

func readManifest(filePath string) (operationManifest, error) {
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return operationManifest{}, fmt.Errorf("%w: operation manifest is unsafe", ErrUnsafePath)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return operationManifest{}, fmt.Errorf("open artifact operation manifest: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var manifest operationManifest
	if err := decoder.Decode(&manifest); err != nil {
		return operationManifest{}, fmt.Errorf("decode artifact operation manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return operationManifest{}, fmt.Errorf("decode artifact operation manifest: trailing data")
	}
	if manifest.Version != 1 || !domain.ValidUUID(manifest.ID) {
		return operationManifest{}, fmt.Errorf("%w: invalid operation manifest", ErrOperationState)
	}
	switch manifest.State {
	case OperationPrepared, OperationFilesInstalled, OperationCommitted, OperationRolledBack:
	default:
		return operationManifest{}, fmt.Errorf("%w: invalid operation state", ErrOperationState)
	}
	seen := make(map[string]struct{}, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		if err := ValidateKey(entry.Key); err != nil || (entry.Action != "write" && entry.Action != "delete") {
			return operationManifest{}, fmt.Errorf("%w: invalid operation entry", ErrOperationState)
		}
		if entry.Action == "write" {
			if (entry.Mode != 0o600 && entry.Mode != 0o644) || len(entry.Digest) != 64 {
				return operationManifest{}, fmt.Errorf("%w: invalid operation write entry", ErrOperationState)
			}
			if _, err := hex.DecodeString(entry.Digest); err != nil {
				return operationManifest{}, fmt.Errorf("%w: invalid operation digest", ErrOperationState)
			}
		} else if entry.Mode != 0 || entry.Digest != "" {
			return operationManifest{}, fmt.Errorf("%w: invalid operation delete entry", ErrOperationState)
		}
		if entry.HadOriginal {
			if (entry.OriginalMode != 0o600 && entry.OriginalMode != 0o644) || len(entry.OriginalDigest) != 64 {
				return operationManifest{}, fmt.Errorf("%w: invalid original artifact evidence", ErrOperationState)
			}
			if _, err := hex.DecodeString(entry.OriginalDigest); err != nil {
				return operationManifest{}, fmt.Errorf("%w: invalid original artifact digest", ErrOperationState)
			}
		} else if entry.OriginalMode != 0 || entry.OriginalDigest != "" || entry.BackupReady {
			return operationManifest{}, fmt.Errorf("%w: unexpected original artifact evidence", ErrOperationState)
		}
		if _, exists := seen[entry.Key]; exists {
			return operationManifest{}, fmt.Errorf("%w: duplicate operation key", ErrOperationState)
		}
		seen[entry.Key] = struct{}{}
	}
	return manifest, nil
}

func verifyStaged(filePath string, entry operationEntry) error {
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || uint32(info.Mode().Perm()) != entry.Mode {
		return fmt.Errorf("%w: staged artifact %s is invalid", ErrOperationState, entry.Key)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(file, MaxArtifactSize+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > MaxArtifactSize || hex.EncodeToString(hash.Sum(nil)) != entry.Digest {
		return fmt.Errorf("%w: staged artifact %s digest mismatch", ErrOperationState, entry.Key)
	}
	return nil
}

func digestFile(filePath string) (string, error) {
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: artifact file is unsafe", ErrUnsafePath)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(file, MaxArtifactSize+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > MaxArtifactSize {
		return "", fmt.Errorf("artifact file cannot be hashed safely")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (operation *Operation) cleanup() error {
	if err := os.RemoveAll(operation.dir); err != nil {
		return fmt.Errorf("remove artifact operation: %w", err)
	}
	return syncDirectory(filepath.Dir(operation.dir))
}

func validateAbsolutePath(value string) error {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%w: path must be clean and absolute", ErrUnsafePath)
	}
	return nil
}

func writeExclusiveFile(filePath string, mode os.FileMode, data []byte) error {
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create file %s: %w", filePath, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeAtomicFile(filePath string, mode os.FileMode, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		return err
	}
	temporary := filePath + ".tmp"
	if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeExclusiveFile(temporary, mode, data); err != nil {
		return err
	}
	if err := os.Rename(temporary, filePath); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(filepath.Dir(filePath))
}

func removeRegularIfPresent(filePath string) error {
	info, err := os.Lstat(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: refusing to remove non-regular artifact", ErrUnsafePath)
	}
	return os.Remove(filePath)
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}

func callCrash(inject CrashInjector, point CrashPoint) error {
	if inject == nil {
		return nil
	}
	return inject(point)
}
