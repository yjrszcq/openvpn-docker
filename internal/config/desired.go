package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrDesiredConflict = errors.New("desired configuration changed")
	ErrInvalidDesired  = errors.New("desired configuration is invalid")
	desiredUpdateMu    sync.Mutex
)

type DesiredView struct {
	Digest string `json:"digest"`
	Config View   `json:"config"`
}

func LoadDesired(path string) (DesiredView, error) {
	value, err := LoadFile(path)
	if err != nil {
		return DesiredView{}, err
	}
	digest, err := Digest(value)
	if err != nil {
		return DesiredView{}, err
	}
	return DesiredView{Digest: digest, Config: NewView(value)}, nil
}

// UpdateDesiredFile atomically replaces desired YAML after a digest CAS check.
func UpdateDesiredFile(path, expectedDigest string, view View) (DesiredView, error) {
	desiredUpdateMu.Lock()
	defer desiredUpdateMu.Unlock()

	current, err := LoadDesired(path)
	if err != nil {
		return DesiredView{}, err
	}
	if expectedDigest == "" || current.Digest != expectedDigest {
		return DesiredView{}, ErrDesiredConflict
	}
	value, err := FromView(view)
	if err != nil {
		return DesiredView{}, fmt.Errorf("%w: %v", ErrInvalidDesired, err)
	}
	digest, err := Digest(value)
	if err != nil {
		return DesiredView{}, err
	}
	snapshot, err := NewAppliedSnapshot(1, value)
	if err != nil {
		return DesiredView{}, err
	}
	data, err := ExportYAML(snapshot)
	if err != nil {
		return DesiredView{}, err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".config.yaml.api-*")
	if err != nil {
		return DesiredView{}, fmt.Errorf("create desired configuration temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return DesiredView{}, err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return DesiredView{}, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return DesiredView{}, err
	}
	if err := temporary.Close(); err != nil {
		return DesiredView{}, err
	}
	// Recheck immediately before replacement to reject ordinary concurrent edits.
	latest, err := LoadDesired(path)
	if err != nil {
		return DesiredView{}, err
	}
	if latest.Digest != expectedDigest {
		return DesiredView{}, ErrDesiredConflict
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return DesiredView{}, err
	}
	directoryFile, err := os.Open(directory)
	if err == nil {
		err = directoryFile.Sync()
		err = errors.Join(err, directoryFile.Close())
	}
	if err != nil {
		return DesiredView{}, err
	}
	return DesiredView{Digest: digest, Config: NewView(value)}, nil
}
