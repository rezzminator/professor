package place

import (
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// Fleet is one fleet scan as Herdr placement reads it: every row, compose's
// project roots, the home directory, and the workbenches a new chat's
// directory resolves through. The scan must not depend on where the caller
// runs: a working directory seeds a project's root and would move the fold.
// Faults are the scan's failures to read part of the fleet (a native seat, a
// workbench): the rows stand without them, and a placement names each.
type Fleet struct {
	Rows        []compose.Row
	ProjectDirs map[string]string
	Home        string
	Benches     []workbench.Bench
	Faults      []string
}

// Labeled files the fleet's rows, then extra rows after them, and gives every
// place the label the rail shows for it: Build's, told apart from a namesake.
func (fleet Fleet) Labeled(extra ...compose.Row) Index {
	rows := append(append(make([]compose.Row, 0, len(fleet.Rows)+len(extra)), fleet.Rows...), extra...)
	index := Build(rows, fleet.ProjectDirs, fleet.Home)
	roots := make([]string, len(index.Places))
	labels := make([]string, len(index.Places))
	for position, entry := range index.Places {
		roots[position], labels[position] = entry.Root, entry.Label
	}
	Disambiguate(roots, labels, nil)
	for position := range index.Places {
		index.Places[position].Label = labels[position]
	}
	return index
}

// Of is the place whose Herdr workspace holds a chat started in cwd, the one
// door every Herdr placement asks: the project cwd sits in (a worktree or a
// subdirectory files under its repository, a workbench under the project
// holding it), folded as the rail folds it, the fleet's other rows as they
// stand. The rail files a workbench's chats as its own group.
func (fleet Fleet) Of(cwd string) Place {
	if bench, found := workbench.Owner(fleet.Benches, cwd); found {
		cwd = bench.Root
	}
	name, root := compose.ProjectOf(cwd, nil)
	index := fleet.Labeled(compose.Row{Kind: compose.LiveClaude, Project: name, CWD: root})
	return index.PlaceOf(len(index.RowPlace) - 1)
}

// PlaceOf is the place row position is filed in; the zero Place for a Global row.
func (index Index) PlaceOf(position int) Place {
	for _, entry := range index.Places {
		if entry.Root == index.RowPlace[position] {
			return entry
		}
	}
	return Place{}
}
