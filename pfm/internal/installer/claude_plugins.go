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

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/deps"
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

// claudePlugin is one Claude Code plugin pfm install ensures once, through the
// primary account, in the store every account links:
// Source is the marketplace `plugin marketplace add` takes, ID the
// `name@marketplace` key settings.json's enabledPlugins carries once installed.
type claudePlugin struct {
	Source string
	ID     string
}

// claudePlugins is the single table of plugins the shared store carries.
var claudePlugins = []claudePlugin{
	{Source: "rezzminator/cache-live-control", ID: "cache-live-control@cache-live-control"},
	{Source: "rezzminator/sub-agent-compact", ID: "sub-agent-compact@sub-agent-compact"},
	{Source: "rezzminator/agent-effort", ID: "agent-effort@agent-effort"},
}

// ErrClaudeSettingsAbsent is ClaudePluginGaps' answer for a store with no
// settings.json: a store never set up, not one missing its plugins.
var ErrClaudeSettingsAbsent = errors.New("no settings.json")

// ClaudePluginGaps reads the store's settings.json and names every plugin id
// not enabled. A missing file returns ErrClaudeSettingsAbsent; a file that cannot be read or parsed
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

// ensureClaudePlugins installs the shared plugins through the primary account.
// On a roster host its dir passes the refusal every launch applies, so the
// Options.ConfigDir fallback (the store) never runs; a host with no roster
// keeps Options.ConfigDir, there an ordinary config dir. A --config-dir on a
// roster host carries no roster but ClaudeRosterHost, so the same refusal holds.
func (installer *engine) ensureClaudePlugins(ctx context.Context) error {
	dir := installer.options.PrimaryConfigDir
	if dir == "" {
		dir = installer.options.ConfigDir
	}
	if len(installer.options.ClaudeAccounts) > 0 || installer.options.ClaudeRosterHost {
		if err := installer.checkLaunchConfigDir(0, dir); err != nil {
			failure := fmt.Errorf("claude plugins in %s: %w", dir, err)
			installer.say("  FAIL    %s", failure)
			installer.record("fail", failure.Error(), err)
			return failure
		}
	}
	document, err := readClaudeSettingsDocument(filepath.Join(dir, "settings.json"))
	if err != nil {
		installer.skip("claude plugins in " + dir + ": settings unreadable, state unknown: " + err.Error())
		return nil
	}
	missing, err := ClaudePluginsNotInstalled(dir)
	if err != nil {
		installer.skip("claude plugins in " + dir + ": install record unreadable, state unknown: " + err.Error())
		return nil
	}
	binary, resolveErr := ResolveClaudeBinary(
		installer.options.Home,
		installer.options.ClaudeBinary,
		installer.env().Get("PATH"),
	)
	var failures []error
	guarded := false
	for _, plugin := range claudePlugins {
		if pluginEnabled(document, plugin.ID) && !slices.Contains(missing, plugin.ID) {
			installer.ok("claude plugin " + plugin.ID + " installed and enabled in " + dir)
			continue
		}
		if resolveErr != nil {
			installer.skip(
				fmt.Sprintf("claude plugin %s in %s: real claude binary not resolved: %v", plugin.ID, dir, resolveErr),
			)
			continue
		}
		if !guarded {
			guarded = true
			dirs := []string{dir}
			for _, account := range installer.options.ClaudeAccounts {
				dirs = append(dirs, account.ConfigDir)
			}
			pids := map[int]bool{}
			liveDirs := []string{}
			for _, accountDir := range cleanUniquePaths(dirs) {
				live, err := liveChatPIDs(installer.options.ProcRoot, accountDir)
				if err != nil {
					return installer.pluginFailure(
						dir,
						plugin.ID,
						fmt.Errorf("read live chats in %s: %w", accountDir, err),
					)
				}
				if len(live) > 0 {
					liveDirs = append(liveDirs, accountDir)
				}
				for _, pid := range live {
					id, _ := strconv.Atoi(pid)
					pids[id] = true
				}
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
				installer.skip(
					fmt.Sprintf(
						"claude plugins: live chats %s on %s — close them and rerun pfm install --yes",
						strings.Join(names, ","),
						strings.Join(liveDirs, ","),
					),
				)
				return nil
			}
		}
		if err := installer.installClaudePlugin(ctx, binary, dir, plugin); err != nil {
			failures = append(failures, installer.pluginFailure(dir, plugin.ID, err))
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
		options := deps.RunOptions{Env: pluginCommandEnvironment(dir)}
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

// pluginCommandEnvironment is the inherited environment with CLAUDE_CONFIG_DIR
// set to dir and the login default's sentinel dropped: the child runs on the
// dir pfm chose, so nothing may read it as the inherited default.
func pluginCommandEnvironment(dir string) []string {
	inherited := deps.EnvironmentWith(claudeConfigDirEnv, dir)
	environment := make([]string, 0, len(inherited))
	for _, entry := range inherited {
		if !strings.HasPrefix(entry, claudelaunch.ConfigDirDefaultEnv+"=") {
			environment = append(environment, entry)
		}
	}
	return environment
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
