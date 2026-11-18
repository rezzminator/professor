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

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/modelglyph"
)

// The pulse is the context gauge as one strip of cells that reads twice: how
// many cells are lit, from the left, is how full the context window is; the
// height of each cell is how much the context grew in one step of five
// seconds, newest on the right, so the trace moves one cell a step. The
// professor plugin's agent row draws the same strip on the same scale (its
// src/pulse.ts), so a chat's trace and its agents' compare by eye.
var (
	// pulseGlyphs is the glyph per growth level, none to most: the sparkline ladder.
	pulseGlyphs = modelglyph.SparkLevels
	// pulseSteps is the fewest tokens one step must add to reach each level above the first.
	pulseSteps = [...]int64{1, 1_000, 5_000, 20_000, 50_000}
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
	// pulseCells is the strip's width, pulseCellsNarrow its width once the line must give.
	pulseCells       = 15
	pulseCellsNarrow = 8
	pulseLevelDrop   = -1
	pulseLevelUnborn = -2
	// reverse makes a lit cell: the tone as the cell's ground, the glyph cut out of it.
	reverse = "\x1b[7m"
)

// pulseReading is the context's size at one render, At in Unix milliseconds.
type pulseReading struct {
	At     int64 `json:"at"`
	Tokens int64 `json:"tokens"`
}

// pulseCell is one cell of the strip: level 0 to 5 by pulseSteps,
// pulseLevelDrop, or pulseLevelUnborn before the first reading.
type pulseCell struct {
	level int
	lit   bool
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

// pulseLit is how many of cells are lit: one at least once anything is used,
// all only when the window is full.
func pulseLit(used, window int64, cells int) int {
	if window <= 0 || used <= 0 {
		return 0
	}
	if used >= window {
		return cells
	}
	return min(cells-1, max(1, int(math.Round(float64(used)/float64(window)*float64(cells)))))
}

// pulseTrace is the strip at now from readings, oldest first: the newest
// cells steps on a fixed grid of pulseStep, the first lit ones lit, a step
// with no reading before it unborn. grown is the newest step's growth.
func pulseTrace(readings []pulseReading, now time.Time, lit, cells int) (strip []pulseCell, grown int64) {
	width := pulseStep.Milliseconds()
	first := now.UnixMilli()/width - int64(cells-1)
	next := 0
	var before *pulseReading
	for next < len(readings) && readings[next].At/width < first {
		before, next = &readings[next], next+1
	}
	strip = make([]pulseCell, cells)
	for cell := range strip {
		start := before
		for next < len(readings) && readings[next].At/width <= first+int64(cell) {
			before, next = &readings[next], next+1
		}
		strip[cell] = pulseCell{level: pulseLevelUnborn, lit: cell < lit}
		if start != nil {
			grown = before.Tokens - start.Tokens
			strip[cell].level = pulseLevel(grown)
		}
	}
	return strip, grown
}

// pulseStrip draws a strip: a lit cell in tone and reverse video, an unlit one
// in the window colour, an unborn one muted, a lit unborn one a blank.
func pulseStrip(strip []pulseCell, tone string) string {
	var out strings.Builder
	run, style := "", ""
	flush := func() {
		if run != "" {
			out.WriteString(style + run + reset)
		}
		run = ""
	}
	for _, cell := range strip {
		next, glyph := cWindow, pulseDrop
		switch {
		case cell.lit:
			next = tone + reverse
		case cell.level == pulseLevelUnborn:
			next = cMuted
		}
		switch {
		case cell.level == pulseLevelUnborn && cell.lit:
			glyph = " "
		case cell.level == pulseLevelUnborn:
			glyph = pulseGlyphs[0]
		case cell.level >= 0:
			glyph = pulseGlyphs[cell.level]
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
	first := reading.At/width - pulseCells + 1
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
