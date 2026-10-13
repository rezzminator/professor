package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// GlobalSourceRepo resolves the clone every global agent/command/skill link
// is anchored on for a caller that has no installer options at all — pfm
// doctor, checking what a PRIOR install wired. It is the marker-and-default
// half of globalSourceRepoRoot, kept as one implementation so the checker
// can never anchor on a different clone than the installer it checks.
func GlobalSourceRepo(home string) (string, error) {
	marker := paths.SourceRepoPath(home)
	if _, err := os.Lstat(marker); errors.Is(err, fs.ErrNotExist) {
		return filepath.Clean(filepath.Join(home, ".professor")), nil
	} else if err != nil {
		return "", fmt.Errorf("inspect source repository marker %s: %w", marker, err)
	}
	repo, err := paths.ReadSourceRepoMarker(home)
	if err != nil {
		return "", err
	}
	return filepath.Clean(repo), nil
}

// ReportGlobalAgents prints the machine rows `engine-sync doctor --machine`
// classifies — the Claude store's agents registry and every Codex agents
// registry against the recorded clone — as "doctor: {message}" lines, and
// counts its warn and fail rows. The clone is resolved here, the same way the
// installer anchors its links: no marker and no clone at the default path is
// the no-clone state engine-sync names; a clone that cannot be resolved is an
// UNRESOLVED warning. An engine-sync that could not run is a failing row,
// never an absent one.
func ReportGlobalAgents(
	w io.Writer,
	home string,
	claudeAbsent bool,
	env paths.Env,
	capabilities []string,
	codexHomes ...string,
) (warnings, failures int) {
	if len(codexHomes) == 0 {
		codexHomes = []string{filepath.Join(home, ".codex")}
	}
	options := enginesync.MachineDoctorOptions{
		ClaudeConfigDir: ClaudeStore(home),
		CodexHomes:      codexHomes,
		ClaudeAbsent:    claudeAbsent,
	}
	if !claudeAbsent {
		repo, cloned, err := RecordedGlobalClone(home)
		if err != nil {
			fmt.Fprintf(
				w,
				"doctor: global-agents source=%s state=UNRESOLVED error=%s hint=\"run pfm install\"\n",
				home,
				err,
			)
			return 1, 0
		}
		if cloned {
			options.SourceRepo = repo
		}
	}
	runner := enginesync.Runner{Env: env, Home: home, Capabilities: capabilities}
	if err := runner.RequireMinimumVersion(context.Background()); err != nil {
		fmt.Fprintf(w, "doctor: global-agents state=FAIL %v\n", err)
		return 0, 1
	}
	versionWarnings, versionFailures := ReportEngineSyncVersions(w, home, env)
	warnings += versionWarnings
	failures += versionFailures
	rows, err := runner.MachineDoctor(context.Background(), options)
	if err != nil {
		fmt.Fprintf(w, "doctor: global-agents state=FAIL %v\n", err)
		return warnings, failures + 1
	}
	for _, row := range rows {
		fmt.Fprintf(w, "doctor: %s\n", row.Message)
		switch row.Level {
		case enginesync.LevelWarn:
			warnings++
		case enginesync.LevelFail:
			failures++
		}
	}
	return warnings, failures
}

// RecordedGlobalClone resolves the clone a prior install anchored on, and
// whether there is one at all: no marker and nothing at the default path is
// no clone. A marker or path that cannot be inspected is an error, never "no
// clone".
func RecordedGlobalClone(home string) (string, bool, error) {
	repo, err := GlobalSourceRepo(home)
	if err != nil {
		return "", false, err
	}
	if _, markerErr := os.Lstat(paths.SourceRepoPath(home)); errors.Is(markerErr, fs.ErrNotExist) {
		if _, repoErr := os.Lstat(repo); errors.Is(repoErr, fs.ErrNotExist) {
			return repo, false, nil
		} else if repoErr != nil {
			return "", false, fmt.Errorf("inspect source repository %s: %w", repo, repoErr)
		}
	} else if markerErr != nil {
		return "", false, fmt.Errorf("inspect source repository marker: %w", markerErr)
	}
	return repo, true, nil
}

// wireGlobalSkills fetches source skills, then links template skills into the store.
func (installer *engine) wireGlobalSkills() error {
	sourceRepo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return fmt.Errorf("resolve global skills source repository: %w", err)
	}
	// Source-fetched skills first: their retirement frees a name that newly
	// clashes with a template skill before wireTemplateSkills links it.
	if err := installer.wireSourceFetchedSkills(sourceRepo); err != nil {
		return err
	}
	return installer.wireTemplateSkills(sourceRepo)
}

// wireTemplateSkills links clone skill directories into the store skills registry.
// sources.json is a registry, not a skill; an absent or empty source is reported.
func (installer *engine) wireTemplateSkills(sourceRepo string) error {
	source := filepath.Join(sourceRepo, "templates", "global", "skills")
	entries, err := os.ReadDir(source)
	if errors.Is(err, fs.ErrNotExist) {
		installer.skip("global skills source absent at " + source + " (0 entries)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect global skills source %s: %w", source, err)
	}
	linked := 0
	for _, entry := range entries {
		path := filepath.Join(source, entry.Name())
		info, statErr := os.Stat(path)
		if statErr != nil {
			return fmt.Errorf("inspect global skill source %s: %w", path, statErr)
		}
		if !info.IsDir() {
			continue
		}
		linked++
		if err := installer.wireGlobalSkill(sourceRepo, path, entry.Name()); err != nil {
			return err
		}
	}
	if linked == 0 {
		installer.skip("global skills source empty at " + source + " (0 skill directories)")
	}
	return nil
}

// wireGlobalSkill links one clone skill directory at {store}/skills/{name}.
func (installer *engine) wireGlobalSkill(sourceRepo, source, name string) error {
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); errors.Is(err, fs.ErrNotExist) {
		installer.skip("SKILL-SOURCE-MISSING " + name + " (" + filepath.Join(source, "SKILL.md") + " absent)")
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect %s skill source: %w", name, err)
	}
	config := installer.options.ConfigDir
	if err := installer.wireGlobalLink(
		source,
		filepath.Join(config, "skills", name),
		sourceRepo,
		true,
	); err != nil {
		return err
	}

	return nil
}
