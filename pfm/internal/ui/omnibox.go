package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	fleetplace "github.com/rezzminator/professor/pfm/internal/place"
)

// The find bar is an omnibox with two modes, read off what is typed:
//
//	text           find — fuzzy-match chats, as it always has
//	@name  @~/dir  go — preview a place live as it is typed; ⏎ moves there,
//	               esc backs out. A path that exists is a place even when no
//	               chat has run there yet.
//
// A new chat is never typed: ⏎ on the ✦ row walks its steps (launchwizard.go).
type omniMode uint8

// findPrompt is the find bar's prompt in its default mode.
const findPrompt = "find › "

const (
	omniFind omniMode = iota
	omniGo
)

// omni reads the query's mode: the go text, or the find text.
func (model Model) omni() (omniMode, string) {
	value := model.query.Value()
	if text, found := strings.CutPrefix(value, "@"); found {
		return omniGo, text
	}
	return omniFind, value
}

func (model Model) placeByRoot(root string) (place, bool) {
	for _, entry := range model.places {
		if entry.root == root {
			return entry, true
		}
	}
	return place{}, false
}

// effectivePlace is the place on screen: the `@` preview while one matches,
// the committed place otherwise.
func (model Model) effectivePlace() place {
	if model.goPreviewOK {
		return model.goPreview
	}
	if entry, found := model.placeByRoot(model.place); found {
		return entry
	}
	return place{root: allPlaceRoot, label: allPlaceLabel}
}

// inPlace reports whether row index belongs on screen in the place
// rebuildOrder resolved (model.here) — once per pass, not once per row.
// Global rows — the fleet launch row and the notices — belong everywhere.
func (model *Model) inPlace(index int) bool {
	current := model.here
	if current.root == allPlaceRoot || index >= len(model.rowDir) {
		return true
	}
	dir := model.rowDir[index]
	if dir == "" {
		return true
	}
	if current.adhoc {
		return fleetplace.IsUnder(dir, current.root) ||
			fleetplace.IsUnder(fleetplace.Clean(model.rows[index].CWD), current.root)
	}
	return model.rowPlace[index] == current.root
}

// resolveGo finds the place an `@` query names: a typed directory first (an
// existing place, or a new ad-hoc one when the directory exists), then the
// best name match on the rail. A directory that could not be looked at is an
// error, never "no match"; a relative path never falls back to a name match.
func (model Model) resolveGo(text string) (place, bool, error) {
	base := model.effectivePlaceRootForPaths()
	if path, ok := typedPath(text, model.deck.home, base); ok {
		if entry, found := model.placeByRoot(path); found {
			return entry, true, nil
		}
		if model.dirExists == nil {
			return place{}, false, nil
		}
		exists, err := model.dirExists(path)
		if err != nil {
			return place{}, false, fmt.Errorf("cannot look at %s: %w", tildePath(path, model.deck.home), err)
		}
		if !exists {
			return place{}, false, nil
		}
		return place{root: path, label: adhocLabel(path, model.places, model.deck.home), adhoc: true}, true, nil
	}
	if trimmed := strings.TrimSpace(text); strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, "../") {
		return place{}, false, nil
	}
	found, ok := fuzzyPlace(model.places, text, model.deck.home)
	return found, ok, nil
}

// effectivePlaceRootForPaths anchors ./ and ../ in an `@` path: the committed
// place, else the opening place, else the directory the picker was opened in
// (a picker opened in ~ opens on the everywhere stop).
func (model Model) effectivePlaceRootForPaths() string {
	if model.place != allPlaceRoot && model.place != "" {
		return model.place
	}
	if model.openPlace != allPlaceRoot && model.openPlace != "" {
		return model.openPlace
	}
	return fleetplace.Clean(model.launchHome.cwd)
}

// refreshGoPreview recomputes the `@` preview after the query changed.
func (model *Model) refreshGoPreview() {
	mode, text := model.omni()
	model.goError = ""
	if mode != omniGo {
		model.goPreview, model.goPreviewOK = place{}, false
		return
	}
	var err error
	model.goPreview, model.goPreviewOK, err = model.resolveGo(text)
	if err != nil {
		model.goError = err.Error()
	}
}

// swapPlace moves the committed place one stop along the rail.
func (model *Model) swapPlace(direction int) {
	if len(model.places) == 0 {
		return
	}
	at := 0
	for position, entry := range model.places {
		if entry.root == model.place {
			at = position
			break
		}
	}
	at = (at + direction + len(model.places)) % len(model.places)
	model.place = model.places[at].root
	follow := model.selectedKey()
	model.rebuildOrder()
	model.refilter(follow, 0)
}

// commitGo moves to the `@` preview and clears the query; a query that
// names nothing says so instead of doing nothing.
func (model Model) commitGo() Model {
	_, text := model.omni()
	if !model.goPreviewOK {
		if model.goError != "" {
			model.killStatus = "@" + text + ": " + model.goError
			return model
		}
		if strings.TrimSpace(text) == "" {
			model.killStatus = "@ — type a place name, or a path like ~/src/app"
		} else {
			model.killStatus = "no place or directory matches @" + text
		}
		return model
	}
	target := model.goPreview
	if target.adhoc {
		if _, found := model.placeByRoot(target.root); !found {
			model.adhocPlaces = append(model.adhocPlaces, place{root: target.root})
		}
	}
	model.place = target.root
	model.query.SetValue("")
	model.query.Prompt = findPrompt
	model.goPreview, model.goPreviewOK, model.goError = place{}, false, ""
	model.rebuild("", 0)
	return model
}

// retargetLaunchRows points the fleet launch row at the place on screen, so
// it groups under that place's header and Enter starts the chat there. A
// workbench row keeps its own directory; the everywhere stop restores the
// opening project compose chose (launchHome). It also resolves model.here, the
// place the rest of the rebuildOrder pass files rows against.
func (model *Model) retargetLaunchRows() {
	current := model.effectivePlace()
	model.here = current
	if !model.mergeNewChat {
		return
	}
	for index := range model.rows {
		row := &model.rows[index]
		if !isFleetNewChatRow(*row) {
			continue
		}
		if current.root == allPlaceRoot {
			row.Project, row.CWD = model.launchHome.project, model.launchHome.cwd
		} else {
			row.Project, row.CWD = current.label, current.root
		}
		if index < len(model.search) {
			model.search[index] = searchText(*row) // a find matches the place it now names
		}
	}
}

// renderPlaceRail draws the place strip under the tabs: the place on screen as
// a solid chip, the rest dim with their ●live ↻resumable tallies, windowed so
// the solid chip never scrolls off a narrow terminal.
func (model Model) renderPlaceRail(width int) string {
	palette := configuredPalette
	current := model.effectivePlace()
	strip := tone{fg: palette.Dim, bg: palette.HeaderBg}
	hint := "  ⇧←→ swap · @ go "
	if model.goPreviewOK {
		hint = "  ⏎ go · esc back "
	} else if model.goError != "" {
		hint = "  unreadable · esc back "
	} else if mode, _ := model.omni(); mode == omniGo {
		hint = "  no match · esc back "
	}
	lead := []span{{text: " places ", paint: strip}}
	chips := make([][]span, 0, len(model.places)+1)
	active := -1
	entries := model.places
	if model.goPreviewOK && current.adhoc {
		if _, found := model.placeByRoot(current.root); !found {
			entries = insertAfterOwner(append([]place(nil), entries...), current)
		}
	}
	for _, entry := range entries {
		label := " " + entry.label
		if counts := placeTally(entry); counts != "" {
			label += " " + counts
		}
		label += " "
		paint := tone{fg: palette.Muted, bg: palette.HeaderBg}
		if entry.adhoc {
			paint.fg = palette.Dim
		}
		if entry.root == current.root {
			active = len(chips)
			paint = tone{fg: paletteInk(mastheadInk), bg: palette.Accent, bold: true}
			if model.goPreviewOK {
				paint.bg = palette.Warn
			}
		}
		chips = append(chips, []span{{text: label, paint: paint}, {text: " ", paint: strip}})
	}
	budget := width - spansWidth(lead) - ansi.StringWidth(hint)
	first, last := railWindow(chips, active, budget)
	spans := append([]span(nil), lead...)
	if first > 0 {
		spans = append(spans, span{text: "‹ ", paint: strip})
	}
	for position := first; position <= last && position < len(chips); position++ {
		spans = append(spans, chips[position]...)
	}
	if last < len(chips)-1 {
		spans = append(spans, span{text: "› ", paint: strip})
	}
	if rest := width - spansWidth(spans) - ansi.StringWidth(hint); rest > 0 {
		spans = append(spans, span{text: strings.Repeat(" ", rest), paint: strip})
	}
	spans = append(spans, span{text: hint, paint: strip})
	return fillLine(ansi.Truncate(joinSpans(spans), width, ""), width)
}

// railWindow picks the run of chips that fits budget cells and keeps the
// active chip in view, growing right first, then left.
func railWindow(chips [][]span, active, budget int) (first, last int) {
	if len(chips) == 0 {
		return 0, -1
	}
	if active < 0 {
		active = 0
	}
	const markers = 4 // room for the ‹ and › overflow marks
	used := spansWidth(chips[active])
	first, last = active, active
	for grown := true; grown; {
		grown = false
		if last+1 < len(chips) && used+spansWidth(chips[last+1])+markers <= budget {
			last++
			used += spansWidth(chips[last])
			grown = true
		}
		if first > 0 && used+spansWidth(chips[first-1])+markers <= budget {
			first--
			used += spansWidth(chips[first])
			grown = true
		}
	}
	return first, last
}

// indexPlaces derives the rail from the current rows. The opening place is
// fixed on the first build: the fleet launch row's directory, which compose
// already resolved to the project the picker was started in.
func (model *Model) indexPlaces() {
	if !model.placeChosen {
		for index := range model.rows {
			if isFleetNewChatRow(model.rows[index]) {
				model.launchHome.project, model.launchHome.cwd = model.rows[index].Project, model.rows[index].CWD
				break
			}
		}
	}
	built := model.buildPlaces()
	if !model.placeChosen {
		model.openPlace, model.placeChosen, model.placeAuto = built.open, true, true
	}
	if model.placeAuto {
		// Until the first keystroke the place follows the rows: the project
		// the picker opened in, or everywhere while nothing has run there yet
		// (the launch row still starts here). A key ends it (omniKey), so a
		// refresh never narrows the list under a reader.
		model.place = allPlaceRoot
		for _, entry := range built.places {
			if entry.root == built.open && entry.live+entry.resume > 0 {
				model.place = built.open
			}
		}
	}
	known := model.place == allPlaceRoot
	for _, entry := range built.places {
		known = known || entry.root == model.place
	}
	if !known {
		// The committed place lost its last chat to a refresh: it stays a
		// stop, now holding whatever sits under it, rather than the list
		// jumping somewhere the reader did not choose.
		model.adhocPlaces = append(model.adhocPlaces, place{root: model.place})
		built = model.buildPlaces()
	}
	model.places, model.rowDir, model.rowPlace = built.places, built.rowDir, built.rowPlace
}

// buildPlaces derives the rail from the rows the Model holds now.
func (model Model) buildPlaces() placeIndex {
	// Only the interactive picker scopes to a place; plain and TSV list all.
	openRoot := ""
	if model.mergeNewChat {
		openRoot = model.launchHome.cwd
	}
	return buildPlaces(
		model.rows, model.projectDirs, model.deck.home, openRoot, model.launchHome.project, model.adhocPlaces,
		model.visibleInView,
	)
}

// placeState is the Model's place and omnibox state. rowDir and rowPlace align
// with rows; place is the committed place's root and openPlace the one the
// picker opened on; placeAuto holds while the opening choice may still follow
// the rows; goPreview is the place an `@` query previews while it types.
type placeState struct {
	places      []place
	rowDir      []string
	rowPlace    []string
	elsewhere   []int
	place       string
	openPlace   string
	placeChosen bool
	placeAuto   bool
	adhocPlaces []place
	goPreview   place
	goPreviewOK bool
	goError     string // the `@` path could not be looked at
	here        place  // the effective place of the last rebuildOrder pass
	projectDirs map[string]string
	dirExists   func(string) (bool, error)
	launchHome  struct{ project, cwd string }
}

// omniKey takes the keys the rail and the omnibox own before the picker's own
// bindings: esc backs out of a mode before it closes anything, ⇧←→ swaps the
// place, and ⏎ on an `@` query moves there. Any key ends the automatic place
// choice, so the list never narrows under a reader.
func (model Model) omniKey(key string) (Model, bool) {
	model.placeAuto = false
	if model.tab != TabChats {
		return model, false
	}
	mode, _ := model.omni()
	switch {
	case key == keyEsc && mode != omniFind:
		model.updateQuery("")
		return model, true
	case key == "shift+left" || key == "shift+right":
		direction := 1
		if key == "shift+left" {
			direction = -1
		}
		model.swapPlace(direction)
		return model, true
	case key == keyEnter && mode == omniGo:
		return model.commitGo(), true
	}
	return model, false
}

// syncOmni follows a query change: the prompt names the mode, the `@` preview
// is re-resolved, and the list is re-ordered when the place on screen moved.
func (model *Model) syncOmni(before string) {
	switch mode, _ := model.omni(); mode {
	case omniGo:
		model.query.Prompt = "go › "
	default:
		model.query.Prompt = findPrompt
	}
	model.refreshGoPreview()
	if model.effectivePlace().root != before {
		model.rebuildOrder()
	}
}

// omniRows is the list a go query shows instead of a search: the previewed
// place unfiltered. moded is false in find mode.
func (model Model) omniRows() (rows []int, moded bool) {
	if mode, _ := model.omni(); mode == omniGo {
		return model.order, true
	}
	return nil, false
}

// searchSpace is what a typed search reaches: this place's rows, then every
// other place's — finding a chat is how a reader gets to it, wherever it runs.
func (model Model) searchSpace() []int {
	candidates := make([]int, 0, len(model.order)+len(model.elsewhere))
	candidates = append(candidates, model.order...)
	return append(candidates, model.elsewhere...)
}

// outOfPlaceRows lists, in group order, the visible chats the place on screen
// leaves out; a search reaches them after its own matches.
func (model *Model) outOfPlaceRows() []int {
	if model.here.root == allPlaceRoot {
		return nil
	}
	rows := make([]int, 0)
	for _, group := range model.groups {
		for _, index := range group.indices {
			row := &model.rows[index]
			if index >= len(model.rowDir) || model.rowDir[index] == "" || isNewChatActionKind(row.Kind) ||
				!model.visibleInView(
					*row,
				) || model.inPlace(index) || row.ScheduleFired { // a fired row is already pinned
				continue
			}
			rows = append(rows, index)
		}
	}
	return rows
}
