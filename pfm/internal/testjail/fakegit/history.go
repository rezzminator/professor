package fakegit

import (
	"fmt"
	"sort"
	"strings"
)

// diff answers {rev} -- {path}…: the unified diff from rev's tree to the work
// tree, three lines of context, hunks closer than seven lines merged.
func (c *fakeGitCall) diff(r *fakeGitRepo, args []string) (int, error) {
	_, rest, err := fakeGitFlags(args, map[string]int{"--no-ext-diff": 0, "--no-textconv": 0, "--no-color": 0})
	revs, specs := fakeGitPathspec(rest)
	if err != nil || len(revs) != 1 {
		return 0, fmt.Errorf("diff models {rev} -- {path} alone: %v", err)
	}
	id, ok := r.resolveCommit(revs[0])
	if !ok {
		return c.fatal("bad revision '%s'", revs[0]), nil
	}
	work, err := r.workFiles()
	if err != nil {
		return c.fatal("%v", err), nil
	}
	tree, paths := r.state.Commits[id].Tree, map[string]bool{}
	for p := range tree {
		paths[p] = true
	}
	for p := range r.index() {
		paths[p] = true
	}
	listed := make([]string, 0, len(paths))
	for p := range paths {
		if fakeGitUnder(p, specs) {
			listed = append(listed, p)
		}
	}
	sort.Strings(listed)
	for _, p := range listed {
		old, inOld := tree[p]
		local, inNew := work[p]
		if (inOld && inNew && old == local.entry) || (!inOld && !inNew) {
			continue
		}
		if err := c.diffFile(p, old, local, inOld, inNew, string(r.state.Blobs[old.Blob])); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

func (c *fakeGitCall) diffFile(p string, old fakeGitEntry, local fakeGitFile, inOld, inNew bool, before string) error {
	after := string(local.body)
	a, b := fakeGitLines(before), fakeGitLines(after)
	changes, err := fakeGitChanges(a, b)
	if err != nil {
		return fmt.Errorf("diff of %s: %w", p, err)
	}
	fmt.Fprintf(&c.out, "diff --git a/%s b/%s\n", p, p)
	from, to := "a/"+p, "b/"+p
	switch {
	case !inOld:
		fmt.Fprintf(&c.out, "new file mode %s\nindex 0000000..%s\n", local.entry.Mode, fakeGitShort(local.entry.Blob))
		from = devNull
	case !inNew:
		fmt.Fprintf(&c.out, "deleted file mode %s\nindex %s..0000000\n", old.Mode, fakeGitShort(old.Blob))
		to = devNull
	case old.Mode != local.entry.Mode && old.Blob == local.entry.Blob:
		fmt.Fprintf(&c.out, "old mode %s\nnew mode %s\n", old.Mode, local.entry.Mode)
	case old.Mode != local.entry.Mode:
		fmt.Fprintf(&c.out, "old mode %s\nnew mode %s\nindex %s..%s\n", old.Mode, local.entry.Mode,
			fakeGitShort(old.Blob), fakeGitShort(local.entry.Blob))
	default:
		fmt.Fprintf(&c.out, "index %s..%s %s\n", fakeGitShort(old.Blob), fakeGitShort(local.entry.Blob), old.Mode)
	}
	if before == after {
		return nil
	}
	fmt.Fprintf(&c.out, "--- %s\n+++ %s\n", from, to)
	lines := func(mark string, from []string) {
		for _, line := range from {
			c.out.WriteString(mark + line)
			if !strings.HasSuffix(line, "\n") {
				c.out.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	for first := 0; first < len(changes); {
		last := first
		for last+1 < len(changes) && changes[last+1].aStart-changes[last].aEnd <= 2*fakeGitContext {
			last++
		}
		startA := max(changes[first].aStart-fakeGitContext, 0)
		startB := changes[first].bStart - (changes[first].aStart - startA)
		endA := min(changes[last].aEnd+fakeGitContext, len(a))
		endB := changes[last].bEnd + (endA - changes[last].aEnd)
		fmt.Fprintf(&c.out, "@@ -%s +%s @@%s\n", fakeGitRange(startA, endA-startA), fakeGitRange(startB, endB-startB),
			fakeGitFuncname(a[:startA]))
		at := startA
		for _, change := range changes[first : last+1] {
			lines(" ", a[at:change.aStart])
			lines("-", a[change.aStart:change.aEnd])
			lines("+", b[change.bStart:change.bEnd])
			at = change.aEnd
		}
		lines(" ", a[at:endA])
		first = last + 1
	}
	return nil
}

// log answers [--reverse] --format={%h,%s} [{base}..]{tip} [-- path…]: the
// first-parent commits after base up to tip that change a path (the model
// writes no merge commit); any other placeholder, an omitted range side or
// A...B is refused.
func (c *fakeGitCall) log(r *fakeGitRepo, args []string) (int, error) {
	flags, rest, err := fakeGitFlags(args, map[string]int{
		"--no-color": 0, "--no-show-signature": 0, "--reverse": 0, "--format": 1,
	})
	revs, specs := fakeGitPathspec(rest)
	if err != nil || len(revs) != 1 || len(flags["--format"]) != 1 {
		return 0, fmt.Errorf("log models --format={format} {base}..{tip} alone: %v", err)
	}
	if err := fakeGitLogFormat(flags["--format"][0]); err != nil {
		return 0, err
	}
	baseRev, tipRev, isRange := strings.Cut(revs[0], "..")
	if !isRange {
		baseRev, tipRev = "", revs[0]
	}
	if strings.Contains(revs[0], "...") || (isRange && (baseRev == "" || tipRev == "")) {
		return 0, fmt.Errorf("log of the range %q is not modelled", revs[0])
	}
	tip, ok := r.resolveCommit(tipRev)
	excluded := map[string]int{}
	if baseRev != "" {
		base, baseOK := r.resolveCommit(baseRev)
		ok = ok && baseOK
		excluded = r.ancestors(base)
	}
	if !ok {
		return c.fatal("bad revision '%s'", revs[0]), nil
	}
	var lines []string
	for id := tip; id != ""; {
		if _, stop := excluded[id]; stop {
			break
		}
		commit, parentTree := r.state.Commits[id], map[string]fakeGitEntry{}
		next := ""
		if len(commit.Parents) > 0 {
			next = commit.Parents[0]
			parentTree = r.state.Commits[next].Tree
		}
		if fakeGitTouches(parentTree, commit.Tree, specs) {
			lines = append(lines, strings.NewReplacer("%h", fakeGitShort(id), "%s", commit.Subject).
				Replace(flags["--format"][0]))
		}
		id = next
	}
	if flags["--reverse"] != nil {
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
	}
	for _, line := range lines {
		fmt.Fprintln(&c.out, line)
	}
	return 0, nil
}

func fakeGitTouches(before, after map[string]fakeGitEntry, specs []string) bool {
	for _, pair := range [][2]map[string]fakeGitEntry{{before, after}, {after, before}} {
		for p, entry := range pair[0] {
			if fakeGitUnder(p, specs) && pair[1][p] != entry {
				return true
			}
		}
	}
	return false
}

// fakeGitLogFormat refuses a --format holding any placeholder but %h and %s.
func fakeGitLogFormat(format string) error {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		if i+1 < len(format) && (format[i+1] == 'h' || format[i+1] == 's') {
			i++
			continue
		}
		return fmt.Errorf("log --format=%s: placeholders other than %%h and %%s are not modelled", format)
	}
	return nil
}
