package fakegit

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func (c *fakeGitCall) revParse(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{
		"--verify": 0, "--short": 0, "--git-common-dir": 0, "--git-dir": 0,
		"--absolute-git-dir": 0, "--is-inside-work-tree": 0, "--show-toplevel": 0,
	})
	switch {
	case err != nil:
		return 0, err
	case flags["--is-inside-work-tree"] != nil:
		fmt.Fprintln(&c.out, r.work != "")
		return 0, nil
	case flags["--show-toplevel"] != nil:
		fmt.Fprintln(&c.out, r.work)
		return 0, nil
	case flags["--git-common-dir"] != nil, flags["--git-dir"] != nil, flags["--absolute-git-dir"] != nil:
		gitDir := r.gitDir
		if r.linked != "" && flags["--git-common-dir"] == nil {
			gitDir = r.adminDir(r.linked)
		}
		if flags["--absolute-git-dir"] == nil && r.linked == "" && c.dir == r.work {
			gitDir = fakeGitDirName
		}
		fmt.Fprintln(&c.out, gitDir)
		return 0, nil
	case len(rest) != 1:
		return 0, fmt.Errorf("rev-parse takes one revision")
	}
	if rev, name, isPath := strings.Cut(rest[0], ":"); isPath && rev != "" {
		return c.revParsePath(r, flags["--verify"] != nil, rev, name)
	}
	id, ok := r.resolve(rest[0])
	if !ok {
		if flags["--verify"] != nil {
			return c.fatal("Needed a single revision"), nil
		}
		return c.fatal("ambiguous argument '%s': unknown revision or path not in the working tree.", rest[0]), nil
	}
	if flags["--short"] != nil {
		id = fakeGitShort(id)
	}
	fmt.Fprintln(&c.out, id)
	return 0, nil
}

// revParsePath prints the id of {rev}:{path}, a blob or a directory's tree
// ("" the root's), a path starting ./ or ../ read from the directory git runs
// in and refused when it leaves the work tree; --verify -q (the model drops
// -q) answers a missing one with exit 1 and no output.
func (c *fakeGitCall) revParsePath(r *fakeGitRepo, verify bool, rev, name string) (int, error) {
	if !verify {
		return 0, fmt.Errorf("rev-parse of {rev}:{path} models --verify -q alone")
	}
	if name == "." || name == ".." || strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../") {
		rel, err := filepath.Rel(r.work, c.path(name))
		if err != nil {
			return 0, fmt.Errorf("rev-parse of %q outside the work tree: %w", name, err)
		}
		rel = filepath.ToSlash(rel)
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return c.fatal("'%s' is outside repository at '%s'", name, r.work), nil
		}
		name = rel
		if name == "." {
			name = ""
		}
	}
	id, ok := r.resolveCommit(rev)
	if !ok {
		return 1, nil
	}
	tree := r.state.Commits[id].Tree
	if entry, ok := tree[name]; ok {
		fmt.Fprintln(&c.out, entry.Blob)
		return 0, nil
	}
	prefix := name + "/"
	if name == "" {
		prefix = ""
	}
	for p := range tree {
		if name == "" || strings.HasPrefix(p, prefix) {
			fmt.Fprintln(&c.out, fakeGitTreeID(tree, prefix))
			return 0, nil
		}
	}
	return 1, nil
}

// hashObject prints the blob id of one file, read from the directory git runs
// in; a symlink is followed to the file it names, as git reads a path argument.
func (c *fakeGitCall) hashObject(_ *fakeGitRepo, args []string) (int, error) {
	args = slices.DeleteFunc(slices.Clone(args), func(arg string) bool { return arg == "--" })
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return 0, fmt.Errorf("hash-object models one file alone")
	}
	body, err := os.ReadFile(c.path(args[0]))
	if err != nil {
		return c.fatal("could not open '%s' for reading: No such file or directory", args[0]), nil
	}
	fmt.Fprintln(&c.out, fakeGitHash("blob", body))
	return 0, nil
}

func (c *fakeGitCall) symbolicRef(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{"--short": 0})
	if err != nil || len(rest) != 1 || rest[0] != gitHead {
		return 0, fmt.Errorf("symbolic-ref reads HEAD alone: %v", err)
	}
	head := r.head()
	if !strings.HasPrefix(head, "refs/") {
		return 1, nil
	}
	if flags["--short"] != nil {
		head = strings.TrimPrefix(head, "refs/heads/")
	}
	fmt.Fprintln(&c.out, head)
	return 0, nil
}

// describe answers --tags --abbrev=0: the tag nearest the commit, an
// annotated one first among tags on the same commit.
func (c *fakeGitCall) describe(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{gitTags: 0, "--abbrev": 1})
	if err != nil || flags[gitTags] == nil || len(flags["--abbrev"]) != 1 || flags["--abbrev"][0] != "0" ||
		len(rest) > 1 {
		return 0, fmt.Errorf("describe models --tags --abbrev=0 [rev] alone: %v", err)
	}
	if len(rest) == 0 {
		rest = []string{gitHead}
	}
	start, ok := r.resolveCommit(rest[0])
	if !ok {
		return c.fatal("Not a valid object name %s", rest[0]), nil
	}
	distance := r.ancestors(start)
	best, bestDistance, bestAnnotated := "", -1, false
	for _, ref := range fakeGitTagRefs(r) {
		id := r.state.Refs[ref]
		commit, _ := r.commitOf(id)
		d, reachable := distance[commit]
		_, annotated := r.state.Tags[id]
		if reachable && (bestDistance < 0 || d < bestDistance || (d == bestDistance && annotated && !bestAnnotated)) {
			best, bestDistance, bestAnnotated = strings.TrimPrefix(ref, "refs/tags/"), d, annotated
		}
	}
	if best == "" {
		if len(fakeGitTagRefs(r)) == 0 {
			return c.fatal("No names found, cannot describe anything."), nil
		}
		return c.fatal("No tags can describe '%s'.\nTry --always, or create some tags.", start), nil
	}
	fmt.Fprintln(&c.out, best)
	return 0, nil
}

func fakeGitTagRefs(r *fakeGitRepo) []string {
	const prefix = "refs/tags/"
	var refs []string
	for ref := range r.state.Refs {
		if strings.HasPrefix(ref, prefix) {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	return refs
}

func (c *fakeGitCall) mergeBase(r *fakeGitRepo, args []string) (int, error) {
	if len(args) != 3 || args[0] != "--is-ancestor" {
		return 0, fmt.Errorf("merge-base models --is-ancestor A B alone")
	}
	ancestor, okA := r.resolveCommit(args[1])
	descendant, okB := r.resolveCommit(args[2])
	if !okA || !okB {
		return c.fatal("Not a valid commit name"), nil
	}
	if _, ok := r.ancestors(descendant)[ancestor]; ok {
		return 0, nil
	}
	return 1, nil
}

// status answers --porcelain: index-against-HEAD and work-against-index
// columns, then the untracked files, each group sorted.
// lsFiles answers -z --cached --others --exclude-standard [--deduplicate]:
// the tracked and the unignored untracked files, sorted, NUL-terminated.
// lsTree answers --name-only {rev} {dir}/: the entries directly under dir.

// show answers {rev}:{path}, the bytes a commit holds at a file path; the
// refusals carry git's own wording, which callers tell apart.
func (c *fakeGitCall) show(r *fakeGitRepo, args []string) (int, error) {
	rev, path, found := "", "", false
	if len(args) == 1 {
		rev, path, found = strings.Cut(args[0], ":")
	}
	if !found || path == "" {
		return 0, fmt.Errorf("show models {rev}:{path} alone")
	}
	id, ok := r.resolveCommit(rev)
	if !ok {
		return c.fatal("invalid object name '%s'.", rev), nil
	}
	tree := r.state.Commits[id].Tree
	if entry, ok := tree[path]; ok {
		c.out.Write(r.state.Blobs[entry.Blob])
		return 0, nil
	}
	for p := range tree {
		if strings.HasPrefix(p, path+"/") {
			return 0, fmt.Errorf("show of the directory %s is not modelled", path)
		}
	}
	if _, err := os.Lstat(filepath.Join(r.work, filepath.FromSlash(path))); r.work != "" && err == nil {
		return c.fatal("path '%s' exists on disk, but not in '%s'", path, rev), nil
	}
	return c.fatal("path '%s' does not exist in '%s'", path, rev), nil
}
