package fakegit

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// fakeGitUntrackedNormal is status's default --untracked-files mode.
const fakeGitUntrackedNormal = "normal"

func (c *fakeGitCall) status(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{"--porcelain": 0, "--untracked-files": 1, "--ignored": 0})
	mode := fakeGitUntrackedNormal
	if values := flags["--untracked-files"]; len(values) > 0 {
		mode = values[len(values)-1]
	}
	modes := []string{"no", fakeGitUntrackedNormal, "all"}
	if err != nil || flags["--porcelain"] == nil || !slices.Contains(modes, mode) ||
		(flags["--ignored"] != nil && mode != "all") {
		return 0, fmt.Errorf(
			"status models --porcelain [--untracked-files=no|normal|all] [--ignored with =all] alone: %v", err)
	}
	revs, specs := fakeGitPathspec(rest)
	specs = c.rootRelative(r, slices.Concat(revs, specs))
	work, err := r.workFiles()
	if err != nil {
		return c.fatal("%v", err), nil
	}
	head, index := r.headTree(), r.index()
	paths := map[string]bool{}
	for _, tree := range []map[string]fakeGitEntry{head, index} {
		for p := range tree {
			paths[p] = true
		}
	}
	var tracked, untracked []string
	shown := map[string]bool{}
	for p := range work {
		if _, ok := index[p]; ok || mode == "no" || !fakeGitUnder(p, specs) {
			continue
		}
		if mode == fakeGitUntrackedNormal {
			p = fakeGitUntrackedDir(index, p, specs)
		}
		if !shown[p] {
			shown[p] = true
			untracked = append(untracked, "?? "+p)
		}
	}
	for p := range paths {
		if !fakeGitUnder(p, specs) {
			continue
		}
		x, y := fakeGitColumn(head, index, p), byte(' ')
		if staged, ok := index[p]; ok {
			if local, onDisk := work[p]; !onDisk {
				y = 'D'
			} else if local.entry != staged {
				y = 'M'
			}
		}
		if x != ' ' || y != ' ' {
			tracked = append(tracked, string([]byte{x, y})+" "+p)
		}
	}
	sort.Slice(tracked, func(i, j int) bool { return tracked[i][3:] < tracked[j][3:] })
	sort.Strings(untracked)
	var ignored []string
	if flags["--ignored"] != nil {
		files, err := r.ignoredFiles()
		if err != nil {
			return c.fatal("%v", err), nil
		}
		for _, p := range files {
			if fakeGitUnder(p, specs) {
				ignored = append(ignored, "!! "+p)
			}
		}
	}
	for _, line := range append(append(tracked, untracked...), ignored...) {
		fmt.Fprintln(&c.out, line)
	}
	return 0, nil
}

// fakeGitUntrackedDir is how --untracked-files=normal shows the untracked
// file p: as its shallowest directory the pathspecs cover that holds no
// tracked file, "dir/", else as p itself.
func fakeGitUntrackedDir(index map[string]fakeGitEntry, p string, specs []string) string {
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts); i++ {
		dir := strings.Join(parts[:i], "/")
		holds := false
		for tracked := range index {
			holds = holds || strings.HasPrefix(tracked, dir+"/")
		}
		if !holds && fakeGitUnder(dir, specs) {
			return dir + "/"
		}
	}
	return p
}

func fakeGitColumn(before, after map[string]fakeGitEntry, p string) byte {
	old, inBefore := before[p]
	current, inAfter := after[p]
	switch {
	case !inBefore && inAfter:
		return 'A'
	case inBefore && !inAfter:
		return 'D'
	case old != current:
		return 'M'
	}
	return ' '
}

func (c *fakeGitCall) lsFiles(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{
		"-z": 0, "--cached": 0, "--others": 0, "--exclude-standard": 0, "--deduplicate": 0,
	})
	if err != nil || flags["-z"] == nil || flags["--cached"] == nil || flags["--others"] == nil ||
		flags["--exclude-standard"] == nil {
		return 0, fmt.Errorf("ls-files models -z --cached --others --exclude-standard alone: %v", err)
	}
	work, err := r.workFiles()
	if err != nil {
		return c.fatal("%v", err), nil
	}
	// git lists the untracked files first, then the tracked ones, each sorted,
	// under the pathspecs (the directory it runs in without any) and relative
	// to that directory.
	revs, paths := fakeGitPathspec(rest)
	specs := slices.Concat(revs, paths)
	if len(specs) == 0 {
		specs = []string{"."}
	}
	specs = c.rootRelative(r, specs)
	index := r.index()
	var others, cached []string
	for p := range work {
		if _, tracked := index[p]; !tracked && fakeGitUnder(p, specs) {
			others = append(others, p)
		}
	}
	for p := range index {
		if fakeGitUnder(p, specs) {
			cached = append(cached, p)
		}
	}
	sort.Strings(others)
	sort.Strings(cached)
	for _, p := range append(others, cached...) {
		rel, err := filepath.Rel(c.dir, filepath.Join(r.work, filepath.FromSlash(p)))
		if err != nil {
			return c.fatal("%v", err), nil
		}
		c.out.WriteString(filepath.ToSlash(rel) + "\x00")
	}
	return 0, nil
}

func (c *fakeGitCall) lsTree(r *fakeGitRepo, args []string) (int, error) {
	if len(args) != 3 || args[0] != "--name-only" || !strings.HasSuffix(args[2], "/") {
		return 0, fmt.Errorf("ls-tree models --name-only {rev} {dir}/ alone")
	}
	id, ok := r.resolveCommit(args[1])
	if !ok {
		return c.fatal("Not a valid object name %s", args[1]), nil
	}
	names := map[string]bool{}
	for p := range r.state.Commits[id].Tree {
		if rest, under := strings.CutPrefix(p, args[2]); under {
			name, _, _ := strings.Cut(rest, "/")
			names[args[2]+name] = true
		}
	}
	listed := make([]string, 0, len(names))
	for name := range names {
		listed = append(listed, name)
	}
	sort.Strings(listed)
	for _, name := range listed {
		fmt.Fprintln(&c.out, name)
	}
	return 0, nil
}
