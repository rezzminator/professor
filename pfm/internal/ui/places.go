package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
	fleetplace "github.com/rezzminator/professor/pfm/internal/place"
)

// A place is a directory the fleet lives under. The picker opens on the place
// holding the project it was started in and lists every chat whose project
// sits at or beneath that root — nested repositories, linked worktrees and
// workbenches included — so opening the list inside a repo shows that repo's
// whole family and nothing from elsewhere. The rail under the tabs names every
// other place; ⇧←→ swaps between them and `@` jumps by name or by path.
//
// Places are derived from the rows alone (their project's root directory),
// never probed: a place exists because chats ran there.
type place struct {
	root     string // cleaned absolute directory; allPlaceRoot is the everywhere stop
	label    string
	live     int
	resume   int
	newestNS int64
	// adhoc marks a place reached by typing a directory after `@`, or a
	// nested project the picker was opened in: it holds every row whose
	// project or cwd sits under root, rather than the rows folded into it.
	adhoc bool
}

// allPlaceRoot is the rail's last stop: every place at once, the pre-place
// listing.
const allPlaceRoot = "*"

const allPlaceLabel = "✱ all"

// placeLabel names a root for the rail: its base name, ~ for home.
func placeLabel(root, home string) string {
	if root == allPlaceRoot {
		return allPlaceLabel
	}
	return fleetplace.RootLabel(root, home)
}

// tildePath writes path as ~/… under home.
func tildePath(path, home string) string {
	home = fleetplace.Clean(home)
	if home != "" && fleetplace.IsUnder(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// placeIndex is the derived place state for one row set.
type placeIndex struct {
	places   []place  // rail order: the opening place first, then by activity, then ✱ all
	rowDir   []string // per row: the root it is filed under, place.Root ("" = global row)
	rowPlace []string // per row: the folded place root ("" = global row)
	open     string   // the opening place's root (allPlaceRoot when none)
}

// buildPlaces derives the rail from rows. openRoot is the project the picker
// was opened in (the fleet launch row's directory); adhoc places the reader
// typed survive every refresh. visible decides which rows count.
func buildPlaces(
	rows []compose.Row,
	projectDirs map[string]string,
	home, openRoot, openLabel string,
	adhoc []place,
	visible func(compose.Row) bool,
) placeIndex {
	home = fleetplace.Clean(home)
	openRoot = fleetplace.Clean(openRoot)
	index := placeIndex{open: allPlaceRoot}
	openable := openRoot != "" && openRoot != home && openRoot != string(filepath.Separator)
	var opening []string
	if openable {
		opening = append(opening, openRoot)
	}
	filed := fleetplace.Build(rows, projectDirs, home, opening...)
	index.rowDir, index.rowPlace = filed.RowRoot, filed.RowPlace
	folded := filed.Folded
	byRoot := make(map[string]*place)
	order := make([]string, 0, len(filed.Places))
	ensure := func(root string) *place {
		if existing, found := byRoot[root]; found {
			return existing
		}
		byRoot[root] = &place{root: root, label: placeLabel(root, home)}
		order = append(order, root)
		return byRoot[root]
	}
	for _, entry := range filed.Places {
		ensure(entry.Root).label = entry.Label
	}
	all := place{root: allPlaceRoot, label: allPlaceLabel}
	for position := range rows {
		owner := index.rowPlace[position]
		row := rows[position]
		if owner == "" || visible != nil && !visible(row) || isNewChatActionKind(row.Kind) {
			continue
		}
		tally(byRoot[owner], row)
		tally(&all, row)
	}
	// The opening project is a place even before any chat ran in it. When it
	// folded into an outer place, the outer place leads and the project
	// itself rides beside it as a zoomed, ad-hoc stop.
	nested := place{}
	if openable {
		owner := folded[openRoot]
		ensure(owner)
		if owner == openRoot && openLabel != "" {
			byRoot[owner].label = openLabel
		}
		index.open = owner
		if owner != openRoot {
			nested = place{root: openRoot, adhoc: true}
			index.open = openRoot
		}
	}
	places := make([]place, 0, len(order)+len(adhoc)+2)
	for _, root := range order {
		places = append(places, *byRoot[root])
	}
	sort.SliceStable(places, func(left, right int) bool {
		if places[left].newestNS != places[right].newestNS {
			return places[left].newestNS > places[right].newestNS
		}
		return places[left].label < places[right].label
	})
	if openable {
		lead := folded[openRoot]
		for position := range places {
			if places[position].root == lead && position != 0 {
				moved := places[position]
				copy(places[1:position+1], places[:position])
				places[0] = moved
				break
			}
		}
	}
	extras := make([]place, 0, len(adhoc)+1)
	if nested.root != "" {
		extras = append(extras, nested)
	}
	for _, typed := range adhoc {
		if typed.root != nested.root {
			extras = append(extras, typed)
		}
	}
	for _, extra := range extras {
		if _, found := byRoot[extra.root]; found {
			continue // a typed path that is already a place is that place
		}
		extra.adhoc = true
		extra.label = adhocLabel(extra.root, places, home)
		for position := range rows {
			row := rows[position]
			if index.rowDir[position] == "" || visible != nil && !visible(row) || isNewChatActionKind(row.Kind) {
				continue
			}
			if fleetplace.IsUnder(index.rowDir[position], extra.root) ||
				fleetplace.IsUnder(fleetplace.Clean(row.CWD), extra.root) {
				tally(&extra, row)
			}
		}
		places = insertAfterOwner(places, extra)
	}
	disambiguateLabels(places)
	places = append(places, all)
	index.places = places
	return index
}

func tally(target *place, row compose.Row) {
	if row.Kind.IsLiveSeat() {
		target.live++
	} else {
		target.resume++
	}
	if row.ActivityNS > target.newestNS {
		target.newestNS = row.ActivityNS
	}
}

// adhocLabel names a typed or nested place relative to the place holding it
// ("acme › tools/cli"), or as a ~ path when no place holds it.
func adhocLabel(root string, places []place, home string) string {
	for _, owner := range places {
		if owner.adhoc || owner.root == allPlaceRoot || owner.root == root {
			continue
		}
		if owner.root != fleetplace.Clean(home) && fleetplace.IsUnder(root, owner.root) {
			return owner.label + " › " + strings.TrimPrefix(root, owner.root+string(filepath.Separator))
		}
	}
	return tildePath(root, home)
}

// insertAfterOwner seats a zoomed place right after the place that holds it.
func insertAfterOwner(places []place, extra place) []place {
	at := len(places)
	for position, owner := range places {
		if !owner.adhoc && owner.root != allPlaceRoot && fleetplace.IsUnder(extra.root, owner.root) {
			at = position + 1
			break
		}
	}
	places = append(places, place{})
	copy(places[at+1:], places[at:])
	places[at] = extra
	return places
}

// disambiguateLabels gives two places sharing a base name their parent too;
// an ad-hoc place keeps its label but still counts.
func disambiguateLabels(places []place) {
	roots := make([]string, len(places))
	labels := make([]string, len(places))
	fixed := make([]bool, len(places))
	for position, entry := range places {
		roots[position], labels[position], fixed[position] = entry.root, entry.label, entry.adhoc
	}
	fleetplace.Disambiguate(roots, labels, fixed)
	for position := range places {
		places[position].label = labels[position]
	}
}

// placeTally is the chip's count suffix: ●live ↻resumable, zeros omitted.
func placeTally(entry place) string {
	parts := make([]string, 0, 2)
	if entry.live > 0 {
		parts = append(parts, fmt.Sprintf("●%d", entry.live))
	}
	if entry.resume > 0 {
		parts = append(parts, fmt.Sprintf("↻%d", entry.resume))
	}
	return strings.Join(parts, " ")
}

// fuzzyPlace finds the best place for an `@` query: a label or path that
// contains it, then a subsequence match, earliest rail position first.
func fuzzyPlace(places []place, query, home string) (place, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return place{}, false
	}
	for _, pass := range []func(string) bool{
		func(candidate string) bool { return strings.HasPrefix(candidate, query) },
		func(candidate string) bool { return strings.Contains(candidate, query) },
		func(candidate string) bool { return runeSubsequence(query, candidate) },
	} {
		for _, entry := range places {
			candidates := []string{strings.ToLower(entry.label)}
			if entry.root != allPlaceRoot {
				candidates = append(candidates, strings.ToLower(tildePath(entry.root, home)))
			}
			for _, candidate := range candidates {
				if pass(candidate) {
					return entry, true
				}
			}
		}
	}
	return place{}, false
}

// typedPath resolves an `@` query that names a directory — /abs, ~/rel or
// ./rel against the opening place — to a cleaned absolute path. ok is false
// when the query is a name, not a path.
func typedPath(query, home, base string) (string, bool) {
	query = strings.TrimSpace(query)
	switch {
	case strings.HasPrefix(query, "/"):
		return filepath.Clean(query), true
	case query == "~" || strings.HasPrefix(query, "~/"):
		if home == "" {
			return "", false
		}
		return filepath.Clean(filepath.Join(home, strings.TrimPrefix(query, "~"))), true
	case strings.HasPrefix(query, "./") || strings.HasPrefix(query, "../"):
		if base == "" || base == allPlaceRoot {
			return "", false
		}
		return filepath.Clean(filepath.Join(base, query)), true
	}
	return "", false
}
