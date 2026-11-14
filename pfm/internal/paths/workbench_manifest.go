package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// SettingsFile is the adopter's pfm settings file, read at its managed root.
const SettingsFile = "pfm.settings.json"

// Settings is the pfm settings file of the managed root root.
func Settings(root string) string { return filepath.Join(root, SettingsFile) }

// SettingsPrompt is the prompt file a settings `extend` composes for engine:
// one per managed root and engine under the pfm state directory.
func SettingsPrompt(home, root string, engine pfmengine.ID) string {
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(home, ".local", "state", "pfm", "settings-prompts", hex.EncodeToString(sum[:8]),
		pfmengine.MustLookup(engine).LongName+".md")
}

// HasWorkbenchManifest inspects a workbench marker without following links.
func HasWorkbenchManifest(dir string) (bool, error) {
	path := WorkbenchManifest(dir)
	for _, candidate := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(candidate)
		// ENOTDIR: a regular file named .professor holds no manifest.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("inspect workbench %s: %w", candidate, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false, nil
		}
		if candidate == path && !info.Mode().IsRegular() {
			return false, fmt.Errorf("inspect workbench %s: not a regular file", path)
		}
	}
	return true, nil
}
