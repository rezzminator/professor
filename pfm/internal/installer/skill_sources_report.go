package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	skillLinkLedgerName    = "skill-links.json"
	skillLinkLedgerVersion = 1
)

type skillLinkLedger struct {
	Version  int      `json:"version"`
	LinkDirs []string `json:"link_dirs"`
}

func skillLinkLedgerPath(home string) string {
	return filepath.Join(managedRootForHome(home), skillLinkLedgerName)
}

func readSkillLinkLedger(home string) ([]string, bool, error) {
	path := skillLinkLedgerPath(home)
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("skill link ledger %s: %w", path, err)
	}
	var ledger skillLinkLedger
	if err := json.Unmarshal(content, &ledger); err != nil {
		return nil, true, fmt.Errorf("skill link ledger %s: %w", path, err)
	}
	if ledger.Version != skillLinkLedgerVersion {
		return nil, true, fmt.Errorf(
			"skill link ledger %s: version %d, want %d",
			path,
			ledger.Version,
			skillLinkLedgerVersion,
		)
	}
	if len(ledger.LinkDirs) == 0 {
		return nil, true, fmt.Errorf("skill link ledger %s: link_dirs is empty", path)
	}
	for _, dir := range ledger.LinkDirs {
		if !filepath.IsAbs(dir) {
			return nil, true, fmt.Errorf("skill link ledger %s: link dir %s is not absolute", path, dir)
		}
	}
	return ledger.LinkDirs, true, nil
}

// SkillSourceState is one source-fetched skill's install state, measured the
// way wireSourceFetchedSkills builds it. CHECK-FAILED is its own state on
// purpose: "we failed to look" never reads as MISSING or NOT-FETCHED.
type SkillSourceState string

const (
	SkillSourceLinked      SkillSourceState = "linked"
	SkillSourceMissing     SkillSourceState = "MISSING"
	SkillSourceConflict    SkillSourceState = "CONFLICT"
	SkillSourceNotFetched  SkillSourceState = "NOT-FETCHED"
	SkillSourceOffline     SkillSourceState = "OFFLINE"
	SkillSourceSkipped     SkillSourceState = "SKIPPED"
	SkillSourceCheckFailed SkillSourceState = "CHECK-FAILED"
	SkillSourceNoClone     SkillSourceState = "NO-CLONE"
	SkillSourceNoRegistry  SkillSourceState = "NO-REGISTRY"
)

// SkillSourceStatus is one doctor row: a registered skill (Name set) or the
// registry itself (Name empty, Path the registry, clone or link ledger).
type SkillSourceStatus struct {
	Name      string
	Path      string
	State     SkillSourceState
	Missing   []string
	Conflicts []string
	Error     string
}

// Describe renders the row after "doctor: skill-source ".
func (status SkillSourceStatus) Describe() string {
	line := "registry=" + status.Path
	if status.Name != "" {
		line = "name=" + status.Name + " store=" + status.Path
	}
	line += " state=" + string(status.State)
	if len(status.Missing) != 0 {
		line += " missing=" + strings.Join(status.Missing, ",")
	}
	if len(status.Conflicts) != 0 {
		line += " conflict=" + strings.Join(status.Conflicts, ",")
	}
	if status.Error != "" {
		line += " error=" + status.Error
	}
	switch status.State {
	case SkillSourceLinked, SkillSourceNoRegistry:
		return line
	case SkillSourceNoClone:
		return line + ` note="no Professor clone recorded or at the default path — source-fetched skills install from a clone's registry"`
	case SkillSourceOffline:
		return line + ` note="not fetched: PFM_SKILL_SOURCES_OFFLINE=1 is set, and pfm install fetches nothing while it is"`
	case SkillSourceConflict:
		return line + ` hint="the conflicting path is not pfm's; remove it, then run pfm install --yes"`
	case SkillSourceSkipped:
		return line
	default:
		return line + ` hint="run pfm install --yes"`
	}
}

// InspectSkillSources classifies each registered skill once against its managed
// storage and the recorded link dirs, falling back to the default store's
// skills registry and ~/.agents/skills when there is no link ledger.
func InspectSkillSources(home string, offline bool) []SkillSourceStatus {
	repo, err := GlobalSourceRepo(home)
	if err != nil {
		return []SkillSourceStatus{{Path: home, State: SkillSourceCheckFailed, Error: err.Error()}}
	}
	if _, err := os.Lstat(repo); errors.Is(err, fs.ErrNotExist) {
		return []SkillSourceStatus{{Path: repo, State: SkillSourceNoClone}}
	} else if err != nil {
		return []SkillSourceStatus{{Path: repo, State: SkillSourceCheckFailed, Error: err.Error()}}
	}
	registry := filepath.Join(repo, filepath.FromSlash(skillSourcesRelative))
	sources, present, err := loadSkillSources(repo, ThemeManifestURL(""))
	if err != nil {
		return []SkillSourceStatus{{Path: registry, State: SkillSourceCheckFailed, Error: err.Error()}}
	}
	if !present {
		return []SkillSourceStatus{{Path: registry, State: SkillSourceNoRegistry}}
	}
	storeRoot := skillStoreRoot(home)
	linkDirs, recorded, err := readSkillLinkLedger(home)
	if err != nil {
		return []SkillSourceStatus{{Path: skillLinkLedgerPath(home), State: SkillSourceCheckFailed, Error: err.Error()}}
	}
	if !recorded {
		linkDirs = []string{filepath.Join(ClaudeStore(home), "skills"), filepath.Join(home, ".agents", "skills")}
	}
	statuses := make([]SkillSourceStatus, 0, len(sources))
	for _, source := range sources {
		store := filepath.Join(storeRoot, source.Name)
		statuses = append(statuses, inspectSkillSource(source, storeRoot, store, linkDirs, offline))
	}
	return statuses
}

// inspectSkillSource classifies one registered skill: its store root and
// store must be real directories (install neither reads through nor replaces
// anything else), its root SKILL.md usable (checkSkillFile: an unusable one is
// SKIPPED, as install skips linking it), and every target linked.
func inspectSkillSource(
	source skillSource, storeRoot, store string, linkDirs []string, offline bool,
) SkillSourceStatus {
	status := SkillSourceStatus{Name: source.Name, Path: store, State: SkillSourceLinked}
	if source.Problem != "" {
		status.State, status.Error = SkillSourceSkipped, source.Problem
		return status
	}
	for _, dir := range []string{storeRoot, store} {
		if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
			status.State, status.Conflicts = SkillSourceConflict, []string{dir}
			return status
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			status.State, status.Error = SkillSourceCheckFailed, err.Error()
			return status
		}
	}
	if err := checkSkillFile(store); errors.Is(err, fs.ErrNotExist) {
		status.State = SkillSourceNotFetched
		if offline {
			status.State = SkillSourceOffline
		}
		return status
	} else if errors.Is(err, errSkillFileUnusable) {
		status.State, status.Error = SkillSourceSkipped, err.Error()
		return status
	} else if err != nil {
		status.State, status.Error = SkillSourceCheckFailed, err.Error()
		return status
	}
	for _, dir := range linkDirs {
		target := filepath.Join(dir, source.Name)
		state, _, err := codexgen.ClassifyGlobalLink(target, store, storeRoot, codexgen.GlobalLinkDir)
		if err != nil {
			status.State, status.Error = SkillSourceCheckFailed, err.Error()
			return status
		}
		switch state {
		case codexgen.GlobalLinkCorrect:
		case codexgen.GlobalLinkCopy, codexgen.GlobalLinkConflict:
			status.Conflicts = append(status.Conflicts, target)
		default:
			status.Missing = append(status.Missing, target)
		}
	}
	switch {
	case len(status.Missing) != 0:
		status.State = SkillSourceMissing
	case len(status.Conflicts) != 0:
		status.State = SkillSourceConflict
	}
	return status
}

// ReportGlobalRegistries reports store agents, Codex agents, source-fetched skills,
// and dead pfm links using the same predicate as install.
func ReportGlobalRegistries(
	w io.Writer,
	home string,
	claudeAbsent bool,
	env paths.Env,
	codexHomes ...string,
) (warnings, failures int) {
	warnings, failures = ReportGlobalAgents(w, home, claudeAbsent, codexHomes...)
	skillWarnings, skillFailures := ReportSkillSources(w, home, paths.SkillSourcesOfflineIn(env))
	warnings += skillWarnings
	failures += skillFailures
	repo, err := GlobalSourceRepo(home)
	if err != nil {
		fmt.Fprintf(w, "doctor: registry dead-link check failed: %s\n", err)
		return warnings, failures + 1
	}
	repos := []string{repo}
	fallback := filepath.Join(home, ".professor")
	if repo != fallback {
		repos = append(repos, fallback)
	}
	dead, err := InspectDeadRegistryLinks(home, ClaudeStore(home), repos)
	if err != nil {
		fmt.Fprintf(w, "doctor: registry dead-link check failed: %s\n", err)
		return warnings, failures + 1
	}
	for _, link := range dead {
		fmt.Fprintf(w, "doctor: registry dead link %s -> %s — run pfm install --yes\n", link.Path, link.Target)
		warnings++
	}
	return warnings, failures
}

// ReportSkillSources prints one "doctor: skill-source" line per registered
// skill (or one for the registry when it could not be read). MISSING and
// CHECK-FAILED are failures — a state pfm install --yes owns, or a check that
// did not run; NOT-FETCHED, CONFLICT and SKIPPED are warnings; linked,
// OFFLINE, NO-CLONE and NO-REGISTRY count neither.
func ReportSkillSources(
	w io.Writer,
	home string,
	offline bool,
) (warnings, failures int) {
	for _, status := range InspectSkillSources(home, offline) {
		fmt.Fprintf(w, "doctor: skill-source %s\n", status.Describe())
		switch status.State {
		case SkillSourceLinked, SkillSourceOffline, SkillSourceNoClone, SkillSourceNoRegistry:
		case SkillSourceMissing, SkillSourceCheckFailed:
			failures++
		default:
			warnings++
		}
	}
	return warnings, failures
}
