//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestClaudePluginInstallFixture is run by the jailed Claude stub for plugin
// commands. It writes enabledPlugins in settings.json and the plugin install records and cache dirs.
func TestClaudePluginInstallFixture(t *testing.T) {
	if os.Getenv("PFM_E2E_CLAUDE_PLUGIN") != "1" {
		t.Skip("Claude plugin fixture subprocess only")
	}
	home := os.Getenv(e2eHomeEnv)
	// testjail.Run clears ambient CLAUDE_CONFIG_DIR before a fixture test.
	dir := os.Getenv("PFM_E2E_PLUGIN_CONFIG_DIR")
	if home == "" || dir == "" {
		t.Fatal("plugin fixture requires a private home and config dir")
	}
	relative, err := filepath.Rel(home, dir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		t.Fatalf("plugin fixture escaped its private home: %s", dir)
	}
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		t.Fatal("plugin fixture missing argv")
	}
	args := os.Args[separator+1:]
	pluginsDir := filepath.Join(dir, "plugins")
	if len(args) >= 4 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "add" {
		if err := os.MkdirAll(pluginsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		return
	}
	if len(args) < 3 || args[0] != "plugin" || args[1] != "install" {
		t.Fatalf("unexpected plugin command: %v", args)
	}
	id := args[2]
	settingsPath := filepath.Join(dir, "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	settings := map[string]any{}
	if err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			t.Fatal(err)
		}
	}
	enabled, _ := settings["enabledPlugins"].(map[string]any)
	if enabled == nil {
		enabled = map[string]any{}
	}
	enabled[id] = true
	settings["enabledPlugins"] = enabled
	updated, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	installPath := filepath.Join(pluginsDir, "cache", id)
	if err := os.MkdirAll(installPath, 0o700); err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(pluginsDir, "installed_plugins.json")
	record := struct {
		Plugins map[string][]map[string]string `json:"plugins"`
	}{Plugins: map[string][]map[string]string{}}
	if raw, err := os.ReadFile(recordPath); err == nil {
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if record.Plugins == nil {
		record.Plugins = map[string][]map[string]string{}
	}
	record.Plugins[id] = []map[string]string{{"installPath": installPath}}
	updated, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (h *e2eHarness) newHome(binary string) string {
	h.t.Helper()
	home := testjail.ShortRoot(h.t)
	for _, relative := range []string{
		".claude", ".cc/1", ".cc/2", ".cc/3",
		".codex", ".config", "proc", "cgroup", "tmux", "tmp", ".local/bin",
	} {
		if err := os.MkdirAll(filepath.Join(home, relative), 0o700); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte("{}\n"), 0o600); err != nil {
		h.t.Fatal(err)
	}
	projects := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		h.t.Fatal(err)
	}
	for id := 1; id <= len(managedSettings); id++ {
		path := filepath.Join(projects, fmt.Sprintf("fixture-%d.jsonl", id))
		if err := os.WriteFile(
			path,
			[]byte(fmt.Sprintf("{\"type\":\"fixture\",\"account\":%d}\n", id)),
			0o600,
		); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := copyFile(binary, filepath.Join(home, e2eCanonicalPFM), 0o755); err != nil {
		h.t.Fatalf("stage pfm binary: %v", err)
	}
	testBinary, err := os.Executable()
	if err != nil {
		h.t.Fatal(err)
	}
	// Named a real vMAJOR.MINOR.PATCH string (matching the fixture's own
	// --version output below) rather than an arbitrary "fixture": pfm's
	// launcher now selects the versions/ candidate by parsed semantic
	// version, in Go, and an unparsed name is never chosen (see
	// internal/installer/claude_versions.go).
	native := filepath.Join(home, ".local", "share", "claude", "versions", "2.1.238")
	launcherEvidence := filepath.Join(home, "launcher-evidence")
	body := "#!/bin/sh\n" +
		"if [ \"${1-}\" = -p ]; then exec env PFM_E2E_CLAUDE_CAPTURE=1 " + shellQuoteFixture(testBinary) + " -test.run '^TestClaudeHarnessCaptureFixture$' -- \"$@\"; fi\n" +
		"if [ \"${1-}\" = plugin ]; then exec env PFM_E2E_CLAUDE_PLUGIN=1 " +
		"PFM_E2E_PLUGIN_CONFIG_DIR=\"$CLAUDE_CONFIG_DIR\" " + shellQuoteFixture(testBinary) +
		" -test.run '^TestClaudePluginInstallFixture$' -- \"$@\"; fi\n" +
		"if [ \"${1-}\" = --version ]; then printf '2.1.238 (Claude Code)\\n'; exit 0; fi\n" +
		"printf '%s\\n' \"${TMUX%%,*}\" > " + shellQuoteFixture(launcherEvidence) + "\n" +
		"exit 0\n"
	if err := os.MkdirAll(filepath.Dir(native), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := testjail.WriteExecutable(native, []byte(body), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.Symlink(native, filepath.Join(home, e2eCanonicalClaude)); err != nil {
		h.t.Fatal(err)
	}
	codex := filepath.Join(home, ".local", "bin", "codex")
	codexBody := "#!/bin/sh\n" +
		"if [ \"${1-}\" = app-server ]; then exec env PFM_E2E_CODEX_HOOK_FIXTURE=1 " + shellQuoteFixture(testBinary) + " -test.run '^TestCodexHookAPIFixture$'; fi\n" + `
if [ "${1-}" = --version ]; then printf 'codex-cli 0.149.0\n'; exit 0; fi
if [ "${1-}" = doctor ] && [ "${2-}" = --help ]; then printf 'usage: codex doctor\n'; exit 0; fi
if [ "${1-}" = doctor ]; then printf 'healthy\n'; exit 0; fi
exit 2
`
	if err := testjail.WriteExecutable(codex, []byte(codexBody), 0o700); err != nil {
		h.t.Fatal(err)
	}
	auth := filepath.Join(home, ".codex", "auth.json")
	if err := os.WriteFile(
		auth,
		[]byte(`{"tokens":{"access_token":"fixture-token","account_id":"fixture-account"}}`+"\n"),
		0o600,
	); err != nil {
		h.t.Fatal(err)
	}
	// A fresh fixture uses the split config; the clone example still contains
	// mcp.servers.harvester, which the host gate now refuses.
	roots := make([]string, 0, len(managedSettings))
	for _, relative := range managedSettings {
		roots = append(roots, filepath.Join(home, filepath.Dir(relative), "projects"))
	}
	config := pfmconfig.Defaults(home, roots)
	for name, server := range config.MCPServers {
		server.Enabled = false
		config.MCPServers[name] = server
	}
	content, err := pfmconfig.Marshal(config, false)
	if err != nil {
		h.t.Fatalf("marshal target-shape config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "pfm.config.json"), content, 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.writeJSON(filepath.Join(home, "harvester.config.json"), map[string]any{"enabled": false})
	stageSchedulerFixtures(h.t, home)
	return home
}

func shellQuoteFixture(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (h *e2eHarness) oldShapeHome(binary string) string {
	h.t.Helper()
	home := h.newHome(binary)
	for index, relative := range managedSettings {
		account := filepath.Dir(filepath.Join(home, relative))
		if err := os.MkdirAll(filepath.Join(account, "projects"), 0o700); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, relative), []byte("{}\n"), 0o600); err != nil {
			h.t.Fatal(err)
		}
		name := fmt.Sprintf("fixture-%d.jsonl", index+1)
		if err := os.Rename(
			filepath.Join(home, ".claude", "projects", name),
			filepath.Join(account, "projects", name),
		); err != nil {
			h.t.Fatal(err)
		}
	}
	return home
}

// refusalSnapshot includes directories and modes as well as file bytes and links.
func (h *e2eHarness) refusalSnapshot(home string) surfaceSnapshot {
	h.t.Helper()
	snapshot := surfaceSnapshot{}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += ":" + target
		case !info.IsDir():
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(":%x", sha256.Sum256(body))
		}
		snapshot[filepath.ToSlash(relative)] = value
		return nil
	})
	if err != nil {
		h.t.Fatalf("snapshot refused home: %v", err)
	}
	return snapshot
}
