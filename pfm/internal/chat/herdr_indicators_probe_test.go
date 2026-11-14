package chat

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// TestHerdrTabProbeSeesWhatNoHookAnnounces pins the provider's probe: after an
// enumeration it reports no change until an input behind it changes with no
// hook to nudge the blinker — a tabbed chat's transcript (a failed turn), the
// engine in the pane's foreground (an abrupt death), the tab status setting,
// the pane's Herdr revision — and a fresh enumeration takes the new baseline.
// A chat's or pane's own change lets a read that is not fresh reuse the fleet
// scan and re-inspect only that chat, a tmux chat's on the pane its tab shows;
// a pane whose terminal moved, or a probe that could not tell, scans again; a
// chat whose views named a failure is read again.
// The engine's foreground is read again only once tabEngineProbe passed.
// A turn of a chat with no tab is no change. A pane list it cannot read is an
// error, never "unchanged".
func TestHerdrTabProbeSeesWhatNoHookAnnounces(t *testing.T) {
	dir := t.TempDir()
	transcript, untabbed := filepath.Join(dir, "s1.jsonl"), filepath.Join(dir, "s2.jsonl")
	other := filepath.Join(dir, "s3.jsonl")
	for _, path := range []string{transcript, untabbed, other} {
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	focused := true
	mode, alive, revision, terminal := "emoji", true, uint64(1), "term1"
	bridgedRevision, unproved := uint64(1), false
	var panesErr, modeErr error
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	probe := &tabProbe{
		panes: func(context.Context, string) ([]herdr.TabPane, error) {
			return []herdr.TabPane{{
				Pane: "w1:p1", Terminal: terminal, Workspace: "w1", Tab: "w1:t1",
				Focused: &focused, Revision: revision, State: "idle",
			}, {
				Pane: "w1:p2", Terminal: "term2", Workspace: "w1", Tab: "w1:t2",
				Focused: &focused, Revision: bridgedRevision, State: "idle",
			}}, panesErr
		},
		foreground: func(_ context.Context, seat fleetdb.Seat) (bool, error) {
			if seat.HerdrSocket != "endpoint" || seat.HerdrPane != "w1:p1" && seat.HerdrPane != "w1:p2" {
				t.Errorf("foreground probed seat %+v, want a tabbed chat's pane on the endpoint", seat)
			}
			return alive || seat.HerdrPane == "w1:p2", nil
		},
		clock: clk,
	}
	scans := 0
	var inspected, viewed []string
	warnings := &tickWarnings{out: &bytes.Buffer{}}
	provider := herdrTabProvider{
		scan: func(context.Context) (FleetScan, error) {
			scans++
			return FleetScan{Rows: []compose.Row{{
				Backend: "herdr", SeatID: "seat1", ID: "s1", Socket: "endpoint", PaneID: "w1:p1",
				Path: transcript, Name: "Chat", Kind: compose.LiveClaude,
			}, {
				Backend: "tmux", SeatID: "seat2", ID: "s2", Path: untabbed, Name: "Elsewhere", Kind: compose.LiveClaude,
			}, {
				Backend: "tmux", SeatID: "seat3", ID: "s3", Socket: "endpoint", PaneID: "%3",
				Path: other, Name: "Other", Kind: compose.LiveClaude,
			}}}, nil
		},
		inspect: func(_ context.Context, c headless.Chat, _ time.Time) (headless.Status, error) {
			inspected = append(inspected, c.ID)
			return headless.Status{State: "idle"}, nil
		},
		views: func(_ context.Context, c headless.Chat, _ string) ([]herdr.TabDecoration, error) {
			viewed = append(viewed, c.ID)
			switch c.ID {
			case "s2":
				if unproved {
					_, _ = warnings.Write([]byte("Herdr tab of s2 left untouched: seat seat2 ownership unproved\n"))
				}
				return nil, nil
			case "s3": // a tmux chat's tab shows on the Herdr pane its client runs in
				return []herdr.TabDecoration{{Pane: "w1:p2", Identity: c.SeatID + ":" + c.ID}}, nil
			}
			return []herdr.TabDecoration{{Pane: c.Pane, Identity: c.SeatID + ":" + c.ID}}, nil
		},
		mode:  func() (string, error) { return mode, modeErr },
		warn:  warnings,
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
		if _, err := provider.tabs(ctx, "endpoint", true); err != nil {
			t.Fatal(err)
		}
	}
	// reread reads not fresh, as a probed change does, and checks the scans and inspections it took.
	reread := func(step string, wantScans int, wantInspected ...string) {
		t.Helper()
		scans, inspected = 0, nil
		decorations, err := provider.tabs(ctx, "endpoint", false)
		if err != nil || scans != wantScans || !slices.Equal(inspected, wantInspected) || len(decorations) != 2 {
			t.Fatalf("%s: scans %d, inspected %q, %d tabs, %v; want %d scans, inspected %q, 2 tabs",
				step, scans, inspected, len(decorations), err, wantScans, wantInspected)
		}
	}
	assertChanged("before any enumeration", true)
	enumerate()
	assertChanged("right after an enumeration", false)
	assertChanged("probed again with nothing new", false)

	appendTurn := func(path string) {
		t.Helper()
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.WriteString(`{"type":"assistant","isApiErrorMessage":true}` + "\n")
		if closeErr := file.Close(); writeErr != nil || closeErr != nil {
			t.Fatalf("append turn to %s: %v %v", path, writeErr, closeErr)
		}
	}
	appendTurn(untabbed)
	assertChanged("a turn of a chat with no tab", false)
	appendTurn(transcript)
	assertChanged("a turn only the tabbed chat's transcript holds", true)
	reread("the transcript's turn re-read", 0, "s1")
	assertChanged("the transcript's turn enumerated", false)

	alive = false
	clk.Advance(tabEngineProbe - time.Nanosecond)
	assertChanged("the engine left its pane's foreground within tabEngineProbe of its last read", false)
	clk.Advance(time.Nanosecond)
	assertChanged("the engine left its pane's foreground", true)
	reread("the dead engine re-read", 0, "s1")
	assertChanged("the dead engine enumerated", false)

	mode = herdr.TabStatusOff
	assertChanged("the tab status setting turned off", true)
	enumerate()

	revision++
	assertChanged("the pane's Herdr revision moved", true)
	reread("the new revision re-read", 0, "s1")
	assertChanged("the new revision enumerated", false)

	terminal = "term9"
	assertChanged("the pane's terminal moved", true)
	reread("the moved terminal re-read", 1, "s1", "s3")
	assertChanged("the moved terminal enumerated", false)

	bridgedRevision++
	assertChanged("the pane a tmux chat's tab shows on moved", true)
	reread("the tmux chat's pane re-read", 0, "s3")
	assertChanged("the tmux chat's pane enumerated", false)

	modeErr = errors.New("config unreadable")
	if got, err := provider.changed(ctx, "endpoint"); !got || err == nil {
		t.Fatalf("unreadable setting: changed = %t, %v; want a change and the named failure", got, err)
	}
	modeErr = nil
	reread("the read after a probe that could not tell", 1, "s1", "s3")

	unproved = true
	enumerate()
	unproved = false
	appendTurn(transcript)
	assertChanged("a turn after a read that left a chat's owner unproved", true)
	viewed = nil
	reread("the turn re-read", 0, "s1")
	if want := []string{"s1", "s2"}; !slices.Equal(viewed, want) {
		t.Fatalf("views read %q, want %q: a chat whose views named a failure is read again", viewed, want)
	}

	panesErr = errors.New("pane.list refused")
	if got, err := provider.changed(ctx, "endpoint"); !got || err == nil ||
		!strings.Contains(err.Error(), "pane.list refused") {
		t.Fatalf("unreadable panes: changed = %t, %v; want a change and the named failure", got, err)
	}
	panesErr = nil
	provider.scan = func(context.Context) (FleetScan, error) { return FleetScan{}, errors.New("fleet denied") }
	if _, err := provider.tabs(ctx, "endpoint", true); err == nil {
		t.Fatal("a failed scan enumerated")
	}
	assertChanged("after a failed enumeration", true)
}

// TestHerdrTabProbeSeesATabbedOpenCodeChatRetitled pins the OpenCode twin of a
// tabbed chat's transcript: OpenCode writes none, and the title its tab paints
// lands only in its store, which the fleet scan's mirror re-reads once the
// store changed. A retitle no hook announces is a change; a turn of an
// untabbed OpenCode chat is none, so the probe never re-reads the whole store.
// A retitle landing while the scan runs, after its mirror read, painted the
// old title, so it is a change too; so is a newly tabbed chat, whose title no
// read before the scan holds.
func TestHerdrTabProbeSeesATabbedOpenCodeChatRetitled(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	store, err := sqlitedb.OpenStore(ctx, index.OpenCodeStorePath(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := store.Exec(
		`CREATE TABLE session(id TEXT,parent_id TEXT,title TEXT,directory TEXT,time_created INT,time_updated INT);` +
			`INSERT INTO session VALUES('ses_tab',NULL,'New session','/work',1,1),('ses_other',NULL,'other','/work',1,1);`,
	); err != nil {
		t.Fatal(err)
	}
	focused := true
	var duringScan func()
	probe := &tabProbe{
		panes: func(context.Context, string) ([]herdr.TabPane, error) {
			return []herdr.TabPane{{
				Pane: "w1:p1", Terminal: "term1", Workspace: "w1", Tab: "w1:t1",
				Focused: &focused, Revision: 1, State: "idle",
			}}, nil
		},
		foreground:    func(context.Context, fleetdb.Seat) (bool, error) { return true, nil },
		openCodeRoots: []string{root},
	}
	provider := herdrTabProvider{
		scan: func(context.Context) (FleetScan, error) {
			if duringScan != nil {
				duringScan()
				duringScan = nil
			}
			return FleetScan{Rows: []compose.Row{{
				Backend: "herdr", SeatID: "seat1", ID: "ses_tab", Socket: "endpoint", PaneID: "w1:p1",
				Name: "New session", Kind: compose.LiveOpenCode,
			}}}, nil
		},
		inspect: func(context.Context, headless.Chat, time.Time) (headless.Status, error) {
			return headless.Status{State: "idle"}, nil
		},
		views: func(_ context.Context, c headless.Chat, _ string) ([]herdr.TabDecoration, error) {
			return []herdr.TabDecoration{{Pane: c.Pane, Identity: c.SeatID + ":" + c.ID}}, nil
		},
		mode:  func() (string, error) { return "emoji", nil },
		warn:  &bytes.Buffer{},
		probe: probe,
	}
	assertChanged := func(step string, want bool) {
		t.Helper()
		got, err := provider.changed(ctx, "endpoint")
		if err != nil || got != want {
			t.Fatalf("%s: changed = %t, %v; want %t", step, got, err, want)
		}
	}
	if _, err := provider.tabs(ctx, "endpoint", true); err != nil {
		t.Fatal(err)
	}
	assertChanged("a newly tabbed OpenCode chat, its title not read before the scan", true)
	if _, err := provider.tabs(ctx, "endpoint", true); err != nil {
		t.Fatal(err)
	}
	assertChanged("after the enumeration", false)
	if _, err := store.Exec(
		`UPDATE session SET title='other retitled',time_updated=2 WHERE id='ses_other'`,
	); err != nil {
		t.Fatal(err)
	}
	assertChanged("an untabbed OpenCode chat retitled", false)
	if _, err := store.Exec(`UPDATE session SET title='Fix the parser',time_updated=3 WHERE id='ses_tab'`); err != nil {
		t.Fatal(err)
	}
	assertChanged("the tabbed OpenCode chat retitled", true)
	if _, err := provider.tabs(ctx, "endpoint", true); err != nil {
		t.Fatal(err)
	}
	assertChanged("after re-enumerating", false)
	duringScan = func() {
		if _, err := store.Exec(
			`UPDATE session SET title='Fix the lexer',time_updated=4 WHERE id='ses_tab'`,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.tabs(ctx, "endpoint", true); err != nil {
		t.Fatal(err)
	}
	assertChanged("the tabbed OpenCode chat retitled during the scan", true)
	if _, err := provider.tabs(ctx, "endpoint", true); err != nil {
		t.Fatal(err)
	}
	assertChanged("after the enumeration that painted it", false)
}
