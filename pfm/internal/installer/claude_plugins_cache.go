package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// Claude Code install and update copy a directory-marketplace plugin into
// {config}/plugins/cache/{marketplace}/{name}/{version} and record that
// installPath and version per scope in plugins/installed_plugins.json, a new
// snapshot only when the version changes. A session never runs that snapshot:
// a plugin whose entry source is a path in a directory marketplace, every
// local copy's, is read in place from the copy, CLAUDE_PLUGIN_ROOT included (Claude
// Code 2.1.300-2.1.310, probed), so a refreshed copy reaches every new chat and
// /reload-plugins at its version. Only the recorded version is kept current.

// claudePluginCache is a local target's install record entries in one account
// dir against its checkout: the checkout's version and every entry recording
// another. A checkout naming no version is unversioned: Claude caches by
// version, so nothing is compared.
type claudePluginCache struct {
	target      ClaudePluginTarget
	record      string
	want        string
	unversioned bool
	behind      []claudePluginInstall
}

// readClaudePluginCache reads target's install record entries in configDir.
func readClaudePluginCache(configDir string, target ClaudePluginTarget) (claudePluginCache, error) {
	cache := claudePluginCache{
		target: target,
		record: filepath.Join(configDir, "plugins", "installed_plugins.json"),
	}
	record, err := readClaudePluginInstalls(configDir)
	entries := record[target.ID]
	if err != nil || len(entries) == 0 {
		return cache, err
	}
	if cache.want, err = ClaudePluginCheckoutVersion(target.Checkout); err != nil {
		return cache, err
	}
	if cache.want == "" {
		cache.unversioned = true
		return cache, nil
	}
	for _, entry := range entries {
		if entry.version() != cache.want {
			cache.behind = append(cache.behind, entry)
		}
	}
	return cache, nil
}

// behindGap names an entry recording a version other than the checkout's,
// with the repair updateDir names for an entry pfm cannot move.
func (cache claudePluginCache) behindGap(entry claudePluginInstall) ClaudePluginGap {
	scope := "scope " + EscapeControl(entry.Scope)
	if entry.ProjectPath != "" {
		scope += " " + EscapeControl(entry.ProjectPath)
	}
	recorded := "version " + EscapeControl(entry.version())
	if entry.version() == "" {
		recorded = "no version string"
	}
	gap := ClaudePluginGap{
		Problem: fmt.Sprintf("plugin %s cache in %s records %s (%s), checkout %s is %s",
			cache.target.ID, cache.record, recorded, scope, cache.target.Checkout,
			EscapeControl(cache.want)),
		Fix: ClaudePluginRepair,
	}
	if _, repair := cache.updateDir(entry); repair != "" {
		gap.Fix = repair
	}
	return gap
}

// unversionedGap names a checkout whose plugin.json gives no version: Claude
// caches by version, so its snapshots cannot be checked or moved.
func (cache claudePluginCache) unversionedGap() ClaudePluginGap {
	manifest := filepath.Join(cache.target.Checkout, ".claude-plugin", "plugin.json")
	return ClaudePluginGap{
		Problem: fmt.Sprintf(
			"plugin %s cache in %s cannot be checked: %s names no version, and Claude caches by version",
			cache.target.ID,
			cache.record,
			manifest,
		),
		Fix: "add a version to " + manifest + ", then run pfm install --yes",
	}
}

// ClaudePluginCacheGaps names, for every local target in configDir, each
// install record entry behind its checkout's version and a recorded target
// whose checkout names no version. A snapshot's content is never compared:
// Claude reads the copy in place. A record or checkout that cannot be read is
// an error naming its target, never an answer.
func ClaudePluginCacheGaps(configDir string, targets []ClaudePluginTarget) ([]ClaudePluginGap, error) {
	var gaps []ClaudePluginGap
	var failures []error
	for index := range targets {
		target := targets[index]
		if !target.Local || target.Undecided != "" {
			continue
		}
		cache, err := readClaudePluginCache(configDir, target)
		if err != nil {
			failures = append(failures, fmt.Errorf("plugin %s cache: %w", target.ID, err))
			continue
		}
		if cache.unversioned {
			gaps = append(gaps, cache.unversionedGap())
		}
		for _, entry := range cache.behind {
			gaps = append(gaps, cache.behindGap(entry))
		}
	}
	return gaps, errors.Join(failures...)
}

// updateDir is the working directory `claude plugin update --scope` moves
// entry from (claudePluginScopeDir).
func (cache claudePluginCache) updateDir(entry claudePluginInstall) (dir, repair string) {
	return claudePluginScopeDir(cache.target.ID, "update", entry)
}

// claudePluginScopeDir is the working directory a `claude plugin {verb}
// --scope` of id's entry runs in: none for the user scope, its project for a
// project or local one. An entry pfm cannot reach — a project that is not an
// absolute directory, a scope the command does not take — returns the repair
// the user runs instead.
func claudePluginScopeDir(id, verb string, entry claudePluginInstall) (dir, repair string) {
	switch entry.Scope {
	case "user":
		return "", ""
	case "project", "local":
		project := entry.ProjectPath
		if info, err := os.Stat(project); filepath.IsAbs(project) && err == nil && info.IsDir() {
			return project, ""
		}
		shown := EscapeControl(project)
		if shown == "" {
			shown = `""`
		}
		return "", fmt.Sprintf("its project %s is not an absolute directory: recreate it and run pfm install --yes,"+
			" or remove the entry with claude plugin uninstall %s --scope %s from that project",
			shown, id, entry.Scope)
	default:
		return "", fmt.Sprintf("claude plugin %s takes no scope %s: %s it the way it was installed",
			verb, EscapeControl(entry.Scope), verb)
	}
}

// ensureClaudePluginCache moves every entry of an installed local target that
// records a version other than its checkout's with `claude plugin update {id}
// --scope {scope} -y`, once per scope and project, then reads the record back:
// an entry still behind is an error naming both versions. An entry pfm cannot
// move, or whose update fails, is an error too, the rest still updated. A
// checkout naming no version is a skip line: Claude caches by version, so
// nothing is checked.
func (installer *engine) ensureClaudePluginCache(
	ctx context.Context,
	run *claudePluginRun,
	target *ClaudePluginTarget,
) error {
	cache, err := readClaudePluginCache(run.dir, *target)
	if err != nil {
		return err
	}
	if cache.unversioned {
		gap := cache.unversionedGap()
		installer.skip(fmt.Sprintf("claude plugin %s in %s: %s — %s", target.ID, run.dir, gap.Problem, gap.Fix))
		return nil
	}
	if len(cache.behind) == 0 {
		return nil
	}
	if run.resolveErr != nil {
		return fmt.Errorf("%s, and the real claude binary that updates it is not resolved: %w",
			cache.behindGap(cache.behind[0]).Problem, run.resolveErr)
	}
	var failures []error
	moved := map[[2]string]bool{}
	for _, entry := range cache.behind {
		gap := cache.behindGap(entry)
		dir, repair := cache.updateDir(entry)
		if repair != "" {
			failures = append(failures, errors.New(gap.Problem+" — "+repair))
			continue
		}
		key := [2]string{entry.Scope, dir}
		if _, seen := moved[key]; seen {
			continue
		}
		update := []string{run.binary, claudePluginCommand, "update", target.ID, "--scope", entry.Scope, "-y"}
		line := fmt.Sprintf("%s=%s %s", claudeConfigDirEnv, run.dir, shellCommandLine(update...))
		if dir != "" {
			line = "cd " + shellCommandLine(dir) + " && " + line
		}
		err := installer.change("run "+line+": "+gap.Problem, func() error {
			options := deps.RunOptions{Env: pluginCommandEnvironment(run.dir), Dir: dir}
			return runClaudePluginCommand(ctx, installer.processRunner(), update, options)
		})
		moved[key] = err == nil
		if err != nil {
			failures = append(failures, err)
		}
	}
	if !installer.apply {
		return errors.Join(failures...)
	}
	after, err := readClaudePluginCache(run.dir, *target)
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, entry := range after.behind {
		// An entry already failed above is named once.
		if dir, repair := after.updateDir(entry); repair == "" && moved[[2]string{entry.Scope, dir}] {
			failures = append(failures, errors.New(after.behindGap(entry).Problem+" after claude plugin update"))
		}
	}
	return errors.Join(failures...)
}
