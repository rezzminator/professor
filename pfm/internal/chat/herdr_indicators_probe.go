package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// tabProbe fingerprints, without the fleet scan, the inputs behind a tab
// enumeration that change with no hook to nudge the blinker: the tab status
// setting and the theme's glyphs, the endpoint's panes (revision, agent
// status, focus), each live native chat's engine in its pane's foreground (an
// engine that died abruptly) and each decorated chat's transcript (size and
// mtime: a failed turn arrives only there), or for an OpenCode chat, which
// writes none, its title in OpenCode's store. Only decorated chats' transcripts
// count: a chat with no tab paints nothing, so its turns never warrant a scan.
// tabs takes the baseline in three phases: the OpenCode titles before the scan
// reads its mirror, the surface after the scan and each decorated chat's
// transcript right before its status is inspected, so a change that lands
// after its read is a change the next probe sees.
type tabProbe struct {
	// panes reads the endpoint's panes (nil: herdr.ReadTabPanes); foreground
	// says whether a seat's engine is its pane's foreground (nil:
	// call.HerdrEngine).
	panes      func(context.Context, string) ([]herdr.TabPane, error)
	foreground func(context.Context, fleetdb.Seat) (bool, error)
	// openCodeRoots are the OpenCode stores a tabbed OpenCode chat's title is
	// read from: OpenCode writes no transcript, its title lands only there.
	openCodeRoots []string
	engines       []headless.Chat
	tabbed        []headless.Chat
	// titles renders, before the scan, the titles of titleIDs: the OpenCode
	// chats the last enumeration tabbed, the ids this one most likely tabs.
	titles   string
	titleIDs []string
	// pending is the baseline being taken; pendingOK says its surface read.
	pending   strings.Builder
	pendingOK bool
	baseline  string
	// dirty is what the last changed saw differ from the baseline.
	dirty tabDirty
	// clock dates engineReads (nil: clock.Real): each live engine's last
	// foreground read, asked again only once tabEngineProbe passed, so a tick
	// makes no Herdr call per live engine and an abrupt death shows within it.
	clock       clock.Clock
	engineReads map[string]tabEngineRead
}

// changed is the provider's herdr.TabProbe: whether the fingerprint differs
// from the one taken at the last enumeration, or no enumeration left one.
func (provider *herdrTabProvider) changed(ctx context.Context, socket string) (bool, error) {
	probe := provider.probe
	if probe == nil {
		return false, nil
	}
	probe.dirty = tabDirty{} // a probe that cannot tell leaves the next read a fresh scan
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
	probe.dirty = tabInputsDiff(probe.baseline, now)
	return probe.baseline == "" || now != probe.baseline, nil
}

// begin drops the baseline, so an enumeration that fails leaves none, and
// reads the titles of the OpenCode chats the last enumeration tabbed before
// the scan reads its mirror: a retitle landing during the scan is then one
// the baseline lacks, so the next probe sees it.
func (probe *tabProbe) begin(ctx context.Context) {
	if probe != nil {
		probe.baseline = ""
		probe.titleIDs = probe.openCodeIDs()
		var titles strings.Builder
		probe.writeOpenCodeTitles(ctx, &titles, probe.titleIDs)
		probe.titles = titles.String()
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
	probe.forgetEngineReads()
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
// the enumeration it describes succeeded. Titles read before the scan join it
// only when this enumeration tabbed the same OpenCode chats; otherwise the
// baseline carries none, so the next probe sees a change and the next
// enumeration reads the new chats' titles before its scan.
func (probe *tabProbe) commit() {
	if probe != nil && probe.pendingOK {
		if slices.Equal(probe.openCodeIDs(), probe.titleIDs) {
			probe.pending.WriteString(probe.titles)
		}
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
	probe.writeOpenCodeTitles(ctx, &out, probe.openCodeIDs())
	return out.String(), nil
}

// openCodeIDs are the tabbed OpenCode chats, sorted: the chats whose titles
// a fingerprint carries.
func (probe *tabProbe) openCodeIDs() []string {
	var ids []string
	for i := range probe.tabbed {
		if target := &probe.tabbed[i]; target.Engine == pfmengine.OpenCode && target.Path == "" {
			ids = append(ids, target.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

// writeOpenCodeTitles renders the titles of the OpenCode chats ids names as
// their store holds them: OpenCode writes no transcript, and the title its
// tab paints lands only there. Only tabbed chats' rows are read, so another
// chat's turn is no change. A store that cannot be read carries the error in
// its line, so a lasting failure reads as no change.
func (probe *tabProbe) writeOpenCodeTitles(ctx context.Context, out *strings.Builder, ids []string) {
	if len(ids) == 0 {
		return
	}
	for _, root := range probe.openCodeRoots {
		titles, err := index.ReadOpenCodeTitles(ctx, root, ids)
		if errors.Is(err, index.ErrNoOpenCodeStore) {
			continue
		}
		if err != nil {
			writeTabInput(out, "title", strconv.Quote(root), "", fmt.Sprintf("error %q", err))
			continue
		}
		for _, id := range ids {
			if title, found := titles[id]; found {
				writeTabInput(out, "title", strconv.Quote(root)+" "+strconv.Quote(id), "", strconv.Quote(title))
			}
		}
	}
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
		foreground = call.HerdrEngine
	}
	panes, err := readPanes(ctx, socket)
	if err != nil {
		return fmt.Errorf("probe tab panes: %w", err)
	}
	writeTabInput(out, "mode", "", "", fmt.Sprintf("%q glyphs %q", mode, glyphs))
	for i := range panes {
		pane := &panes[i]
		writeTabInput(out, "pane", tabPaneKey(pane.Pane),
			fmt.Sprintf("%d %q %t", pane.Revision, pane.State, pane.Focused != nil && *pane.Focused),
			fmt.Sprintf("%q %q %s", pane.Terminal, pane.Tab, pane.Session))
	}
	for i := range probe.engines {
		target := &probe.engines[i]
		seat := fleetdb.Seat{HerdrSocket: socket, HerdrPane: target.Pane, Engine: string(target.Engine)}
		state := ""
		if alive, err := probe.engineForeground(ctx, seat, foreground); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("probe tab engine of %s: %w", target.ID, err)
			}
			state = fmt.Sprintf("error %q", err)
		} else {
			state = strconv.FormatBool(alive)
		}
		writeTabInput(out, "engine", tabChatKey(target), state, "")
	}
	return nil
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
