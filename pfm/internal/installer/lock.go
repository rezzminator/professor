package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Keep one stable inode outside the root uninstall retires. The lock spans the
// entire applied install, including plugin CLI writes.
func lockInstaller(root string) (func() error, error) {
	path := filepath.Join(filepath.Dir(root), ".installer.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create installer lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open installer lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("lock installer %s (another installer may be running; retry): %w", path,
			errors.Join(err, file.Close()))
	}
	return func() error {
		if err := errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close()); err != nil {
			return fmt.Errorf("release installer lock %s: %w", path, err)
		}
		return nil
	}, nil
}
