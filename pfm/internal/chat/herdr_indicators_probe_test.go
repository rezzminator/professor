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
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// TestHerdrTabProbeSeesWhatNoHookAnnounces pins the provider's probe: after an
// enumeration it reports no change until an input behind it changes with no
// hook to nudge the blinker — a tabbed chat's transcript (a failed turn), the
// engine in the pane's foreground (an abrupt death), the tab status setting,
// the pane's Herdr revision — and a fresh enumeration takes the new baseline.
// A turn of a chat with no tab is no change. A pane list it cannot read is an
// error, never "unchanged".
func TestHerdrTabProbeSeesWhatNoHookAnnounces(t *testing.T) {
	dir := t.TempDir()
	transcript, untabbed := filepath.Join(dir, "s1.jsonl"), filepath.Join(dir, "s2.jsonl")
	for _, path := range []string{transcript, untabbed} {
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
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
			}, {
				Backend: "tmux", SeatID: "seat2", ID: "s2", Path: untabbed, Name: "Elsewhere", Kind: compose.LiveClaude,
			}}}, nil
		},
		inspect: func(context.Context, headless.Chat, time.Time) (headless.Status, error) {
			return headless.Status{State: "idle"}, nil
		},
		views: func(_ context.Context, c headless.Chat, _ string) ([]herdr.TabDecoration, error) {
			if c.ID == "s2" {
				return nil, nil
			}
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
	if _, err := provider.tabs(ctx, "endpoint"); err != nil {
		t.Fatal(err)
	}
	assertChanged("a newly tabbed OpenCode chat, its title not read before the scan", true)
	if _, err := provider.tabs(ctx, "endpoint"); err != nil {
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
	if _, err := provider.tabs(ctx, "endpoint"); err != nil {
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
	if _, err := provider.tabs(ctx, "endpoint"); err != nil {
		t.Fatal(err)
	}
	assertChanged("the tabbed OpenCode chat retitled during the scan", true)
	if _, err := provider.tabs(ctx, "endpoint"); err != nil {
		t.Fatal(err)
	}
	assertChanged("after the enumeration that painted it", false)
}
