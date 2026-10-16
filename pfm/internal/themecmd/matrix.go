package themecmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/themesync"
)

const offLine = "pfm theme: off — pfm theme set <palette> turns it on"

// verdictColor is the SGR code a verdict is drawn in: green for a surface that
// agrees, yellow for one still to catch up, dim for one that is not installed,
// red for one that disagrees or failed.
func verdictColor(verdict themesync.Verdict) string {
	switch verdict {
	case themesync.VerdictInSync, themesync.VerdictApplied, themesync.VerdictRestored:
		return "32"
	case themesync.VerdictPending:
		return "33"
	case themesync.VerdictAbsent:
		return "2"
	default:
		return "31"
	}
}

func paint(text, code string, color bool) string {
	if !color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// drawMatrix prints the header, one row per surface and the closing proof
// line. A report with the sync off and nothing restored is the off line.
func drawMatrix(out io.Writer, report themesync.Report, color bool) {
	if !report.Enabled && len(report.Surfaces) == 0 {
		fmt.Fprintln(out, offLine)
		return
	}
	if report.Enabled {
		fmt.Fprintf(
			out,
			"pfm theme · %s · %s → %s · gen %d\n",
			report.Palette,
			report.Mode,
			report.Appearance,
			report.Generation,
		)
	} else {
		fmt.Fprintln(out, "pfm theme · off")
	}
	surfaceWidth, verdictWidth := 0, 0
	for _, surface := range report.Surfaces {
		surfaceWidth = max(surfaceWidth, len(surface.Surface))
		verdictWidth = max(verdictWidth, len(surface.Verdict))
	}
	for _, surface := range report.Surfaces {
		generation := "-"
		if surface.Generation > 0 {
			generation = "g" + strconv.Itoa(surface.Generation)
		}
		verdict := paint(string(surface.Verdict), verdictColor(surface.Verdict), color) +
			strings.Repeat(" ", verdictWidth-len(surface.Verdict))
		row := fmt.Sprintf(
			"  %-*s  %s  %-4s  %s  %s",
			surfaceWidth,
			surface.Surface,
			verdict,
			generation,
			surface.Path,
			surface.Detail,
		)
		fmt.Fprintln(out, strings.TrimRight(row, " "))
	}
	if report.Enabled {
		fmt.Fprintln(out, proofLine(report, color))
	}
}

// proofLine is the one sentence the matrix closes on: every surface at the
// same generation, or exactly which surfaces are not.
func proofLine(report themesync.Report, color bool) string {
	var bad []string
	agree, pending, absent := 0, 0, 0
	for _, surface := range report.Surfaces {
		switch surface.Verdict {
		case themesync.VerdictDrift, themesync.VerdictError:
			bad = append(bad, surface.Surface+" "+string(surface.Verdict))
		case themesync.VerdictPending:
			pending++
		case themesync.VerdictAbsent:
			absent++
		default:
			agree++
		}
	}
	if len(bad) != 0 {
		return paint("OUT OF SYNC · "+strings.Join(bad, ", "), "31", color)
	}
	line := fmt.Sprintf("in sync · %d of %d surfaces at gen %d", agree, len(report.Surfaces), report.Generation)
	if pending != 0 {
		line += fmt.Sprintf(" · %d pending", pending)
	}
	if absent != 0 {
		line += fmt.Sprintf(" · %d absent", absent)
	}
	if pending == 0 {
		return paint(line, "32", color)
	}
	return paint(line, "33", color)
}

// hexRGB parses #rrggbb.
func hexRGB(hex string) (red, green, blue int, ok bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, 0, 0, false
	}
	value, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(value >> 16 & 0xff), int(value >> 8 & 0xff), int(value & 0xff), true
}

// block is two cells of the colour; with no truecolor or a malformed colour it
// is the hex text, so a colour is never silently missing.
func block(hex string, color bool) string {
	red, green, blue, ok := hexRGB(hex)
	if !color || !ok {
		return hex
	}
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm  \x1b[0m", red, green, blue)
}

// sample is "Aa" in the variant's foreground on its background.
func sample(variant themesync.Variant, color bool) string {
	fr, fg, fb, fOK := hexRGB(variant.Foreground)
	br, bg, bb, bOK := hexRGB(variant.Background)
	if !color || !fOK || !bOK {
		return "bg " + variant.Background + " fg " + variant.Foreground
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm Aa \x1b[0m", fr, fg, fb, br, bg, bb)
}

func drawVariant(out io.Writer, label string, variant themesync.Variant, color bool) {
	parts := []string{sample(variant, color)}
	if color {
		ansi := make([]string, 0, 8)
		for _, hex := range variant.ANSI[:8] {
			ansi = append(ansi, block(hex, true))
		}
		parts = append(parts, strings.Join(ansi, ""))
	} else {
		parts = append(parts, "accent "+variant.Accent)
	}
	for _, state := range []struct{ name, hex string }{
		{"blocked", variant.State.Blocked},
		{"working", variant.State.Working},
		{"done", variant.State.Done},
		{"idle", variant.State.Idle},
	} {
		if color {
			parts = append(parts, block(state.hex, true)+" "+state.name)
		} else {
			parts = append(parts, state.name+" "+state.hex)
		}
	}
	fmt.Fprintf(out, "    %-5s %s\n", label, strings.Join(parts, "  "))
}

// ls prints every palette with both variants' swatches, the current one marked.
func (run runner) ls(env themesync.Env) int {
	palettes := run.deps.Ops.Palettes()
	if len(palettes) == 0 {
		return run.fail(fmt.Errorf("no palettes are built in"))
	}
	current := ""
	if run.deps.Ops.SyncOn(env.StateDir) {
		report, err := run.deps.Ops.ReadBack(run.ctx, env)
		if err != nil {
			fmt.Fprintf(run.stderr, "pfm theme: current palette unknown: %v\n", err)
		}
		current = report.Palette
	}
	for i := range palettes {
		palette := &palettes[i]
		marker := " "
		if palette.Name == current {
			marker = "*"
		}
		fmt.Fprintf(run.stdout, "%s %s  %s\n", marker, palette.Name, palette.Title)
		drawVariant(run.stdout, "dark", palette.Dark, run.deps.Color)
		drawVariant(run.stdout, "light", palette.Light, run.deps.Color)
	}
	return 0
}
