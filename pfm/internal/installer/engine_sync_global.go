package installer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// engineSyncGlobalTimeout bounds one `engine-sync global` run, which an install
// makes twice (the preflight --plan and the apply): a wedged engine-sync ends
// the step with an error naming it and this deadline instead of hanging
// `pfm install` and `pfm update` with no message.
const engineSyncGlobalTimeout = 5 * time.Minute

// engineSyncMissingRepair is what an adopter does when the install finds no
// engine-sync binary and has Codex homes or a Professor clone to wire: the
// plugin's wrapper downloads the release binary at the next Claude session
// start and links it at the stable path (README.md § engine-sync).
const engineSyncMissingRepair = "install the engine-sync plugin: run `claude plugin install engine-sync@professor`, " +
	"start a new Claude session so the plugin wrapper downloads and links the binary, " +
	"then rerun pfm update (or pfm install --yes)"

// engineSync is the engine-sync runner this install drives: its environment
// and home.
func (installer *engine) engineSync() enginesync.Runner {
	return enginesync.Runner{
		Env:          installer.env(),
		Home:         installer.options.Home,
		Exec:         installer.engineSyncExec,
		Capabilities: enginesync.HostCapabilities(installer.options.GptAgents),
	}
}

// engineSyncGlobalDeadline is the bound of one `engine-sync global` run.
func (installer *engine) engineSyncGlobalDeadline() time.Duration {
	if installer.engineSyncDeadline > 0 {
		return installer.engineSyncDeadline
	}
	return engineSyncGlobalTimeout
}

// deferEngineSyncUninstallFault handles an engine-sync fault on an uninstall
// that is not an absent binary (too old, a failing global run, a conflict):
// the preview skips with the fault's text, so the preflight does not refuse the
// teardown; the apply defers the failure, so every unwire step still runs and
// the run exits non-zero naming the fault.
func (installer *engine) deferEngineSyncUninstallFault(fault error) {
	if !installer.apply {
		installer.skip(fmt.Sprintf(
			"engine-sync fault, deferred to the apply (Codex roles, global commands left in place): %v", fault,
		))
		return
	}
	installer.deferFailure(fault)
}

// wireEngineSyncGlobal runs the installer's Codex steps through `engine-sync
// global`: the global agents (Claude links, declared variants, Codex roles),
// the retirement of orphaned and renamed roles and undeclared variants, the
// global commands, and the retired explorer role's strays; under uninstall,
// the retirement of all of them. A dry run passes --plan, so the preview IS the
// apply's list, with the future command roster staged as --source-home exactly
// as the commands step would see it. Every event is rendered through this
// installer's own transcript; a conflict blocks the apply and is a plan error
// of a preview.
func (installer *engine) wireEngineSyncGlobal(assets []assetFile) (returnErr error) {
	sourceRepo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return fmt.Errorf("resolve the engine-sync global source repository: %w", err)
	}
	runner := installer.engineSync()
	if err := runner.RequireMinimumVersion(context.Background()); err != nil {
		var unavailable *enginesync.UnavailableError
		if installer.options.Mode == ModeUninstall {
			if errors.As(err, &unavailable) {
				return installer.leaveEngineSyncOwned(err)
			}
			installer.deferEngineSyncUninstallFault(err)
			return nil
		}
		if errors.As(err, &unavailable) && len(installer.codexHomes()) == 0 {
			if _, inspectErr := os.Stat(sourceRepo); errors.Is(inspectErr, fs.ErrNotExist) {
				installer.skip(err.Error() + " (no source clone or Codex homes configured)")
				return nil
			} else if inspectErr != nil {
				return errors.Join(
					err,
					fmt.Errorf("inspect engine-sync global source repository %s: %w", sourceRepo, inspectErr),
				)
			}
		}
		if errors.As(err, &unavailable) {
			return fmt.Errorf("%w — %s", err, engineSyncMissingRepair)
		}
		return err
	}
	reason, err := installer.engineSyncPreviewSkip(sourceRepo)
	if err != nil {
		return err
	}
	if reason != "" {
		installer.skip(reason)
		return nil
	}
	options := enginesync.GlobalOptions{
		SourceRepo:       sourceRepo,
		ClaudeConfigDirs: []string{installer.options.ConfigDir},
		CodexHomes:       installer.codexHomes(),
		Plan:             !installer.apply,
		Uninstall:        installer.options.Mode == ModeUninstall,
		Adopt:            installer.options.AdoptAgents,
	}
	if options.Plan && !options.Uninstall && len(options.CodexHomes) != 0 &&
		filepath.Clean(
			installer.options.ConfigDir,
		) == filepath.Join(
			filepath.Clean(installer.options.Home),
			".claude",
		) {
		sourceHome, cleanup, err := installer.futureCommandSource(assets)
		if err != nil {
			return err
		}
		defer func() {
			if err := cleanup(); err != nil {
				returnErr = errors.Join(returnErr, err)
			}
		}()
		options.SourceHome = sourceHome
	}
	deadline := installer.engineSyncGlobalDeadline()
	globalContext, cancelGlobal := context.WithTimeout(context.Background(), deadline)
	result, runErr := runner.Global(globalContext, options)
	if runErr != nil && errors.Is(globalContext.Err(), context.DeadlineExceeded) {
		runErr = fmt.Errorf("engine-sync global timed out after %s: %w", deadline, runErr)
	}
	cancelGlobal()
	var conflicts []string
	for _, event := range result.Events {
		switch event.Verb {
		case enginesync.VerbChange:
			if err := installer.change(event.Message, nil); err != nil {
				return err
			}
		case enginesync.VerbRetire:
			installer.markRemoved(event.Path)
			if err := installer.change(event.Message, nil); err != nil {
				return err
			}
		case enginesync.VerbOK:
			installer.ok(event.Message)
		case enginesync.VerbSkip:
			installer.skip(event.Message)
		case enginesync.VerbConflict:
			installer.say("  conflict %s", event.Message)
			installer.record(enginesync.VerbConflict, event.Message, nil)
			conflicts = append(conflicts, event.Message)
		}
	}
	if runErr != nil {
		var unavailable *enginesync.UnavailableError
		if options.Uninstall && errors.As(runErr, &unavailable) {
			return installer.leaveEngineSyncOwned(runErr)
		}
		if options.Uninstall {
			installer.deferEngineSyncUninstallFault(runErr)
			return nil
		}
		return runErr
	}
	if result.OK {
		return nil
	}
	conflictErr := fmt.Errorf("engine-sync global: %s", strings.Join(conflicts, "; "))
	if options.Uninstall {
		installer.deferEngineSyncUninstallFault(conflictErr)
		return nil
	}
	if !installer.apply {
		installer.planErrors = append(installer.planErrors, conflictErr)
		return nil
	}
	return conflictErr
}

// engineSyncAbsentText is what an uninstall leaves behind when engine-sync is
// not installed: pfm tears down everything it owns itself, and only what
// engine-sync writes (the Codex roles, the global commands) and the global
// file whose targets serve it stay for a second uninstall.
const engineSyncAbsentText = "engine-sync unavailable: Codex roles, global commands and ~/.claude/engine-sync.json left in place; " +
	"run pfm uninstall again with engine-sync present"

// leaveEngineSyncOwned handles an uninstall whose engine-sync binary is
// absent. The preview skips with the text and raises no plan error, so the
// preflight does not refuse the rest of the teardown; the apply defers the
// failure, so the run finishes and still exits non-zero. An install never
// reaches here: it either skips an empty global step or fails the install.
func (installer *engine) leaveEngineSyncOwned(cause error) error {
	installer.engineSyncAbsent = true
	if !installer.apply {
		installer.skip(engineSyncAbsentText)
		return nil
	}
	installer.deferFailure(fmt.Errorf("%s: %w", engineSyncAbsentText, cause))
	return nil
}

func (installer *engine) ownsEngineSyncPolicy(target, sourceRepo string) (bool, error) {
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect engine-sync policy %s: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	found, err := os.Readlink(target)
	if err != nil {
		return false, fmt.Errorf("read engine-sync policy link %s: %w", target, err)
	}
	if !filepath.IsAbs(found) {
		found = filepath.Join(filepath.Dir(target), found)
	}
	repos, err := installer.engineSyncPolicySourceRepos()
	if err != nil {
		return false, err
	}
	repos = append(repos, sourceRepo, filepath.Join(installer.options.Home, ".professor"))
	return ownedGlobalLink(repos, "engine-sync", "config.json", filepath.Clean(found)), nil
}

// A retired clone still identifies its policy link; other registries retain
// their live-source validation before deciding what can be removed.
func (installer *engine) engineSyncPolicySourceRepos() ([]string, error) {
	repos, err := installer.recordedProfessorSourceRepos()
	if !errors.Is(err, paths.ErrSourceRepoUnusable) {
		return repos, err
	}
	marker := paths.SourceRepoPath(installer.options.Home)
	raw, err := os.ReadFile(marker)
	if err != nil {
		return nil, fmt.Errorf("read historical policy source repository marker %s: %w", marker, err)
	}
	repo := strings.TrimSpace(string(raw))
	if !filepath.IsAbs(repo) || strings.ContainsAny(repo, "\r\n") {
		return nil, fmt.Errorf("historical policy source repository marker %s is not one absolute path", marker)
	}
	return []string{paths.PhysicalPath(filepath.Clean(repo))}, nil
}
