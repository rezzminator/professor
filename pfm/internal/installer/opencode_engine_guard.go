package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// openCodeEngineGuardPlugin is the plugin file OpenCode loads from its config
// dir's plugins/ (it scans {plugin,plugins}/*.{ts,js} there), so every
// OpenCode chat on the host runs the engine guard, launched by pfm or not.
const openCodeEngineGuardPlugin = "pfm-engine-guard.js"

// wireOpenCodeEngineGuard stages the engine-guard plugin beside the OpenCode
// config, or removes it on uninstall.
func (installer *engine) wireOpenCodeEngineGuard() error {
	config := strings.TrimSpace(installer.options.OpenCodeConfigPath)
	if config == "" {
		installer.skip("no OpenCode config path configured — the engine-guard plugin has no home")
		return nil
	}
	path := filepath.Join(filepath.Dir(physicalSettingsPath(config)), "plugins", openCodeEngineGuardPlugin)
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read OpenCode engine-guard plugin %s: %w", path, err)
	}
	exists := err == nil
	if installer.options.Mode == ModeUninstall {
		if !exists {
			return nil
		}
		return installer.change("remove "+path, func() error { return os.Remove(path) })
	}
	binary := installer.options.Home + "/.local/bin/pfm"
	want, err := workbench.OpenCodeEngineGuardPluginBody(binary)
	if err != nil {
		return err
	}
	if exists && string(current) == want {
		installer.ok(path)
		return nil
	}
	return installer.change(changeDescription(path, exists), func() error {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create OpenCode plugin dir %s: %w", filepath.Dir(path), err)
		}
		return workbench.StageOpenCodeEngineGuardPlugin(path, binary)
	})
}
