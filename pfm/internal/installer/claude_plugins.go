package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// claudeConfigDirEnv is the variable that points a claude process at one
// account's config directory.
const claudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

// claudePlugin is one Claude Code plugin pfm install ensures on every account:
// Source is the marketplace `plugin marketplace add` takes, ID the
// `name@marketplace` key settings.json's enabledPlugins carries once installed.
type claudePlugin struct {
	Source string
	ID     string
}

// claudePlugins is the single table of plugins every Claude account gets.
var claudePlugins = []claudePlugin{
	{Source: "rezzminator/cache-live-control", ID: "cache-live-control@cache-live-control"},
	{Source: "rezzminator/sub-agent-compact", ID: "sub-agent-compact@sub-agent-compact"},
}

// claudeEnvDefaults is the settings.json env every Claude account gets, each
// key written only when absent: a user's own value always wins.
var claudeEnvDefaults = []struct {
	Key   string
	Value string
}{
	{Key: "CLAUDE_CODE_ENABLE_FUNCTION_HOOKS", Value: "1"},
	{Key: "CLAUDE_CODE_AUTO_COMPACT_WINDOW", Value: "100000"},
}

// addClaudeEnvDefaults adds each claudeEnvDefaults key missing from the
// document's env object, creating env when it is absent. An env that is not
// an object is operator content and is left untouched.
func addClaudeEnvDefaults(document map[string]any) bool {
	raw, present := document[configEnvKey]
	env, isObject := raw.(map[string]any)
	if present && !isObject {
		return false
	}
	if env == nil {
		env = map[string]any{}
	}
	changed := false
	for _, entry := range claudeEnvDefaults {
		if _, exists := env[entry.Key]; !exists {
			env[entry.Key] = entry.Value
			changed = true
		}
	}
	if changed {
		document[configEnvKey] = env
	}
	return changed
}

// ErrClaudeSettingsAbsent is ClaudePluginGaps' answer for an account with no
// settings.json: an account never set up, not one missing its plugins.
var ErrClaudeSettingsAbsent = errors.New("no settings.json")

// ClaudePluginGaps reads one account's settings.json and names every plugin
// id not enabled and every claudeEnvDefaults key absent. A missing file
// returns ErrClaudeSettingsAbsent; a file that cannot be read or parsed
// returns its error, never an answer, because its real state is unknown.
func ClaudePluginGaps(path string) ([]string, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrClaudeSettingsAbsent, path)
	}
	document, err := readClaudeSettingsDocument(path)
	if err != nil {
		return nil, err
	}
	var gaps []string
	for _, plugin := range claudePlugins {
		if !pluginEnabled(document, plugin.ID) {
			gaps = append(gaps, "plugin "+plugin.ID+" not enabled")
		}
	}
	env, _ := document[configEnvKey].(map[string]any)
	for _, entry := range claudeEnvDefaults {
		if _, exists := env[entry.Key]; !exists {
			gaps = append(gaps, "env "+entry.Key+" absent")
		}
	}
	return gaps, nil
}

// ClaudePluginsNotInstalled reads one account's plugins/installed_plugins.json
// and names every claudePlugins id with no install record whose installPath
// exists. Accounts may share one settings.json, so this record — one per
// config dir — is what says a plugin is installed in THIS account. A missing
// file means none is installed; a file that cannot be read or parsed returns
// its error, never an answer.
func ClaudePluginsNotInstalled(configDir string) ([]string, error) {
	path := filepath.Join(configDir, "plugins", "installed_plugins.json")
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var record struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if err == nil {
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	var missing []string
	for _, plugin := range claudePlugins {
		installed := false
		for _, entry := range record.Plugins[plugin.ID] {
			if info, statErr := os.Stat(entry.InstallPath); entry.InstallPath != "" && statErr == nil && info.IsDir() {
				installed = true
				break
			}
		}
		if !installed {
			missing = append(missing, plugin.ID)
		}
	}
	return missing, nil
}

func readClaudeSettingsDocument(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if document == nil {
		document = map[string]any{}
	}
	return document, nil
}

func pluginEnabled(document map[string]any, id string) bool {
	enabled, _ := document["enabledPlugins"].(map[string]any)
	return enabled[id] == true
}

// ensureClaudePlugins installs every claudePlugins entry on every Claude
// account where it is not both enabled in settings.json and recorded as
// installed in that account's own config dir, running the real
// claude binary with CLAUDE_CONFIG_DIR pointed at that account. A failed
// account is reported and joined into the returned error; the other accounts
// still run. A settings file it cannot read is skipped by name, the way
// wireSettings skips it.
func (installer *engine) ensureClaudePlugins(ctx context.Context) error {
	var env paths.Env = paths.OSEnv{}
	if installer.options.Env != nil {
		env = installer.options.Env
	}
	pathEnv := env.Get("PATH")
	binary, resolveErr := ResolveClaudeBinary(installer.options.Home, installer.options.ClaudeBinary, pathEnv)
	var failures []error
	for _, dir := range installer.claudeConfigDirs() {
		settingsPath := filepath.Join(dir, "settings.json")
		document, err := readClaudeSettingsDocument(settingsPath)
		if err != nil {
			installer.skip("claude plugins in " + dir + ": settings unreadable, state unknown: " + err.Error())
			continue
		}
		missing, err := ClaudePluginsNotInstalled(dir)
		if err != nil {
			installer.skip("claude plugins in " + dir + ": install record unreadable, state unknown: " + err.Error())
			continue
		}
		for _, plugin := range claudePlugins {
			if pluginEnabled(document, plugin.ID) && !slices.Contains(missing, plugin.ID) {
				installer.ok("claude plugin " + plugin.ID + " installed and enabled in " + dir)
				continue
			}
			if resolveErr != nil {
				installer.skip(fmt.Sprintf(
					"claude plugin %s in %s: real claude binary not resolved: %v", plugin.ID, dir, resolveErr,
				))
				continue
			}
			if err := installer.installClaudePlugin(ctx, binary, dir, plugin); err != nil {
				failures = append(failures, installer.pluginFailure(dir, plugin.ID, err))
			}
		}
	}
	return errors.Join(failures...)
}

func (installer *engine) installClaudePlugin(ctx context.Context, binary, dir string, plugin claudePlugin) error {
	add := []string{binary, "plugin", "marketplace", "add", plugin.Source}
	install := []string{binary, "plugin", "install", plugin.ID, "-y"}
	message := fmt.Sprintf(
		"run %s=%s %s && %s", claudeConfigDirEnv, dir, strings.Join(add, " "), strings.Join(install, " "),
	)
	return installer.change(message, func() error {
		options := deps.RunOptions{Env: deps.EnvironmentWith(claudeConfigDirEnv, dir)}
		// An already-added marketplace exits non-zero; the install that
		// follows is the judge, so its failure carries this answer too.
		addErr := runClaudePluginCommand(ctx, installer.processRunner(), add, options)
		if err := runClaudePluginCommand(ctx, installer.processRunner(), install, options); err != nil {
			if addErr != nil {
				return errors.Join(addErr, err)
			}
			return err
		}
		return nil
	})
}

func runClaudePluginCommand(ctx context.Context, runner deps.Runner, argv []string, options deps.RunOptions) error {
	result, err := runner.Run(ctx, argv, options)
	if err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv[1:], " "), err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf(
			"%s: exit %d stderr=%q",
			strings.Join(argv[1:], " "),
			result.ExitCode,
			strings.TrimSpace(string(result.Stderr)),
		)
	}
	return nil
}

func (installer *engine) pluginFailure(dir, plugin string, err error) error {
	failure := fmt.Errorf("claude plugin %s in %s: %w", plugin, dir, err)
	installer.say("  FAIL    %s", failure)
	installer.record("fail", failure.Error(), err)
	return failure
}
