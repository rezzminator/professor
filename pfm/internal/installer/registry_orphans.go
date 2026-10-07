package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// DeadLink is a dangling registry link whose resolved target belongs to pfm.
type DeadLink struct{ Path, Target string }

// InspectDeadRegistryLinks shares ownership and absence rules between install
// and doctor. WalkDir never follows a symlink into an operator's directory.
func InspectDeadRegistryLinks(home, store string, repos []string) ([]DeadLink, error) {
	roots := append([]string(nil), repos...)
	roots = append(roots, managedRootForHome(home), paths.GeneratedClaudeAgentsDir(home))
	var dead []DeadLink
	for _, registry := range []string{
		filepath.Join(store, "agents"), filepath.Join(store, "commands"),
		filepath.Join(store, "skills"), filepath.Join(home, ".agents", "skills"),
	} {
		err := filepath.WalkDir(registry, func(path string, entry fs.DirEntry, walkErr error) error {
			if path == registry && errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			if walkErr != nil {
				return fmt.Errorf("inspect registry path %s: %w", path, walkErr)
			}
			if path == registry && !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				return fmt.Errorf("inspect registry path %s: not a directory", path)
			}
			if entry.Type()&os.ModeSymlink == 0 {
				return nil
			}
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read registry link %s: %w", path, err)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			target = filepath.Clean(target)
			owned := false
			for _, root := range roots {
				if withinGlobalSource(target, filepath.Clean(root)) {
					owned = true
					break
				}
			}
			if !owned {
				return nil
			}
			if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
				dead = append(dead, DeadLink{Path: path, Target: target})
			} else if err != nil {
				return fmt.Errorf("inspect registry link %s target %s: %w", path, target, err)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return dead, nil
}

func (installer *engine) retireDeadRegistryLinks() error {
	storeRoot := skillStoreRoot(installer.options.Home)
	unlock, busy, err := installer.lockSkillStore(storeRoot)
	if err != nil {
		installer.skip("registry dead-link check skipped: " + err.Error())
		return nil
	}
	if busy {
		installer.skip("registry dead-link check skipped: another pfm install holds " + storeRoot)
		return nil
	}
	defer unlock()
	repos, err := installer.recordedProfessorSourceRepos()
	if err != nil {
		installer.skip("registry dead-link check skipped: " + err.Error() +
			" — rerun pfm install --yes from inside your Professor clone")
		return nil
	}
	repos = append(repos, filepath.Join(installer.options.Home, ".professor"))
	dead, err := InspectDeadRegistryLinks(installer.options.Home, installer.options.ConfigDir, repos)
	if err != nil {
		return err
	}
	registries := map[string]bool{}
	for _, name := range []string{"agents", "commands", "skills"} {
		registries[filepath.Join(installer.options.ConfigDir, name)] = true
	}
	registries[filepath.Join(installer.options.Home, ".agents", "skills")] = true
	directories := map[string]bool{}
	for _, link := range dead {
		if err := installer.retire(link.Path, "dead pfm link -> "+link.Target); err != nil {
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

// reportTranscriptArtifact checks the installed dependency chat_digest uses,
// independently of cwd discovery or the account's configured directory.
// Equality is measured against the prior install's recorded source clone.
func reportTranscriptArtifact(w io.Writer, home string) (failures int) {
	installed := filepath.Join(ClaudeStore(home), "skills", "transcript", "transcript.py")
	repo, err := GlobalSourceRepo(home)
	if err != nil {
		fmt.Fprintf(w, "doctor: transcript path=%s state=CHECK-FAILED error=%s\n", installed, err)
		return 1
	}
	source := filepath.Join(repo, "templates", "global", "skills", "transcript", "transcript.py")
	// A binary-only install has no clone-owned transcript to stage. A recorded
	// source still implies an expected artifact, even when that source is gone.
	if _, repoErr := os.Lstat(repo); errors.Is(repoErr, fs.ErrNotExist) {
		if _, markerErr := os.Lstat(paths.SourceRepoPath(home)); errors.Is(markerErr, fs.ErrNotExist) {
			fmt.Fprintf(w, "doctor: transcript path=%s source=%s state=NO-CLONE\n", installed, source)
			return 0
		}
		fmt.Fprintf(w, "doctor: transcript path=%s source=%s state=CHECK-FAILED error=%s\n", installed, source, repoErr)
		return 1
	}
	state, detail := "installed", ""
	actual, err := readTranscriptArtifact(installed)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		state, detail = "MISSING", err.Error()
	case err != nil:
		state, detail = "CHECK-FAILED", err.Error()
	default:
		expected, sourceErr := readTranscriptArtifact(source)
		if sourceErr != nil {
			state, detail = "CHECK-FAILED", sourceErr.Error()
		} else if !bytes.Equal(actual, expected) {
			state, detail = "MISMATCH", "installed bytes differ from shipped source"
		}
	}
	fmt.Fprintf(w, "doctor: transcript path=%s source=%s state=%s", installed, source, state)
	if detail != "" {
		fmt.Fprintf(w, " error=%s — run pfm install --yes", detail)
		failures = 1
	}
	fmt.Fprintln(w)
	return failures
}

// readTranscriptArtifact follows installed skill links, but validates the
// opened object itself. O_NONBLOCK prevents a FIFO replacement from hanging
// doctor between lookup and read; only regular-file bytes may prove parity.
func readTranscriptArtifact(path string) (_ []byte, resultErr error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open transcript artifact %s: %w", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close transcript artifact %s: %w", path, err))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect transcript artifact %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("transcript artifact %s is not a regular file", path)
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read transcript artifact %s: %w", path, err)
	}
	return content, nil
}
