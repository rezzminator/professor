package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// receiptTone classifies the status line's receipt: a refusal or a failure is
// the warn colour, everything that landed is green. The wording is the model's
// (toggleKilled, deactivate); only the colour is decided here.
func receiptTone(receipt string) tone {
	palette := configuredPalette
	if strings.Contains(receipt, "refused") || strings.Contains(receipt, "failed") {
		return tone{fg: palette.Warn, bold: true}
	}
	return tone{fg: palette.LimitGreen, bold: true}
}

// renderQuery is the bar under the header. On Chats it is the search input and
// the count of rows it leaves; the other tabs keep their own one-line status.
func (model Model) renderQuery(width int) string {
	if model.tab == TabStats {
		return model.renderStatsSubtabs(width)
	}
	if model.tab == TabLimits {
		return dimStyle.Render(fillLine(" limits  live usage windows · ↑↓ scroll", width))
	}
	if model.tab == TabCosmos {
		// Precedence is the receipt of the LAST keystroke first: a refused key
		// names itself before anything else, then the reticle's card, then
		// the ledger's own state.
		switch {
		case model.cosmosStatus != "":
			return warnStyle.Render(
				fillLine(" cosmos  "+ansiTruncateRunes(model.cosmosStatus, maxInt(0, width-9)), width),
			)
		case model.cosmosSelected != "":
			return dimStyle.Render(
				fillLine(" cosmos  "+ansiTruncateRunes(model.cosmosSelectionHUD(), maxInt(0, width-9)), width),
			)
		}
		status := "live comms ledger"
		if model.cosmosLoading {
			status = "sampling…"
		}
		return dimStyle.Render(fillLine(" cosmos  "+status+" · newest 24h", width))
	}
	palette := configuredPalette
	input := model.query.View()
	status := tone{fg: palette.Dim}.render(fmt.Sprintf("%d/%d visible", len(model.filtered), len(model.order)))
	// ⌃X takes the status line either way: the receipt when it landed, the
	// reason when it was refused. It acts immediately, so its outcome has to
	// be as immediate — and as visible — as the keystroke. The next keystroke
	// retires it (updateKey), so a stale receipt never outlives its moment.
	if model.killStatus != "" {
		status = receiptTone(model.killStatus).render(model.killStatus)
	} else if strings.TrimSpace(model.query.Value()) != "" {
		status = tone{fg: palette.Accent}.render(fmt.Sprintf("%d/%d match", len(model.filtered), len(model.order)))
	}
	available := maxInt(8, width-lipgloss.Width(status)-2)
	input = ansi.Truncate(input, available, "…")
	line := input + strings.Repeat(
		" ",
		maxInt(1, width-lipgloss.Width(input)-lipgloss.Width(status)),
	) + status
	return fillLine(line, width)
}

// keycap draws one key and what it does: the key as a small solid cap, the
// label beside it. A key that would be refused right now is dimmed whole.
func keycap(key, label string, enabled bool) []span {
	palette := configuredPalette
	capPaint := tone{fg: palette.Header, bg: palette.Selected, bold: true}
	text := tone{fg: palette.Muted}
	if !enabled {
		capPaint = tone{fg: palette.Dim, bg: palette.Selected, dim: true}
		text = tone{fg: palette.Dim, dim: true}
	}
	return []span{
		{text: " " + key + " ", paint: capPaint},
		{text: " " + label + "  ", paint: text},
	}
}

// enterLabel names what Enter does on the selected row: the armed carousel
// action on a chat, and its own verb on a row that is an action.
func (model Model) enterLabel(row compose.Row, hasRow bool) string {
	switch {
	case hasRow && row.Kind == compose.ProfessorUpdate:
		return "upgrade"
	case hasRow && isLaunchFailureNotice(row.Kind):
		return "—"
	case hasRow && isNewChatActionKind(row.Kind):
		return "start"
	}
	return carouselActions[model.actionIndex].Label
}

// chatsFooterKeys lists the Chats footer's two lines for the selected row. The
// armed carousel action names the Enter key, and a key the row would refuse
// (reboot on a resumable chat, say) is shown dimmed rather than hidden.
func (model Model) chatsFooterKeys(compact bool) (first, second []span) {
	row, hasRow := model.selectedRow()
	live := hasRow && row.Kind.IsLiveSeat()
	enter := model.enterLabel(row, hasRow)
	kill := "hide"
	switch {
	case hasRow && row.Killed:
		kill = "unhide"
	case live:
		kill = "kill"
	}
	cache := "1h cache off"
	if model.cache1H {
		cache = "1h cache on"
	}
	if compact {
		first = append(first, keycap("↑↓", "move", true)...)
		first = append(first, keycap("⏎", enter, hasRow)...)
		first = append(first, keycap("esc", "close", true)...)
		second = append(second, keycap("⌃X", kill, hasRow)...)
		second = append(second, keycap("⌃O", "reboot", live)...)
		second = append(second, keycap("⌃E", "1h", true)...)
		second = append(second, keycap("⌃S", "acct", true)...)
		return first, second
	}
	first = append(first, keycap("↑↓", "move", true)...)
	first = append(first, keycap("←→", "action", hasRow)...)
	first = append(first, keycap("⏎", enter, hasRow)...)
	first = append(first, keycap("esc", "close", true)...)
	first = append(first, span{text: "type to fuzzy-find", paint: tone{fg: configuredPalette.Dim}})
	second = append(second, keycap("⌃X", kill, hasRow)...)
	second = append(second, keycap("⌃O", "reboot", live)...)
	second = append(second, keycap("⌃E", cache, true)...)
	second = append(second, keycap("⌃S", "account", true)...)
	return first, second
}

// renderFooter is the two help lines under the body.
func (model Model) renderFooter(width int) string {
	if model.tab == TabStats {
		first := " ↑↓ focus/rows  ←→ focused tab  c CPU sort  m RAM sort"
		second := " esc cancel · live samples every 2s only while Stats is focused"
		return dimStyle.Render(fillLine(first, width)) + "\n" +
			dimStyle.Render(fillLine(second, width))
	}
	if model.tab == TabLimits {
		first := " ↑↓ scroll  pgup/pgdown page  home/end jump"
		second := " tab/shift+tab cycle tabs · esc cancel · live samples every 2s while focused"
		return dimStyle.Render(fillLine(first, width)) + "\n" +
			dimStyle.Render(fillLine(second, width))
	}
	if model.tab == TabCosmos {
		first := " ↑↓ select · enter open · s system · o classic sky · tab/shift+tab cycle tabs · esc cancel"
		second := " [ ] ±5m · { } ±1h · space play · n now · ledger samples every 2s while focused"
		if width < 96 {
			first = " ↑↓ select · enter open · s system · o classic · esc cancel"
			second = " [ ] ±5m · { } ±1h · space play · n now"
		}
		return dimStyle.Render(fillLine(first, width)) + "\n" +
			dimStyle.Render(fillLine(second, width))
	}
	first, second := model.chatsFooterKeys(width < 96)
	lead := span{text: " "}
	return fillLine(joinSpans(append([]span{lead}, first...)), width) + "\n" +
		fillLine(joinSpans(append([]span{lead}, second...)), width)
}
