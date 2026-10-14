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
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
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

// ClaudePluginRepair is the repair every plugin gap pfm install closes names.
const ClaudePluginRepair = "run pfm install --yes"

// ClaudePluginUnresolvedFix is the repair for a listed plugin pfm cannot install.
const ClaudePluginUnresolvedFix = `give its claude.plugins entry a "source" (what claude plugin marketplace add takes), ` +
	`or run claude plugin marketplace add <source>, then run pfm install --yes`

// ClaudePluginBuild is what decides where each plugin comes from: pfm's own
// stamped version, the home pfm's paths resolve from, claude.pluginCheckoutRoot,
// and the source clone whose plugins/ is the checkout root when that key is
// unset (SourceRepoErr when the clone could not be resolved).
type ClaudePluginBuild struct {
	Version       string
	Home          string
	CheckoutRoot  string
	SourceRepo    string
	SourceRepoErr error
	Plugins       []pfmconfig.ClaudePlugin
}

// ClaudePluginTarget is one claude.plugins entry as this build ensures it. ID
// is the enabledPlugins key ensured and Source what `plugin marketplace add`
// takes, empty for a full id whose entry has no source. For a bare name (Own):
// the GitHub repository and GitHubID on a release, or on an -alpha build with
// a checkout (Local) the pfm-owned marketplace dir Marketplace, holding a copy
// of Checkout, and DevID. Fallback says why an -alpha build ensures the GitHub
// copy instead; empty on a release. Undecided says why an -alpha build could
// not tell whether a checkout exists (its source clone or checkout could not
// be read): install and doctor then decide neither copy, never reading the
// error as a missing checkout.
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
	Undecided   string
	// Own marks a bare name of ours, which has a GitHub copy and a -dev copy.
	Own bool
	// MarketplaceName is the marketplace a full id names; empty for a bare name.
	MarketplaceName string
}

// NeedsKnownMarketplace reports whether the target installs only from a
// marketplace Claude Code already knows: a full id whose entry has no source.
func (target ClaudePluginTarget) NeedsKnownMarketplace() bool {
	return target.Source == "" && target.MarketplaceName != ""
}

// ClaudePluginTargets plans every claude.plugins entry for build. A bare name is
// a plugin of ours: its GitHub repository rezzminator/{name} with the id
// {name}@{name}, or on an -alpha build with a checkout its local -dev copy. A
// full id is ensured as named, from the entry's source when it has one.
func ClaudePluginTargets(build ClaudePluginBuild) []ClaudePluginTarget {
	alpha := obs.AlphaBuild(build.Version)
	targets := make([]ClaudePluginTarget, 0, len(build.Plugins))
	for _, plugin := range build.Plugins {
		if !plugin.Bare() {
			targets = append(targets, ClaudePluginTarget{
				Name:            plugin.Name(),
				ID:              plugin.ID,
				Source:          plugin.Source,
				MarketplaceName: plugin.Marketplace(),
			})
			continue
		}
		id := plugin.ID + "@" + plugin.ID
		target := ClaudePluginTarget{
			Name:     plugin.ID,
			ID:       id,
			GitHubID: id,
			DevID:    plugin.ID + "@" + devMarketplaceName(plugin.ID),
			Source:   claudePluginOwner + "/" + plugin.ID,
			Own:      true,
		}
		if alpha {
			target = localClaudePluginTarget(target, build)
		}
		targets = append(targets, target)
	}
	return targets
}

// BareClaudePluginRepair is the repair install's FAIL line (and pfm doctor)
// names when a bare claude.plugins name could not be installed: pfm cannot know
// offline whether the GitHub repository the name stands for exists.
func BareClaudePluginRepair(name string) string {
	return fmt.Sprintf(
		"claude.plugins entry %q is a bare name: pfm installs it from github.com/%s/%s — "+
			"fix or remove the entry in claude.plugins if that repository does not exist",
		name, claudePluginOwner, name,
	)
}

// FallbackNote is the one line install and doctor print for a Fallback.
func (target ClaudePluginTarget) FallbackNote() string {
	return target.Name + ": alpha build falls back to the GitHub copy " + target.GitHubID + " — " + target.Fallback
}

// UndecidedNote names an Undecided target's two copies and why neither is chosen.
func (target ClaudePluginTarget) UndecidedNote() string {
	return "its copy is undecided (" + target.GitHubID + " or " + target.DevID + "): " + target.Undecided
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
// Options.ConfigDir fallback (the store) never runs. A host with no roster,
// whose Options.ConfigDir is the store, skips the step with one named line: no
// claude subprocess runs on the store (it would write .claude.json and backups
// there). A --config-dir on a roster host carries no roster but ClaudeRosterHost, so the same refusal holds.
// An -alpha build ensures each bare name with a checkout from its local copy
// (ClaudePluginTargets); every build then disables the copy of ours it does not
// ensure. A bare name whose source is undecided is a failure naming the error,
// both its copies left as they are. A listed id pfm cannot install is a
// failure naming it; an id the list does not name is never read or written.
func (installer *engine) ensureClaudePlugins(ctx context.Context) error {
	dir := installer.options.PrimaryConfigDir
	if dir == "" {
		dir = installer.options.ConfigDir
	}
	if len(installer.options.ClaudeAccounts) > 0 || installer.options.ClaudeRosterHost {
		if err := installer.checkLaunchConfigDir(0, dir); err != nil {
			return installer.pluginStepFailure(dir, err)
		}
	} else if claudelaunch.InspectConfigDir(
		claudelaunch.ClaudeStore(installer.options.Home), dir,
	).State == claudelaunch.ConfigDirStore {
		installer.skip(fmt.Sprintf(
			"claude plugins: not run — no account roster, so the only config dir is the Claude store %s, where a claude process writes account identity; add an account to pfm.config.json and rerun pfm install --yes",
			dir,
		))
		return nil
	}
	// The clone the global links anchor on is the one whose plugins/ an
	// -alpha build copies from when claude.pluginCheckoutRoot is unset.
	sourceRepo, sourceRepoErr := installer.globalSourceRepoRoot()
	targets := ClaudePluginTargets(ClaudePluginBuild{
		Version:       installer.options.Version,
		Home:          installer.options.Home,
		CheckoutRoot:  installer.options.ClaudePluginCheckoutRoot,
		SourceRepo:    sourceRepo,
		SourceRepoErr: sourceRepoErr,
		Plugins:       installer.options.ClaudePlugins,
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
	// An unreadable marketplace record fails only the ids that need it, below.
	unresolved, unresolvedErr := ClaudePluginsUnresolved(dir, targets)
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
		if target.Undecided != "" {
			// Neither copy is installed, enabled or disabled on an error.
			failures = append(failures, installer.pluginFailure(
				dir, target.Name, errors.New(target.UndecidedNote()+"; both left as they are"),
			))
			continue
		}
		if gap, ok := unresolved[target.ID]; ok {
			failures = append(
				failures,
				installer.pluginFailure(dir, target.ID, fmt.Errorf("%s — %s", gap.Problem, gap.Fix)),
			)
			continue
		}
		ensured := pluginEnabled(document, target.ID) && !slices.Contains(missing, target.ID)
		if unresolvedErr != nil && target.NeedsKnownMarketplace() && !ensured {
			failures = append(failures, installer.pluginFailure(
				dir, target.ID, fmt.Errorf("marketplace record unreadable, state unknown: %w", unresolvedErr),
			))
			continue
		}
		if target.Local {
			if err := installer.ensureClaudePluginCopy(target); err != nil {
				failures = append(failures, installer.pluginFailure(dir, target.ID, err))
				continue
			}
		}
		if ensured {
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
				if target.Own && !target.Local {
					err = fmt.Errorf("%w — %s", err, BareClaudePluginRepair(target.Name))
				}
				failures = append(failures, installer.pluginFailure(dir, target.ID, err))
				continue
			}
		}
		if !target.Own {
			// A listed full id is ensured as named; its settings entries are all
			// that is touched, and no other id is its twin to retire.
			continue
		}
		for _, plan := range []func(string, *ClaudePluginTarget) (string, func() error, error){
			planOtherCopyRetirement, planRetiredPluginDisable,
		} {
			message, edit, err := plan(settingsPath, target)
			if err != nil {
				failures = append(failures, installer.pluginFailure(dir, target.ID, err))
				break
			}
			if message == "" {
				continue
			}
			stop, err := blockedByLiveChats()
			if err != nil {
				return probeFailed(target.ID, err)
			}
			if stop {
				break
			}
			if err := installer.change(message, edit); err != nil {
				failures = append(failures, installer.pluginFailure(dir, target.ID, err))
			}
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
	install := []string{binary, "plugin", "install", target.ID, "-y"}
	var add []string
	message := fmt.Sprintf("run %s=%s %s", claudeConfigDirEnv, dir, shellCommandLine(install...))
	if target.Source != "" {
		add = []string{binary, "plugin", "marketplace", "add", target.Source}
		message = fmt.Sprintf(
			"run %s=%s %s && %s", claudeConfigDirEnv, dir, shellCommandLine(add...), shellCommandLine(install...),
		)
	}
	return installer.change(message, func() error {
		options := deps.RunOptions{Env: pluginCommandEnvironment(dir)}
		var addErr error
		if add != nil {
			// An already-added marketplace exits non-zero; the install that
			// follows is the judge, so its failure carries this answer too.
			addErr = runClaudePluginCommand(ctx, installer.processRunner(), add, options)
		}
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
