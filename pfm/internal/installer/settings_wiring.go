package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"hostops/pfm/internal/atomicfile"
)

func (installer *engine) reconcileUnvisitedSettingsOwnership(
	ownership map[string]settingsHookCounts,
	seen map[string]bool,
) error {
	for path, owned := range ownership {
		if seen[path] {
			continue
		}
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			delete(ownership, path)
			continue
		}
		if err != nil {
			return fmt.Errorf("read unvisited owned settings path %s: %w", path, err)
		}
		if installer.options.Mode == ModeUninstall {
			return fmt.Errorf("refuse to strand hooks in unvisited owned settings path %s", path)
		}
		var document map[string]any
		if err := unmarshalKeepingNumbers(raw, &document); err != nil {
			return fmt.Errorf("refuse to drop ownership for invalid settings JSON at %s: %w", path, err)
		}
		if removeOwnedSettingsHooks(document, owned) {
			updated, err := json.MarshalIndent(document, "", "  ")
			if err != nil {
				return fmt.Errorf("encode dropped settings seat %s: %w", path, err)
			}
			updated = append(updated, '\n')
			if err := installer.change("rewrite "+path+" (dropped seat; backup preserved)", func() error {
				backup := availableBackup(path, installer.stamp)
				if err := copyBackup(path, backup); err != nil {
					return fmt.Errorf("backup %s: %w", path, err)
				}
				return atomicfile.Write(path, updated, 0o600)
			}); err != nil {
				return err
			}
		}
		delete(ownership, path)
	}
	return nil
}
