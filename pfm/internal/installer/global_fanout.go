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
	"github.com/rezzminator/professor/pfm/internal/globallink"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

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
	accountDirs []string,
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
	versionWarnings, versionFailures := ReportEngineSyncVersions(w, home, env, accountDirs)
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
	repo, err := paths.GlobalSourceRepo(home)
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

// wireGlobalSkills fetches source skills, then links template skills into both registries.
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

// wireTemplateSkills links clone skill directories into both skills registries.
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

// wireGlobalSkill links one clone skill directory into the store and ~/.agents/skills.
func (installer *engine) wireGlobalSkill(sourceRepo, source, name string) error {
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); errors.Is(err, fs.ErrNotExist) {
		installer.skip("SKILL-SOURCE-MISSING " + name + " (" + filepath.Join(source, "SKILL.md") + " absent)")
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect %s skill source: %w", name, err)
	}
	for _, target := range installer.skillSourceLinkTargets(name) {
		if err := installer.wireGlobalLink(source, target, sourceRepo, true); err != nil {
			return err
		}
	}

	return nil
}

// globalRulesSource is the clone directory whose *.md files become the store
// rules registry's links.
func globalRulesSource(clone string) string {
	return filepath.Join(clone, "templates", "global", "rules")
}

// globalRuleNames lists the regular *.md files of a clone's rules source and
// names every other entry in skipped. An absent source wraps fs.ErrNotExist;
// install, the registry plan and doctor all enumerate through it so they
// never disagree on what ships.
func globalRuleNames(source string) (names, skipped []string, err error) {
	entries, err := os.ReadDir(source)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect global rules source %s: %w", source, err)
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && filepath.Ext(entry.Name()) == ".md" {
			names = append(names, entry.Name())
		} else {
			skipped = append(skipped, filepath.Join(source, entry.Name()))
		}
	}
	return names, skipped, nil
}

// wireGlobalRules links each clone rule file into the store rules registry,
// as wireGlobalCommands does for commands. Dead links are pruned separately
// by retireDeadRegistryLinks.
func (installer *engine) wireGlobalRules() error {
	sourceRepo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return fmt.Errorf("resolve global rules source repository: %w", err)
	}
	source := globalRulesSource(sourceRepo)
	names, skipped, err := globalRuleNames(source)
	if errors.Is(err, fs.ErrNotExist) {
		installer.skip("global rules source absent at " + source + " (0 entries)")
		return nil
	}
	if err != nil {
		return err
	}
	for _, path := range skipped {
		installer.skip("global rules source entry " + path + " is not a regular .md file; not linked")
	}
	if len(names) == 0 {
		installer.skip("global rules source holds no .md rule at " + source + " (0 entries)")
		return nil
	}
	target := filepath.Join(installer.options.ConfigDir, globalRulesRegistry)
	for _, name := range names {
		if err := installer.wireGlobalLink(
			filepath.Join(source, name), filepath.Join(target, name), sourceRepo, false,
		); err != nil {
			return err
		}
	}
	return nil
}

// ReportGlobalRules prints one "doctor: rules" row per rule whose store link
// is not correct, with the fix, then one summary row. A clone or source that
// cannot be read is a failing row, never "0 rules": a rule never linked is a
// warning (install owns the fix), a conflict a warning install preserves.
func ReportGlobalRules(w io.Writer, home string) (warnings, failures int) {
	clone, cloned, err := RecordedGlobalClone(home)
	if err != nil {
		fmt.Fprintf(w, "doctor: rules state=CHECK-FAILED error=%s\n", err)
		return 0, 1
	}
	if !cloned {
		fmt.Fprintf(w, "doctor: rules state=NO-CLONE source=%s\n", clone)
		return 0, 0
	}
	source := globalRulesSource(clone)
	names, _, err := globalRuleNames(source)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(w, "doctor: rules state=NO-SOURCE source=%s (0 rules)\n", source)
		return 0, 0
	}
	if err != nil {
		fmt.Fprintf(w, "doctor: rules state=CHECK-FAILED error=%s\n", err)
		return 0, 1
	}
	linked := 0
	for _, name := range names {
		target := filepath.Join(ClaudeStore(home), globalRulesRegistry, name)
		state, found, err := globallink.ClassifyLink(target, filepath.Join(source, name), clone, globallink.File)
		if err != nil {
			fmt.Fprintf(w, "doctor: rules state=CHECK-FAILED error=%s\n", err)
			failures++
			continue
		}
		if state == globallink.Correct {
			linked++
			continue
		}
		// install preserves a conflict, so rerunning it alone never clears the row.
		fix := installFix
		if state == globallink.Conflict {
			fix = "pfm install preserves it; move it aside, then run pfm install --yes"
		}
		fmt.Fprintf(w, "doctor: rules %s state=%s — %s; %s\n",
			target, state, globallink.DescribeState(state, target, filepath.Join(source, name), found), fix)
		warnings++
	}
	if warnings == 0 && failures == 0 {
		fmt.Fprintf(w, "doctor: rules ok (%d linked)\n", linked)
	}
	return warnings, failures
}
