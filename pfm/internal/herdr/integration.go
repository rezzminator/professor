package herdr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Herdr's own bundled Claude integration: `herdr integration install claude`
// writes ClaudeIntegrationScript into {CLAUDE_CONFIG_DIR}/hooks and adds one
// SessionStart hook running it to {CLAUDE_CONFIG_DIR}/settings.json, through a
// symlinked settings.json. Config dirs sharing one settings.json each add their
// own entry, so the hook would fire once per dir: the store, never the dir, is
// the unit that holds exactly one. The hook stores a `herdr:claude` agent session
// on the pane, which in a herdr-shield pane (Herdr sees only the attach client)
// makes Herdr ignore every pfm report for good, so pfm runs it guarded
// (GuardClaudeHook): a Claude carrying EnvShieldInner skips it. Herdr's install
// is not idempotent over a guarded hook, it adds an unguarded duplicate.
const (
	// Binary is the herdr executable's name on PATH.
	Binary                  = "herdr"
	ClaudeIntegrationScript = "herdr-agent-state.sh"
	ClaudeConfigDirEnv      = "CLAUDE_CONFIG_DIR"
	claudeSettingsName      = "settings.json"
)

// HostBinary is the herdr a host runs: HERDR_BIN_PATH when set, else Binary.
func HostBinary(getenv func(string) string) string {
	if binary := getenv(EnvBin); binary != "" {
		return binary
	}
	return Binary
}

// ClaudeIntegrationArgv is the install of Herdr's Claude integration, run
// through binary with ClaudeConfigDirEnv naming the config dir it installs into.
func ClaudeIntegrationArgv(binary string) []string {
	return []string{binary, "integration", "install", Label(pfmengine.Claude)}
}

// ClaudeIntegrationCommand is that install for dir through binary (HostBinary)
// as one shell line: the command doctor's fix and the installer's rows print. A
// binary other than Binary is a path from EnvBin, quoted.
func ClaudeIntegrationCommand(binary, dir string) string {
	if binary != Binary {
		binary = cmdparse.Quote(binary)
	}
	return ClaudeConfigDirEnv + "=" + cmdparse.Quote(dir) + " " + strings.Join(ClaudeIntegrationArgv(binary), " ")
}

// claudeHookGuard prefixes Herdr's hook command: the hook runs only in a Claude
// outside a herdr-shield pane, none of which carries EnvShieldInner.
const claudeHookGuard = `[ -n "${` + EnvShieldInner + `:-}" ] || `

// GuardClaudeHook is command run only outside a herdr-shield pane; a command
// already guarded comes back as it is.
func GuardClaudeHook(command string) string {
	if ClaudeHookGuarded(command) {
		return command
	}
	return claudeHookGuard + command
}

// ClaudeHookGuarded reports whether the hook command carries the shield guard.
func ClaudeHookGuarded(command string) bool {
	return strings.HasPrefix(command, claudeHookGuard)
}

// GuardClaudeSettings is the Claude settings.json bytes raw with the hook
// command rewritten to its guarded form, every other byte kept. A command that
// is not exactly one JSON string literal in raw is an error: the file is never
// rewritten from a guess.
func GuardClaudeSettings(raw []byte, command string) ([]byte, error) {
	literal := func(value string) ([]byte, error) {
		var out bytes.Buffer
		encoder := json.NewEncoder(&out)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			return nil, fmt.Errorf("encode Claude hook command %q: %w", value, err)
		}
		return bytes.TrimRight(out.Bytes(), "\n"), nil
	}
	old, err := literal(command)
	if err != nil {
		return nil, err
	}
	guarded, err := literal(GuardClaudeHook(command))
	if err != nil {
		return nil, err
	}
	if found := bytes.Count(raw, old); found != 1 {
		return nil, fmt.Errorf("guard Claude hook %q: found %d times in settings, want exactly 1", command, found)
	}
	return bytes.Replace(raw, old, guarded, 1), nil
}

// ClaudeStore is one physical Claude settings.json and the config dirs sharing it.
type ClaudeStore struct {
	// Settings is the physical settings.json path (symlinks resolved).
	Settings string
	// Dirs are the config dirs whose settings.json is Settings, in roster order.
	Dirs []string
	// Hooks are the hook commands in Settings naming ClaudeIntegrationScript,
	// events in name order, each event's in file order.
	Hooks []string
	// Chosen is the config dir the integration installs under: the dir whose
	// hooks/ClaudeIntegrationScript the single existing hook names, else Dirs[0].
	Chosen string
	// Err is a settings.json that could not be read or decoded: Hooks is then
	// unknown, never empty.
	Err error
}

// ClaudeStores groups Claude config dirs, in roster order, by the physical
// settings.json they share and scans each store once.
func ClaudeStores(dirs []string) []ClaudeStore {
	var stores []ClaudeStore
	index := map[string]int{}
	for _, dir := range dirs {
		settings := paths.PhysicalPath(filepath.Join(dir, claudeSettingsName))
		at, seen := index[settings]
		if !seen {
			index[settings] = len(stores)
			stores = append(stores, ClaudeStore{Settings: settings, Dirs: []string{dir}})
			continue
		}
		if !slices.Contains(stores[at].Dirs, dir) {
			stores[at].Dirs = append(stores[at].Dirs, dir)
		}
	}
	for at := range stores {
		store := &stores[at]
		store.Chosen = store.Dirs[0]
		store.Hooks, store.Err = ClaudeIntegrationHooks(store.Settings)
		if len(store.Hooks) == 1 {
			if dir := claudeIntegrationDir(store.Hooks[0]); dir != "" {
				store.Chosen = dir
			}
		}
	}
	return stores
}

// ClaudeIntegrationHooks lists the hook commands of any event in the Claude
// settings.json at path that name ClaudeIntegrationScript. A missing file holds
// none; one that cannot be read or decoded is an error, never none found.
func ClaudeIntegrationHooks(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Claude settings %s: %w", path, err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("decode Claude settings %s: %w", path, err)
	}
	var commands []string
	for _, event := range slices.Sorted(maps.Keys(settings.Hooks)) {
		for _, entry := range settings.Hooks[event] {
			for _, hook := range entry.Hooks {
				if strings.Contains(hook.Command, ClaudeIntegrationScript) {
					commands = append(commands, hook.Command)
				}
			}
		}
	}
	return commands, nil
}

// claudeIntegrationDir is the config dir whose hooks/ClaudeIntegrationScript
// command runs (Herdr writes `bash '{dir}/hooks/herdr-agent-state.sh' session`),
// or "" when no word of it is that script. A guarded command is read past its guard.
func claudeIntegrationDir(command string) string {
	words, err := cmdparse.SplitShellWords(strings.TrimPrefix(command, claudeHookGuard))
	if err != nil {
		return ""
	}
	for _, word := range words {
		if filepath.Base(word) == ClaudeIntegrationScript && filepath.Base(filepath.Dir(word)) == "hooks" {
			return filepath.Dir(filepath.Dir(word))
		}
	}
	return ""
}
