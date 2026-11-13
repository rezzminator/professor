package fakegit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (c *fakeGitCall) initRepo(args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{"--bare": 0})
	if err != nil || len(rest) > 1 {
		return 0, fmt.Errorf("init models [--bare] [dir] alone: %v", err)
	}
	target := c.dir
	if len(rest) == 1 {
		target = c.path(rest[0])
	}
	bare := flags["--bare"] != nil
	repo := &fakeGitRepo{gitDir: filepath.Join(target, fakeGitDirName), work: target, state: newFakeGitState(bare)}
	if bare {
		repo.gitDir, repo.work = target, ""
	}
	if _, err := os.Stat(filepath.Join(repo.gitDir, fakeGitStateFile)); err == nil {
		return 0, nil
	}
	if err := os.MkdirAll(repo.gitDir, 0o755); err != nil {
		return c.fatal("%v", err), nil
	}
	if err := repo.writeConfig([]string{"[core]", fmt.Sprintf("\tbare = %t", bare)}); err != nil {
		return c.fatal("%v", err), nil
	}
	return 0, repo.save()
}

func (c *fakeGitCall) config(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{"--get": 0})
	switch {
	case err != nil:
		return 0, err
	case len(rest) == 1:
		value, ok := c.configGet(r, rest[0])
		if !ok {
			return 1, nil
		}
		fmt.Fprintln(&c.out, value)
		return 0, nil
	case len(rest) == 2 && flags["--get"] == nil:
		return 0, r.configSet(rest[0], rest[1])
	}
	return 0, fmt.Errorf("config models [--get] {key} and {key} {value} alone")
}

// add stages each pathspec: the files under it the work tree holds, and the
// removal of tracked ones it no longer holds; -A and "." stage everything.
func (c *fakeGitCall) add(r *fakeGitRepo, args []string) (int, error) {
	flags, specs, err := fakeGitFlags(args, map[string]int{"-A": 0, "--all": 0})
	if err != nil {
		return 0, err
	}
	if flags["-A"] != nil || flags["--all"] != nil {
		specs = append(specs, ".")
	}
	work, err := r.workFiles()
	if err != nil {
		return c.fatal("%v", err), nil
	}
	index := map[string]fakeGitEntry{}
	for p, entry := range r.index() {
		index[p] = entry
	}
	for _, spec := range specs {
		matched := false
		for p := range index {
			if _, onDisk := work[p]; !onDisk && fakeGitUnder(p, []string{spec}) {
				delete(index, p)
				matched = true
			}
		}
		for p, file := range work {
			if fakeGitUnder(p, []string{spec}) {
				r.state.Blobs[file.entry.Blob] = file.body
				index[p] = file.entry
				matched = true
			}
		}
		if !matched {
			return c.fatal("pathspec '%s' did not match any files", spec), nil
		}
	}
	r.setIndex(index)
	return 0, nil
}

func (c *fakeGitCall) commit(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{"-m": 1})
	if err != nil || len(rest) != 0 || len(flags["-m"]) != 1 {
		return 0, fmt.Errorf("commit models -m {message} alone: %v", err)
	}
	var parents []string
	if head, ok := r.resolve(gitHead); ok {
		if fakeGitTreeID(r.state.Commits[head].Tree, "") == fakeGitTreeID(r.index(), "") {
			fmt.Fprintln(&c.out, "nothing to commit, working tree clean")
			return 1, nil
		}
		parents = []string{head}
	}
	id, code := c.writeCommit(r, r.index(), parents, flags["-m"][0])
	if code == 0 {
		r.moveHead(id)
	}
	return code, nil
}

func (c *fakeGitCall) tag(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{"-a": 0, "-m": 1, "--list": 0, "-l": 0})
	if err != nil || len(rest) > 2 {
		return 0, fmt.Errorf("tag models --list and [-a -m {message}] {name} [rev] alone: %v", err)
	}
	if len(rest) == 0 || flags["--list"] != nil || flags["-l"] != nil {
		for _, ref := range fakeGitTagRefs(r) {
			fmt.Fprintln(&c.out, strings.TrimPrefix(ref, "refs/tags/"))
		}
		return 0, nil
	}
	rev := gitHead
	if len(rest) == 2 {
		rev = rest[1]
	}
	commit, ok := r.resolveCommit(rev)
	if !ok {
		return c.fatal("Failed to resolve '%s' as a valid ref.", rev), nil
	}
	ref := "refs/tags/" + rest[0]
	if _, exists := r.state.Refs[ref]; exists {
		return c.fatal("tag '%s' already exists", rest[0]), nil
	}
	if flags["-a"] == nil {
		r.state.Refs[ref] = commit
		return 0, nil
	}
	tagger, ok := c.identity(r, "COMMITTER")
	if !ok || len(flags["-m"]) != 1 {
		return c.fatal("an annotated tag needs an identity and -m"), nil
	}
	body := fmt.Sprintf("object %s\ntype commit\ntag %s\ntagger %s\n\n%s\n", commit, rest[0], tagger,
		strings.TrimRight(flags["-m"][0], " \n"))
	id := fakeGitHash("tag", []byte(body))
	r.state.Tags[id], r.state.Refs[ref] = commit, id
	return 0, nil
}

func (c *fakeGitCall) branch(r *fakeGitRepo, args []string) (int, error) {
	if len(args) < 1 || len(args) > 2 || strings.HasPrefix(args[0], "-") {
		return 0, fmt.Errorf("branch models {name} [rev] alone")
	}
	rev := gitHead
	if len(args) == 2 {
		rev = args[1]
	}
	commit, ok := r.resolveCommit(rev)
	if !ok {
		return c.fatal("not a valid object name: '%s'", rev), nil
	}
	if _, exists := r.state.Refs["refs/heads/"+args[0]]; exists {
		return c.fatal("a branch named '%s' already exists", args[0]), nil
	}
	r.state.Refs["refs/heads/"+args[0]] = commit
	return 0, nil
}

// checkout restores paths from the index, or switches to a branch (-b, -B
// creating it) or to a detached commit.
// moveWork runs moveTo and prints git's refusal when a local change is in the way.
