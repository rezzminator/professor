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
// status, focus), each live native chat's engine in its pane's foreground (an
// engine that died abruptly) and each decorated chat's transcript (size and
// mtime: a failed turn arrives only there). Only decorated chats' transcripts
// count: a chat with no tab paints nothing, so its turns never warrant a scan.
// tabs takes the baseline in two phases, the surface after the scan and each
// decorated chat's transcript right before its status is inspected, so a
// change that lands after its read is a change the next probe sees.
type tabProbe struct {
	// panes reads the endpoint's panes (nil: herdr.ReadTabPanes); foreground
	// says whether a seat's engine is its pane's foreground (nil:
	// tabEngineForeground).
	panes      func(context.Context, string) ([]herdr.TabPane, error)
	foreground func(context.Context, fleetdb.Seat) (bool, error)
	engines    []headless.Chat
	tabbed     []headless.Chat
	// pending is the baseline being taken; pendingOK says its surface read.
	pending   strings.Builder
	pendingOK bool
	baseline  string
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

// begin drops the baseline: an enumeration that fails leaves none.
func (probe *tabProbe) begin() {
	if probe != nil {
		probe.baseline = ""
	}
}

// record starts an enumeration's baseline from its scanned rows and the mode
// and glyphs it decorates with: the surface now, the transcripts as track
// meets them. A surface that cannot be read leaves no baseline, so the next
// probe re-reads and names the failure.
func (probe *tabProbe) record(ctx context.Context, socket string, rows []compose.Row, mode, glyphs string) {
	if probe == nil {
		return
	}
	probe.engines, probe.tabbed = probe.engines[:0], probe.tabbed[:0]
	for i := range rows {
		if target := FromRow(rows[i]); target.Live && target.Backend == spawn.BackendHerdr && target.Socket == socket {
			probe.engines = append(probe.engines, target)
		}
	}
	probe.pending.Reset()
	probe.pendingOK = probe.surface(ctx, socket, mode, glyphs, &probe.pending) == nil
}

// track adds a decorated chat's transcript to the baseline being taken.
func (probe *tabProbe) track(target headless.Chat) {
	if probe == nil {
		return
	}
	probe.tabbed = append(probe.tabbed, target)
	writeTranscript(&probe.pending, &target)
}

// commit makes the baseline being taken the one changed compares with, once
// the enumeration it describes succeeded.
func (probe *tabProbe) commit() {
	if probe != nil && probe.pendingOK {
		probe.baseline = probe.pending.String()
	}
}

// fingerprint renders the probed inputs.
func (probe *tabProbe) fingerprint(ctx context.Context, socket, mode, glyphs string) (string, error) {
	var out strings.Builder
	if err := probe.surface(ctx, socket, mode, glyphs, &out); err != nil {
		return "", err
	}
	for i := range probe.tabbed {
		writeTranscript(&out, &probe.tabbed[i])
	}
	return out.String(), nil
}

// surface renders the mode, glyphs, panes and live engines. An engine that
// cannot be read carries that error in its line, so a lasting failure reads
// as no change while the scan it was first seen by names it.
func (probe *tabProbe) surface(ctx context.Context, socket, mode, glyphs string, out *strings.Builder) error {
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
		return fmt.Errorf("probe tab panes: %w", err)
	}
	fmt.Fprintf(out, "mode %q glyphs %q\n", mode, glyphs)
	for i := range panes {
		pane := &panes[i]
		fmt.Fprintf(out, "pane %q %q %q %d %q %t %s\n",
			pane.Pane, pane.Terminal, pane.Tab, pane.Revision, pane.State,
			pane.Focused != nil && *pane.Focused, pane.Session)
	}
	for i := range probe.engines {
		target := &probe.engines[i]
		seat := fleetdb.Seat{HerdrSocket: socket, HerdrPane: target.Pane, Engine: string(target.Engine)}
		fmt.Fprintf(out, "engine %q %q", target.SeatID, target.ID)
		if alive, err := foreground(ctx, seat); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("probe tab engine of %s: %w", target.ID, err)
			}
			fmt.Fprintf(out, " error %q\n", err)
		} else {
			fmt.Fprintf(out, " %t\n", alive)
		}
	}
	return nil
}

// writeTranscript renders a chat's transcript line; one that cannot be read
// carries the error, so a lasting failure reads as no change.
func writeTranscript(out *strings.Builder, target *headless.Chat) {
	if target.Path == "" {
		return
	}
	fmt.Fprintf(out, "transcript %q %q", target.SeatID, target.ID)
	if info, err := os.Stat(target.Path); err != nil {
		fmt.Fprintf(out, " error %q\n", err)
	} else {
		fmt.Fprintf(out, " %d %d\n", info.Size(), info.ModTime().UnixNano())
	}
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
