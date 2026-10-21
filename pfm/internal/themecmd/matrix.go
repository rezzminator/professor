package themecmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/themesync"
)

const offLine = "pfm theme: off — pfm theme on turns it on"

// verdictColor is the SGR code a verdict is drawn in: green for a surface that
// agrees, yellow for one still to catch up, dim for one that is not installed,
// red for one that disagrees or failed.
func verdictColor(verdict themesync.Verdict) string {
	switch verdict {
	case themesync.VerdictInSync, themesync.VerdictApplied, themesync.VerdictRestored, themesync.VerdictSource:
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
	if report.Enabled || report.Source != nil {
		fmt.Fprintf(
			out,
			"pfm theme · %s · %s · gen %d\n",
			sourceTitle(report.Source),
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
	if report.LastFlip != nil {
		fmt.Fprintln(out, flipLine(*report.LastFlip))
	}
}

// sourceTitle names the theme the header follows.
func sourceTitle(source *themesync.SourceInfo) string {
	switch {
	case source == nil:
		return "no source"
	case source.Title == "" || source.Title == source.Value:
		return source.Value
	}
	return source.Value + " (" + source.Title + ")"
}

// flipLine is the measured latency of the last source change (D23).
func flipLine(flip themesync.Flip) string {
	at := flip.At
	if parsed, err := time.Parse(time.RFC3339Nano, flip.At); err == nil {
		at = parsed.UTC().Format(time.RFC3339)
	}
	measured := func(name string, offset *float64) string {
		if offset == nil {
			return name + " unmeasured"
		}
		return fmt.Sprintf("%s +%.2f s", name, *offset)
	}
	parts := []string{
		measured("tmux", flip.Tmux), measured("herdr", flip.Herdr),
		measured("vscode contract", flip.VSCode),
	}
	line := fmt.Sprintf("last flip %s %s: %s", at, flip.Source, strings.Join(parts, " · "))
	if flip.Ack != nil {
		line += fmt.Sprintf(", ack +%.2f s", *flip.Ack)
	}
	return line
}

// proofLine is the one sentence the matrix closes on: every surface at the
// same generation, or exactly which surfaces are not.
func proofLine(report themesync.Report, color bool) string {
	var bad []string
	agree, pending, absent, sources := 0, 0, 0, 0
	for _, surface := range report.Surfaces {
		switch surface.Verdict {
		case themesync.VerdictDrift, themesync.VerdictError:
			bad = append(bad, surface.Surface+" "+string(surface.Verdict))
		case themesync.VerdictPending:
			pending++
		case themesync.VerdictAbsent:
			absent++
		case themesync.VerdictSource:
			sources++
		default:
			agree++
		}
	}
	if len(bad) != 0 {
		return paint("OUT OF SYNC · "+strings.Join(bad, ", "), "31", color)
	}
	line := fmt.Sprintf("in sync · %d of %d surfaces at gen %d", agree, len(report.Surfaces)-sources, report.Generation)
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
