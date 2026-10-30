// Package place files every chat under its place: the root of the project it
// ran in, folded into the outermost other root that holds it. It is the one
// derivation behind the picker's rail and the Herdr workspace each chat sits
// in, so both name the same places with the same labels.
//
// Places are derived from the rows alone (their project's root directory),
// never probed: a place exists because chats ran there.
package place

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// Place is one top-level place: a root no other root holds, and its label.
type Place struct {
	Root  string
	Label string
}

// Index is rows filed under their places.
type Index struct {
	RowRoot  []string          // per row: the root it is filed under; "" for a Global row
	RowPlace []string          // per row: the place holding that root; "" for a Global row
	Folded   map[string]string // every root, the extra ones included, to its place
	Places   []Place           // every place a row is filed in, in the order the rows first reach it
}

// IsUnder reports whether path is root or sits beneath it, on a path-segment
// boundary: /a/bc is not under /a/b.
func IsUnder(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	if root == string(filepath.Separator) {
		return filepath.IsAbs(path)
	}
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// Clean is filepath.Clean that keeps "" as "" instead of ".".
func Clean(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Clean(path)
}

// Global reports whether a row belongs to every place rather than one: a
// fleet-wide launch row (it retargets to whichever place is in view) and the
// notices.
func Global(row compose.Row) bool {
	switch row.Kind {
	case compose.NewClaude, compose.NewCodex, compose.NewOpenCode:
		return row.Workbench == ""
	case compose.ProfessorUpdate, compose.ProfessorUpdateFailed, compose.WorkbenchInvalid:
		return true
	default:
		return false
	}
}

// Root is the directory a row is filed under: its project's root, which
// compose already resolved through the repository (a worktree files under its
// main checkout) and through any workbench; the literal cwd when the project
// has no recorded root; "" for a Global row.
func Root(row compose.Row, projectDirs map[string]string) string {
	if Global(row) {
		return ""
	}
	if row.Workbench != "" {
		return Clean(row.Workbench)
	}
	if dir := Clean(projectDirs[row.Project]); dir != "" {
		return dir
	}
	return Clean(row.CWD)
}

// Fold maps every root to its place: the outermost other root that contains
// it, or itself. home and the filesystem root never absorb anything — a chat
// run in ~ must not swallow every project on the machine.
func Fold(roots []string, home string) map[string]string {
	unique := make([]string, 0, len(roots))
	seen := make(map[string]bool, len(roots))
	for _, root := range roots {
		if root != "" && !seen[root] {
			seen[root] = true
			unique = append(unique, root)
		}
	}
	sort.Slice(unique, func(left, right int) bool {
		if len(unique[left]) != len(unique[right]) {
			return len(unique[left]) < len(unique[right])
		}
		return unique[left] < unique[right]
	})
	home = Clean(home)
	folded := make(map[string]string, len(unique))
	accepted := make([]string, 0, len(unique))
	for _, root := range unique {
		owner := root
		for _, candidate := range accepted {
			if candidate == home || candidate == string(filepath.Separator) {
				continue
			}
			if candidate != root && IsUnder(root, candidate) {
				owner = candidate
				break
			}
		}
		if owner == root {
			accepted = append(accepted, root)
		}
		folded[root] = owner
	}
	return folded
}

// RootLabel names a root by its base name, ~ for home.
func RootLabel(root, home string) string {
	if home != "" && root == Clean(home) {
		return "~"
	}
	base := filepath.Base(root)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return root
	}
	return base
}

// Build files rows under their places. extra roots join the fold with no row
// of their own (the project a picker was opened in).
func Build(rows []compose.Row, projectDirs map[string]string, home string, extra ...string) Index {
	home = Clean(home)
	index := Index{RowRoot: make([]string, len(rows)), RowPlace: make([]string, len(rows))}
	roots := make([]string, 0, len(rows)+len(extra))
	for position := range rows {
		index.RowRoot[position] = Root(rows[position], projectDirs)
		roots = append(roots, index.RowRoot[position])
	}
	index.Folded = Fold(append(roots, extra...), home)
	at := make(map[string]int)
	for position, root := range index.RowRoot {
		if root == "" {
			continue
		}
		owner := index.Folded[root]
		index.RowPlace[position] = owner
		if _, found := at[owner]; !found {
			at[owner] = len(index.Places)
			index.Places = append(index.Places, Place{Root: owner, Label: RootLabel(owner, home)})
		}
	}
	// A place wears the label compose gave the rows filed at its root, so a
	// rail chip and its group header read the same. Home stays ~.
	for position, root := range index.RowRoot {
		if root == "" || root == home || index.Folded[root] != root {
			continue
		}
		if entry := &index.Places[at[root]]; entry.Label == RootLabel(root, home) && rows[position].Project != "" {
			entry.Label = rows[position].Project
		}
	}
	return index
}

// Disambiguate gives every label two or more entries share its root's parent
// too ("tools/api"), except the entries fixed marks, which still count.
func Disambiguate(roots, labels []string, fixed []bool) {
	counts := make(map[string]int, len(labels))
	for _, label := range labels {
		counts[label]++
	}
	for position := range labels {
		if counts[labels[position]] < 2 || position < len(fixed) && fixed[position] {
			continue
		}
		labels[position] = filepath.Base(filepath.Dir(roots[position])) + "/" + labels[position]
	}
}
