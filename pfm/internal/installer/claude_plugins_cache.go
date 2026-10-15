package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// Claude Code runs an installed directory-marketplace plugin from a snapshot:
// install and update copy the marketplace copy into
// {config}/plugins/cache/{marketplace}/{name}/{version} and record that
// installPath and version per scope in plugins/installed_plugins.json. It takes
// a new snapshot only when the version changes, so a refreshed -dev copy reaches
// a chat only through `claude plugin update` after a version bump; a chat
// already running keeps the snapshot it loaded, which Claude leaves on disk.

// pluginCacheSkipped names the entries a snapshot and its checkout are
// compared without: what the copy leaves out, and the marker Claude drops into
// a snapshot no entry points at any more.
func pluginCacheSkipped(name string) bool { return pluginCopySkipped(name) || name == ".orphaned_at" }

// pluginCacheDigest maps every file under root to its content hash; modes are
// left out, the snapshot being Claude's copy, not pfm's.
func pluginCacheDigest(root string) (map[string]string, error) {
	digest := map[string]string{}
	err := walkPluginTree(root, pluginCacheSkipped, func(rel, path string, info fs.FileInfo) error {
		if info.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		digest[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	return digest, err
}

// claudePluginCache is a local target's snapshots in one account dir against
// its checkout: the checkout's version, every entry recording another, and a
// gap per snapshot at that version whose content differs. A checkout naming
// no version is unversioned: Claude caches by version, so nothing is compared.
type claudePluginCache struct {
	target      ClaudePluginTarget
	record      string
	want        string
	unversioned bool
	behind      []claudePluginInstall
	drifted     []ClaudePluginGap
}

// readClaudePluginCache reads target's snapshots in configDir. An entry whose
// installPath is gone has no content to compare: ClaudePluginsNotInstalled
// reports an id none of whose snapshots exists.
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
	var source map[string]string
	compared := map[string]bool{}
	for _, entry := range entries {
		if entry.version() != cache.want {
			cache.behind = append(cache.behind, entry)
			continue
		}
		if entry.InstallPath == "" || compared[entry.InstallPath] {
			continue
		}
		compared[entry.InstallPath] = true
		if _, err := os.Stat(entry.InstallPath); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if source == nil {
			if source, err = pluginCacheDigest(target.Checkout); err != nil {
				return cache, fmt.Errorf("read checkout %s: %w", target.Checkout, err)
			}
		}
		snapshot, err := pluginCacheDigest(entry.InstallPath)
		if err != nil {
			return cache, fmt.Errorf("read cache %s: %w", entry.InstallPath, err)
		}
		if !maps.Equal(source, snapshot) {
			cache.drifted = append(cache.drifted, cache.driftGap(entry))
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

// driftGap names a snapshot at the checkout's version whose content differs:
// an update leaves it, so only a version bump moves it.
func (cache claudePluginCache) driftGap(entry claudePluginInstall) ClaudePluginGap {
	return ClaudePluginGap{
		Problem: fmt.Sprintf("plugin %s cache %s differs from %s at version %s",
			cache.target.ID, EscapeControl(entry.InstallPath), cache.target.Checkout, EscapeControl(cache.want)),
		Fix: "bump the version in " + filepath.Join(cache.target.Checkout, ".claude-plugin", "plugin.json") +
			": Claude caches by version; then " + ClaudePluginRepair,
	}
}

// ClaudePluginCacheGaps names, for every local target in configDir, each
// install record entry behind its checkout's version, each snapshot at that
// version whose content differs from the checkout, and a recorded target
// whose checkout names no version. A record, checkout or
// snapshot that cannot be read is an error naming its target, never an answer.
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
		gaps = append(gaps, cache.drifted...)
	}
	return gaps, errors.Join(failures...)
}

// updateDir is the working directory `claude plugin update --scope` moves
// entry from: none for the user scope, its project for a project or local
// one. An entry pfm cannot move — a project that is not an absolute directory,
// a scope update does not take — returns the repair the user runs instead.
func (cache claudePluginCache) updateDir(entry claudePluginInstall) (dir, repair string) {
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
			shown, cache.target.ID, entry.Scope)
	default:
		return "", fmt.Sprintf("claude plugin update takes no scope %s: update it the way it was installed",
			EscapeControl(entry.Scope))
	}
}

// ensureClaudePluginCache moves every entry of an installed local target that
// records a version other than its checkout's with `claude plugin update {id}
// --scope {scope} -y`, once per scope and project, then reads the record back:
// an entry still behind is an error naming both versions. An entry pfm cannot
// move, or whose update fails, is an error too, the rest still updated. A
// snapshot at the checkout's version whose content differs is a warning, after
// the updates as before them: an update leaves it. A checkout naming no
// version is a skip line: Claude caches by version, so nothing is checked.
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
	installer.warnClaudePluginDrift(run, target, cache)
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
	installer.warnClaudePluginDrift(run, target, after)
	return errors.Join(failures...)
}

// warnClaudePluginDrift prints a warning per snapshot at the checkout's
// version whose content differs from the checkout.
func (installer *engine) warnClaudePluginDrift(
	run *claudePluginRun,
	target *ClaudePluginTarget,
	cache claudePluginCache,
) {
	for _, gap := range cache.drifted {
		installer.say("  warn    claude plugin %s in %s: %s — %s", target.ID, run.dir, gap.Problem, gap.Fix)
	}
}
