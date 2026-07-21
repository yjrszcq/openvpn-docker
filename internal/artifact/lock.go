package artifact

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var ErrLocked = errors.New("lock is held")

type LockMode int

const (
	LockShared LockMode = iota + 1
	LockExclusive
)

type FileLock struct {
	file *os.File
}

// RuntimeLockPath returns the cross-container coordination lock stored with
// the shared instance data rather than container-local runtime sockets.
func RuntimeLockPath(dataDir string) string {
	return filepath.Join(dataDir, ".ovpn-runtime.lock")
}

func AcquireLock(ctx context.Context, lockPath string, mode LockMode) (*FileLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLocked, err)
	}
	if err := validateAbsolutePath(lockPath); err != nil {
		return nil, err
	}
	if mode != LockShared && mode != LockExclusive {
		return nil, fmt.Errorf("invalid lock mode")
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("create lock parent: %w", err)
	}
	created := false
	descriptor, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_EXCL|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err == nil {
		created = true
	} else if errors.Is(err, syscall.EEXIST) {
		descriptor, err = syscall.Open(lockPath, syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), lockPath)
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	if created {
		if err := file.Chmod(0o600); err != nil {
			return nil, fmt.Errorf("set lock permissions: %w", err)
		}
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%w: lock file must be a regular file with mode 0600", ErrUnsafePath)
	}
	operation := syscall.LOCK_SH | syscall.LOCK_NB
	if mode == LockExclusive {
		operation = syscall.LOCK_EX | syscall.LOCK_NB
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := syscall.Flock(descriptor, operation)
		if err == nil {
			closeOnError = false
			return &FileLock{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("acquire lock: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %v", ErrLocked, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (lock *FileLock) Release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	if err := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("release lock: %w", err)
	}
	err := lock.file.Close()
	lock.file = nil
	return err
}
