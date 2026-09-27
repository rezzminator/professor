package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// mergeLayoutSession keeps the store's copy on conflict and retains the
// account's copy in the journal. The snapshots also restore dropped duplicates.
func mergeLayoutSession(journal *Journal, finding LayoutFinding, paths []string, store string) ([]string, error) {
	account := finding.Path
	conflicts := []string{}
	err := journal.mutate(finding, paths, func() error {
		if err := os.MkdirAll(store, 0o700); err != nil {
			return err
		}
		if err := mergeLayoutChildren(journal, account, store, "", &conflicts); err != nil {
			return err
		}
		if err := os.Remove(account); err != nil {
			return err
		}
		return os.Symlink(store, account)
	})
	return conflicts, err
}

func mergeLayoutChildren(journal *Journal, source, destination, prefix string, conflicts *[]string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		from, to := filepath.Join(source, name), filepath.Join(destination, name)
		relative := filepath.Join(prefix, name)
		left, err := os.Lstat(from)
		if err != nil {
			return err
		}
		right, err := os.Lstat(to)
		if errors.Is(err, fs.ErrNotExist) {
			if err := moveLayoutPath(journal.env, from, to); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if left.IsDir() && right.IsDir() {
			if err := mergeLayoutChildren(journal, from, to, relative, conflicts); err != nil {
				return err
			}
			if err := os.Remove(from); err != nil {
				return err
			}
			continue
		}
		if left.Mode().IsRegular() && right.Mode().IsRegular() {
			a, readErr := os.ReadFile(from)
			if readErr != nil {
				return readErr
			}
			b, readErr := os.ReadFile(to)
			if readErr != nil {
				return readErr
			}
			if bytes.Equal(a, b) {
				if err := os.Remove(from); err != nil {
					return err
				}
				continue
			}
		}
		parked := filepath.Join(journal.dir, "backup", "conflicts", relative)
		if err := os.MkdirAll(filepath.Dir(parked), 0o700); err != nil {
			return err
		}
		if err := moveLayoutPath(journal.env, from, parked); err != nil {
			return err
		}
		*conflicts = append(*conflicts, fmt.Sprintf("%s -> %s", relative, parked))
	}
	return nil
}

func moveLayoutPath(env LayoutEnv, source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	different, available, err := env.probeMove(source, destination)
	if err != nil {
		return err
	}
	if different {
		if err := ensureLayoutMoveSpaceWithAvailable(source, available); err != nil {
			return err
		}
		if err := copyLayoutTree(source, destination); err != nil {
			return err
		}
		return os.RemoveAll(source)
	}
	if err := os.Rename(source, destination); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := ensureLayoutMoveSpace(env, source, destination); err != nil {
		return err
	}
	if err := copyLayoutTree(source, destination); err != nil {
		return err
	}
	return os.RemoveAll(source)
}

func ensureLayoutMoveSpace(env LayoutEnv, source, destination string) error {
	_, available, err := env.probeSpace(filepath.Dir(destination))
	if err != nil {
		return err
	}
	return ensureLayoutMoveSpaceWithAvailable(source, available)
}

func ensureLayoutMoveSpaceWithAvailable(source string, available uint64) error {
	var needed int64
	if err := filepath.WalkDir(source, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		needed += info.Size()
		return nil
	}); err != nil {
		return err
	}
	if available < uint64(needed) {
		return fmt.Errorf("insufficient free space: need %d bytes, have %d", needed, available)
	}
	return nil
}
