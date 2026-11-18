package statusline

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Line 2 is the chat's instrument row, in the field order and forms of the
// professor plugin's agent row, so the two read as one instrument:
//
//	🟢 ▰▱▱▱▱▱▱▱ ··•··●   +1.2K   8%  79.7K/1.0M │ ⏳ 8m12s │ Σ $0.10 · 5.9M in · 32.4K out · 73 tools │ 💾1h✓59m54s 95%
//
// the context gauge and the pulse (pulse.go) with the newest step's growth,
// the share and the tokens over the window; the session's clock; the spend
// (spend_text.go); the prompt cache (cache_window.go). The emoji takes the two
// cells an agent row's lead takes and a blank follows it, as one leads the
// agent row's gauge, so the gauges of a chat and of its agents stand on the
// same cells.

// ContextForm is one form of the context block line 2 and the professor
// plugin's agent rows both open with: the gauge and the share always, Pulse
// the pulse beside the gauge, Growth the newest step's figure, Window the
// tokens over the window. From is the fewest terminal columns, the hook's
// COLUMNS, the form is drawn at.
type ContextForm struct {
	From   int  `json:"from"`
	Pulse  bool `json:"pulse"`
	Growth bool `json:"growth"`
	Window bool `json:"window"`
}

// contextForms is the context block's one width table, fullest first: the
// terminal's width alone picks the form, so line 2 and the agent rows under it
// (agentsLook hands them the table) draw the same block on the same cells.
// The thresholds hold an agent row list beside its block at each width; the
// block gives its parts up in the agent row's order, the growth figure, the
// pulse, the tokens over the window.
var contextForms = [...]ContextForm{
	{From: 120, Pulse: true, Growth: true, Window: true},
	{From: 100, Pulse: true, Window: true},
	{From: 90, Window: true},
	{From: 0},
}

// contextFormAt is the context block's form at a terminal of columns, the
// fullest where the width is unknown.
func contextFormAt(columns int) ContextForm {
	for _, form := range contextForms {
		if columns <= 0 || columns >= form.From {
			return form
		}
	}
	return contextForms[len(contextForms)-1]
}

// sepTight is sep without the blanks around its bar: what a narrow terminal
// takes before it gives up a fact.
const sepTight = dim + "│" + reset

// instrumentRank is how much of line 2 after the context block a terminal's
// width leaves: the spend's form and whether it is drawn, the clock, and the
// separator between the parts.
type instrumentRank struct {
	spend      spendForm
	spendShown bool
	clock      bool
	joint      string
}

// instrumentRanks is the order line 2 gives its parts up in, fullest first: a
// shorter spelling (the spend in slashes), then the blanks around each
// separator, then the facts least missed first, the spend's counts, the
// clock, the spend. The context block takes its form from contextForms, and
// the cache window is never given up: a recorded launch reports its cache and
// its transcript's readability at every width.
var instrumentRanks = [...]instrumentRank{
	{spend: spendWords, spendShown: true, clock: true, joint: sep},
	{spend: spendSlash, spendShown: true, clock: true, joint: sep},
	{spend: spendSlash, spendShown: true, clock: true, joint: sepTight},
	{spend: spendUSD, spendShown: true, clock: true, joint: sepTight},
	{spend: spendUSD, spendShown: true, joint: sepTight},
	{joint: sepTight},
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
	clock := cElapsed + "⏳ " + spanText(time.Duration(data.Cost.TotalDurationMS)*time.Millisecond) + reset
	var spend *spendText
	if runtime.Engine != pfmengine.Codex {
		books := readLedgerBooks(runtime, data.SessionID)
		text := sessionSpendSegment(data, runtime.Prices, books, gauge.scanned, runtime.scans)
		spend = &text
	}
	block := contextColumn(runtime.Engine, data, gauge, readings, now, contextFormAt(runtime.Columns))
	cache := cacheColumn(runtime, data, gauge, now)
	line := ""
	for _, rank := range instrumentRanks {
		line = block
		if rank.clock {
			line += rank.joint + clock
		}
		if spend != nil && rank.spendShown {
			line += rank.joint + spend.text(rank.spend)
		}
		if body, joined := strings.CutPrefix(cache, sep); joined {
			line += rank.joint + body
		} else {
			line += cache
		}
		if runtime.Columns <= 0 || ansi.StringWidth(line) <= runtime.Columns-footerPadding {
			break
		}
	}
	return gauge, line
}

// contextColumn is the strip and the context's figures: the gauge, the pulse,
// the newest step's growth, the share of the window, the tokens over the window, then a
// window pfm could not resolve (ctx unknown) and, on Codex, the human prompts.
func contextColumn(
	engine pfmengine.ID,
	data input,
	gauge contextGauge,
	readings []pulseReading,
	now time.Time,
	form ContextForm,
) string {
	label, tone, on := "?%", cMuted, 0
	if (gauge.window > 0 && gauge.known) || gauge.percentKnown ||
		(!gauge.known && data.ContextWindow.UsedPercentage > 0) {
		label, tone = strconv.Itoa(gauge.percent)+"%", shareTone(gauge.percent)
		on = gaugeLit(int64(gauge.percent), 100, gaugeCells)
		if gauge.known && gauge.window > 0 {
			on = gaugeLit(gauge.tokens, gauge.window, gaugeCells)
		}
	}
	trace, grown := pulseTrace(readings, now, pulseCellsWide)
	share := gauge.marker + label
	// Without a growth figure the pulse widens over its blank cells and the share's pad.
	figure := form.Growth && grown != 0
	widen := form.Pulse && !figure
	out := urgencyEmoji(gauge.percent) + " " + fillBar(on, gaugeCells, tone)
	if form.Pulse {
		out += " " + pulseStrip(trace[len(trace)-pulseCellsAt(form, figure, share):])
	}
	if form.Growth && !widen {
		out += " " + growthFigure(grown)
	}
	if !widen {
		share = fmt.Sprintf("%*s", shareCells, share)
	}
	out += " " + tone + share + reset
	if form.Window {
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
