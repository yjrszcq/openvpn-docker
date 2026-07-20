package artifact_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
)

func TestFileLocksCoordinateSharedAndExclusiveUsers(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "runtime", "state.lock")
	first, err := artifact.AcquireLock(context.Background(), lockPath, artifact.LockShared)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := artifact.AcquireLock(context.Background(), lockPath, artifact.LockShared)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := artifact.AcquireLock(ctx, lockPath, artifact.LockExclusive); !errors.Is(err, artifact.ErrLocked) {
		t.Fatalf("exclusive conflict error=%v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	exclusive, err := artifact.AcquireLock(context.Background(), lockPath, artifact.LockExclusive)
	if err != nil {
		t.Fatal(err)
	}
	if err := exclusive.Release(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode=%v", info.Mode().Perm())
	}
}

func TestFileLockRejectsUnsafeExistingFile(t *testing.T) {
	directory := t.TempDir()
	unsafe := filepath.Join(directory, "unsafe.lock")
	if err := os.WriteFile(unsafe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := artifact.AcquireLock(context.Background(), unsafe, artifact.LockExclusive); !errors.Is(err, artifact.ErrUnsafePath) {
		t.Fatalf("unsafe lock mode error=%v", err)
	}
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := artifact.AcquireLock(context.Background(), link, artifact.LockExclusive); err == nil {
		t.Fatal("symlink lock was accepted")
	}
}
