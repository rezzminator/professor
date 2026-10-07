package installer

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
)

var accountLinkRename = os.Rename

// repointAccountLink displaces the entry before checking ownership. A writer
// racing the move is preserved in that displacement, never overwritten.
func (installer *engine) repointAccountLink(target, path, inspected string) error {
	current, err := os.Readlink(path)
	if err != nil || current != inspected {
		return fmt.Errorf("account entry %s changed since inspection; kept operator entry: %v", path, err)
	}
	backup := filepath.Join(
		filepath.Dir(path),
		fmt.Sprintf(".%s.pfm-link-%d-%016x", filepath.Base(path), os.Getpid(), rand.Uint64()),
	)
	if err := accountLinkRename(path, backup); err != nil {
		return fmt.Errorf("preserve account entry %s at %s: %w", path, backup, err)
	}
	restore := func(cause error) error {
		if err := os.Link(backup, path); err != nil {
			return errors.Join(
				cause,
				fmt.Errorf("account entry preserved at %s; restore to %s refused: %w", backup, path, err),
			)
		}
		return errors.Join(cause, os.Remove(backup))
	}
	current, err = os.Readlink(backup)
	if err != nil || current != inspected {
		return restore(fmt.Errorf("account entry %s changed since inspection; kept operator entry: %v", path, err))
	}
	if target != "" {
		if err := os.Symlink(target, path); err != nil {
			return restore(fmt.Errorf("publish account link %s: %w", path, err))
		}
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("remove displaced account link %s: %w", backup, err)
	}
	return nil
}
