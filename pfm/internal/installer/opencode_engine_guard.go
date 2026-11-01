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

// openCodeEngineGuardStaging is where the engine-guard plugin is staged for
// the OpenCode config path config, and the pfm binary it runs: the plugins/
// of the config's directory as written, the one OpenCode scans, never the
// directory a symlinked config file resolves into.
func openCodeEngineGuardStaging(home, config string) (path, binary string) {
	return filepath.Join(filepath.Dir(config), "plugins", openCodeEngineGuardPlugin),
		filepath.Join(home, ".local", "bin", "pfm")
}

// probeOpenCodeEngineGuard is doctor's row for the engine-guard plugin install
// stages beside home's OpenCode config: MISSING when absent, STALE when its
// body is not the one install writes, UNREADABLE when it cannot be read or
// rendered; none when current. The guard fails open without its plugin, so
// its absence is a failure, never silence.
func probeOpenCodeEngineGuard(home string) []HookProbeResult {
	path, binary := openCodeEngineGuardStaging(home, OpenCodeConfigPath(home))
	hook := ExpectedHook{Target: hookTargetOpenCode, File: path, Event: "tool.execute.before", Name: "engine-guard"}
	raw, absent, err := readHookFile(path)
	if err == nil && !absent {
		var want string
		if want, err = workbench.OpenCodeEngineGuardPluginBody(binary); err == nil && string(raw) == want {
			return nil
		}
	}
	switch {
	case err != nil:
		return []HookProbeResult{{Hook: hook, State: stateUnreadable, Error: err.Error()}}
	case absent:
		return []HookProbeResult{{Hook: hook, State: stateMissing}}
	default:
		return []HookProbeResult{{Hook: hook, State: stateStale}}
	}
}

// wireOpenCodeEngineGuard stages the engine-guard plugin beside the OpenCode
// config, or removes it on uninstall.
func (installer *engine) wireOpenCodeEngineGuard() error {
	config := strings.TrimSpace(installer.options.OpenCodeConfigPath)
	if config == "" {
		installer.skip("no OpenCode config path configured — the engine-guard plugin has no home")
		return nil
	}
	path, binary := openCodeEngineGuardStaging(installer.options.Home, config)
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
