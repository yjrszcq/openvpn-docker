package runtime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStreamLinesReadsRotationsInChronologicalOrder(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "openvpn.log")
	writeTestLog(t, path+".2", "old-1\nold-2\n")
	writeTestLog(t, path+".1", "middle-1\nmiddle-2\n")
	writeTestLog(t, path, "new-1\nnew-2\n")
	var got []string
	err := StreamLines(context.Background(), StreamOptions{Path: path, Rotated: true, Lines: 3}, func(line string) error {
		got = append(got, line)
		return nil
	})
	if err != nil || !reflect.DeepEqual(got, []string{"middle-2", "new-1", "new-2"}) {
		t.Fatalf("history=%v err=%v", got, err)
	}
}

func TestStreamLinesFollowsRenameRotationAndTruncation(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "events.jsonl")
	writeTestLog(t, path, "initial\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := make(chan string, 8)
	done := make(chan error, 1)
	go func() {
		done <- StreamLines(ctx, StreamOptions{Path: path, Lines: 1, Follow: true}, func(line string) error {
			lines <- line
			return nil
		})
	}()
	wantLine(t, lines, "initial")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	writeTestLog(t, path, "rotated\n")
	wantLine(t, lines, "rotated")
	writeTestLog(t, path, "short\n")
	wantLine(t, lines, "short")
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("truncated\n")
	_ = file.Close()
	wantLine(t, lines, "truncated")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow did not stop")
	}
}

func TestStreamLinesRefusesSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	writeTestLog(t, target, "secret\n")
	path := filepath.Join(directory, "openvpn.log")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := StreamLines(context.Background(), StreamOptions{Path: path, Lines: 10}, func(string) error { return nil }); err == nil {
		t.Fatal("symlink stream was accepted")
	}
}

func writeTestLog(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func wantLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	select {
	case got := <-lines:
		if got != want {
			t.Fatalf("line=%q want=%q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}
