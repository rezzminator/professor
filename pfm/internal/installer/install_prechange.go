package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// PreChange is what `pfm install`'s pre-change refusals read from the command.
type PreChange struct {
	// Runtime is the command's resolved runtime, before the layout moved anything.
	Runtime pfmconfig.Runtime
	// PlanMigration plans the pre-split config migration (pfmconfig.PlanMigrationFrom).
	PlanMigration func(pfmconfig.Config, string) (pfmconfig.Migration, error)
	// PrintDependencies is the required dependency preflight: it prints the
	// table to w and returns its failure count; every probe is read-only.
	PrintDependencies func(w io.Writer, runtime pfmconfig.Runtime) int
	// Check is --check: answer where the apply would write.
	Check bool
}

// PreChangeRefusals runs, for `pfm install --yes` and `--check`, every refusal
// the apply makes before its first host write (ApplyLayout). The install gate
// answers first: a row it refuses leaves the planning inputs below (the config
// that row would move) unsettled, so a later refusal would name a symptom
// instead of the live chat or holder behind it. With pre.Check the gate's
// answer prints only once every later refusal passed, and a 0 means the check
// answered ok; ApplyLayout still asks the gate before its first write.
func PreChangeRefusals(env LayoutEnv, findings []LayoutFinding, pre PreChange, stdout, stderr io.Writer) int {
	if pre.Check {
		var answer bytes.Buffer
		if code := RunInstallCheck(env, findings, &answer, stderr); code != 0 {
			return code
		}
		if code := refuseBeforeWrite(env, findings, pre, stdout, stderr); code != 0 {
			return code
		}
		if _, err := answer.WriteTo(stdout); err != nil {
			fmt.Fprintf(stderr, "pfm install: print install check: %v\n", err)
			return 1
		}
		return 0
	}
	if err := installGate(env, findings); err != nil {
		fmt.Fprintf(stderr, "pfm install: %v\n", err)
		return 1
	}
	return refuseBeforeWrite(env, findings, pre, stdout, stderr)
}

// refuseBeforeWrite makes, before the first host write and after the install
// gate passed, every read-only refusal the apply otherwise reaches only past
// its first write, in this order: the config migration's plan and the layout's
// stray pre-split config refusal, the moved databases' resolved paths, the
// required dependency preflight, the apply's service-stop probes when the
// layout has work (PreviewLayoutStop: a probe the stop could not read
// refuses here), then the name-sync job's running state — the transient one
// last, nearest the write. The apply keeps the post-layout twin
// of the path check, the stray config refusal and the scheduler probe as race
// guards; the dependency preflight has none, an apply runs it here alone. A
// stray config refused here also covers pfmconfig.ApplyMigration's park
// refusal: a migration that parks a pre-split file is one this refusal names.
// An apply prints the dependency table here; --check prints it only beside a
// refusal. --check answers InstallCheckBlocked for a running job (wait for it
// or stop it, like the gate's holders) and 1 for the rest, like the other
// refusals before the gate.
func refuseBeforeWrite(env LayoutEnv, findings []LayoutFinding, pre PreChange, stdout, stderr io.Writer) int {
	runtime := pre.Runtime
	planned, err := env.InstallConfig(runtime, false)
	if err != nil {
		fmt.Fprintf(stderr, "pfm install: plan install config: %v\n", err)
		return 1
	}
	runtime.Config = planned
	// A config the layout seeds (no file yet, nothing moved there) comes from
	// this release's example, already on the current layout: nothing to plan.
	source := ConfigMoveSource(findings, runtime.Config.Path)
	_, statErr := os.Stat(runtime.Config.Path)
	if source != "" || !errors.Is(statErr, fs.ErrNotExist) {
		migration, err := pre.PlanMigration(runtime.Config, source)
		if err != nil {
			fmt.Fprintf(stderr, "pfm install: plan config migration: %v\n", err)
			return 1
		}
		if err := refuseStrayConfig(migration, source); err != nil {
			fmt.Fprintf(stderr, "pfm install: layout config migration: %v\n", err)
			return 1
		}
		if !migration.Empty() {
			runtime.Config = migration.Preview(runtime.Config)
		}
	}
	if err := MovedDatabasePathsAgree(env, findings); err != nil {
		fmt.Fprintf(stderr, "pfm install: migrate moved databases: %v\n", err)
		return 1
	}
	var table bytes.Buffer
	dependencies := stdout
	if pre.Check {
		dependencies = &table
	}
	if pre.PrintDependencies(dependencies, runtime) != 0 {
		if _, err := table.WriteTo(stderr); err != nil {
			fmt.Fprintf(stderr, "pfm install: print dependency table: %v\n", err)
		}
		fmt.Fprintln(stderr, "pfm install: required dependency preflight failed")
		return 1
	}
	if err := PreviewLayoutStop(context.Background(), env, findings); err != nil {
		fmt.Fprintf(
			stderr,
			"pfm install: refused before any change: stopping the pfm services cannot be probed: %v\n",
			err,
		)
		return 1
	}
	if err := CheckScheduler(context.Background(), env.commandRunner()); err != nil {
		fmt.Fprintln(stderr, SchedulerRefusal("install", err))
		if pre.Check {
			return InstallCheckBlocked
		}
		return 97
	}
	return 0
}

// CheckScheduler is Run's name-sync gate asked on its own: ErrLaunchAgentRunning
// (launchd) or ErrNameSyncRunning (systemd) while the job executes right now,
// nil when it is idle or the manager could not be asked (Run then marks the
// gate unprobed and proceeds). A nil runner is the real one. `pfm install`
// asks it before its first host write; Run asks again before its own writes,
// the race guard for a job its schedule started in between — never one the
// layout's restart fired, since RestartSchedulerUnits runs after Run.
func CheckScheduler(ctx context.Context, runner CommandRunner) error {
	if runner == nil {
		runner = execCommandRunner{}
	}
	_, err := schedulerGate(ctx, runner)
	return err
}

// schedulerGate probes the platform's name-sync job: probed is false when the
// manager gave no answer, err names the job running now.
func schedulerGate(ctx context.Context, runner CommandRunner) (probed bool, err error) {
	if schedulerIsLaunchd {
		running, answered := launchAgentRunning(ctx, runner)
		if running {
			return true, ErrLaunchAgentRunning
		}
		return answered, nil
	}
	running, answered := nameSyncServiceRunning(ctx, runner)
	if running {
		return true, ErrNameSyncRunning
	}
	return answered, nil
}

// ConfigMoveSource is the file the layout's config row moves to configPath,
// or "" when it moves none there.
func ConfigMoveSource(findings []LayoutFinding, configPath string) string {
	for _, finding := range findings {
		if finding.Row == "config" && finding.Verdict == VerdictMove && finding.Err == nil &&
			finding.Source != "" && finding.Path == configPath {
			return finding.Source
		}
	}
	return ""
}

// MovedDatabasePathsAgree is `pfm install`'s moved-database path check,
// answered before the layout: when a database row moves, the paths it moves
// to must be the ones pfmconfig.ResolvePaths will name once the config moved.
// A row only a pfm service holds moves too: the apply stops the service first.
func MovedDatabasePathsAgree(layoutEnv LayoutEnv, findings []LayoutFinding) error {
	moving := false
	for _, finding := range findings {
		movable := finding.Verdict == VerdictMove || finding.Verdict == VerdictRefuse && finding.serviceHeld
		if (finding.Row == "state-db" || finding.Row == "cache-db") && movable &&
			finding.Err == nil && finding.Source != "" {
			moving = true
		}
	}
	if !moving {
		return nil
	}
	resolved, err := resolvedPathsAfterLayout(findings)
	if err != nil {
		return fmt.Errorf("resolve moved database paths: %w", err)
	}
	return DatabasePathsAgree(layoutEnv.StateDB, layoutEnv.CacheDB, resolved)
}

// resolvedPathsAfterLayout answers pfmconfig.ResolvePaths as it will resolve
// once ApplyLayout ran. The layout changes neither PFM_* nor the source-repo
// marker (installer.Run records that later), so the config path resolves the
// same; only the file there changes, and when the config row moves one there,
// its source holds the bytes the path will.
func resolvedPathsAfterLayout(findings []LayoutFinding) (paths.Values, error) {
	resolved, err := paths.Resolve()
	if err != nil {
		return paths.Values{}, fmt.Errorf("resolve state paths: %w", err)
	}
	var env paths.Env = paths.OSEnv{}
	if configPath, pathErr := pfmconfig.ResolvePathFrom(env, resolved.Home); pathErr == nil {
		if source := ConfigMoveSource(findings, configPath); source != "" {
			if err := pfmconfig.RefuseAmbientConfigHomeFrom(env, resolved.Home); err != nil {
				return paths.Values{}, fmt.Errorf("resolve state paths: %w", err)
			}
			env = configOverrideEnv{Env: env, path: source}
		}
	}
	resolved.StateDB, resolved.CacheDB, err = pfmconfig.StatePathsFrom(env, resolved.Home)
	if err != nil {
		return paths.Values{}, fmt.Errorf("resolve state paths: %w", err)
	}
	return resolved, nil
}

// configOverrideEnv reads the environment with PFM_CONFIG naming path.
type configOverrideEnv struct {
	paths.Env
	path string
}

func (env configOverrideEnv) Get(name string) string {
	if name == paths.EnvConfig {
		return env.path
	}
	return env.Env.Get(name)
}

func (env configOverrideEnv) Lookup(name string) (string, bool) {
	if name == paths.EnvConfig {
		return env.path, true
	}
	return env.Env.Lookup(name)
}

// DatabasePathsAgree refuses moved databases whose paths are not the resolved ones.
func DatabasePathsAgree(statePath, cachePath string, resolved paths.Values) error {
	if resolved.StateDB != statePath || resolved.CacheDB != cachePath {
		return fmt.Errorf("moved database paths differ from resolved paths: state %s != %s; cache %s != %s",
			statePath, resolved.StateDB, cachePath, resolved.CacheDB)
	}
	return nil
}

// SchedulerRefusal is the actionable line for a name-sync job running now
// (CheckScheduler, Run), or "" for any other err.
func SchedulerRefusal(command string, err error) string {
	switch {
	case errors.Is(err, ErrNameSyncRunning):
		return "pfm " + command +
			": the pfm name-sync service is running; wait for it to finish or run `systemctl --user stop pfm-name-sync.service`, then retry"
	case errors.Is(err, ErrLaunchAgentRunning):
		return "pfm " + command +
			": the pfm name-sync launch agent is running; wait for it to finish or `launchctl bootout gui/$(id -u)/com.professor.pfm.name-sync` first"
	}
	return ""
}

// LayoutExitCode is `pfm install`'s exit status for an ApplyLayout error: 97
// for a name-sync job still running once the apply stopped its schedule, like
// the pre-change ask; 1 for any other.
func LayoutExitCode(err error) int {
	if SchedulerRefusal("install", err) != "" {
		return 97
	}
	return 1
}

// refuseStrayConfig is the layout's refusal of a config migration it cannot
// journal — a pre-split config.json outside HostLayout — answered from the
// plan; moved is the config row's source, which the move takes away first.
func refuseStrayConfig(migration pfmconfig.Migration, moved string) error {
	if migration.StrayLegacyPath != "" && migration.StrayLegacyPath == moved {
		migration.StrayLegacyPath = ""
	}
	if migration.LegacyPath != "" || migration.StrayLegacyPath != "" {
		return fmt.Errorf("%w: pre-split config path is outside HostLayout", errLayoutRefuse)
	}
	return nil
}

// schedulerUnit reports whether a unit ApplyLayout stops is a name-sync
// scheduler unit: starting one can fire the job at once, where installer.Run's
// scheduler gate would refuse it after the layout's writes.
func schedulerUnit(unit string) bool {
	return unit == nameSyncPathUnit || unit == nameSyncTimerUnit || unit == launchdLabel
}
