package workbench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/gitroot"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
)

const (
	professorDirName   = ".professor"
	workbenchesDirName = ".workbenches"
)

// WalkError identifies a directory discovery could not inspect.
type WalkError struct {
	Root, Path string
	Err        error
}

func (err WalkError) Error() string {
	return fmt.Sprintf("workbench discovery under %s could not read %s: %v", err.Root, err.Path, err.Err)
}

// Discover walks eligible directories, preserving failures alongside benches.
func Discover(roots []string) ([]Bench, []WalkError) {
	var benches []Bench
	var walkErrors []WalkError
	seen := make(map[string]bool)
	for _, root := range roots {
		root = filepath.Clean(root)
		var walk func(string)
		walk = func(dir string) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				walkError := WalkError{Root: root, Path: dir, Err: err}
				obs.Logger(context.Background()).Error("workbench discovery", "path", dir, obs.FieldErr, walkError)
				walkErrors = append(walkErrors, walkError)
				return
			}
			if eligibleBench(root, dir) && !seen[dir] {
				found, err := paths.HasWorkbenchManifest(dir)
				if err != nil {
					walkError := WalkError{Root: root, Path: dir, Err: err}
					obs.Logger(context.Background()).Error("workbench discovery", "path", dir, obs.FieldErr, walkError)
					walkErrors = append(walkErrors, walkError)
				} else if found {
					benches = append(benches, LoadBench(dir, root))
					seen[dir] = true
				}
			}
			for _, entry := range entries {
				child := filepath.Join(dir, entry.Name())
				if entry.IsDir() && eligibleBench(root, child) {
					walk(child)
				}
			}
		}
		walk(root)
	}
	sort.Slice(benches, func(i, j int) bool { return benches[i].Dir < benches[j].Dir })
	assignKeys(benches)
	return benches, walkErrors
}

func eligibleBench(root, dir string) bool {
	if !insideDirectory(root, dir) || dir == root {
		return false
	}
	relative := strings.TrimPrefix(dir, root+string(filepath.Separator))
	segments := strings.Split(relative, string(filepath.Separator))
	if len(segments) > 6 {
		return false
	}
	current := root
	for _, segment := range segments {
		if (strings.HasPrefix(segment, ".") && segment != professorDirName && segment != workbenchesDirName) ||
			segment == "node_modules" || segment == "vendor" || segment == "venv" {
			return false
		}
		current = filepath.Join(current, segment)
		if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

// ManagedRoots resolves distinct managed roots using the project owner.
func ManagedRoots(dirs []string) ([]string, error) {
	var roots []string
	seen := make(map[string]bool)
	for _, dir := range dirs {
		mapped, err := mappedDirectory(dir)
		if err != nil {
			return nil, err
		}
		root, found, err := professor.ResolveProjectRoot(mapped)
		if err != nil {
			obs.Logger(context.Background()).Error("workbench managed root", "path", mapped, obs.FieldErr, err)
			return nil, err
		}
		if found && !seen[root] {
			roots = append(roots, root)
			seen[root] = true
		}
	}
	sort.Strings(roots)
	return roots, nil
}

// Owner returns the deepest bench containing the cwd mapped to its checkout.
func Owner(benches []Bench, cwd string) (Bench, bool) {
	mapped, err := mappedDirectory(cwd)
	if err != nil {
		return Bench{}, false
	}
	var owner Bench
	for i := range benches {
		bench := &benches[i]
		if insideDirectory(bench.Dir, mapped) && len(bench.Dir) > len(owner.Dir) {
			owner = *bench
		}
	}
	return owner, owner.Dir != ""
}

// Nearest finds the first eligible manifest above cwd in the tree the launch
// runs in: a linked worktree finds its managed root through the main checkout,
// then reads its own bench, whose files its engines load.
func Nearest(cwd string) (Bench, bool, error) {
	dirs, err := launchDirectories(cwd)
	if err != nil {
		return Bench{}, false, err
	}
	root, found, err := professor.ResolveProjectRoot(dirs.mapped)
	if err != nil {
		obs.Logger(context.Background()).Error("workbench managed root", "path", dirs.mapped, obs.FieldErr, err)
		return Bench{}, false, err
	}
	if !found {
		return Bench{}, false, nil
	}
	start := dirs.mapped
	if dirs.top != "" && insideDirectory(dirs.main, root) {
		relative, err := filepath.Rel(dirs.main, root)
		if err != nil {
			wrapped := fmt.Errorf("place managed root %s in worktree %s: %w", root, dirs.top, err)
			obs.Logger(context.Background()).Error("workbench nearest", "path", dirs.own, obs.FieldErr, wrapped)
			return Bench{}, false, wrapped
		}
		start, root = dirs.own, filepath.Join(dirs.top, relative)
	}
	for dir := start; dir != root; dir = filepath.Dir(dir) {
		if !eligibleBench(root, dir) {
			continue
		}
		found, err := paths.HasWorkbenchManifest(dir)
		if err != nil {
			obs.Logger(context.Background()).Error("workbench nearest", "path", dir, obs.FieldErr, err)
			return Bench{}, false, err
		}
		if found {
			return LoadBench(dir, root), true, nil
		}
	}
	return Bench{}, false, nil
}

func mappedDirectory(cwd string) (string, error) {
	dirs, err := launchDirectories(cwd)
	return dirs.mapped, err
}

// launchPaths is cwd made absolute and, inside a linked worktree, its top,
// the main checkout, and cwd's twin at the same relative path there.
type launchPaths struct{ own, mapped, top, main string }

func launchDirectories(cwd string) (launchPaths, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		wrapped := fmt.Errorf("resolve workbench directory %s: %w", cwd, err)
		obs.Logger(context.Background()).Error("workbench directory", "path", cwd, obs.FieldErr, wrapped)
		return launchPaths{}, wrapped
	}
	plain := launchPaths{own: abs, mapped: abs}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if info, err := os.Lstat(filepath.Join(dir, ".git")); err == nil && info.Mode().IsRegular() {
			main, ok := gitroot.MainCheckout(dir)
			if !ok {
				return plain, nil
			}
			relative, err := filepath.Rel(dir, abs)
			if err != nil {
				wrapped := fmt.Errorf("map workbench directory %s from %s: %w", abs, dir, err)
				obs.Logger(context.Background()).Error("workbench directory", "path", abs, obs.FieldErr, wrapped)
				return launchPaths{}, wrapped
			}
			return launchPaths{own: abs, mapped: filepath.Join(main, relative), top: dir, main: main}, nil
		}
		if filepath.Dir(dir) == dir {
			return plain, nil
		}
	}
}

func assignKeys(benches []Bench) {
	counts := make(map[string]int)
	for i := range benches {
		counts[benches[i].Project+" › "+benches[i].Title]++
	}
	for i := range benches {
		bench := &benches[i]
		bench.Key = bench.Project + " › " + bench.Title
		if counts[bench.Key] > 1 {
			repoRoot, _ := gitroot.Project(bench.Root)
			relative, err := filepath.Rel(repoRoot, bench.Dir)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				relative = strings.TrimPrefix(bench.Dir, bench.Root+string(filepath.Separator))
			}
			bench.Key = bench.Project + " › " + filepath.ToSlash(relative)
		}
	}
	// Two clones of one repository hold the same benches at the same relative
	// paths; the key is a picker group's identity, so a key still shared names
	// each bench by its absolute directory.
	shared := make(map[string]int)
	for i := range benches {
		shared[benches[i].Key]++
	}
	for i := range benches {
		if bench := &benches[i]; shared[bench.Key] > 1 {
			bench.Key = bench.Project + " › " + filepath.ToSlash(bench.Dir)
		}
	}
}
