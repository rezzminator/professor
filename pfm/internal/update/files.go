package update

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/config"
)

// updateConfigPathAfterInstall resolves the --config path the candidate's OWN
// install left behind (issue #24 finding 4): runtime.Config.Path was
// resolved BEFORE the migration renamed it inside the candidate process only.
// A non-ENOENT stat error on either candidate path (issue #24 F2 — EACCES,
// ENOTDIR, or anything else) is returned as an error, exactly like sibling
// listUpdateInstallJournals: it is never folded into the "gone/migrated" notes,
// which would misreport a stat failure as an absent file. The caller treats
// a returned error as a failed update step.
func updateConfigPathAfterInstall(runtime config.Runtime) (path, note string, err error) {
	original := runtime.Config.Path
	if original == "" {
		return "", "", nil
	}
	if _, statErr := os.Stat(original); statErr == nil {
		return original, "", nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return "", "", fmt.Errorf("stat config %s: %w", original, statErr)
	}
	migrated := filepath.Join(filepath.Dir(original), config.FileName)
	if _, statErr := os.Stat(migrated); statErr == nil {
		return migrated, fmt.Sprintf("config migrated by the update: %s → %s", original, migrated), nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return "", "", fmt.Errorf("stat migrated config %s: %w", migrated, statErr)
	}
	return "", fmt.Sprintf("config %s is gone after install and no %s replaced it", original, config.FileName), nil
}
