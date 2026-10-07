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
	"github.com/rezzminator/professor/pfm/internal/obs"
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

// claudePlugin is one Claude Code plugin pfm install ensures once, through the
// primary account, in the store every account links:
// Name is the plugin's own name, Source the GitHub marketplace `plugin
// marketplace add` takes, ID the `name@marketplace` key settings.json's
// enabledPlugins carries once installed from it.
type claudePlugin struct {
	Name   string
	Source string
	ID     string
}

// claudePlugins is the single table of plugins the shared store carries.
var claudePlugins = []claudePlugin{
	{Name: "cache-live-control", Source: "rezzminator/cache-live-control", ID: "cache-live-control@cache-live-control"},
	{Name: "sub-agent-compact", Source: "rezzminator/sub-agent-compact", ID: "sub-agent-compact@sub-agent-compact"},
	{Name: "agent-effort", Source: "rezzminator/agent-effort", ID: "agent-effort@agent-effort"},
}

// ClaudePluginRepair is the repair every plugin gap pfm install closes names.
const ClaudePluginRepair = "run pfm install --yes"

// ClaudePluginBuild is what decides where each plugin comes from: pfm's own
// stamped version, the home pfm's paths resolve from, and
// claude.pluginCheckoutRoot.
type ClaudePluginBuild struct {
	Version      string
	Home         string
	CheckoutRoot string
}

// ClaudePluginTarget is one table plugin as this build ensures it. ID is the
// enabledPlugins key ensured and Source what `plugin marketplace add` takes:
// the GitHub repository and GitHubID on a release, or on an -alpha build with
// a checkout (Local) the pfm-owned marketplace dir Marketplace, holding a copy
// of Checkout, and DevID. Fallback says why an -alpha build ensures the GitHub
// copy instead; empty on a release.
type ClaudePluginTarget struct {
	Name        string
	ID          string
	GitHubID    string
	DevID       string
	Source      string
	Local       bool
	Marketplace string
	Checkout    string
	Fallback    string
}

// ClaudePluginTargets plans every table plugin for build.
func ClaudePluginTargets(build ClaudePluginBuild) []ClaudePluginTarget {
	alpha := obs.AlphaBuild(build.Version)
	targets := make([]ClaudePluginTarget, 0, len(claudePlugins))
	for _, plugin := range claudePlugins {
		target := ClaudePluginTarget{
			Name:     plugin.Name,
			ID:       plugin.ID,
			GitHubID: plugin.ID,
			DevID:    plugin.Name + "@" + devMarketplaceName(plugin.Name),
			Source:   plugin.Source,
		}
		if alpha {
			target = localClaudePluginTarget(target, build)
		}
		targets = append(targets, target)
	}
	return targets
}

// FallbackNote is the one line install and doctor print for a Fallback.
func (target ClaudePluginTarget) FallbackNote() string {
	return target.Name + ": alpha build falls back to the GitHub copy " + target.GitHubID + " — " + target.Fallback
}

// ClaudePluginGap is one plugin problem pfm doctor reports and its repair.
type ClaudePluginGap struct {
	Problem string
	Fix     string
}

// ErrClaudeSettingsAbsent is ClaudePluginGaps' answer for a store with no
// settings.json: a store never set up, not one missing its plugins.
var ErrClaudeSettingsAbsent = errors.New("no settings.json")

// ClaudePluginGaps reads the store's settings.json and names every target id
// not enabled, every plugin enabled from both its GitHub and its -dev
// marketplace, and every local target whose copy is missing or a symlink. A
// missing file returns ErrClaudeSettingsAbsent; a file that cannot be read or
// parsed returns its error, never an answer, because its real state is unknown.
func ClaudePluginGaps(path string, targets []ClaudePluginTarget) ([]ClaudePluginGap, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrClaudeSettingsAbsent, path)
	}
	document, err := readClaudeSettingsDocument(path)
	if err != nil {
		return nil, err
	}
	var gaps []ClaudePluginGap
	for index := range targets {
		target := &targets[index]
		if !pluginEnabled(document, target.ID) {
			gaps = append(gaps, ClaudePluginGap{"plugin " + target.ID + " not enabled in " + path, ClaudePluginRepair})
		}
		if pluginEnabled(document, target.GitHubID) && pluginEnabled(document, target.DevID) {
			// Every build's install disables the copy it does not ensure.
			gaps = append(gaps, ClaudePluginGap{
				fmt.Sprintf(
					"plugin %s enabled twice (%s and %s) in %s",
					target.Name,
					target.GitHubID,
					target.DevID,
					path,
				),
				ClaudePluginRepair,
			})
		}
		if target.Local {
			gap, err := claudePluginCopyGap(target)
			if err != nil {
				return nil, err
			}
			if gap != "" {
				gaps = append(gaps, ClaudePluginGap{"plugin " + target.DevID + " copy " + gap, ClaudePluginRepair})
			}
		}
	}
	return gaps, nil
}

// ClaudePluginsNotInstalled reads one account's plugins/installed_plugins.json
// and names every target id with no install record whose installPath
// exists. Accounts may share one settings.json, so this record — one per
// config dir — is what says a plugin is installed in THIS account. A missing
// file means none is installed; a file that cannot be read or parsed returns
// its error, never an answer.
func ClaudePluginsNotInstalled(configDir string, targets []ClaudePluginTarget) ([]string, error) {
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
	for index := range targets {
		target := &targets[index]
		installed := false
		for _, entry := range record.Plugins[target.ID] {
			if info, statErr := os.Stat(entry.InstallPath); entry.InstallPath != "" && statErr == nil && info.IsDir() {
				installed = true
				break
			}
		}
		if !installed {
			missing = append(missing, target.ID)
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
// An -alpha build ensures each plugin with a checkout from its local copy
// (ClaudePluginTargets); every build then disables the copy it does not ensure.
func (installer *engine) ensureClaudePlugins(ctx context.Context) error {
	dir := installer.options.PrimaryConfigDir
	if dir == "" {
		dir = installer.options.ConfigDir
	}
	if len(installer.options.ClaudeAccounts) > 0 || installer.options.ClaudeRosterHost {
		if err := installer.checkLaunchConfigDir(0, dir); err != nil {
			return installer.pluginStepFailure(dir, err)
		}
	}
	targets := ClaudePluginTargets(ClaudePluginBuild{
		Version:      installer.options.Version,
		Home:         installer.options.Home,
		CheckoutRoot: installer.options.ClaudePluginCheckoutRoot,
	})
	for index := range targets {
		target := &targets[index]
		if target.Fallback != "" {
			installer.skip("claude plugin " + target.FallbackNote())
		}
	}
	settingsPath := filepath.Join(dir, "settings.json")
	document, err := readClaudeSettingsDocument(settingsPath)
	if err != nil {
		return installer.pluginStepFailure(dir, fmt.Errorf("settings unreadable, state unknown: %w", err))
	}
	missing, err := ClaudePluginsNotInstalled(dir, targets)
	if err != nil {
		return installer.pluginStepFailure(dir, fmt.Errorf("install record unreadable, state unknown: %w", err))
	}
	binary, resolveErr := ResolveClaudeBinary(
		installer.options.Home,
		installer.options.ClaudeBinary,
		installer.env().Get("PATH"),
	)
	var failures []error
	guarded, blocked := false, false
	// blockedByLiveChats asks once, before the first command or settings edit.
	blockedByLiveChats := func() (bool, error) {
		if guarded {
			return blocked, nil
		}
		guarded = true
		live, liveErr := installer.pluginLiveChats(dir)
		blocked = live
		return live, liveErr
	}
	// probeFailed ends the loop at a failed live-chat probe, keeping every
	// failure an earlier plugin already hit. A live chat itself holds back only
	// commands and settings edits: every later plugin's copy is still refreshed.
	probeFailed := func(id string, err error) error {
		failures = append(failures, installer.pluginFailure(dir, id, err))
		return errors.Join(failures...)
	}
	for index := range targets {
		target := &targets[index]
		if target.Local {
			if err := installer.ensureClaudePluginCopy(target); err != nil {
				failures = append(failures, installer.pluginFailure(dir, target.ID, err))
				continue
			}
		}
		if pluginEnabled(document, target.ID) && !slices.Contains(missing, target.ID) {
			installer.ok("claude plugin " + target.ID + " installed and enabled in " + dir)
		} else {
			if resolveErr != nil {
				installer.skip(
					fmt.Sprintf(
						"claude plugin %s in %s: real claude binary not resolved: %v",
						target.ID,
						dir,
						resolveErr,
					),
				)
				continue
			}
			stop, err := blockedByLiveChats()
			if err != nil {
				return probeFailed(target.ID, err)
			}
			if stop {
				continue
			}
			if err := installer.installClaudePlugin(ctx, binary, dir, target); err != nil {
				failures = append(failures, installer.pluginFailure(dir, target.ID, err))
				continue
			}
		}
		message, edit, err := planOtherCopyRetirement(settingsPath, target)
		if err != nil {
			failures = append(failures, installer.pluginFailure(dir, target.ID, err))
			continue
		}
		if message == "" {
			continue
		}
		stop, err := blockedByLiveChats()
		if err != nil {
			return probeFailed(target.ID, err)
		}
		if stop {
			continue
		}
		if err := installer.change(message, edit); err != nil {
			failures = append(failures, installer.pluginFailure(dir, target.ID, err))
		}
	}
	return errors.Join(failures...)
}

// pluginLiveChats reports whether a live chat on the chosen dir, a passed
// account or a roster dir holds the plugin commands back, printing the skip
// line that names them.
func (installer *engine) pluginLiveChats(dir string) (bool, error) {
	dirs := []string{dir}
	for _, account := range installer.options.ClaudeAccounts {
		dirs = append(dirs, account.ConfigDir)
	}
	dirs = append(dirs, installer.options.RosterConfigDirs...)
	seen := map[string]bool{}
	pids := map[int]bool{}
	liveDirs := []string{}
	for _, accountDir := range dirs {
		if accountDir == "" {
			continue
		}
		physical := paths.PhysicalPath(accountDir)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		live, err := liveChatPIDs(installer.options.ProcRoot, accountDir)
		if err != nil {
			return false, fmt.Errorf("read live chats in %s: %w", accountDir, err)
		}
		if len(live) > 0 {
			liveDirs = append(liveDirs, accountDir)
		}
		for _, pid := range live {
			id, _ := strconv.Atoi(pid)
			pids[id] = true
		}
	}
	if len(pids) == 0 {
		return false, nil
	}
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
	return true, nil
}

func (installer *engine) installClaudePlugin(
	ctx context.Context,
	binary, dir string,
	target *ClaudePluginTarget,
) error {
	add := []string{binary, "plugin", "marketplace", "add", target.Source}
	install := []string{binary, "plugin", "install", target.ID, "-y"}
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
	return installer.fail(fmt.Errorf("claude plugin %s in %s: %w", plugin, dir, err))
}

func (installer *engine) pluginStepFailure(dir string, err error) error {
	return installer.fail(fmt.Errorf("claude plugins in %s: %w", dir, err))
}
