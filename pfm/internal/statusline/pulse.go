package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// The context strip is two instruments side by side. The gauge (fillBar) is
// how full the context window is: its first cells on, in the share's tone, the
// rest dimmed, as line 3's quota bars are drawn. The pulse is how much the
// context grew in each step of five seconds, one cell a step, newest on the
// right, so the trace moves one cell a step. The professor plugin's agent row
// draws the same gauge and pulse from the agents door's look (agents_look.go),
// so a chat's strip and its agents' compare by eye.
var (
	// pulseGlyphs is the glyph per growth level, none to most.
	pulseGlyphs = [...]string{"·", "•", "●"}
	// pulseSteps is the fewest tokens one step must add to reach each level above the first.
	pulseSteps = [...]int64{1, 5_000}
	// pulseSession is a session id a pulse file may be named by: one path element.
	pulseSession = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

const (
	// pulseDrop is the cell of a step in which the context shrank: a compaction.
	pulseDrop = "↓"
	// pulseDropFrom is the fewest tokens a step must shed to be one: a recount
	// sheds a few, and draws as no growth.
	pulseDropFrom = 1_000
	// pulseStep is the wall time one cell covers, the agent row's five-second reading.
	pulseStep = 5 * time.Second
	// pulseCells is the pulse's width in cells beside a drawn growth figure.
	pulseCells = 6
	// pulseCellsWide is its widest: widened over a blank growth figure's
	// cells and the share's pad, the most cells a strip draws, the readings
	// it keeps.
	pulseCellsWide   = pulseCells + 1 + growthCells + shareCells - 2
	pulseLevelDrop   = -1
	pulseLevelUnborn = -2
	// pulseGrowth is the colour of a step that grew, the growth figure's beside the strip.
	pulseGrowth = cWindow
	// gaugeOn and gaugeOff are a gauge's used and free cells; gaugeCells is the context gauge's width.
	gaugeOn    = "▰"
	gaugeOff   = "▱"
	gaugeCells = 8
	// growthCells is the growth figure's width in cells and shareCells the
	// share's, each right-aligned in them (contextColumn).
	growthCells = 7
	shareCells  = 4
)

// pulseReading is the context's size at one render, At in Unix milliseconds.
type pulseReading struct {
	At     int64 `json:"at"`
	Tokens int64 `json:"tokens"`
}

// pulseLevel is the level of one step's growth in tokens.
func pulseLevel(grown int64) int {
	if grown <= -pulseDropFrom {
		return pulseLevelDrop
	}
	level := 0
	for _, step := range pulseSteps {
		if grown >= step {
			level++
		}
	}
	return level
}

// gaugeLit is how many of a gauge's cells are on: one at least once anything
// is used, all only when the window is full.
func gaugeLit(used, window int64, cells int) int {
	if window <= 0 || used <= 0 {
		return 0
	}
	if used >= window {
		return cells
	}
	return min(cells-1, max(1, int(math.Round(float64(used)/float64(window)*float64(cells)))))
}

// fillBar is a gauge of width cells, the first on of them used, in tone, the
// rest dimmed. ▰▱ (U+25B0/U+25B1) on purpose: block elements ▓░ are WebGL
// custom glyphs in VS Code's terminal and render stale under repaint.
func fillBar(on, width int, tone string) string {
	return tone + strings.Repeat(gaugeOn, on) + dim + strings.Repeat(gaugeOff, width-on) + reset
}

// pulseTrace is the pulse at now from readings, oldest first: the level of
// each of the newest cells steps on a fixed grid of pulseStep, a step with no
// reading before it unborn. grown is the newest step's growth.
func pulseTrace(readings []pulseReading, now time.Time, cells int) (levels []int, grown int64) {
	width := pulseStep.Milliseconds()
	first := now.UnixMilli()/width - int64(cells-1)
	next := 0
	var before *pulseReading
	for next < len(readings) && readings[next].At/width < first {
		before, next = &readings[next], next+1
	}
	levels = make([]int, cells)
	for cell := range levels {
		start := before
		for next < len(readings) && readings[next].At/width <= first+int64(cell) {
			before, next = &readings[next], next+1
		}
		levels[cell] = pulseLevelUnborn
		if start != nil {
			grown = before.Tokens - start.Tokens
			levels[cell] = pulseLevel(grown)
		}
	}
	return levels, grown
}

// pulseStrip draws a pulse: a step that grew in the growth figure's colour, a
// drop in the window colour, a quiet or unborn step muted.
func pulseStrip(levels []int) string {
	var out strings.Builder
	run, style := "", ""
	flush := func() {
		if run != "" {
			out.WriteString(style + run + reset)
		}
		run = ""
	}
	for _, level := range levels {
		next, glyph := cWindow, pulseDrop
		switch {
		case level > 0:
			next, glyph = pulseGrowth, pulseGlyphs[level]
		case level != pulseLevelDrop:
			next, glyph = cMuted, pulseGlyphs[0]
		}
		if next != style {
			flush()
			style = next
		}
		run += glyph
	}
	flush()
	return out.String()
}

// pulseReadings is a session's readings with this render's added, kept in
// {CacheDir}/cc-sl-pulse-{session}: one reading a step, and no step older than
// the strip needs. A render that knows no context size, or reads the size the
// last reading holds, adds none and writes nothing: the trace draws the steps
// between two readings as flat. A session with no usable id, or a file that
// cannot be read, has no trace.
func pulseReadings(runtime Runtime, sessionID string, now time.Time, tokens int64, known bool) []pulseReading {
	if !pulseSession.MatchString(sessionID) {
		return nil
	}
	path := filepath.Join(runtime.CacheDir, "cc-sl-pulse-"+sessionID)
	body, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(runtime.Warn, "statusline: read pulse readings %s: %v\n", path, err)
		return nil
	}
	var readings []pulseReading
	if len(body) > 0 {
		if err := json.Unmarshal(body, &readings); err != nil {
			fmt.Fprintf(runtime.Warn, "statusline: parse pulse readings %s: %v\n", path, err)
			readings = nil
		}
	}
	if !known {
		return readings
	}
	width := pulseStep.Milliseconds()
	reading := pulseReading{At: now.UnixMilli(), Tokens: tokens}
	last := len(readings) - 1
	switch {
	case last >= 0 && readings[last].Tokens == tokens:
		return readings
	case last >= 0 && readings[last].At/width == reading.At/width:
		readings[last] = reading
	default:
		readings = append(readings, reading)
	}
	slices.SortStableFunc(readings, func(a, b pulseReading) int { return int(a.At - b.At) })
	// The newest reading before the strip's first step stays: the first cell grows from it.
	first := reading.At/width - pulseCellsWide + 1
	keep := slices.IndexFunc(readings, func(r pulseReading) bool { return r.At/width >= first })
	readings = readings[max(0, keep-1):]
	next, err := json.Marshal(readings)
	if err != nil {
		fmt.Fprintf(runtime.Warn, "statusline: encode pulse readings %s: %v\n", path, err)
		return readings
	}
	if err := atomicfile.Write(path, next, 0o600); err != nil {
		fmt.Fprintf(runtime.Warn, "statusline: write pulse readings %s: %v\n", path, err)
	}
	return readings
}

// pulseCellsAt is the pulse's width in a block of form whose share reads
// share: its base cells beside a drawn growth figure (figure), else widened
// over the figure's blank cells and the share's pad, so the strip runs to one
// blank before the share and the share stays on its cells either way.
func pulseCellsAt(form ContextForm, figure bool, share string) int {
	if figure {
		return pulseCells
	}
	cells := pulseCells + max(0, shareCells-ansi.StringWidth(share))
	if form.Growth {
		cells += 1 + growthCells
	}
	return cells
}

// growthFigure is the newest step's growth in figures, beside the pulse: it
// names what the trace counts. A step at rest is blank cells, never a figure
// read as a size of 0.
func growthFigure(grown int64) string {
	switch {
	case grown > 0:
		return pulseGrowth + fmt.Sprintf("%*s", growthCells, "+"+formatContextTokens(grown)) + reset
	case grown < 0:
		return cWindow + fmt.Sprintf("%*s", growthCells, "−"+formatContextTokens(-grown)) + reset
	}
	return strings.Repeat(" ", growthCells)
}
