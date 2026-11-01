package chat

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// TestHerdrTabProbeSeesWhatNoHookAnnounces pins the provider's probe: after an
// enumeration it reports no change until an input behind it changes with no
// hook to nudge the blinker — the transcript (a failed turn), the engine in
// the pane's foreground (an abrupt death), the tab status setting, the pane's
// Herdr revision — and a fresh enumeration takes the new baseline. A pane
// list it cannot read is an error, never "unchanged".
func TestHerdrTabProbeSeesWhatNoHookAnnounces(t *testing.T) {
	transcript := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	focused := true
	mode, alive, revision := "emoji", true, uint64(1)
	var panesErr error
	probe := &tabProbe{
		panes: func(context.Context, string) ([]herdr.TabPane, error) {
			return []herdr.TabPane{{
				Pane: "w1:p1", Terminal: "term1", Workspace: "w1", Tab: "w1:t1",
				Focused: &focused, Revision: revision, State: "idle",
			}}, panesErr
		},
		foreground: func(_ context.Context, seat fleetdb.Seat) (bool, error) {
			if seat.HerdrSocket != "endpoint" || seat.HerdrPane != "w1:p1" {
				t.Errorf("foreground probed seat %+v, want the chat's pane on the endpoint", seat)
			}
			return alive, nil
		},
	}
	provider := herdrTabProvider{
		scan: func(context.Context) (FleetScan, error) {
			return FleetScan{Rows: []compose.Row{{
				Backend: "herdr", SeatID: "seat1", ID: "s1", Socket: "endpoint", PaneID: "w1:p1",
				Path: transcript, Name: "Chat", Kind: compose.LiveClaude,
			}}}, nil
		},
		inspect: func(context.Context, headless.Chat, time.Time) (headless.Status, error) {
			return headless.Status{State: "idle"}, nil
		},
		views: func(_ context.Context, c headless.Chat, _ string) ([]herdr.TabDecoration, error) {
			return []herdr.TabDecoration{{Pane: c.Pane, Identity: c.SeatID + ":" + c.ID}}, nil
		},
		mode:  func() (string, error) { return mode, nil },
		warn:  &bytes.Buffer{},
		probe: probe,
	}
	ctx := context.Background()
	assertChanged := func(step string, want bool) {
		t.Helper()
		got, err := provider.changed(ctx, "endpoint")
		if err != nil || got != want {
			t.Fatalf("%s: changed = %t, %v; want %t", step, got, err, want)
		}
	}
	enumerate := func() {
		t.Helper()
		if _, err := provider.tabs(ctx, "endpoint"); err != nil {
			t.Fatal(err)
		}
	}
	assertChanged("before any enumeration", true)
	enumerate()
	assertChanged("right after an enumeration", false)
	assertChanged("probed again with nothing new", false)

	file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(`{"type":"assistant","isApiErrorMessage":true}` + "\n")
	if closeErr := file.Close(); writeErr != nil || closeErr != nil {
		t.Fatalf("append turn: %v %v", writeErr, closeErr)
	}
	assertChanged("a turn only the transcript holds", true)
	enumerate()
	assertChanged("the transcript's turn enumerated", false)

	alive = false
	assertChanged("the engine left its pane's foreground", true)
	enumerate()
	assertChanged("the dead engine enumerated", false)

	mode = herdr.TabStatusOff
	assertChanged("the tab status setting turned off", true)
	enumerate()

	revision++
	assertChanged("the pane's Herdr revision moved", true)
	enumerate()
	assertChanged("the new revision enumerated", false)

	panesErr = errors.New("pane.list refused")
	if got, err := provider.changed(ctx, "endpoint"); !got || err == nil ||
		!strings.Contains(err.Error(), "pane.list refused") {
		t.Fatalf("unreadable panes: changed = %t, %v; want a change and the named failure", got, err)
	}
	panesErr = nil
	provider.scan = func(context.Context) (FleetScan, error) { return FleetScan{}, errors.New("fleet denied") }
	if _, err := provider.tabs(ctx, "endpoint"); err == nil {
		t.Fatal("a failed scan enumerated")
	}
	assertChanged("after a failed enumeration", true)
}
