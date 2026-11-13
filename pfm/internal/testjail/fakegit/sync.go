package fakegit

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (c *fakeGitCall) remote(r *fakeGitRepo, args []string) (int, error) {
	if len(args) != 3 || args[0] != gitAdd {
		return 0, fmt.Errorf("remote models add {name} {url} alone")
	}
	if _, exists := c.configGet(r, "remote."+args[1]+".url"); exists {
		fmt.Fprintf(&c.errOut, "error: remote %s already exists.\n", args[1])
		return 3, nil
	}
	return 0, r.configSet("remote."+args[1]+".url", args[2])
}

// other opens the repository a remote names: its configured url, else the
// argument read as a path, which must be a repository's root (git never
// searches a remote path's parents).
func (c *fakeGitCall) other(r *fakeGitRepo, name string) (*fakeGitRepo, int) {
	url, ok := c.configGet(r, "remote."+name+".url")
	if !ok {
		url = name
	}
	dir := c.path(url)
	_, dotGit := os.Stat(filepath.Join(dir, fakeGitDirName))
	_, bare := os.Stat(filepath.Join(dir, fakeGitStateFile))
	if dotGit != nil && bare != nil {
		return nil, c.fatal("'%s' does not appear to be a git repository", url)
	}
	return (&fakeGitCall{dir: dir, env: []string{}, overrides: map[string]string{}}).open()
}

// push sends each refspec ({src}, {src}:{dst} or HEAD) and with --tags every
// tag, carrying the objects along.
func (c *fakeGitCall) push(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{gitTags: 0})
	if err != nil || len(rest) == 0 {
		return 0, fmt.Errorf("push models {remote} [refspec…] [--tags] alone: %v", err)
	}
	target, code := c.other(r, rest[0])
	if target == nil {
		c.errOut.WriteString("fatal: Could not read from remote repository.\n")
		return code, nil
	}
	updates := map[string]string{}
	for _, spec := range rest[1:] {
		src, dst, mapped := strings.Cut(spec, ":")
		id, ok := r.resolve(src)
		if !ok {
			return c.fatal("src refspec %s does not match any", src), nil
		}
		switch {
		case mapped && !strings.HasPrefix(dst, "refs/"):
			return 0, fmt.Errorf("push to the destination %q, no full refname, is not modelled", dst)
		case mapped:
		case src == gitHead && strings.HasPrefix(r.head(), "refs/"):
			dst = r.head()
		case r.state.Refs["refs/tags/"+src] != "":
			dst = "refs/tags/" + src
		case r.state.Refs["refs/heads/"+src] != "":
			dst = "refs/heads/" + src
		default:
			return c.fatal("the refspec %s names no branch or tag", src), nil
		}
		updates[dst] = id
	}
	if flags[gitTags] != nil {
		for _, ref := range fakeGitTagRefs(r) {
			updates[ref] = r.state.Refs[ref]
		}
	}
	target.copyObjects(r)
	for ref, id := range updates {
		target.state.Refs[ref] = id
	}
	return 0, target.save()
}

// fetch takes the remote's objects, its branches as refs/remotes/{remote}/,
// and the tags this repository lacks: every tag with --tags, else those on a
// fetched branch's history; a tag it holds differently is refused. Any other
// ref of the remote stays behind.
func (c *fakeGitCall) fetch(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{gitTags: 0})
	if err != nil || len(rest) > 1 {
		return 0, fmt.Errorf("fetch models [--tags] [remote] alone: %v", err)
	}
	name := "origin"
	if len(rest) == 1 {
		name = rest[0]
	}
	if _, configured := c.configGet(r, "remote."+name+".url"); !configured {
		return 0, fmt.Errorf("fetch from %q, no configured remote, is not modelled", name)
	}
	source, code := c.other(r, name)
	if source == nil {
		c.errOut.WriteString("fatal: Could not read from remote repository.\n")
		return code, nil
	}
	r.copyObjects(source)
	fetched := map[string]int{}
	for ref, id := range source.state.Refs {
		if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			r.state.Refs["refs/remotes/"+name+"/"+branch] = id
			maps.Copy(fetched, r.ancestors(id))
		}
	}
	for ref, id := range source.state.Refs {
		commit, _ := r.commitOf(id)
		_, followed := fetched[commit]
		if !strings.HasPrefix(ref, "refs/tags/") || (flags[gitTags] == nil && !followed) {
			continue
		}
		if mine, held := r.state.Refs[ref]; !held {
			r.state.Refs[ref] = id
		} else if mine != id {
			fmt.Fprintf(
				&c.errOut,
				" ! [rejected] %s (would clobber existing tag)\n",
				strings.TrimPrefix(ref, "refs/tags/"),
			)
			return 1, nil
		}
	}
	return 0, nil
}

func (c *fakeGitCall) lsRemote(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{gitTags: 0})
	if err != nil || flags[gitTags] == nil || len(rest) == 0 {
		return 0, fmt.Errorf("ls-remote models --tags {remote} [pattern…] alone: %v", err)
	}
	source, code := c.other(r, rest[0])
	if source == nil {
		return code, nil
	}
	for _, ref := range fakeGitTagRefs(source) {
		matched := len(rest) == 1
		for _, pattern := range rest[1:] {
			matched = matched || ref == pattern || strings.HasSuffix(ref, "/"+pattern)
		}
		if !matched {
			continue
		}
		id := source.state.Refs[ref]
		fmt.Fprintf(&c.out, "%s\t%s\n", id, ref)
		if commit, annotated := source.state.Tags[id]; annotated {
			fmt.Fprintf(&c.out, "%s\t%s^{}\n", commit, ref)
		}
	}
	return 0, nil
}

// worktree answers add --detach {path} {rev} and remove --force {path}.
func (c *fakeGitCall) worktree(r *fakeGitRepo, args []string) (int, error) {
	if len(args) == 0 {
		return 0, fmt.Errorf("worktree needs a subcommand")
	}
	flags, rest, err := fakeGitFlags(args[1:], map[string]int{"--detach": 0, "--force": 0})
	switch {
	case err != nil:
		return 0, err
	case args[0] == "add" && flags["--detach"] != nil && len(rest) == 2:
		return c.worktreeAdd(r, c.path(rest[0]), rest[1])
	case args[0] == "remove" && flags["--force"] != nil && len(rest) == 1:
		path := c.path(rest[0])
		if _, ok := r.state.Worktrees[path]; !ok {
			return c.fatal("'%s' is not a working tree", rest[0]), nil
		}
		if err := os.RemoveAll(path); err != nil {
			return c.fatal("%v", err), nil
		}
		if err := os.RemoveAll(r.adminDir(path)); err != nil {
			return c.fatal("%v", err), nil
		}
		delete(r.state.Worktrees, path)
		return 0, nil
	}
	return 0, fmt.Errorf("worktree models add --detach {path} {rev} and remove --force {path} alone")
}

func (c *fakeGitCall) worktreeAdd(r *fakeGitRepo, path, rev string) (int, error) {
	commit, ok := r.resolveCommit(rev)
	if !ok {
		return c.fatal("invalid reference: %s", rev), nil
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return c.fatal("'%s' already exists", path), nil
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return c.fatal("%v", err), nil
	}
	name := filepath.Base(path)
	for n := 1; ; n++ {
		if _, err := os.Lstat(filepath.Join(r.gitDir, "worktrees", name)); os.IsNotExist(err) {
			break
		}
		name = filepath.Base(path) + strconv.Itoa(n)
	}
	admin := filepath.Join(r.gitDir, "worktrees", name)
	link := "gitdir: " + admin + "\n"
	if err := os.WriteFile(filepath.Join(path, fakeGitDirName), []byte(link), 0o644); err != nil {
		return c.fatal("%v", err), nil
	}
	// The administrative directory git keeps for a linked work tree: commondir
	// names the shared git dir relative to it, gitdir the work tree's link back.
	if err := os.MkdirAll(admin, 0o755); err != nil {
		return c.fatal("%v", err), nil
	}
	for name, content := range map[string]string{
		"commondir": "../..\n",
		"gitdir":    filepath.Join(path, fakeGitDirName) + "\n",
	} {
		if err := os.WriteFile(filepath.Join(admin, name), []byte(content), 0o644); err != nil {
			return c.fatal("%v", err), nil
		}
	}
	tree := r.state.Commits[commit].Tree
	index := map[string]fakeGitEntry{}
	view := *r
	view.work = path
	for p, entry := range tree {
		index[p] = entry
		if err := view.writeFile(p, entry); err != nil {
			return c.fatal("%v", err), nil
		}
	}
	r.state.Worktrees[path] = &fakeGitLinked{Head: commit, Index: index, Admin: name}
	return 0, nil
}
