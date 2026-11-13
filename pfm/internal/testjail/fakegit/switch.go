package fakegit

import (
	"fmt"
	"slices"
	"strings"
)

func (c *fakeGitCall) checkout(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{"-b": 1, "-B": 1, "--detach": 0})
	if err != nil {
		return 0, err
	}
	if revs, paths := fakeGitPathspec(rest); len(revs) == 0 && len(paths) > 0 {
		return c.checkoutPaths(r, paths)
	}
	create := slices.Concat(flags["-b"], flags["-B"])
	if len(rest) == 0 && len(create) == 0 && flags["--detach"] == nil {
		return 0, fmt.Errorf("checkout with no revision is not modelled")
	}
	rev := gitHead
	if len(rest) == 1 {
		rev = rest[0]
	}
	target, ok := r.resolveCommit(rev)
	if !ok || len(rest) > 1 {
		return c.fatal("invalid reference: %s", rev), nil
	}
	if _, exists := r.state.Refs["refs/heads/"+strings.Join(flags["-b"], "")]; flags["-b"] != nil && exists {
		return c.fatal("a branch named '%s' already exists", flags["-b"][0]), nil
	}
	if code := c.moveWork(r, target, "checkout", 1); code != 0 {
		return code, nil
	}
	_, isBranch := r.state.Refs["refs/heads/"+rev]
	switch {
	case len(create) == 1:
		r.state.Refs["refs/heads/"+create[0]] = target
		r.setHead("refs/heads/" + create[0])
	case isBranch && flags["--detach"] == nil:
		r.setHead("refs/heads/" + rev)
	default:
		r.setHead(target)
	}
	return 0, nil
}

// checkoutPaths restores each index entry under the pathspecs, read from the
// directory git runs in; a pathspec matching no entry refuses them all.
func (c *fakeGitCall) checkoutPaths(r *fakeGitRepo, paths []string) (int, error) {
	index, specs := r.index(), c.rootRelative(r, paths)
	unmatched := false
	for i, spec := range specs {
		matched := false
		for p := range index {
			matched = matched || fakeGitUnder(p, []string{spec})
		}
		if !matched {
			fmt.Fprintf(&c.errOut, "error: pathspec '%s' did not match any file(s) known to git\n", paths[i])
			unmatched = true
		}
	}
	if unmatched {
		return 1, nil
	}
	for p, entry := range index {
		if fakeGitUnder(p, specs) {
			if err := r.writeFile(p, entry); err != nil {
				return c.fatal("%v", err), nil
			}
		}
	}
	return 0, nil
}

func (c *fakeGitCall) moveWork(r *fakeGitRepo, target, verb string, refusal int) int {
	conflicts, err := r.moveTo(target, false)
	if err != nil {
		return c.fatal("%v", err)
	}
	if len(conflicts) > 0 {
		fmt.Fprintf(&c.errOut, "error: Your local changes to the following files would be overwritten by %s:\n\t%s\n",
			verb, strings.Join(conflicts, "\n\t"))
		return refusal
	}
	return 0
}

func (c *fakeGitCall) reset(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{"--keep": 0, "--hard": 0})
	if err != nil || len(rest) > 1 || (flags["--keep"] == nil) == (flags["--hard"] == nil) {
		return 0, fmt.Errorf("reset models --keep|--hard [rev] alone: %v", err)
	}
	rev := gitHead
	if len(rest) == 1 {
		rev = rest[0]
	}
	target, ok := r.resolveCommit(rev)
	if !ok {
		return c.fatal("ambiguous argument '%s': unknown revision", rev), nil
	}
	if flags["--hard"] != nil {
		if _, err := r.moveTo(target, true); err != nil {
			return c.fatal("%v", err), nil
		}
	} else if code := c.moveWork(r, target, "reset", 128); code != 0 {
		return code, nil
	}
	r.moveHead(target)
	return 0, nil
}

func (c *fakeGitCall) merge(r *fakeGitRepo, args []string) (int, error) {
	if len(args) != 2 || args[0] != "--ff-only" {
		return 0, fmt.Errorf("merge models --ff-only {rev} alone")
	}
	target, ok := r.resolveCommit(args[1])
	head, headOK := r.resolve(gitHead)
	if !ok || !headOK {
		return c.fatal("%s - not something we can merge", args[1]), nil
	}
	if _, merged := r.ancestors(head)[target]; merged {
		fmt.Fprintln(&c.out, "Already up to date.")
		return 0, nil
	}
	if _, forward := r.ancestors(target)[head]; !forward {
		return c.fatal("Not possible to fast-forward, aborting."), nil
	}
	if code := c.moveWork(r, target, "merge", 1); code != 0 {
		return code, nil
	}
	r.moveHead(target)
	return 0, nil
}
