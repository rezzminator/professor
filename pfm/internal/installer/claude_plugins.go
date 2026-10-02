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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// claudeConfigDirEnv is the variable that points a claude process at one
// account's config directory.
const claudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

// claudePluginCommandTimeout bounds each `claude plugin` command: a plugin
// command that hangs (a network wait, a prompt) must not stall pfm install.
const claudePluginCommandTimeout = 2 * time.Minute

// claudePluginWaitDelay bounds the wait for a command's output pipes after it
// is killed, so a grandchild still holding them cannot stall Run.
const claudePluginWaitDelay = 5 * time.Second

// claudePluginTimeout is the per-command deadline in force. It is a variable
// only as a test seam: a test shortens it and restores it in t.Cleanup.
var claudePluginTimeout = claudePluginCommandTimeout

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
	{Source: "rezzminator/agent-effort", ID: "agent-effort@agent-effort"},
}

// ErrClaudeSettingsAbsent is ClaudePluginGaps' answer for an account with no
// settings.json: an account never set up, not one missing its plugins.
var ErrClaudeSettingsAbsent = errors.New("no settings.json")

// ClaudePluginGaps reads one account's settings.json and names every plugin
// id not enabled. A missing file
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
	dirs := installer.claudeConfigDirs()
	sharers := map[string][]string{}
	for _, dir := range dirs {
		physical := physicalSettingsPath(filepath.Join(dir, "settings.json"))
		sharers[physical] = append(sharers[physical], dir)
	}
	var failures []error
	for _, dir := range dirs {
		settingsPath := filepath.Join(dir, "settings.json")
		physical := physicalSettingsPath(settingsPath)
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
		guarded := false
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
			if !guarded {
				guarded = true
				pids := map[int]bool{}
				var guardErr error
				for _, sharer := range sharers[physical] {
					live, err := liveChatPIDs(installer.options.ProcRoot, sharer)
					if err != nil {
						guardErr = fmt.Errorf("read live chats in %s: %w", sharer, err)
						break
					}
					for _, pid := range live {
						id, _ := strconv.Atoi(pid)
						pids[id] = true
					}
				}
				if guardErr != nil {
					failures = append(failures, installer.pluginFailure(dir, plugin.ID, guardErr))
					break
				}
				if len(pids) > 0 {
					ordered := make([]int, 0, len(pids))
					for pid := range pids {
						ordered = append(ordered, pid)
					}
					sort.Ints(ordered)
					names := make([]string, 0, len(ordered))
					for _, pid := range ordered {
						names = append(names, strconv.Itoa(pid))
					}
					installer.skip(fmt.Sprintf(
						"claude plugins in %s: live chats %s on %s — close them and rerun pfm install --yes",
						dir, strings.Join(names, ","), physical,
					))
					break
				}
			}
			if err := installer.installClaudePlugin(ctx, binary, dir, plugin); err != nil {
				failures = append(failures, installer.pluginFailure(dir, plugin.ID, err))
				break
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
	targets := []string{physicalSettingsPath(filepath.Join(dir, "settings.json")), filepath.Join(dir, "plugins")}
	return installer.changePathsOrRestore(message, targets, func() error {
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
	cmdCtx, cancel := context.WithTimeout(ctx, claudePluginTimeout)
	defer cancel()
	options.ProcessGroup = true
	options.WaitDelay = claudePluginWaitDelay
	result, err := runner.Run(cmdCtx, argv, options)
	if cmdCtx.Err() != nil && (err != nil || result.ExitCode != 0) {
		if ctx.Err() != nil {
			return fmt.Errorf("%s: cancelled: %w", strings.Join(argv[1:], " "), ctx.Err())
		}
		return fmt.Errorf(
			"%s: timed out after %s: %w", strings.Join(argv[1:], " "), claudePluginTimeout, cmdCtx.Err(),
		)
	}
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
