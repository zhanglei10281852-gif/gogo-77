package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// filePerm and dirPerm are the modes used for everything the store creates.
const (
	filePerm fs.FileMode = 0o644
	dirPerm  fs.FileMode = 0o755
)

// WriteAtomic replaces the file at path with data in one step.
//
// The payload is written to a temporary file in the same directory, flushed to
// the filesystem, and only then renamed over the target. A rename inside one
// directory is atomic on every platform this runs on, so a reader either sees
// the whole previous file or the whole new one. Interrupting the process can
// leave a temporary file behind but can never leave a half-written document that
// a later run would try to parse.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, ".fluwatershed-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	tempName := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}
	if _, err := temp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temporary file %s: %w", tempName, err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("flush temporary file %s: %w", tempName, err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("close temporary file %s: %w", tempName, err)
	}
	if err := os.Chmod(tempName, filePerm); err != nil && !errors.Is(err, fs.ErrPermission) {
		_ = os.Remove(tempName)
		return fmt.Errorf("set mode on %s: %w", tempName, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// AppendLines adds pre-encoded JSONL records to the end of a file.
//
// The whole block is handed to one write call on a file opened for append, so a
// concurrent reader sees either none of the new records or all of them; it can
// never observe a record cut in half. The file is then flushed so that the
// records survive a crash straight after the call returns.
func AppendLines(path string, lines [][]byte) error {
	if len(lines) == 0 {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	var block []byte
	for _, line := range lines {
		block = append(block, line...)
		if len(line) == 0 || line[len(line)-1] != '\n' {
			block = append(block, '\n')
		}
	}
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		return fmt.Errorf("open %s for append: %w", path, err)
	}
	if _, err := handle.Write(block); err != nil {
		_ = handle.Close()
		return fmt.Errorf("append to %s: %w", path, err)
	}
	if err := handle.Sync(); err != nil {
		_ = handle.Close()
		return fmt.Errorf("flush %s: %w", path, err)
	}
	if err := handle.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// exists reports whether a regular file is present at path.
func exists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// SweepTemporaries removes leftover temporary files from an interrupted write and
// returns the names it removed, sorted. It is safe to run before any write
// because the temporary prefix is unique to this program.
func SweepTemporaries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read directory %s: %w", dir, err)
	}
	var removed []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, ".fluwatershed-") || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return nil, fmt.Errorf("remove stale temporary %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	sort.Strings(removed)
	return removed, nil
}
