package statusline

import (
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/x/ansi"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Line 2 is the chat's instrument row, in the field order and forms of the
// professor plugin's agent row, so the two read as one instrument:
//
//	🟢__⎽⎼_______⎽___   +1.2K   8%  79.7K/1.0M │ ⏳ 8m12s │ Σ $0.10 · 5.9M in · 32.4K out · 73 tools │ 💾1h✓59m54s 95%
//
// the pulse (pulse.go) with the newest step's growth, the share and the
// tokens over the window; the session's clock; the spend (spend_text.go); the
// prompt cache (cache_window.go). The emoji takes the two cells an agent row's
// lead takes, so the strips of a chat and of its agents stand on the same cells.

// instrumentRank is how much of line 2 a terminal's width leaves: the strip's
// cells, the growth figure, the tokens over the window, the spend's form.
type instrumentRank struct {
	cells  int
	growth bool
	window bool
	spend  spendForm
}

// instrumentRanks is the order line 2 gives its parts up in, fullest first,
// the agent row's own order for the parts both draw. The cache window is
// never given up: a recorded launch reports its cache and its transcript's
// readability at every width.
var instrumentRanks = [...]instrumentRank{
	{cells: pulseCells, growth: true, window: true, spend: spendWords},
	{cells: pulseCells, growth: true, window: true, spend: spendSlash},
	{cells: pulseCells, window: true, spend: spendSlash},
	{cells: pulseCells, window: true, spend: spendUSD},
	{cells: pulseCellsNarrow, window: true, spend: spendUSD},
	{cells: pulseCellsNarrow, spend: spendUSD},
}

// instrumentLine is line 2 at the fullest rank the terminal's width holds,
// every rank when the width is unknown, the last when none fits.
func instrumentLine(runtime Runtime, data input, project string, now time.Time) (contextGauge, string) {
	var mainScan *spendScan
	if runtime.Engine != pfmengine.Codex {
		mainScan = newSpendScan(runtime.Prices)
	}
	gauge := transcriptGauge(runtime, data, project, mainScan)
	readings := pulseReadings(runtime, data.SessionID, now, gauge.tokens, gauge.known)
	// ⏳ (U+23F3, East-Asian-Width W) over ⏱ (U+23F1, width N): every cell
	// model — tmux, xterm.js, the harness — sizes the hourglass at 2 cells,
	// while the stopwatch is 1 cell wide on paper and 2 cells wide in ink.
	clock := sep + cElapsed + "⏳ " + spanText(time.Duration(data.Cost.TotalDurationMS)*time.Millisecond) + reset
	var spend *spendText
	if runtime.Engine != pfmengine.Codex {
		books := readLedgerBooks(runtime, data.SessionID)
		text := sessionSpendSegment(data, runtime.Prices, books, gauge.scanned, runtime.scans)
		spend = &text
	}
	cache := cacheColumn(runtime, data, gauge, now)
	line := ""
	for _, rank := range instrumentRanks {
		line = contextColumn(runtime.Engine, data, gauge, readings, now, rank) + clock
		if spend != nil {
			line += sep + spend.text(rank.spend)
		}
		line += cache
		if runtime.Columns <= 0 || ansi.StringWidth(line) <= runtime.Columns-footerPadding {
			break
		}
	}
	return gauge, line
}

// contextColumn is the pulse and the context's figures: the strip, the newest
// step's growth, the share of the window, the tokens over the window, then a
// window pfm could not resolve (ctx unknown) and, on Codex, the human prompts.
func contextColumn(
	engine pfmengine.ID,
	data input,
	gauge contextGauge,
	readings []pulseReading,
	now time.Time,
	rank instrumentRank,
) string {
	label, tone, lit := "?%", cMuted, 0
	if (gauge.window > 0 && gauge.known) || gauge.percentKnown ||
		(!gauge.known && data.ContextWindow.UsedPercentage > 0) {
		label, tone = strconv.Itoa(gauge.percent)+"%", shareTone(gauge.percent)
		lit = pulseLit(int64(gauge.percent), 100, rank.cells)
		if gauge.known && gauge.window > 0 {
			lit = pulseLit(gauge.tokens, gauge.window, rank.cells)
		}
	}
	strip, grown := pulseTrace(readings, now, lit, rank.cells)
	out := urgencyEmoji(gauge.percent) + pulseStrip(strip, tone)
	if rank.growth {
		// The newest cell's step in figures, beside it: it names what the trace counts.
		figure, color := "+0", cMuted
		switch {
		case grown > 0:
			figure, color = "+"+formatContextTokens(grown), cWindow
		case grown < 0:
			figure, color = "−"+formatContextTokens(-grown), cWindow
		}
		out += " " + color + fmt.Sprintf("%7s", figure) + reset
	}
	out += " " + tone + fmt.Sprintf("%4s", gauge.marker+label) + reset
	if rank.window {
		count, window := "?", "?"
		if gauge.known {
			count = formatContextTokens(gauge.tokens)
		}
		if gauge.window > 0 {
			window = formatContextTokens(gauge.window)
		}
		out += " " + cTokens + fmt.Sprintf("%6s", count) + reset + cMuted + "/" + reset + cWindow + window + reset
	}
	if gauge.capacityState != "" {
		out += " " + cWarn + "ctx " + gauge.capacityState + reset
	}
	if engine == pfmengine.Codex && gauge.humanPrompts > 0 {
		out += " " + dim + "✎" + strconv.Itoa(gauge.humanPrompts) + reset
	}
	return out
}

// cacheColumn is the prompt cache's segment with its leading separator, ""
// when there is none. Codex reports no cache split.
func cacheColumn(runtime Runtime, data input, gauge contextGauge, now time.Time) string {
	if openAIModel(gauge.model) || openAIModel(data.Model.ID) ||
		(data.Model.ID == "" && openAIModel(data.Model.DisplayName)) {
		if gauge.transcriptErr {
			return sep + cBad + "💾!" + reset
		}
		return sep + unmeasuredCacheText(gauge.cacheHit)
	}
	hit := -1
	if runtime.Engine != pfmengine.Codex {
		usage := data.ContextWindow.CurrentUsage
		hit = cacheHitPercent(usage.CacheReadInputTokens, usage.CacheCreationInputTokens, usage.InputTokens)
	}
	return cacheWindowSegment(runtime, now, data.TranscriptPath, hit, data.PromptCache, data.SessionID)
}

// shareWarnFrom and shareBadFrom are the percents of the context window
// shareTone turns at; the agents door hands them to the professor plugin's
// row (agentsLook).
const (
	shareWarnFrom = 50
	shareBadFrom  = 80
)

// shareTone is the colour of a share of the context window: good below half,
// a warning from half, bad from four fifths.
func shareTone(percent int) string {
	switch {
	case percent >= shareBadFrom:
		return cBad
	case percent >= shareWarnFrom:
		return cWarn
	}
	return cGood
}

// spanText is a length of time in a width that holds still while it counts:
// 8s, 3m08s, 1h04m. The session's clock, the cache window's countdown and a
// stale reading's age all read in it, as the agent row's times do.
func spanText(span time.Duration) string {
	seconds := max(0, int64(span/time.Second))
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh%02dm", seconds/3600, seconds%3600/60)
}
