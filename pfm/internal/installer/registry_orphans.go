package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// retireDeadRegistryLinks retires what the registry-ownership scan classes
// STORE, ORPHAN or DEAD, one "retire {path}" line each; RETIRED and unlisted
// entries are never touched. An uninstall plans nothing, so every owned link
// left in a registry goes. It then prunes the directories the retirement
// emptied below a registry.
func (installer *engine) retireDeadRegistryLinks() error {
	storeRoot := skillStoreRoot(installer.options.Home)
	unlock, busy, err := installer.lockSkillStore(storeRoot)
	if err != nil {
		installer.skip("registry-ownership check skipped: " + err.Error())
		return nil
	}
	if busy {
		installer.skip("registry-ownership check skipped: another pfm install holds " + storeRoot)
		return nil
	}
	defer unlock()
	repos, err := installer.recordedProfessorSourceRepos()
	if err != nil {
		installer.skip("registry-ownership check skipped: " + err.Error() +
			" — rerun pfm install --yes from inside your Professor clone")
		return nil
	}
	repos = append(repos, filepath.Join(installer.options.Home, ".professor"))
	scan := RegistryScan{Home: installer.options.Home, Store: installer.options.ConfigDir, Roots: repos}
	if installer.options.Mode != ModeUninstall {
		clone, err := installer.globalSourceRepoRoot()
		if err != nil {
			return fmt.Errorf("resolve registry plan clone: %w", err)
		}
		scan.Plan, err = PlanRegistryLinks(
			installer.options.Home, installer.options.ConfigDir, clone, installer.options.ThemeReleaseURL,
		)
		if errors.Is(err, errPlanSkillSources) {
			installer.skip("registry-ownership check skipped: " + err.Error())
			return nil
		}
		if err != nil {
			return err
		}
	}
	result, err := InspectRegistryOwnership(scan)
	if err != nil {
		return err
	}
	registries := map[string]bool{}
	for _, name := range []string{"agents", "commands", "skills"} {
		registries[filepath.Join(installer.options.ConfigDir, name)] = true
	}
	registries[filepath.Join(installer.options.Home, ".agents", "skills")] = true
	directories := map[string]bool{}
	for _, link := range result.Findings {
		reason := map[string]string{
			RegistryStore:  "retired store link -> ",
			RegistryOrphan: "orphan pfm link -> ",
			RegistryDead:   "dead pfm link -> ",
		}[link.Class]
		if reason == "" {
			continue // RETIRED is reported by pfm doctor, never touched
		}
		if err := installer.retire(link.Path, reason+link.Target); err != nil {
			return fmt.Errorf("retire registry link %s: %w", link.Path, err)
		}
		if registries[link.Path] {
			continue // the registry root itself: nothing below a registry to prune
		}
		for dir := filepath.Dir(link.Path); !registries[dir]; dir = filepath.Dir(dir) {
			directories[dir] = true
		}
	}
	if !installer.apply {
		return nil
	}
	ordered := make([]string, 0, len(directories))
	for dir := range directories {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, dir := range ordered {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("inspect retired registry directory %s: %w", dir, err)
		}
		if len(entries) != 0 {
			continue
		}
		if err := os.Remove(dir); err != nil {
			return fmt.Errorf("remove empty registry directory %s: %w", dir, err)
		}
	}
	return nil
}

// linkStageMinAge is how old a leftover link stage must be before install
// removes it: a younger one may belong to a swap still in flight.
const linkStageMinAge = 10 * time.Minute

// retireStaleLinkStages removes the .pfm-link-* stages a crash left in the
// registries install manages (atomicfile.Symlink and SymlinkOverDir stage a
// link beside its target; SymlinkOverDir leaves the displaced directory there
// when it dies after the swap). A stage younger than linkStageMinAge is left
// alone. Each removal is a change of the install's own transcript.
func (installer *engine) retireStaleLinkStages() error {
	return installer.retireStaleLinkStagesWithWalk(filepath.WalkDir)
}

func (installer *engine) retireStaleLinkStagesWithWalk(walk func(string, fs.WalkDirFunc) error) error {
	storeRoot := skillStoreRoot(installer.options.Home)
	unlock, busy, err := installer.lockSkillStore(storeRoot)
	if err != nil {
		installer.skip("stale link stage sweep skipped: " + err.Error())
		return nil
	}
	if busy {
		installer.skip("stale link stage sweep skipped: another pfm install holds " + storeRoot)
		return nil
	}
	defer unlock()
	recorded, _, err := readSkillLinkLedger(installer.options.Home)
	if err != nil {
		return fmt.Errorf("stale link stage registries: %w", err)
	}
	now := installer.now()
	registries := []string{
		filepath.Join(installer.options.ConfigDir, "agents"),
		filepath.Join(installer.options.ConfigDir, "commands"),
		filepath.Join(installer.options.ConfigDir, "skills"),
		filepath.Join(installer.options.Home, ".agents", "skills"),
	}
	registries = append(registries, recorded...)
	registries = append(registries, filepath.Join(installer.options.Home, ".config", "engine-sync"))
	seen := map[string]bool{}
	for _, registry := range registries {
		if seen[registry] {
			continue
		}
		seen[registry] = true
		err := walk(registry, func(path string, entry fs.DirEntry, walkErr error) error {
			if path == registry && errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			if walkErr != nil {
				installer.skip(fmt.Sprintf("stale link stage path %s unreadable: %v", path, walkErr))
				return nil
			}
			if !strings.HasPrefix(entry.Name(), atomicfile.LinkStagePrefix) {
				return nil
			}
			skipBelow := error(nil)
			if entry.IsDir() {
				skipBelow = fs.SkipDir
			}
			info, err := entry.Info()
			if err != nil {
				installer.skip(fmt.Sprintf("stale link stage %s unreadable: %v", path, err))
				return skipBelow
			}
			if now.Sub(info.ModTime()) < linkStageMinAge {
				return skipBelow
			}
			installer.markRemoved(path)
			if err := installer.change(
				"remove stale link stage "+path+" (left by an interrupted link swap)",
				func() error { return os.RemoveAll(path) },
			); err != nil {
				return fmt.Errorf("remove link stage %s: %w", path, err)
			}
			return skipBelow
		})
		if err != nil {
			return err
		}
	}
	return nil
}
