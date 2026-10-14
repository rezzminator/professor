package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

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

// ClaudePluginGap is one plugin problem pfm doctor reports and its repair.
type ClaudePluginGap struct {
	Problem string
	Fix     string
}

// ErrClaudeSettingsAbsent is ClaudePluginGaps' answer for a store with no
// settings.json: a store never set up, not one missing its plugins.
var ErrClaudeSettingsAbsent = errors.New("no settings.json")

// ClaudePluginGaps reads the store's settings.json and names every target id
// not enabled, every plugin of ours enabled from both its GitHub and its -dev
// marketplace, every local target whose copy is missing or a symlink, and every
// other plugin enabled under two or more keys. A
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
	return append(gaps, claudePluginDuplicateGaps(document, path, targets)...), nil
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

// ClaudePluginsUnresolved names every listed full id pfm cannot install: still
// to be installed or enabled in configDir, its entry without a source, and its
// marketplace not among configDir's plugins/known_marketplaces.json. Claude
// Code resolves such an id only against a marketplace it already knows, so
// install and doctor report it by name with the reason and the repair, keyed by
// id. A missing known_marketplaces.json means none is known; a file that cannot
// be read or parsed returns its error, never an answer. Nothing is read when no
// target is a candidate.
func ClaudePluginsUnresolved(configDir string, targets []ClaudePluginTarget) (map[string]ClaudePluginGap, error) {
	var candidates []ClaudePluginTarget
	for index := range targets {
		if target := targets[index]; target.NeedsKnownMarketplace() {
			candidates = append(candidates, target)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	document, err := readClaudeSettingsDocument(filepath.Join(configDir, "settings.json"))
	if err != nil {
		return nil, err
	}
	missing, err := ClaudePluginsNotInstalled(configDir, candidates)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(configDir, "plugins", "known_marketplaces.json")
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	known := map[string]json.RawMessage{}
	if err == nil {
		if err := json.Unmarshal(raw, &known); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	unresolved := map[string]ClaudePluginGap{}
	for index := range candidates {
		target := &candidates[index]
		if pluginEnabled(document, target.ID) && !slices.Contains(missing, target.ID) {
			continue
		}
		if _, ok := known[target.MarketplaceName]; ok {
			continue
		}
		unresolved[target.ID] = ClaudePluginGap{
			Problem: "marketplace " + target.MarketplaceName +
				" is not known to Claude Code and its " + pfmconfig.KeyClaudePlugins + " entry has no source",
			Fix: ClaudePluginUnresolvedFix,
		}
	}
	return unresolved, nil
}

// ClaudePluginMarketplaceGaps reads the store's plugins/known_marketplaces.json
// and names every target whose marketplace is registered from a source other
// than target.Source: `plugin marketplace add` answers "already added" for a
// same-named marketplace, so pfm install keeps installing from the wrong source
// until it is removed. A target with no source adds no marketplace and is not
// compared. A source compares by its form (claudeMarketplaceSourceMatches); a
// form whose record pfm cannot rebuild is not compared, never named with a
// repair that cannot close it. A missing file or entry names nothing — the
// enabled and installed checks cover a plugin never added; a file that cannot
// be read or parsed returns its error, never an answer. A string read from the
// file prints with its control characters escaped.
func ClaudePluginMarketplaceGaps(store string, targets []ClaudePluginTarget) ([]ClaudePluginGap, error) {
	path := filepath.Join(store, "plugins", "known_marketplaces.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var known map[string]struct {
		Source claudeMarketplaceRecord `json:"source"`
	}
	if err := json.Unmarshal(raw, &known); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var gaps []ClaudePluginGap
	for index := range targets {
		target := &targets[index]
		if target.Source == "" {
			continue
		}
		_, marketplace, _ := strings.Cut(target.ID, "@")
		entry, registered := known[marketplace]
		if !registered || claudeMarketplaceSourceMatches(target.Source, entry.Source) {
			continue
		}
		var got string
		switch entry.Source.Kind {
		case claudeMarketplaceGitHub:
			got = entry.Source.Repo
		case claudeMarketplaceDirectory:
			got = entry.Source.Path
		default:
			got = strings.TrimSpace(entry.Source.Kind + " " + entry.Source.URL)
		}
		gaps = append(gaps, ClaudePluginGap{
			fmt.Sprintf(
				"plugin %s marketplace %s in %s points at %s, want %s",
				target.ID,
				marketplace,
				path,
				escapeControl(got),
				target.Source,
			),
			"claude plugin marketplace remove " + marketplace + ", then " + ClaudePluginRepair,
		})
	}
	return gaps, nil
}

// claudeMarketplaceRecord is the source Claude Code records for one marketplace
// in known_marketplaces.json: a github repo, a git or url record's url, or a
// directory's path.
type claudeMarketplaceRecord struct {
	Kind string `json:"source"`
	Repo string `json:"repo"`
	Path string `json:"path"`
	URL  string `json:"url"`
}

// The record kinds pfm compares a source with.
const (
	claudeMarketplaceGitHub    = "github"
	claudeMarketplaceDirectory = "directory"
)

// claudeGitHubShorthand is an owner/repo source, with an optional #ref.
var claudeGitHubShorthand = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_.-]+(#\S+)?$`)

// claudeMarketplaceSourceMatches reports whether record is what `plugin
// marketplace add source` registers, or a record pfm cannot compare with it. An
// owner/repo[#ref] source matches a github record of the repo, the ref
// stripped; an absolute path a directory record of the same clean path; a URL a
// record holding that url, and a github record (one Claude Code may make from a
// GitHub URL) is not compared. A relative or ~ path resolves where the add
// runs, so a directory record is not compared. Every other pairing is a
// different source.
func claudeMarketplaceSourceMatches(source string, record claudeMarketplaceRecord) bool {
	switch {
	case strings.Contains(source, "://") || strings.HasPrefix(source, "git@"):
		return record.Kind == claudeMarketplaceGitHub || (record.URL != "" && record.URL == source)
	case filepath.IsAbs(source):
		return record.Kind == claudeMarketplaceDirectory && filepath.Clean(record.Path) == filepath.Clean(source)
	case claudeGitHubShorthand.MatchString(source):
		repo, _, _ := strings.Cut(source, "#")
		return record.Kind == claudeMarketplaceGitHub && record.Repo == repo
	default:
		return record.Kind == claudeMarketplaceDirectory
	}
}

// claudePluginDuplicateGaps names every plugin enabled under two or more
// name@marketplace keys in document, managed by pfm or not, sorted by name. A
// plugin whose every enabled key claude.plugins lists is left alone: pfm
// install enables each listed key again. A plugin of ours enabled as exactly
// its GitHub and -dev ids is left to ClaudePluginGaps' own duplicate gap, whose
// repair pfm install owns. A key from the file prints with its control
// characters escaped.
func claudePluginDuplicateGaps(document map[string]any, path string, targets []ClaudePluginTarget) []ClaudePluginGap {
	enabled, _ := document["enabledPlugins"].(map[string]any)
	byName := map[string][]string{}
	for id, value := range enabled {
		if value != true {
			continue
		}
		name, _, _ := strings.Cut(id, "@")
		byName[name] = append(byName[name], id)
	}
	listed := map[string]bool{}
	managed := map[string][]string{}
	for index := range targets {
		listed[targets[index].ID] = true
		if targets[index].Own {
			pair := []string{targets[index].GitHubID, targets[index].DevID}
			slices.Sort(pair)
			managed[targets[index].Name] = pair
		}
	}
	var gaps []ClaudePluginGap
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		ids := byName[name]
		if len(ids) < 2 {
			continue
		}
		slices.Sort(ids)
		if slices.Equal(ids, managed[name]) || !slices.ContainsFunc(ids, func(id string) bool { return !listed[id] }) {
			continue
		}
		shown := make([]string, len(ids))
		for index, id := range ids {
			shown[index] = escapeControl(id)
		}
		gaps = append(gaps, ClaudePluginGap{
			fmt.Sprintf(
				"plugin %s enabled %s (%s) in %s",
				escapeControl(name), timesWord(len(ids)), strings.Join(shown, " and "), path,
			),
			"keep one copy: claude plugin disable each other key",
		})
	}
	return gaps
}

// escapeControl is text read from a file as one printed line: each control
// character escaped as Go spells it, so a newline in a key or a url cannot
// forge a line of its own.
func escapeControl(text string) string {
	if !strings.ContainsFunc(text, unicode.IsControl) {
		return text
	}
	quoted := strconv.Quote(text)
	return quoted[1 : len(quoted)-1]
}

// timesWord spells how many keys enable one plugin.
func timesWord(count int) string {
	if count == 2 {
		return "twice"
	}
	return strconv.Itoa(count) + " times"
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
