package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/enginesync"
)

// engineSync is the engine-sync runner this install drives: its environment
// and home.
func (installer *engine) engineSync() enginesync.Runner {
	return enginesync.Runner{Env: installer.env(), Home: installer.options.Home}
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
	options := enginesync.GlobalOptions{
		SourceRepo:       sourceRepo,
		ClaudeConfigDirs: []string{installer.options.ConfigDir},
		CodexHomes:       installer.codexHomes(),
		Plan:             !installer.apply,
		Uninstall:        installer.options.Mode == ModeUninstall,
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
	result, runErr := installer.engineSync().Global(context.Background(), options)
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
		return runErr
	}
	if result.OK {
		return nil
	}
	conflictErr := fmt.Errorf("engine-sync global: %s", strings.Join(conflicts, "; "))
	if !installer.apply {
		installer.planErrors = append(installer.planErrors, conflictErr)
		return nil
	}
	return conflictErr
}

// engineSyncAbsentText is what an uninstall leaves behind when engine-sync is
// not installed: pfm tears down everything it owns itself, and only what
// engine-sync writes (the Codex roles, the global commands) and the targets
// file that serves it stay for a second uninstall.
const engineSyncAbsentText = "engine-sync unavailable: Codex roles, global commands and targets.json left in place; " +
	"run pfm uninstall again with engine-sync present"

// leaveEngineSyncOwned handles an uninstall whose engine-sync binary is
// absent. The preview skips with the text and raises no plan error, so the
// preflight does not refuse the rest of the teardown; the apply defers the
// failure, so the run finishes and still exits non-zero. An install never
// reaches here: an absent binary fails an install step.
func (installer *engine) leaveEngineSyncOwned(cause error) error {
	installer.engineSyncAbsent = true
	if !installer.apply {
		installer.skip(engineSyncAbsentText)
		return nil
	}
	installer.deferFailure(fmt.Errorf("%s: %w", engineSyncAbsentText, cause))
	return nil
}

// engineSyncTargets is {engine-sync config}/targets.json: the machine targets
// `engine-sync hook claude start` refreshes, the same roster this install ran
// global over.
type engineSyncTargets struct {
	CodexHomes       []string `json:"codexHomes"`
	ClaudeConfigDirs []string `json:"claudeConfigDirs"`
	SourceRepo       string   `json:"sourceRepo"`
}

// wireEngineSyncTargets writes targets.json on install and retires it on
// uninstall. An empty Codex roster is written as an empty list, never left
// for engine-sync to default.
func (installer *engine) wireEngineSyncTargets() error {
	path := enginesync.TargetsPath(installer.env(), installer.options.Home)
	if installer.options.Mode == ModeUninstall {
		if installer.engineSyncAbsent {
			return nil
		}
		return installer.retire(path, "engine-sync machine targets")
	}
	sourceRepo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return fmt.Errorf("resolve the engine-sync targets source repository: %w", err)
	}
	targets := engineSyncTargets{
		CodexHomes:       append([]string{}, installer.codexHomes()...),
		ClaudeConfigDirs: []string{installer.options.ConfigDir},
		SourceRepo:       sourceRepo,
	}
	encoded, err := json.MarshalIndent(targets, "", "  ")
	if err != nil {
		return fmt.Errorf("encode engine-sync targets: %w", err)
	}
	encoded = append(encoded, '\n')
	if sameFile(path, encoded, 0o600) {
		installer.ok(path)
		return nil
	}
	return installer.change("write "+path, func() error {
		return atomicfile.Write(path, encoded, 0o600)
	})
}
