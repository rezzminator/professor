package chat

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// tabProbe fingerprints, without the fleet scan, the inputs behind a tab
// enumeration that change with no hook to nudge the blinker: the tab status
// setting and the theme's glyphs, the endpoint's panes (revision, agent
// status, focus), each scanned chat's transcript (size and mtime: a failed
// turn arrives only there) and each native chat's engine in its pane's
// foreground (an engine that died abruptly). Its baseline is taken by tabs
// between the scan and the reads that build the decorations, so a change
// that lands after it is a change the next probe sees.
type tabProbe struct {
	// panes reads the endpoint's panes (nil: herdr.ReadTabPanes); foreground
	// says whether a seat's engine is its pane's foreground (nil:
	// tabEngineForeground).
	panes      func(context.Context, string) ([]herdr.TabPane, error)
	foreground func(context.Context, fleetdb.Seat) (bool, error)
	targets    []headless.Chat
	baseline   string
}

// changed is the provider's herdr.TabProbe: whether the fingerprint differs
// from the one taken at the last enumeration, or no enumeration left one.
func (provider *herdrTabProvider) changed(ctx context.Context, socket string) (bool, error) {
	probe := provider.probe
	if probe == nil {
		return false, nil
	}
	mode, err := provider.mode()
	if err != nil {
		return true, fmt.Errorf("read tab status configuration: %w", err)
	}
	glyphs := ""
	if provider.drawsGlyphs(mode) {
		read, err := provider.glyphs()
		glyphs = glyphToken(read, err)
	}
	now, err := probe.fingerprint(ctx, socket, mode, glyphs)
	if err != nil {
		return true, err
	}
	return probe.baseline == "" || now != probe.baseline, nil
}

// record takes the baseline of an enumeration from its scanned rows and the
// mode and glyphs it decorates with; a fingerprint that cannot be taken
// leaves none, so the next probe re-reads and names the failure.
func (probe *tabProbe) record(ctx context.Context, socket string, rows []compose.Row, mode, glyphs string) {
	probe.targets = probe.targets[:0]
	for i := range rows {
		probe.targets = append(probe.targets, FromRow(rows[i]))
	}
	fingerprint, err := probe.fingerprint(ctx, socket, mode, glyphs)
	if err != nil {
		fingerprint = ""
	}
	probe.baseline = fingerprint
}

// fingerprint renders the probed inputs. A chat whose transcript or engine
// cannot be read carries that error in its line, so a lasting failure reads
// as no change while the scan it was first seen by names it.
func (probe *tabProbe) fingerprint(ctx context.Context, socket, mode, glyphs string) (string, error) {
	readPanes := probe.panes
	if readPanes == nil {
		readPanes = herdr.ReadTabPanes
	}
	foreground := probe.foreground
	if foreground == nil {
		foreground = tabEngineForeground
	}
	panes, err := readPanes(ctx, socket)
	if err != nil {
		return "", fmt.Errorf("probe tab panes: %w", err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "mode %q glyphs %q\n", mode, glyphs)
	for i := range panes {
		pane := &panes[i]
		fmt.Fprintf(&out, "pane %q %q %q %d %q %t %s\n",
			pane.Pane, pane.Terminal, pane.Tab, pane.Revision, pane.State,
			pane.Focused != nil && *pane.Focused, pane.Session)
	}
	for i := range probe.targets {
		target := &probe.targets[i]
		fmt.Fprintf(&out, "chat %q %q", target.SeatID, target.ID)
		if target.Path != "" {
			if info, err := os.Stat(target.Path); err != nil {
				fmt.Fprintf(&out, " transcript error %q", err)
			} else {
				fmt.Fprintf(&out, " transcript %d %d", info.Size(), info.ModTime().UnixNano())
			}
		}
		if target.Live && target.Backend == spawn.BackendHerdr && target.Socket == socket {
			seat := fleetdb.Seat{HerdrSocket: socket, HerdrPane: target.Pane, Engine: string(target.Engine)}
			if alive, err := foreground(ctx, seat); err != nil {
				if ctx.Err() != nil {
					return "", fmt.Errorf("probe tab engine of %s: %w", target.ID, err)
				}
				fmt.Fprintf(&out, " engine error %q", err)
			} else {
				fmt.Fprintf(&out, " engine %t", alive)
			}
		}
		out.WriteByte('\n')
	}
	return out.String(), nil
}

// drawsGlyphs says whether the provider reads the theme's glyphs in mode: only
// emoji mode draws them.
func (provider *herdrTabProvider) drawsGlyphs(mode string) bool {
	return provider.glyphs != nil && mode != herdr.TabStatusOff && mode != "text"
}

// glyphToken is the theme glyph read a fingerprint carries: the glyphs, or
// the error that made the tabs paint the defaults.
func glyphToken(glyphs herdr.TabGlyphs, err error) string {
	if err != nil {
		return "error " + err.Error()
	}
	return fmt.Sprintf("%+v", glyphs)
}
