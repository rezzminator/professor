package herdr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// OpenStateFile opens name in dir, a lock or marker file pfm keeps for itself:
// dir must be a directory of the effective user that neither group nor others
// can write, and name a regular file of the effective user. Neither is opened
// through a symlink, and name is opened without blocking, so a FIFO or a
// directory put at the path is a named error, never a hang. flag gains
// O_NOFOLLOW, O_CLOEXEC and O_NONBLOCK; a file it creates is 0600. A missing
// dir or name answers an error that is os.ErrNotExist. what names the file in
// every error.
func OpenStateFile(dir, name string, flag int, what string) (*os.File, error) {
	directory, err := os.OpenFile(dir, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s directory: %w", what, err)
	}
	if err := stateFileOwner(directory, true, what); err != nil {
		return nil, errors.Join(err, directory.Close())
	}
	fd, openErr := unix.Openat(
		int(directory.Fd()),
		name,
		flag|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK,
		0o600,
	)
	closeErr := directory.Close()
	if openErr != nil {
		return nil, errors.Join(fmt.Errorf("open %s: %w", what, openErr), closeErr)
	}
	file := os.NewFile(uintptr(fd), filepath.Join(dir, name))
	if closeErr != nil {
		return nil, errors.Join(fmt.Errorf("close %s directory: %w", what, closeErr), file.Close())
	}
	if err := stateFileOwner(file, false, what); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func stateFileOwner(file *os.File, directory bool, what string) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", what, err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%s ownership is not the effective user", what)
	}
	if directory {
		if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s directory is not a protected directory", what)
		}
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", what)
	}
	return nil
}
