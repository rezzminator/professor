package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/modelglyph"
)

// The dossier is the picker's preview pane: a chat is chosen by remembering
// what it was about, and a name alone rarely says. Beside the list on a wide
// terminal it shows the selected chat's last prompt, where it lives, how warm
// it is, and the actions the carousel will run — with the ones that would be
// refused dimmed, so a key is never a surprise.
const (
	dossierWidth = 40
	// dossierLead is the blank gutter between the list and the dossier.
	dossierLead      = 2
	dossierMinWidth  = 112
	dossierLabelW    = 9
	dossierGaugeW    = 10
	dossierPromptMax = 5
	dossierPromptMin = 2
)

// showDossier reports whether the terminal is wide enough for the preview pane.
func (model Model) showDossier() bool {
	return maxInt(40, model.width) >= dossierMinWidth
}

// gaugeBar draws frac (0..1) as a flat bar of cells geometric cells.
func gaugeBar(frac float64, cells int, lit, unlit tone) []span {
	filled := min(cells, max(0, int(math.Round(frac*float64(cells)))))
	return []span{
		{text: strings.Repeat("▰", filled), paint: lit},
		{text: strings.Repeat("▱", cells-filled), paint: unlit},
	}
}

// dossierStatus names a row's state for the chip at the top of the pane.
func dossierStatus(row compose.Row) string {
	switch {
	case row.Killed:
		return "HIDDEN"
	case row.Kind == compose.LiveSplit:
		return "SPLIT"
	case row.Kind.IsLiveSeat():
		return "LIVE"
	case row.Kind == compose.Agent:
		return "AGENT"
	case row.Kind == compose.Booting:
		return "BOOTING"
	case isNewChatActionKind(row.Kind):
		return "NEW"
	case row.Kind == compose.ProfessorUpdate, row.Kind == compose.ProfessorUpdateFailed:
		return "UPDATE"
	case row.Kind == compose.WorkbenchInvalid:
		return "WORKBENCH"
	default:
		return "RESUMABLE"
	}
}

// fleetPeaks are the largest prompt count and transcript size on screen: the
// ceiling the dossier's gauges are drawn against.
func (model Model) fleetPeaks() (prompts, size int64) {
	for index := range model.rows {
		row := &model.rows[index]
		if !hasRecency(*row) {
			continue
		}
		prompts = max(prompts, row.PromptCount)
		size = max(size, row.Size)
	}
	return prompts, size
}

// logFraction places value on a log scale against peak, so one huge transcript
// does not flatten every other gauge to nothing.
func logFraction(value, peak int64) float64 {
	if value <= 0 || peak <= 0 {
		return 0
	}
	return math.Log1p(float64(value)) / math.Log1p(float64(peak))
}

// clockLabel is the absolute time of an activity, in the reader's own zone.
func clockLabel(activityNS, nowNS int64) string {
	if activityNS <= 0 {
		return ""
	}
	then := time.Unix(0, activityNS)
	now := time.Unix(0, nowNS)
	// Days are counted between calendar midnights, not as elapsed hours: 23:00
	// two evenings ago is not "yesterday" because fewer than 48 hours passed.
	// Rounding absorbs the 23- and 25-hour days a DST change makes.
	midnight := func(at time.Time) time.Time {
		return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
	}
	switch dayGap := int(math.Round(midnight(now).Sub(midnight(then)).Hours() / 24)); {
	case dayGap == 0:
		return "today " + then.Format("15:04")
	case dayGap == 1:
		return "yesterday " + then.Format("15:04")
	case dayGap > 1 && dayGap < 7:
		return then.Format("Mon 15:04")
	default:
		return then.Format("Jan 2")
	}
}

// homeShort writes a path with the reader's home as ~.
func homeShort(path, home string) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+"/")) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// tailCells keeps the END of a long path — the directory that matters — behind
// a leading ellipsis.
func tailCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) <= width {
		return text
	}
	return "…" + ansi.TruncateLeft(text, ansi.StringWidth(text)-(width-1), "")
}

// dossierLine is one painted line of the pane, before it is fitted to width.
type dossierLine []span

func (model Model) dossierActionAvailable(index int, row compose.Row) bool {
	switch index {
	case 1:
		return row.Kind.IsLiveSeat()
	case 2:
		return row.ID != "" && !row.NameKilled && row.Kind != compose.Booting &&
			row.Kind != compose.LiveSplit && !isNoticeKind(row.Kind)
	case 3:
		return row.Kind.IsLiveSeat() && row.Kind != compose.LiveSplit && row.Socket != ""
	default:
		return !isLaunchFailureNotice(row.Kind)
	}
}

// dossierPurpose says what Enter does on a row that is an action, not a chat,
// so the pane never quotes a prompt or lists chat actions for a banner. A chat
// returns "".
func dossierPurpose(row compose.Row) string {
	switch {
	case row.Kind == compose.ProfessorUpdate:
		return "Enter starts the guided upgrade."
	case row.Kind == compose.ProfessorUpdateFailed:
		return "The last update check failed. This row is a notice, not a chat."
	case row.Kind == compose.WorkbenchInvalid:
		return "This workbench cannot launch: " + row.Name
	case isNewChatActionKind(row.Kind):
		short := "chat"
		if descriptor, err := pfmengine.Lookup(compose.EngineForKind(row.Kind)); err == nil {
			short = descriptor.Short + " chat"
		}
		if row.Workbench != "" {
			return "Enter starts a new " + short + " in " + row.CWD + " on its workbench prompt."
		}
		return "Enter starts a new " + short + " in your current directory."
	}
	return ""
}

func isNoticeKind(kind compose.Kind) bool {
	return kind == compose.ProfessorUpdate || isLaunchFailureNotice(kind)
}

// dossierActions lists the carousel as a menu: the armed action in its box,
// the ones this row would refuse dimmed.
func (model Model) dossierActions(row compose.Row, inner int) []dossierLine {
	palette := configuredPalette
	hints := []string{"⏎", "⌃O", "⌃X", ""}
	lines := make([]dossierLine, 0, len(carouselActions))
	for index, action := range carouselActions {
		label := action.Label
		if index == 2 && row.Killed {
			label = "unhide"
		}
		armed := index == model.actionIndex
		ok := model.dossierActionAvailable(index, row)
		paint := tone{fg: palette.Muted}
		switch {
		case !ok:
			paint = tone{fg: palette.Dim, dim: true}
		case armed:
			paint = tone{fg: palette.Accent, bold: true}
		}
		open, closing := "  ", "  "
		if armed {
			open, closing = "◖ ", " ◗"
		}
		body := action.Glyph + " " + label
		hint := hints[index]
		gap := max(1, inner-ansi.StringWidth(open+body+closing)-ansi.StringWidth(hint))
		lines = append(lines, dossierLine{
			{text: open + body, paint: paint},
			{text: strings.Repeat(" ", gap)},
			{text: hint, paint: tone{fg: palette.Dim}},
			{text: closing, paint: paint},
		})
	}
	return lines
}

// dossierLines composes the pane for the selected row.
func (model Model) dossierLines(row compose.Row, inner, rows int) []dossierLine {
	if model.mergeNewChat && isNewChatActionKind(row.Kind) {
		row, _ = model.newChatActionRow(row)
	}
	palette := configuredPalette
	engineHex := engineHexOf(row)
	age := rowAgeNS(row, model.nowNS)
	heat := heatOf(age)
	chipPaint := tone{fg: "#0b1020", bg: engineHex}
	if !row.Kind.IsAddressable() {
		chipPaint = tone{fg: palette.Header, bg: palette.Selected}
	}
	head := dossierLine{{text: chipOf(dossierStatus(row), chipPaint)}}
	if descriptor, err := pfmengine.Lookup(compose.EngineForKind(row.Kind)); err == nil {
		head = append(head, span{text: "  " + descriptor.Short, paint: tone{fg: engineHex, bold: true}})
	}
	if medals := rowAccountMedals(row); medals != "" {
		head = append(head, span{text: "  " + medals})
	}

	name := cleanField(row.Name)
	if name == "" {
		name = unnamedChat
	}
	nameLines := strings.Split(ansi.Wrap(name, inner, ""), "\n")
	titled := make([]dossierLine, 0, 2)
	for index, text := range nameLines {
		if index == 2 {
			titled[1] = dossierLine{
				{text: padRightCells(nameLines[1]+" "+text, inner), paint: tone{fg: palette.Header, bold: true}},
			}
			break
		}
		titled = append(
			titled,
			dossierLine{{text: padRightCells(text, inner), paint: tone{fg: palette.Header, bold: true}}},
		)
	}
	rule := dossierLine{{text: strings.Repeat("─", inner), paint: tone{fg: palette.Border}}}

	details := model.dossierDetails(row, inner, heat)
	actions := model.dossierActions(row, inner)
	purpose := dossierPurpose(row)
	if purpose != "" {
		details = nil
		actions = []dossierLine{model.dossierEnterLine(row, inner)}
	}

	build := func(promptLines, detailCount int, compact bool) []dossierLine {
		lines := []dossierLine{head}
		lines = append(lines, titled...)
		lines = append(lines, rule)
		if purpose != "" {
			lines = append(lines, dossierPurposeLines(purpose, inner)...)
		} else {
			lines = append(lines, model.dossierPrompt(row, inner, promptLines)...)
		}
		lines = append(lines, dossierLine{})
		lines = append(lines, details[:min(detailCount, len(details))]...)
		lines = append(lines, dossierLine{})
		if compact {
			lines = append(lines, actions[min(model.actionIndex, len(actions)-1)])
		} else {
			lines = append(lines, actions...)
		}
		return lines
	}
	promptLines, detailCount, compact := dossierPromptMax, len(details), false
	lines := build(promptLines, detailCount, compact)
	for len(lines) > rows && promptLines > dossierPromptMin {
		promptLines--
		lines = build(promptLines, detailCount, compact)
	}
	for len(lines) > rows && detailCount > 2 {
		detailCount--
		lines = build(promptLines, detailCount, compact)
	}
	if len(lines) > rows && !compact {
		compact = true
		lines = build(promptLines, detailCount, compact)
	}
	if len(lines) > rows {
		lines = lines[:max(0, rows)]
	}
	return lines
}

// dossierPurposeLines wraps an action row's purpose to the pane.
func dossierPurposeLines(purpose string, inner int) []dossierLine {
	palette := configuredPalette
	wrapped := strings.Split(ansi.Wrap(purpose, inner, ""), "\n")
	lines := make([]dossierLine, 0, len(wrapped))
	for _, part := range wrapped {
		lines = append(lines, dossierLine{{text: padRightCells(part, inner), paint: tone{fg: palette.Muted}}})
	}
	return lines
}

// dossierEnterLine is the one key an action row offers: Enter and its verb,
// boxed like the armed action of a chat.
func (model Model) dossierEnterLine(row compose.Row, inner int) dossierLine {
	palette := configuredPalette
	label := model.enterLabel(row, true)
	body := "◖ ⏎ " + label + " ◗"
	paint := tone{fg: palette.Accent, bold: true}
	if isLaunchFailureNotice(row.Kind) {
		paint = tone{fg: palette.Dim, dim: true}
	}
	return dossierLine{{text: padRightCells(body, inner), paint: paint}}
}

// rowAccountMedals is the account medal strip of a row, for the pane's head.
func rowAccountMedals(row compose.Row) string {
	return strings.Join(rowMedals(row), " ")
}

// dossierPrompt quotes the chat's last prompt behind an accent rail, wrapped to
// the pane and cut with an ellipsis; a chat with none says so plainly.
func (model Model) dossierPrompt(row compose.Row, inner, maxLines int) []dossierLine {
	palette := configuredPalette
	rail := span{text: "┃ ", paint: tone{fg: palette.Accent}}
	text := strings.Join(strings.Fields(cleanField(row.LastPrompt)), " ")
	if text == "" {
		text = "no prompt recorded for this chat"
		return []dossierLine{{rail, {text: padRightCells(text, inner-2), paint: tone{fg: palette.Dim, italic: true}}}}
	}
	wrapped := strings.Split(ansi.Wrap(text, inner-2, ""), "\n")
	if len(wrapped) > maxLines {
		wrapped = wrapped[:maxLines]
		wrapped[maxLines-1] = padRightCells(wrapped[maxLines-1]+"…", inner-2)
	}
	lines := make([]dossierLine, 0, len(wrapped))
	for _, part := range wrapped {
		lines = append(
			lines,
			dossierLine{rail, {text: padRightCells(part, inner-2), paint: tone{fg: palette.Muted, italic: true}}},
		)
	}
	return lines
}

// dossierDetails is the label/value block, richest line last so a short pane
// drops it first.
func (model Model) dossierDetails(row compose.Row, inner int, heat float64) []dossierLine {
	palette := configuredPalette
	label := func(text string) span {
		return span{text: padRightCells(text, dossierLabelW), paint: tone{fg: palette.Dim}}
	}
	valueWidth := inner - dossierLabelW
	value := func(text string) span {
		return span{text: padRightCells(text, valueWidth), paint: tone{fg: palette.Header}}
	}
	project := cleanField(row.Project)
	if project == "" {
		project = "?"
	}
	details := []dossierLine{{label("project"), value(project)}}
	if row.Model != "" {
		shown := modelglyph.ListSymbol(row.Model) + " " + shortModel(row.Model)
		if row.Effort != "" {
			shown += "  " + modelglyph.RowEffort(row.Effort) + " " + row.Effort
		}
		details = append(details, dossierLine{label("model"), value(shown)})
	}
	if workActive(row) {
		details = append(details, append(dossierLine{label("working")},
			append(workGauge(row, model.nowNS, engineHexOf(row), palette.HeatCold, ""),
				span{text: "  " + workSummary(row), paint: tone{fg: palette.Header}})...))
	}
	if recent := hasRecency(row); recent {
		hot := engineHexOf(row)
		ruler := heatRuler(heat, hot, palette.HeatCold, palette.Border, "")
		clock := clockLabel(row.ActivityNS, model.nowNS)
		since := formatAge(row, model.nowNS) + " ago"
		details = append(details, dossierLine{
			label("active"),
			{text: ruler},
			{text: padRightCells("  "+since, valueWidth-heatRulerCells), paint: tone{fg: palette.Header}},
		})
		if clock != "" {
			details = append(details, dossierLine{label(""), value(clock)})
		}
	}
	if cwd := cleanField(row.CWD); cwd != "" {
		details = append(
			details,
			dossierLine{label("where"), value(tailCells(homeShort(cwd, model.deck.home), valueWidth))},
		)
	}
	if hasRecency(row) {
		peakPrompts, peakSize := model.fleetPeaks()
		gauge := func(frac float64, text string) dossierLine {
			cells := gaugeBar(frac, dossierGaugeW, tone{fg: engineHexOf(row)}, tone{fg: palette.Border})
			return append(dossierLine{}, append(cells, span{text: "  " + text, paint: tone{fg: palette.Header}})...)
		}
		prompts := gauge(logFraction(row.PromptCount, peakPrompts), fmt.Sprintf("%d prompts", row.PromptCount))
		heft := gauge(logFraction(row.Size, peakSize), sizeBadge(row))
		details = append(details,
			append(dossierLine{label("prompts")}, prompts...),
			append(dossierLine{label("heft")}, heft...),
		)
	}
	if row.Socket != "" {
		where := row.Socket
		if row.PaneID != "" {
			where += " " + row.PaneID
		}
		details = append(details, dossierLine{label("tmux"), value(where)})
	}
	if row.ID != "" {
		details = append(details, dossierLine{label("session"), value(tailCells(row.ID, valueWidth))})
	}
	return details
}

// renderDossier draws the preview pane as a framed panel of exactly height
// lines and dossierWidth cells.
func (model Model) renderDossier(height int) string {
	innerWidth := maxInt(1, dossierWidth-dossierLead-1)
	innerHeight := maxInt(1, height-2)
	var lines []string
	if row, ok := model.selectedRow(); ok {
		for _, line := range model.dossierLines(row, innerWidth-2, innerHeight) {
			lines = append(lines, " "+joinSpans(line))
		}
	} else {
		lines = append(lines, dimStyle.Render("  nothing selected"))
	}
	for len(lines) < innerHeight {
		lines = append(lines, strings.Repeat(" ", innerWidth))
	}
	return openPanel(openPanelSpec{title: " dossier ", lead: dossierLead}, lines, dossierWidth)
}
