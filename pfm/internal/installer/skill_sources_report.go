package installer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

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
// registry itself (Name empty, Path the registry or clone).
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
		return line + ` note="not fetched: PFM_SKILL_SOURCES_OFFLINE=1"`
	case SkillSourceConflict:
		return line + ` hint="the conflicting path is not pfm's; remove it, then run pfm install --yes"`
	case SkillSourceSkipped:
		return line
	default:
		return line + ` hint="run pfm install --yes"`
	}
}

// InspectSkillSources classifies every skill the recorded clone's
// templates/global/skills/sources.json registers: its store and its links in
// every account's skills/ (unless no Claude Code binary is installed) and in
// ~/.agents/skills/.
func InspectSkillSources(
	home string,
	accounts []pfmconfig.Account,
	claudeAbsent, offline bool,
) []SkillSourceStatus {
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
	sources, present, err := loadSkillSources(repo, "")
	if err != nil {
		return []SkillSourceStatus{{Path: registry, State: SkillSourceCheckFailed, Error: err.Error()}}
	}
	if !present {
		return []SkillSourceStatus{{Path: registry, State: SkillSourceNoRegistry}}
	}
	storeRoot := skillStoreRoot(home)
	targets := func(name string) []string {
		var list []string
		if !claudeAbsent {
			for _, account := range accounts {
				list = append(list, filepath.Join(account.ConfigDir, "skills", name))
			}
		}
		return append(list, filepath.Join(home, ".agents", "skills", name))
	}
	statuses := make([]SkillSourceStatus, 0, len(sources))
	for _, source := range sources {
		store := filepath.Join(storeRoot, source.Name)
		status := SkillSourceStatus{Name: source.Name, Path: store, State: SkillSourceLinked}
		if source.Problem != "" {
			status.State, status.Error = SkillSourceSkipped, source.Problem
			statuses = append(statuses, status)
			continue
		}
		if _, err := os.Stat(filepath.Join(store, "SKILL.md")); errors.Is(err, fs.ErrNotExist) {
			status.State = SkillSourceNotFetched
			if offline {
				status.State = SkillSourceOffline
			}
			statuses = append(statuses, status)
			continue
		} else if err != nil {
			status.State, status.Error = SkillSourceCheckFailed, err.Error()
			statuses = append(statuses, status)
			continue
		}
		for _, target := range targets(source.Name) {
			state, _, err := codexgen.ClassifyGlobalLink(target, store, storeRoot, codexgen.GlobalLinkDir)
			if err != nil {
				status.State, status.Error = SkillSourceCheckFailed, err.Error()
				break
			}
			switch state {
			case codexgen.GlobalLinkCorrect:
			case codexgen.GlobalLinkCopy, codexgen.GlobalLinkConflict:
				status.Conflicts = append(status.Conflicts, target)
			default:
				status.Missing = append(status.Missing, target)
			}
		}
		if status.State == SkillSourceLinked {
			switch {
			case len(status.Missing) != 0:
				status.State = SkillSourceMissing
			case len(status.Conflicts) != 0:
				status.State = SkillSourceConflict
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// ReportGlobalRegistries is doctor's machine-global registry check: the
// global agents per account (ReportGlobalAgents), then one row per
// source-fetched skill (ReportSkillSources, offline per
// paths.SkillSourcesOffline).
func ReportGlobalRegistries(
	w io.Writer,
	home string,
	accounts []pfmconfig.Account,
	claudeAbsent bool,
	codexHomes ...string,
) (warnings, failures int) {
	warnings, failures = ReportGlobalAgents(w, home, accounts, claudeAbsent, codexHomes...)
	skillWarnings, skillFailures := ReportSkillSources(w, home, accounts, claudeAbsent, paths.SkillSourcesOffline())
	return warnings + skillWarnings, failures + skillFailures
}

// ReportSkillSources prints one "doctor: skill-source" line per registered
// skill (or one for the registry when it could not be read). MISSING and
// CHECK-FAILED are failures — a state pfm install --yes owns, or a check that
// did not run; NOT-FETCHED, CONFLICT and SKIPPED are warnings; linked,
// OFFLINE, NO-CLONE and NO-REGISTRY count neither.
func ReportSkillSources(
	w io.Writer,
	home string,
	accounts []pfmconfig.Account,
	claudeAbsent, offline bool,
) (warnings, failures int) {
	for _, status := range InspectSkillSources(home, accounts, claudeAbsent, offline) {
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
