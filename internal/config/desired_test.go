package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/config"
)

func TestUpdateDesiredFileUsesDigestCASAndAtomicMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(minimalConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err := config.LoadDesired(path)
	if err != nil {
		t.Fatal(err)
	}
	updatedView := current.Config
	updatedView.Server.Endpoint = "new.example.test"
	updated, err := config.UpdateDesiredFile(path, current.Digest, updatedView)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Digest == current.Digest || updated.Config.Server.Endpoint != "new.example.test" {
		t.Fatalf("updated desired=%+v", updated)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("desired mode=%#o, want 0600", info.Mode().Perm())
	}
	if _, err := config.UpdateDesiredFile(path, current.Digest, current.Config); !errors.Is(err, config.ErrDesiredConflict) {
		t.Fatalf("stale update error=%v", err)
	}
	reloaded, err := config.LoadDesired(path)
	if err != nil || reloaded.Digest != updated.Digest {
		t.Fatalf("reloaded desired=%+v err=%v", reloaded, err)
	}
}

func TestUpdateDesiredFileRejectsInvalidView(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(minimalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	current, err := config.LoadDesired(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid := current.Config
	invalid.Server.Port = 0
	if _, err := config.UpdateDesiredFile(path, current.Digest, invalid); !errors.Is(err, config.ErrInvalidDesired) {
		t.Fatalf("invalid update error=%v", err)
	}
	after, err := config.LoadDesired(path)
	if err != nil || after.Digest != current.Digest {
		t.Fatalf("invalid update changed desired: after=%+v err=%v", after, err)
	}
}

func TestConcurrentDesiredUpdatesAllowOneCASWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(minimalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	current, err := config.LoadDesired(path)
	if err != nil {
		t.Fatal(err)
	}
	views := []config.View{current.Config, current.Config}
	views[0].Server.Endpoint = "one.example.test"
	views[1].Server.Endpoint = "two.example.test"
	errorsByUpdate := make([]error, len(views))
	var wait sync.WaitGroup
	for index := range views {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, errorsByUpdate[index] = config.UpdateDesiredFile(path, current.Digest, views[index])
		}(index)
	}
	wait.Wait()
	succeeded, conflicted := 0, 0
	for _, updateErr := range errorsByUpdate {
		switch {
		case updateErr == nil:
			succeeded++
		case errors.Is(updateErr, config.ErrDesiredConflict):
			conflicted++
		default:
			t.Fatalf("concurrent update error=%v", updateErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}
