package chat

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// The tab probe's input lines: one per mode, pane, live engine and decorated
// transcript, the engine's foreground read through its tabEngineProbe memo,
// and the diff of two fingerprints into what a read must re-inspect (tabDirty).

const tabEngineProbe = 5 * time.Second

type tabEngineRead struct {
	at    time.Time
	alive bool
	err   error
}

// engineForeground is foreground of seat through engineReads.
func (probe *tabProbe) engineForeground(
	ctx context.Context, seat fleetdb.Seat, foreground func(context.Context, fleetdb.Seat) (bool, error),
) (bool, error) {
	clk := probe.clock
	if clk == nil {
		clk = clock.Real
	}
	key := seat.HerdrPane + "\x00" + seat.Engine
	if read, found := probe.engineReads[key]; found && clk.Now().Sub(read.at) < tabEngineProbe {
		return read.alive, read.err
	}
	at := clk.Now()
	alive, err := foreground(ctx, seat)
	if ctx.Err() == nil {
		if probe.engineReads == nil {
			probe.engineReads = map[string]tabEngineRead{}
		}
		probe.engineReads[key] = tabEngineRead{at: at, alive: alive, err: err}
	}
	return alive, err
}

// forgetEngineReads drops the memo of every engine the enumeration no longer
// probes, so a long-lived painter keeps no read of a closed pane.
func (probe *tabProbe) forgetEngineReads() {
	live := map[string]bool{}
	for i := range probe.engines {
		live[probe.engines[i].Pane+"\x00"+string(probe.engines[i].Engine)] = true
	}
	maps.DeleteFunc(probe.engineReads, func(key string, _ tabEngineRead) bool { return !live[key] })
}

// tabDirty is what a probe saw change since the last enumeration: partial when
// a read may reuse the last fleet scan, re-inspecting only the chats (tabChatKey)
// and panes (tabPaneKey) it names. A pane added, gone or moved, the tab status
// setting, the glyphs, an OpenCode title or a title a transcript names past the
// scan's read changed need the fleet read again: only it composes the name.
type tabDirty struct {
	partial      bool
	panes, chats map[string]bool
}

// takeDirty hands the next read what the last probe saw, once: a read no
// probe preceded reads the fleet again.
func (probe *tabProbe) takeDirty() tabDirty {
	if probe == nil {
		return tabDirty{}
	}
	dirty := probe.dirty
	probe.dirty = tabDirty{}
	return dirty
}

// tabInputsDiff classifies the inputs that differ between two fingerprints;
// each line is a kind and key, then its state part, then its rows part.
func tabInputsDiff(before, after string) tabDirty {
	dirty := tabDirty{partial: true, panes: map[string]bool{}, chats: map[string]bool{}}
	was, now := tabInputLines(before), tabInputLines(after)
	for key, line := range now {
		if prior, found := was[key]; !found || prior != line {
			dirty.note(key, found && strings.SplitN(prior, "\x1f", 2)[1] == strings.SplitN(line, "\x1f", 2)[1])
		}
	}
	for key := range was {
		if _, found := now[key]; !found {
			dirty.note(key, false)
		}
	}
	return dirty
}

// note marks one changed input; sameRows says its rows part held.
func (dirty *tabDirty) note(key string, sameRows bool) {
	kind, id, _ := strings.Cut(key, " ")
	switch {
	case kind == "pane" && sameRows:
		dirty.panes[id] = true
	case kind == "engine" || (kind == "transcript" && sameRows):
		dirty.chats[id] = true
	default:
		dirty.partial = false
	}
}

// shown says whether a pane one of views shows the chat on moved: a tmux
// chat's tab is a Herdr pane its row does not name.
func (dirty tabDirty) shown(views []herdr.TabDecoration) bool {
	for i := range views {
		if dirty.panes[tabPaneKey(views[i].Pane)] {
			return true
		}
	}
	return false
}

func tabInputLines(fingerprint string) map[string]string {
	lines := map[string]string{}
	for _, line := range strings.Split(fingerprint, "\n") {
		if key, rest, found := strings.Cut(line, "\x1f"); found {
			lines[key] = rest
		}
	}
	return lines
}

// writeTabInput renders one probed input: its kind and key, the state part a
// read re-inspects its chats for, and the rows part it reads the fleet for.
func writeTabInput(out *strings.Builder, kind, key, state, rows string) {
	fmt.Fprintf(out, "%s %s\x1f%s\x1f%s\n", kind, key, state, rows)
}

// tabChatKey and tabPaneKey key a chat's and a pane's probed inputs.
func tabChatKey(target *headless.Chat) string {
	return strconv.Quote(target.SeatID) + " " + strconv.Quote(target.ID)
}

func tabPaneKey(pane string) string { return strconv.Quote(pane) }

// writeTranscript renders a chat's transcript line: its size and mtime, then
// renamed over its size ("" in a baseline). One that cannot be stat'ed
// carries the error, so a lasting failure reads as no change.
func writeTranscript(out *strings.Builder, target *headless.Chat, renamed func(size int64) string) {
	if target.Path == "" {
		return
	}
	state, rows := "", ""
	if info, err := os.Stat(target.Path); err != nil {
		state = fmt.Sprintf("error %q", err)
	} else {
		state = fmt.Sprintf("%d %d", info.Size(), info.ModTime().UnixNano())
		rows = renamed(info.Size())
	}
	writeTabInput(out, "transcript", tabChatKey(target), state, rows)
}
