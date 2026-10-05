package ui

import (
	"fmt"
	"strings"
)

// projectTally is what one project contributes to the visible list: the header
// shows it, so a collapsed glance at the headers is a census of the fleet.
type projectTally struct {
	live, resumable int
}

// projectTallies counts the filtered rows per project, memoised per message
// that can change them. The map is shared; callers only read it.
func (model Model) projectTallies() map[string]projectTally {
	agg := model.deck.agg
	if agg == nil {
		return model.countProjects()
	}
	if agg.talliesRev != model.deck.rev || agg.tallies == nil {
		agg.tallies = model.countProjects()
		agg.talliesRev = model.deck.rev
	}
	return agg.tallies
}

func (model Model) countProjects() map[string]projectTally {
	tallies := make(map[string]projectTally)
	for _, rowIndex := range model.filtered {
		row := model.rows[rowIndex]
		project := cleanField(row.Project)
		if project == "" {
			project = "?"
		}
		tally := tallies[project]
		switch {
		case row.Kind.IsAddressable():
			tally.live++
		case hasRecency(row):
			tally.resumable++
		}
		tallies[project] = tally
	}
	return tallies
}

// projectHeader draws a project's banner: a hanging corner, its name in the
// group colour, a rule, and its census at the right.
func (model Model) projectHeader(project string, ordinal, width int, tally projectTally) string {
	palette := configuredPalette
	hex := palette.GroupA
	if ordinal%2 == 1 {
		hex = palette.GroupB
	}
	census := ""
	if tally.live > 0 {
		census += fmt.Sprintf("● %d", tally.live)
	}
	if tally.resumable > 0 {
		if census != "" {
			census += "  "
		}
		census += fmt.Sprintf("↻ %d", tally.resumable)
	}
	censusWidth := len([]rune(census))
	if census != "" {
		censusWidth += 2
	}
	title := clipRunesEllipsis(project, maxInt(1, width-5-censusWidth))
	rule := strings.Repeat("─", maxInt(0, width-len([]rune(title))-4-censusWidth))
	spans := []span{{text: "╭─ ", paint: tone{fg: hex, bold: true}}}
	spans = append(spans, highlightSpans(title, model.query.Value(),
		tone{fg: hex, bold: true}, tone{fg: palette.Warn, bold: true})...)
	spans = append(spans,
		span{text: " ", paint: tone{fg: hex}},
		span{text: rule, paint: tone{fg: blendHex(palette.Border, hex, 0.35)}},
	)
	if census != "" {
		spans = append(spans, span{text: " " + census + " ", paint: tone{fg: palette.Muted}})
	}
	line := joinSpans(spans)
	return fillLine(line, width)
}

// emptyListLine is the list's one-line answer when nothing is shown, naming
// WHY: a filter that matches nothing and a fleet that has nothing read
// differently.
func (model Model) emptyListLine(width int) string {
	palette := configuredPalette
	query := strings.TrimSpace(model.query.Value())
	text := "  no chats here yet"
	if query != "" {
		text = fmt.Sprintf("  no matches for “%s”  ·  ⌃U clears the filter", query)
	}
	return fillLine(tone{fg: palette.Dim, italic: true}.render(text), width)
}

// renderListPanel draws the fleet list: project banners, name-group headers and
// deck rows, then the tempo axis under them.
func (model Model) renderListPanel(width, height int) string {
	title := fmt.Sprintf(" fleet %d ", len(model.filtered))
	innerWidth := maxInt(1, width-1)
	innerHeight := maxInt(1, height-2)
	tempo := innerHeight >= 7
	rowsHeight := innerHeight
	if tempo {
		rowsHeight--
	}
	lines := make([]string, 0, innerHeight)
	if len(model.filtered) == 0 {
		lines = append(lines, model.emptyListLine(innerWidth))
	} else {
		tallies := model.projectTallies()
		start := maxInt(0, model.cursor-rowsHeight/2)
		previousProject := ""
		previousGroupKey := ""
		for position := start; position < len(model.filtered) &&
			len(lines) < rowsHeight; position++ {
			row := model.rows[model.filtered[position]]
			project := cleanField(row.Project)
			if project == "" {
				project = "?"
			}
			nameGroup, grouped := model.nameGroups[model.filtered[position]]
			groupKey, _, _ := workbenchNameGroup(row)
			// A name group folded across projects (rebuildOrder already placed
			// every member contiguously, at the first project's slot) reads as
			// ONE panel: crossing into a member's own project here must not
			// reopen a second project banner or repeat the group's label.
			continuesGroup := grouped && groupKey == previousGroupKey
			if project != previousProject && !continuesGroup {
				// The selected row always wins the final viewport line.
				if len(lines)+1 < rowsHeight || position != model.cursor {
					lines = append(lines, model.projectHeader(
						project, model.projectOrdinal(project), innerWidth, tallies[project],
					))
				}
				previousGroupKey = ""
			}
			previousProject = project
			if len(lines) >= rowsHeight {
				break
			}
			if grouped && groupKey != previousGroupKey {
				lines = append(lines, labelStyle.Render(fillLine(
					"│  "+nameGroup.name+fmt.Sprintf(" (%d)", nameGroup.count),
					innerWidth,
				)))
			}
			previousGroupKey = ""
			if grouped {
				previousGroupKey = groupKey
			}
			if len(lines) >= rowsHeight {
				break
			}
			lines = append(
				lines,
				model.renderGroupedRow(row, position == model.cursor, innerWidth, grouped),
			)
		}
	}
	for len(lines) < rowsHeight {
		lines = append(lines, strings.Repeat(" ", innerWidth))
	}
	spec := openPanelSpec{title: title, rail: true}
	if !tempo {
		return openPanel(spec, lines, width)
	}
	lines = append(lines, model.tempoStars(innerWidth))
	spec.bottom = model.tempoRuler(width)
	return openPanel(spec, lines, width)
}
