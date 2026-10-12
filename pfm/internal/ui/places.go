package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
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

// isUnder reports whether path is root or sits beneath it, on a path-segment
// boundary: /a/bc is not under /a/b.
func isUnder(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	if root == string(filepath.Separator) {
		return filepath.IsAbs(path)
	}
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// cleanDir is filepath.Clean that keeps "" as "" instead of ".".
func cleanDir(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Clean(path)
}

// isGlobalRow reports whether a row belongs to every place: the fleet-wide
// launch row (it retargets to whichever place is on screen) and the notices.
func isGlobalRow(row compose.Row) bool {
	return isFleetNewChatRow(row) || isNoticeKind(row.Kind)
}

// rowRoot is the directory a row is filed under: its project's root, which
// compose already resolved through the repository (a worktree files under its
// main checkout) and through any workbench; the literal cwd when the project
// has no recorded root.
func rowRoot(row compose.Row, projectDirs map[string]string) string {
	if isGlobalRow(row) {
		return ""
	}
	if row.Workbench != "" {
		return cleanDir(row.Workbench)
	}
	if dir := cleanDir(projectDirs[row.Project]); dir != "" {
		return dir
	}
	return cleanDir(row.CWD)
}

// foldRoots maps every root to its place: the outermost other root that
// contains it, or itself. home and the filesystem root never absorb anything —
// a chat run in ~ must not swallow every project on the machine.
func foldRoots(roots []string, home string) map[string]string {
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
	home = cleanDir(home)
	folded := make(map[string]string, len(unique))
	accepted := make([]string, 0, len(unique))
	for _, root := range unique {
		owner := root
		for _, candidate := range accepted {
			if candidate == home || candidate == string(filepath.Separator) {
				continue
			}
			if candidate != root && isUnder(root, candidate) {
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

// placeLabel names a root for the rail: its base name, ~ for home.
func placeLabel(root, home string) string {
	if root == allPlaceRoot {
		return allPlaceLabel
	}
	if home != "" && root == cleanDir(home) {
		return "~"
	}
	base := filepath.Base(root)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return root
	}
	return base
}

// tildePath writes path as ~/… under home.
func tildePath(path, home string) string {
	home = cleanDir(home)
	if home != "" && isUnder(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// placeIndex is the derived place state for one row set.
type placeIndex struct {
	places   []place  // rail order: the opening place first, then by activity, then ✱ all
	rowDir   []string // per row: rowRoot ("" = global row)
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
	home = cleanDir(home)
	openRoot = cleanDir(openRoot)
	index := placeIndex{
		rowDir:   make([]string, len(rows)),
		rowPlace: make([]string, len(rows)),
		open:     allPlaceRoot,
	}
	roots := make([]string, 0, len(rows)+1)
	for position := range rows {
		index.rowDir[position] = rowRoot(rows[position], projectDirs)
		roots = append(roots, index.rowDir[position])
	}
	openable := openRoot != "" && openRoot != home && openRoot != string(filepath.Separator)
	if openable {
		roots = append(roots, openRoot)
	}
	folded := foldRoots(roots, home)
	byRoot := make(map[string]*place)
	order := make([]string, 0)
	ensure := func(root string) *place {
		if existing, found := byRoot[root]; found {
			return existing
		}
		byRoot[root] = &place{root: root, label: placeLabel(root, home)}
		order = append(order, root)
		return byRoot[root]
	}
	all := place{root: allPlaceRoot, label: allPlaceLabel}
	for position := range rows {
		dir := index.rowDir[position]
		if dir == "" {
			continue
		}
		owner := folded[dir]
		index.rowPlace[position] = owner
		target := ensure(owner)
		row := rows[position]
		if visible != nil && !visible(row) || isNewChatActionKind(row.Kind) {
			continue
		}
		tally(target, row)
		tally(&all, row)
	}
	// A place wears the label compose gave the rows filed at its root, so a
	// rail chip and its group header read the same; the opening place wears
	// the launch row's. Home stays ~.
	for position := range rows {
		if root := index.rowDir[position]; root != "" && root != home && folded[root] == root {
			if entry := byRoot[root]; entry.label == placeLabel(root, home) && rows[position].Project != "" {
				entry.label = rows[position].Project
			}
		}
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
			if isUnder(index.rowDir[position], extra.root) || isUnder(cleanDir(row.CWD), extra.root) {
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
		if owner.root != cleanDir(home) && isUnder(root, owner.root) {
			return owner.label + " › " + strings.TrimPrefix(root, owner.root+string(filepath.Separator))
		}
	}
	return tildePath(root, home)
}

// insertAfterOwner seats a zoomed place right after the place that holds it.
func insertAfterOwner(places []place, extra place) []place {
	at := len(places)
	for position, owner := range places {
		if !owner.adhoc && owner.root != allPlaceRoot && isUnder(extra.root, owner.root) {
			at = position + 1
			break
		}
	}
	places = append(places, place{})
	copy(places[at+1:], places[at:])
	places[at] = extra
	return places
}

// disambiguateLabels gives two places sharing a base name their parent too.
func disambiguateLabels(places []place) {
	counts := make(map[string]int, len(places))
	for _, entry := range places {
		counts[entry.label]++
	}
	for position := range places {
		entry := &places[position]
		if counts[entry.label] < 2 || entry.adhoc {
			continue
		}
		parent := filepath.Base(filepath.Dir(entry.root))
		entry.label = parent + "/" + entry.label
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
