package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type StreamOptions struct {
	Path    string
	Rotated bool
	Lines   int
	Follow  bool
}

func StreamLines(ctx context.Context, options StreamOptions, emit func(string) error) error {
	if options.Path == "" || !filepath.IsAbs(options.Path) || filepath.Clean(options.Path) != options.Path || options.Lines < 0 || emit == nil {
		return fmt.Errorf("invalid runtime stream options")
	}
	lines, current, err := streamHistory(options)
	if err != nil {
		return err
	}
	if current != nil && !options.Follow {
		defer current.Close()
	}
	for _, line := range lines {
		if err := emit(line); err != nil {
			if current != nil {
				_ = current.Close()
			}
			return err
		}
	}
	if !options.Follow {
		return nil
	}
	return followFile(ctx, options.Path, current, emit)
}

func streamHistory(options StreamOptions) ([]string, *os.File, error) {
	queue := make([]string, 0, options.Lines)
	appendLine := func(value string) {
		if options.Lines == 0 {
			return
		}
		if len(queue) == options.Lines {
			copy(queue, queue[1:])
			queue[len(queue)-1] = value
			return
		}
		queue = append(queue, value)
	}
	if options.Lines > 0 && options.Rotated {
		paths, err := rotationPaths(options.Path)
		if err != nil {
			return nil, nil, err
		}
		for _, path := range paths {
			file, err := openRegular(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			if err := scanLines(file, appendLine); err != nil {
				_ = file.Close()
				return nil, nil, err
			}
			_ = file.Close()
		}
	}
	current, err := openRegular(options.Path)
	if errors.Is(err, os.ErrNotExist) {
		return queue, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if options.Lines == 0 {
		if _, err := current.Seek(0, io.SeekEnd); err != nil {
			_ = current.Close()
			return nil, nil, err
		}
		return queue, current, nil
	}
	if err := scanLines(current, appendLine); err != nil {
		_ = current.Close()
		return nil, nil, err
	}
	return queue, current, nil
}

func rotationPaths(path string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	prefix := filepath.Base(path) + "."
	type rotation struct {
		number int
		path   string
	}
	rotations := make([]rotation, 0)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		number, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), prefix))
		if err == nil && number > 0 {
			rotations = append(rotations, rotation{number: number, path: filepath.Join(filepath.Dir(path), entry.Name())})
		}
	}
	sort.Slice(rotations, func(left, right int) bool { return rotations[left].number > rotations[right].number })
	paths := make([]string, len(rotations))
	for index, rotation := range rotations {
		paths[index] = rotation.path
	}
	return paths, nil
}

func openRegular(path string) (*os.File, error) {
	descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("runtime stream path is not a regular file: %s", path)
	}
	return file, nil
}

func scanLines(file *os.File, emit func(string)) error {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		emit(strings.TrimSuffix(scanner.Text(), "\r"))
	}
	return scanner.Err()
}

func followFile(ctx context.Context, path string, current *os.File, emit func(string) error) error {
	defer func() {
		if current != nil {
			_ = current.Close()
		}
	}()
	var reader *bufio.Reader
	var pending string
	if current != nil {
		reader = bufio.NewReaderSize(current, 64<<10)
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if current == nil {
			file, err := openRegular(path)
			if err == nil {
				current, reader, pending = file, bufio.NewReaderSize(file, 64<<10), ""
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if reader != nil {
			part, err := reader.ReadString('\n')
			if len(part) > 0 {
				pending += part
				if len(pending) > 1<<20 {
					return fmt.Errorf("runtime stream line exceeds 1048576 bytes")
				}
			}
			if err == nil {
				line := strings.TrimSuffix(strings.TrimSuffix(pending, "\n"), "\r")
				pending = ""
				if err := emit(line); err != nil {
					return err
				}
				continue
			}
			if !errors.Is(err, io.EOF) {
				return err
			}
			position, seekErr := current.Seek(0, io.SeekCurrent)
			currentInfo, currentErr := current.Stat()
			pathInfo, pathErr := os.Lstat(path)
			if pathErr == nil && (pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular()) {
				return fmt.Errorf("runtime stream path is not a regular file: %s", path)
			}
			if pathErr == nil && currentErr == nil && !os.SameFile(pathInfo, currentInfo) {
				_ = current.Close()
				current, reader, pending = nil, nil, ""
				continue
			}
			if pathErr == nil && seekErr == nil && pathInfo.Size() < position {
				if _, err := current.Seek(0, io.SeekStart); err != nil {
					return err
				}
				reader, pending = bufio.NewReaderSize(current, 64<<10), ""
				continue
			}
			if pathErr != nil && !errors.Is(pathErr, os.ErrNotExist) {
				return pathErr
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
