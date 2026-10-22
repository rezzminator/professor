package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The classes the registry-ownership scan reports (spec D24-D26): ownership is
// decided by a link's target against the install plan, never by an entry's name.
const (
	// RegistryStore: a link into the retired home store.
	RegistryStore = "STORE"
	// RegistryOrphan: an owned link whose target exists but which the install
	// plan does not write.
	RegistryOrphan = "ORPHAN"
	// RegistryDead: an owned link whose target is gone.
	RegistryDead = "DEAD"
	// RegistryRetired: an unmarked regular entry named for a retired agent,
	// command or skill; reported, never touched.
	RegistryRetired = "RETIRED"
)

// RetiredRegistryNames are the names Professor retired, per registry kind: an
// agent by file stem, a command by its path below the commands registry
// without ".md", a skill by directory name. The update package derives them
// from its retired-name table; the installer cannot import it.
type RetiredRegistryNames struct {
	Agents, Commands, Skills map[string]bool
}

// RegistryPlan is what pfm install --yes links now: Links holds every
// registry path the install writes, AgentDirs the directories whose top-level
// .md files engine-sync links into {store}/agents.
type RegistryPlan struct {
	AgentDirs []string
	Links     map[string]bool
}

// RegistryScan is one ownership scan's input. Roots are the recorded clone and
// every recorded repo; the managed root and the retired stores are always owned.
type RegistryScan struct {
	Home, Store string
	Roots       []string
	Plan        RegistryPlan
	Retired     RetiredRegistryNames
}

// RegistryFinding is one entry the scan lists. Target is empty for RETIRED;
// Name is the retired name RETIRED matched.
type RegistryFinding struct{ Class, Path, Target, Name string }

// RegistryOwnership is the scan's result: what it lists and what it covered.
type RegistryOwnership struct {
	Findings            []RegistryFinding
	Entries, Registries int
}

// errPlanSkillSources marks a plan that failed on the source-fetched skills
// registry. Install reports that registry as a named skip and carries on, so
// the sweep that needs the plan skips too (retiring nothing); doctor fails.
var errPlanSkillSources = errors.New("plan registry source-fetched skills")

// PlanRegistryLinks computes what pfm install --yes would link into the store
// and ~/.agents/skills now, from the install's own enumerations: the clone's
// commands, its skill directories carrying a SKILL.md, the registered
// source-fetched skills that do not clash with a template skill, and the
// embedded command and skill assets. A source it cannot read is an error,
// never an empty plan.
func PlanRegistryLinks(home, store, clone, releaseURL string) (RegistryPlan, error) {
	plan := RegistryPlan{
		AgentDirs: []string{
			filepath.Join(clone, "templates", "global", "agents"),
			filepath.Join(clone, "templates", "generated", "agents"),
		},
		Links: map[string]bool{},
	}
	commands := filepath.Join(clone, "templates", "global", "commands")
	entries, err := os.ReadDir(commands)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return RegistryPlan{}, fmt.Errorf("plan registry commands %s: %w", commands, err)
	}
	for _, entry := range entries {
		plan.Links[filepath.Join(store, globalCommandsRegistry, entry.Name())] = true
	}
	skills := filepath.Join(clone, "templates", "global", "skills")
	entries, err = os.ReadDir(skills)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return RegistryPlan{}, fmt.Errorf("plan registry skills %s: %w", skills, err)
	}
	for _, entry := range entries {
		path := filepath.Join(skills, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			return RegistryPlan{}, fmt.Errorf("plan registry skill %s: %w", path, err)
		}
		if !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(path, "SKILL.md")); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return RegistryPlan{}, fmt.Errorf("plan registry skill %s: %w", path, err)
		}
		plan.Links[filepath.Join(store, globalSkillsRegistry, entry.Name())] = true
		plan.Links[filepath.Join(home, ".agents", globalSkillsRegistry, entry.Name())] = true
	}
	sources, _, err := loadSkillSources(clone, releaseURL)
	if err != nil {
		return RegistryPlan{}, fmt.Errorf("%w: %w", errPlanSkillSources, err)
	}
	for _, source := range sources {
		if strings.HasPrefix(source.Problem, "SKILL-SOURCE-CLASH ") {
			continue
		}
		plan.Links[filepath.Join(store, globalSkillsRegistry, source.Name)] = true
		plan.Links[filepath.Join(home, ".agents", globalSkillsRegistry, source.Name)] = true
	}
	assets, err := assetFiles()
	if err != nil {
		return RegistryPlan{}, fmt.Errorf("plan registry host assets: %w", err)
	}
	for _, asset := range assets {
		if target, found := commandTargetIn(store, asset.path); found {
			plan.Links[target] = true
		}
		if target, found := skillTarget(store, asset.path); found {
			plan.Links[target] = true
		}
	}
	return plan, nil
}

// ScanRegistryOwnership runs the scan for a caller with no installer options
// (pfm doctor, --stale): the clone is the one the install anchors on, the
// fallback ~/.professor an owned root too, the plan computed from the clone.
func ScanRegistryOwnership(home, store string, retired RetiredRegistryNames) (RegistryOwnership, error) {
	clone, err := GlobalSourceRepo(home)
	if err != nil {
		return RegistryOwnership{}, err
	}
	roots := []string{clone}
	if fallback := filepath.Join(home, ".professor"); clone != fallback {
		roots = append(roots, fallback)
	}
	plan, err := PlanRegistryLinks(home, store, clone, ThemeReleaseURL(""))
	if err != nil {
		return RegistryOwnership{}, err
	}
	return InspectRegistryOwnership(RegistryScan{
		Home: home, Store: store, Roots: roots, Plan: plan, Retired: retired,
	})
}

// InspectRegistryOwnership classes every Professor-owned link in the four
// registries ({store}/agents, {store}/commands, {store}/skills,
// ~/.agents/skills) and lists the unmarked entries named for a retired name.
// A link outside the owned roots and any other unmarked entry is never listed.
// WalkDir never follows a symlink into an operator's directory. A walk, read
// or stat error returns the error, never a shorter list.
func InspectRegistryOwnership(scan RegistryScan) (RegistryOwnership, error) {
	var result RegistryOwnership
	for _, registry := range []struct{ path, kind string }{
		{filepath.Join(scan.Store, globalAgentsRegistry), globalAgentsRegistry},
		{filepath.Join(scan.Store, globalCommandsRegistry), globalCommandsRegistry},
		{filepath.Join(scan.Store, globalSkillsRegistry), globalSkillsRegistry},
		{filepath.Join(scan.Home, ".agents", globalSkillsRegistry), globalSkillsRegistry},
	} {
		if err := scan.walk(&result, registry.path, registry.kind); err != nil {
			return RegistryOwnership{}, err
		}
	}
	return result, nil
}

func (scan RegistryScan) walk(result *RegistryOwnership, registry, kind string) error {
	return filepath.WalkDir(registry, func(path string, entry fs.DirEntry, walkErr error) error {
		if path == registry && errors.Is(walkErr, fs.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return fmt.Errorf("inspect registry path %s: %w", path, walkErr)
		}
		isLink := entry.Type()&os.ModeSymlink != 0
		if path == registry {
			if !entry.IsDir() && !isLink {
				return fmt.Errorf("inspect registry path %s: not a directory", path)
			}
			result.Registries++
			if isLink {
				return scan.link(result, path, kind, true)
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		result.Entries++
		if isLink {
			return scan.link(result, path, kind, false)
		}
		return scan.regular(result, registry, path, kind, entry)
	})
}

// regular judges an unmarked regular entry by name alone: RETIRED when it is
// named for a retired agent, command or skill, otherwise the user's. A regular
// skill directory whose SKILL.md is a link is judged by that link. Agent and
// skill registries are flat, so a directory is never descended; in commands a
// directory is descended unless it is itself the retired name.
func (scan RegistryScan) regular(
	result *RegistryOwnership, registry, path, kind string, entry fs.DirEntry,
) error {
	name, retired := entry.Name(), map[string]bool(nil)
	switch kind {
	case globalAgentsRegistry:
		name, retired = strings.TrimSuffix(name, ".md"), scan.Retired.Agents
	case globalCommandsRegistry:
		relative, err := filepath.Rel(registry, path)
		if err != nil {
			return fmt.Errorf("inspect registry path %s below %s: %w", path, registry, err)
		}
		name, retired = strings.TrimSuffix(filepath.ToSlash(relative), ".md"), scan.Retired.Commands
	case globalSkillsRegistry:
		retired = scan.Retired.Skills
		if entry.IsDir() {
			skillFile := filepath.Join(path, "SKILL.md")
			info, err := os.Lstat(skillFile)
			switch {
			case err == nil && info.Mode()&os.ModeSymlink != 0:
				if err := scan.link(result, skillFile, kind, false); err != nil {
					return err
				}
				return fs.SkipDir
			case err != nil && !errors.Is(err, fs.ErrNotExist):
				return fmt.Errorf("inspect registry path %s: %w", skillFile, err)
			}
		}
	}
	if retired[name] {
		result.Findings = append(result.Findings, RegistryFinding{Class: RegistryRetired, Path: path, Name: name})
		if entry.IsDir() {
			return fs.SkipDir
		}
		return nil
	}
	if entry.IsDir() && kind != globalCommandsRegistry {
		return fs.SkipDir
	}
	return nil
}

// link judges one registry link by its target: into a retired store is STORE;
// otherwise, if it is not an owned root's, it is the user's; a gone target is
// DEAD; an existing target the plan does not write is ORPHAN. A registry root
// that is itself a link is judged STORE or DEAD only.
func (scan RegistryScan) link(result *RegistryOwnership, path, kind string, registryRoot bool) error {
	target, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("read registry link %s: %w", path, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	target = filepath.Clean(target)
	finding := RegistryFinding{Path: path, Target: target}
	for _, store := range RetiredGeneratedStores(scan.Home) {
		if withinGlobalSource(target, store) {
			finding.Class = RegistryStore
			result.Findings = append(result.Findings, finding)
			return nil
		}
	}
	owned := withinGlobalSource(target, managedRootForHome(scan.Home)) ||
		ownedGlobalLink(scan.Roots, kind, filepath.Base(path), target)
	for _, root := range scan.Roots {
		// Legacy skill installs linked workflows before templates/global/skills.
		owned = owned || withinGlobalSource(target, filepath.Join(root, "workflows"))
	}
	if !owned {
		return nil
	}
	if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
		finding.Class = RegistryDead
		result.Findings = append(result.Findings, finding)
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect registry link %s target %s: %w", path, target, err)
	}
	if registryRoot || scan.planned(kind, path, target) {
		return nil
	}
	finding.Class = RegistryOrphan
	result.Findings = append(result.Findings, finding)
	return nil
}

// planned reports whether the install writes this link. An agent link is
// planned by its target: a top-level .md of a plan agent directory.
func (scan RegistryScan) planned(kind, path, target string) bool {
	if kind == globalAgentsRegistry && strings.HasSuffix(target, ".md") &&
		slices.Contains(scan.Plan.AgentDirs, filepath.Dir(target)) {
		return true
	}
	return scan.Plan.Links[path]
}
