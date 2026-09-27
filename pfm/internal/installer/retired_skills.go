package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// retiredGlobalSkills names every machine-global skill the fan-out no longer
// links, with the clone-relative directory its old link resolved to. The
// fan-out walks only the CURRENT skill sources, so a retired skill's link is
// never revisited and never cleaned up on its own. deep-rr was the in-tree
// workflows/deep-rr skill, linked into every account as skills/deep-rr until
// the workflows/ tree was retired; a future retirement adds its row here
// rather than inventing a second mechanism.
var retiredGlobalSkills = []struct {
	name   string
	source string
}{
	{name: "deep-rr", source: filepath.Join("workflows", "deep-rr")},
}

// retiredGlobalSkillLink reports whether target — a cleaned, absolute link
// target found at {config}/skills/{name} — is the link the fan-out once made
// for a retired skill: the retired source directory inside one of repos,
// whether or not that directory still exists.
func retiredGlobalSkillLink(repos []string, name, target string) bool {
	for _, retired := range retiredGlobalSkills {
		if retired.name != name {
			continue
		}
		for _, repo := range repos {
			if target == filepath.Join(repo, retired.source) {
				return true
			}
		}
	}
	return false
}

// retireRetiredGlobalSkills deletes a retired skill's link from the skills/
// registry of every configured Claude account — live or dangling — but only
// when it is unambiguously the installer's own leftover: a symlink whose
// target is the retired source inside a known Professor clone. A regular
// directory or file of that name, or a link resolving anywhere else, is the
// operator's own and is left alone, named so the kept entry is a reported
// decision.
func (installer *engine) retireRetiredGlobalSkills() error {
	repos, err := installer.recordedProfessorSourceRepos()
	if err != nil {
		return err
	}
	repos = append(repos, filepath.Join(installer.options.Home, ".professor"))
	for _, config := range installer.claudeConfigDirs() {
		for _, retired := range retiredGlobalSkills {
			path := filepath.Join(config, "skills", retired.name)
			info, err := os.Lstat(path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("inspect retired global skill %s: %w", path, err)
			}
			if info.Mode()&os.ModeSymlink == 0 {
				installer.skip(path + " is not a Professor link to the retired " + retired.name + " skill — left alone")
				continue
			}
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read retired global skill link %s: %w", path, err)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			if !retiredGlobalSkillLink(repos, retired.name, filepath.Clean(target)) {
				installer.skip(path + " is an unrelated personal skill link (-> " + target + ") — left alone")
				continue
			}
			if err := installer.retire(path, "retired global skill "+retired.name); err != nil {
				return err
			}
		}
	}
	return nil
}
