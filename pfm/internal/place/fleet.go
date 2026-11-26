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
	// Spaces is each chat's recorded space by chat id, the chats the rows do
	// not list included (compose.Output.Spaces).
	Spaces map[string]string
}

// Labeled files the fleet's rows, then extra rows after them, and gives every
// place the label the rail shows for it: Build's, told apart from a namesake.
func (fleet Fleet) Labeled(extra ...compose.Row) Index {
	rows := append(append(make([]compose.Row, 0, len(fleet.Rows)+len(extra)), fleet.Rows...), extra...)
	index := Build(rows, fleet.ProjectDirs, fleet.Home)
	roots := make([]string, len(index.Places))
	labels := make([]string, len(index.Places))
	fixed := make([]bool, len(index.Places))
	held := make(map[string]bool, len(fleet.Spaces))
	for position, entry := range index.Places {
		roots[position], labels[position], fixed[position] = entry.Root, entry.Label, IsSpaceRoot(entry.Root)
		held[entry.Root] = true
	}
	// A space no listed row wears (its chat fell outside the scan) still owns
	// its label: no folder place may share it.
	for _, space := range fleet.Spaces {
		if root := SpaceRoot(space); space != "" && !held[root] {
			held[root] = true
			roots, labels, fixed = append(roots, root), append(labels, space), append(fixed, true)
		}
	}
	Disambiguate(roots, labels, fixed)
	for position := range index.Places {
		index.Places[position].Label = labels[position]
	}
	return index
}

// Of is the place whose Herdr workspace holds the chat id, started in cwd, the
// one door every Herdr placement asks. A chat recorded in a space sits in that
// space, whatever its folder; any other sits in the place of the project cwd
// sits in (a worktree or a subdirectory files under its repository, a
// workbench under the project holding it), folded as the rail folds it, the
// fleet's other rows as they stand. The rail files a workbench's chats as its
// own group.
func (fleet Fleet) Of(cwd, id string) Place {
	if space := fleet.Spaces[id]; id != "" && space != "" {
		return Place{Root: SpaceRoot(space), Label: space, Dir: Clean(cwd)}
	}
	if bench, found := workbench.Owner(fleet.Benches, cwd); found {
		cwd = bench.Root
	}
	name, root := compose.ProjectOf(cwd, nil)
	index := fleet.Labeled(compose.Row{Kind: compose.LiveClaude, Project: name, CWD: root})
	return index.PlaceOf(len(index.RowPlace) - 1)
}

// Wearing is the place Herdr placement labels label (Labeled), home never: the
// place a Herdr workspace of that label holds.
func (fleet Fleet) Wearing(label string) (Place, bool) {
	if label == "" {
		return Place{}, false
	}
	for _, entry := range fleet.Labeled().Places {
		if entry.Label == label && entry.Root != Clean(fleet.Home) {
			return entry, true
		}
	}
	return Place{}, false
}

// Landing is the folder place a new chat started from at runs in: at itself,
// or for a space the place of the folder its chats run in (Of with no chat
// id); the chat itself files under the space it is born in (BirthSpace).
func (fleet Fleet) Landing(at Place) Place {
	if !IsSpaceRoot(at.Root) {
		return at
	}
	return fleet.Of(at.Directory(), "")
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

// BirthSpace is the space a new chat is born in, the one inheritance decision
// every birth door asks (spawn.BirthSpace): chosen, the space the user started
// it under (the picker's new-chat row of a space's group); else the space of
// parent, the chat that started it, none when parent has none; else, started
// from no chat, workspace when that Herdr workspace's label is a space's. ""
// is no space: the chat files under its folder's place.
func (fleet Fleet) BirthSpace(chosen, parent, workspace string) string {
	switch {
	case chosen != "":
		return chosen
	case parent != "":
		return fleet.Spaces[parent]
	}
	for _, space := range fleet.Spaces {
		if space != "" && space == workspace {
			return space
		}
	}
	return ""
}
