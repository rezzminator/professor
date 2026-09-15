package dream

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hostops/pfm/internal/dream/organ"
)

const nightFailureFilename = "night.failed"

func nightFailurePath(organRoot string) string {
	return filepath.Join(organ.ScratchRoot(organRoot), nightFailureFilename)
}

func writeNightFailure(path, phase string, cause error, offendingPath string, at time.Time) error {
	directory := filepath.Dir(path)
	if err := ensureRealDirectories(directory); err != nil {
		return fmt.Errorf("prepare night failure directory %s: %w", directory, err)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("night failure marker is not a regular non-symlink file: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect night failure marker %s: %w", path, err)
	}

	var body strings.Builder
	fmt.Fprintf(&body, "Phase: %s\n", oneLine(phase))
	fmt.Fprintf(&body, "Reason: %s\n", oneLine(cause.Error()))
	fmt.Fprintf(&body, "Path: %s\n", oneLine(offendingPath))
	if !at.IsZero() {
		fmt.Fprintf(&body, "At: %s\n", at.Format(time.RFC3339Nano))
	}

	temporary, err := os.CreateTemp(directory, ".night.failed.*")
	if err != nil {
		return fmt.Errorf("create night failure marker beside %s: %w", path, err)
	}
	temporaryPath := temporary.Name()
	clean := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		clean()
		return fmt.Errorf("set night failure marker mode: %w", err)
	}
	if _, err := temporary.WriteString(body.String()); err != nil {
		clean()
		return fmt.Errorf("write night failure marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		clean()
		return fmt.Errorf("sync night failure marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close night failure marker: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("replace night failure marker %s: %w", path, err)
	}
	return syncDirectory(directory)
}

func ensureRealDirectories(path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean != path {
		return fmt.Errorf("directory must be absolute and canonical: %s", path)
	}
	current := string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.Mkdir(current, 0o700); err != nil {
				return fmt.Errorf("create real directory %s: %w", current, err)
			}
		case err != nil:
			return fmt.Errorf("inspect directory %s: %w", current, err)
		case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
			return fmt.Errorf("directory component is not a real directory: %s", current)
		}
	}
	return nil
}

func offendingPath(err error, fallback string) string {
	var pathFailure interface{ OffendingPath() string }
	if errors.As(err, &pathFailure) && pathFailure.OffendingPath() != "" {
		return pathFailure.OffendingPath()
	}
	return fallback
}

func clearNightFailure(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect night failure marker %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("night failure marker is not a regular non-symlink file: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("clear night failure marker %s: %w", path, err)
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) (returnErr error) {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open marker directory %s: %w", path, err)
	}
	defer func() {
		if err := directory.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close marker directory %s: %w", path, err))
		}
	}()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync marker directory %s: %w", path, err)
	}
	return nil
}
