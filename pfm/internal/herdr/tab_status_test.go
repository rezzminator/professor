package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

func TestTabStatusLabels(t *testing.T) {
	for _, tc := range []struct {
		state, glyph, off, text string
	}{
		{"working", "🟡", "", "[W]"},
		{"idle", "🟢", "", "[I]"},
		{"blocked", "🔴", "⭕", "[B]"},
		{"error", "🟥", "❌", "[E]"},
		{"dead", "⬛", "", "[D]"},
		{"unknown", "❔", "", "[?]"},
		{"read failure", "❔", "", "[?]"},
	} {
		for _, mode := range []string{"emoji", "text"} {
			for _, on := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/%t", tc.state, mode, on), func(t *testing.T) {
					marker := tc.glyph
					if mode == "text" {
						marker = tc.text
					}
					if tc.off != "" && !on {
						marker = tc.off
						if mode == "text" {
							marker = "[ ]"
						}
					}
					got := DecoratedTabLabel(" \x1b\n日本\t chat ", tc.state, mode, on, TabGlyphs{})
					if got != marker+" 日本 chat" {
						t.Fatalf("label=%q want %q", got, marker+" 日本 chat")
					}
					wantWidth := 2
					if mode == "text" {
						wantWidth = 3
					}
					if ansi.StringWidth(marker) != wantWidth {
						t.Fatalf("marker width=%d want %d", ansi.StringWidth(marker), wantWidth)
					}
					long := DecoratedTabLabel(strings.Repeat("界", 100), tc.state, mode, on, TabGlyphs{})
					if utf8.RuneCountInString(long) != MaxTitleRunes || !strings.HasPrefix(long, marker+" ") {
						t.Fatalf("truncated label=%q", long)
					}
				})
			}
		}
	}
}

type tabStatusFixture struct {
	label, terminal, state, session string
	count                           int
	revision                        uint64
	renames                         []string
	before                          func(string)
}

func newTabStatusFixture() *tabStatusFixture {
	return &tabStatusFixture{label: "Chat", terminal: "term1", state: "blocked", session: "s1", count: 1, revision: 7}
}

func (f *tabStatusFixture) call(_ context.Context, _ string, lines ...[]byte) ([]json.RawMessage, error) {
	var out []json.RawMessage
	for _, line := range lines {
		var r struct {
			Method string `json:"method"`
			Params struct {
				Label string `json:"label"`
			} `json:"params"`
		}
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, err
		}
		if f.before != nil {
			f.before(r.Method)
		}
		var body string
		switch r.Method {
		case "pane.get":
			body = fmt.Sprintf(
				`{"type":"pane_info","pane":{"pane_id":"w1:p1","terminal_id":%q,"workspace_id":"w1","tab_id":"w1:t1","focused":true,"revision":%d,"agent_status":%q,"agent_session":{"source":"pfm","agent":"claude","kind":"id","value":%q}}}`,
				f.terminal,
				f.revision,
				f.state,
				f.session,
			)
		case "tab.get":
			body = fmt.Sprintf(
				`{"type":"tab_info","tab":{"tab_id":"w1:t1","workspace_id":"w1","number":1,"focused":true,"label":%q,"pane_count":%d,"agent_status":%q}}`,
				f.label,
				f.count,
				f.state,
			)
		case "tab.rename":
			f.label = r.Params.Label
			f.renames = append(f.renames, f.label)
			body = `{"type":"ok"}`
		default:
			return nil, fmt.Errorf("unexpected %s", r.Method)
		}
		out = append(out, json.RawMessage(body))
	}
	return out, nil
}

func tabObservation(state string) TabDecoration {
	return TabDecoration{
		Pane:        "w1:p1",
		Terminal:    "term1",
		Tab:         "w1:t1",
		Identity:    "seat1:s1",
		Validate:    func(context.Context) (bool, error) { return true, nil },
		Name:        "Chat",
		State:       state,
		Mode:        "emoji",
		Revision:    7,
		AgentStatus: "blocked",
		Session:     json.RawMessage(`{"source":"pfm","agent":"claude","kind":"id","value":"s1"}`),
	}
}

func TestTabStatusPulseAndNoop(t *testing.T) {
	for _, state := range []string{"working", "idle", "blocked", "error", "dead"} {
		t.Run(state, func(t *testing.T) {
			f := newTabStatusFixture()
			dir := t.TempDir()
			d := tabObservation(state)
			for _, on := range []bool{true, true, false, false, true} {
				if _, err := reconcileTab(context.Background(), "endpoint", dir, d, on, f.call); err != nil {
					t.Fatal(err)
				}
			}
			want := 1
			if state == "blocked" || state == "error" {
				want = 3
			}
			if len(f.renames) != want {
				t.Fatalf("renames=%v want %d", f.renames, want)
			}
		})
	}
}

func TestTabStatusNewNameWins(t *testing.T) {
	f := newTabStatusFixture()
	dir := t.TempDir()
	d := tabObservation("blocked")
	if _, err := reconcileTab(context.Background(), "endpoint", dir, d, true, f.call); err != nil {
		t.Fatal(err)
	}
	// A competing name writer uses the same lock/state, exactly as LabelSoleTab.
	locked, proceed := make(chan struct{}), make(chan struct{})
	nameDone, pulseDone := make(chan error, 1), make(chan error, 1)
	go func() {
		first := true
		nameDone <- writeSoleTabLabel(context.Background(), "endpoint", dir, d.Tab, "Newest", true, func() uint64 { return 1 }, func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
			if first {
				first = false
				close(locked)
				<-proceed
			}
			return f.call(ctx, socket, lines...)
		})
	}()
	<-locked
	go func() {
		_, err := reconcileTab(context.Background(), "endpoint", dir, d, false, f.call)
		pulseDone <- err
	}()
	close(proceed)
	if err := <-nameDone; err != nil {
		t.Fatal(err)
	}
	if err := <-pulseDone; err != nil {
		t.Fatal(err)
	}
	if f.label != "⭕ Newest" {
		t.Fatalf("stale name restored: %q", f.label)
	}
}

// A chat renamed after its tab was decorated reaches the tab at the next
// tick: the chat name the painter last saw is kept with the label, so a new
// one is newer than any earlier label. A label the user set in Herdr belongs
// to the user: no later automatic change of the composed name (an AI title,
// the first prompt) replaces it, nor does a re-announce of an unchanged name
// (ReannounceSoleTab); a name the user chose does: an explicit pfm rename
// (LabelSoleTab, the writer of `pfm chat name` and of a user's name change the
// rename hook carries) or a change of the chat's user name the painter sees
// (TestTabStatusUserRenameReplacesAUserLabel), after which the tab follows the
// chat's name again. A record written before the painter followed names (no
// seen name) starts following without a rename.
func TestTabStatusChatRenameReachesTheTab(t *testing.T) {
	f := newTabStatusFixture()
	dir := t.TempDir()
	d := tabObservation("blocked")
	tick := func(name, want string) {
		t.Helper()
		d.Name = name
		if _, err := reconcileTab(context.Background(), "endpoint", dir, d, true, f.call); err != nil {
			t.Fatal(err)
		}
		if want := DecoratedTabLabel(want, "blocked", "emoji", true, TabGlyphs{}); f.label != want {
			t.Fatalf("after a tick seeing chat name %q the tab reads %q, want %q", name, f.label, want)
		}
	}
	tick("Chat", "Chat")
	tick("Renamed", "Renamed")
	tick("Renamed", "Renamed")
	// A tab Herdr reports with no label holds no label the user set: pfm's name stays.
	f.label = ""
	tick("Renamed", "Renamed")
	f.label = "Mine"
	tick("Renamed", "Mine")
	tick("Renamed", "Mine")
	// The composed name drifts (a first prompt becomes an AI title): the user's label stays.
	tick("Third", "Mine")
	tick("Fourth", "Mine")
	// A session start re-announcing the chat's name is no rename: the user's label stays.
	if err := writeSoleTabLabel(
		context.Background(), "endpoint", dir, d.Tab, "Fourth", false, func() uint64 { return 1 }, f.call,
	); err != nil {
		t.Fatal(err)
	}
	if want := DecoratedTabLabel("Mine", "blocked", "emoji", true, TabGlyphs{}); f.label != want {
		t.Fatalf("after a re-announce the tab reads %q, want the user's %q", f.label, want)
	}
	// An explicit pfm rename wins over the user's label ...
	if err := writeSoleTabLabel(
		context.Background(), "endpoint", dir, d.Tab, "Explicit", true, func() uint64 { return 1 }, f.call,
	); err != nil {
		t.Fatal(err)
	}
	if want := DecoratedTabLabel("Explicit", "blocked", "emoji", true, TabGlyphs{}); f.label != want {
		t.Fatalf("after an explicit rename the tab reads %q, want %q", f.label, want)
	}
	// ... without the painter's next tick, still seeing the older composed name, undoing it ...
	tick("Fourth", "Explicit")
	// ... and the tab follows the chat's name again.
	tick("Fifth", "Fifth")

	legacy := newTabStatusFixture()
	legacyDir := t.TempDir()
	rendered := DecoratedTabLabel("Old", "blocked", "emoji", true, TabGlyphs{})
	legacy.label = rendered
	if err := withTabLabelLock(context.Background(), legacyDir, "endpoint", d.Tab, func(path string) error {
		return writeTabLabel(path, tabLabelRecord{
			Base: "Old", Rendered: rendered, Identity: d.Identity, State: "blocked", Mode: "emoji", On: true,
		})
	}); err != nil {
		t.Fatal(err)
	}
	f, dir = legacy, legacyDir
	tick("New", "Old")
	tick("Newer", "Newer")
}

// A label the user set in Herdr stands against the chat's automatic names
// (an AI title, a first prompt), never against a name the user chose later: a
// Claude /rename (its custom title) or a Codex thread rename replaces it, and
// the tab follows the chat's name again. A record written before the painter
// saw user names, or one only rows not yet read from their source reached (a
// booting chat), starts following them without a rename.
func TestTabStatusUserRenameReplacesAUserLabel(t *testing.T) {
	f := newTabStatusFixture()
	dir := t.TempDir()
	d := tabObservation("blocked")
	known := true
	tick := func(name, user, want string) {
		t.Helper()
		d.Name, d.UserName, d.UserKnown = name, user, known
		if _, err := reconcileTab(context.Background(), "endpoint", dir, d, true, f.call); err != nil {
			t.Fatal(err)
		}
		if want := DecoratedTabLabel(want, "blocked", "emoji", true, TabGlyphs{}); f.label != want {
			t.Fatalf(
				"after a tick seeing chat name %q (user name %q) the tab reads %q, want %q",
				name,
				user,
				f.label,
				want,
			)
		}
	}
	tick("Chat", "", "Chat")
	f.label = "Mine"
	tick("Chat", "", "Mine")
	// An AI title is automatic: the user's label stays.
	tick("AI title", "", "Mine")
	// A /rename is the user's: newer than the user's label.
	tick("Renamed", "Renamed", "Renamed")
	tick("Renamed", "Renamed", "Renamed")
	f.label = "Mine again"
	tick("Renamed", "Renamed", "Mine again")
	tick("Renamed", "Renamed", "Mine again")
	// A row that carries no user name is no rename.
	tick("Renamed", "", "Mine again")
	tick("Renamed twice", "Renamed twice", "Renamed twice")

	legacy := newTabStatusFixture()
	legacyDir := t.TempDir()
	rendered := DecoratedTabLabel("Mine", "blocked", "emoji", true, TabGlyphs{})
	legacy.label = rendered
	if err := withTabLabelLock(context.Background(), legacyDir, "endpoint", d.Tab, func(path string) error {
		return writeTabLabel(path, tabLabelRecord{
			Base: "Mine", Rendered: rendered, Identity: d.Identity, State: "blocked", Mode: "emoji", On: true,
			Seen: "Chat", UserSet: true,
		})
	}); err != nil {
		t.Fatal(err)
	}
	f, dir = legacy, legacyDir
	tick("Named", "Named", "Mine")
	tick("Named again", "Named again", "Named again")

	// A resumed chat booting in a tab: its rows carry no user name until its
	// transcript is read, and the user labels the tab meanwhile. The custom
	// title the chat already had, first read then, is no rename.
	f, dir = newTabStatusFixture(), t.TempDir()
	known = false
	tick("Chat", "", "Chat")
	f.label = "Mine"
	tick("Chat", "", "Mine")
	known = true
	tick("Old title", "Old title", "Mine")
	tick("New title", "New title", "New title")
}

func TestTabStatusTopologyAndGeneration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*tabStatusFixture, *TabDecoration)
	}{
		{"split", func(f *tabStatusFixture, _ *TabDecoration) { f.count = 2 }},
		{"close", func(f *tabStatusFixture, _ *TabDecoration) { f.count = 0 }},
		{"replacement", func(f *tabStatusFixture, _ *TabDecoration) { f.terminal = "replacement" }},
		{"reload", func(f *tabStatusFixture, _ *TabDecoration) { f.session = "s2" }},
		{"new prompt", func(f *tabStatusFixture, _ *TabDecoration) { f.state = "working" }},
		{"moved", func(_ *tabStatusFixture, d *TabDecoration) { d.Tab = "w2:t2" }},
		{"tmux generation", func(_ *tabStatusFixture, d *TabDecoration) {
			d.Validate = func(context.Context) (bool, error) { return false, nil }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTabStatusFixture()
			d := tabObservation("error")
			dir := t.TempDir()
			// Establish decoration first; the second observation is stale.
			if _, err := reconcileTab(context.Background(), "a", dir, d, true, f.call); err != nil {
				t.Fatal(err)
			}
			tc.change(f, &d)
			before := len(f.renames)
			if _, err := reconcileTab(context.Background(), "a", dir, d, false, f.call); err != nil {
				t.Fatal(err)
			}
			if before != 1 || len(f.renames) != before {
				t.Fatalf("stale repaint=%v", f.renames)
			}
		})
	}
}

func TestTabStatusEndpointIsolationAndRestore(t *testing.T) {
	dir := t.TempDir()
	a, b := newTabStatusFixture(), newTabStatusFixture()
	d := tabObservation("error")
	if _, err := reconcileTab(context.Background(), "a", dir, d, true, a.call); err != nil {
		t.Fatal(err)
	}
	d.Name = "Other"
	if _, err := reconcileTab(context.Background(), "b", dir, d, true, b.call); err != nil {
		t.Fatal(err)
	}
	d.Mode = "off"
	if _, err := reconcileTab(context.Background(), "a", dir, d, false, a.call); err != nil {
		t.Fatal(err)
	}
	if a.label != "Chat" || b.label != "🟥 Other" {
		t.Fatalf("a=%q b=%q", a.label, b.label)
	}
}

func TestTabStatusShutdownRestoresOnlyCurrentOwner(t *testing.T) {
	for _, change := range []string{"state", "replacement", "closed"} {
		t.Run(change, func(t *testing.T) {
			f := newTabStatusFixture()
			d := tabObservation("blocked")
			dir := t.TempDir()
			if _, err := reconcileTab(context.Background(), "endpoint", dir, d, true, f.call); err != nil {
				t.Fatal(err)
			}
			call := f.call
			want := "🔴 Chat"
			switch change {
			case "state":
				f.state = "idle"
				f.revision++
				want = "Chat"
			case "replacement":
				f.terminal = "replacement"
			case "closed":
				call = func(context.Context, string, ...[]byte) ([]json.RawMessage, error) {
					return nil, &Refusal{Code: "pane_not_found", Message: "pane closed"}
				}
			}
			painter := indicatorPainter{
				socket:          "endpoint",
				lockDir:         dir,
				call:            call,
				tabObservations: []TabDecoration{d},
			}
			if err := painter.restoreTabs(context.Background()); err != nil {
				t.Fatal(err)
			}
			if f.label != want {
				t.Fatalf("shutdown label=%q want %q", f.label, want)
			}
		})
	}
}

func TestTabStatusActualSocketAndSidebar(t *testing.T) {
	f := newTabStatusFixture()
	socket := serve(t, func(line string) string {
		var r struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Error(err)
		}
		bodies, err := f.call(context.Background(), "", []byte(line))
		if err != nil {
			return fmt.Sprintf(`{"id":%q,"ok":false,"error":{"code":"invalid_params","message":%q}}`, r.ID, err.Error())
		}
		return fmt.Sprintf(`{"id":%q,"ok":true,"result":%s}`, r.ID, bodies[0])
	})
	dir := filepath.Join(t.TempDir(), "locks")
	d := tabObservation("blocked")
	validations := 0
	d.Validate = func(context.Context) (bool, error) {
		validations++
		return true, nil
	}
	for _, on := range []bool{true, true, false, false} {
		if _, err := reconcileTab(context.Background(), socket, dir, d, on, Call); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(f.renames, ",") != "🔴 Chat,⭕ Chat" {
		t.Fatalf("wire tab.rename requests=%v", f.renames)
	}
	// A bridge validation costs tmux execs and Herdr calls: one per tab per tick.
	if validations != 4 {
		t.Fatalf("owner validations over 4 ticks = %d, want 4", validations)
	}
	if got, _ := wantedIndicators("blocked", true); got[IndicatorBlocked] != GlyphBlocked {
		t.Fatalf("sidebar changed: %v", got)
	}
}

func TestTabOwnershipPrecedesPaneReads(t *testing.T) {
	for _, restoring := range []bool{false, true} {
		for _, refusal := range []string{"missing proof", "retired", "unreadable"} {
			t.Run(fmt.Sprintf("restore=%t/%s", restoring, refusal), func(t *testing.T) {
				f := newTabStatusFixture()
				d := tabObservation("blocked")
				d.Validate = nil
				if refusal != "missing proof" {
					d.Validate = func(context.Context) (bool, error) {
						if refusal == "unreadable" {
							return false, fmt.Errorf("owner store unreadable")
						}
						return false, nil
					}
				}
				reads := 0
				f.before = func(method string) {
					if method == "pane.get" {
						reads++
					}
				}
				var err error
				if restoring {
					painter := indicatorPainter{
						socket:          "endpoint",
						lockDir:         t.TempDir(),
						call:            f.call,
						tabObservations: []TabDecoration{d},
					}
					err = painter.restoreTabs(context.Background())
				} else {
					_, err = reconcileTab(context.Background(), "endpoint", t.TempDir(), d, true, f.call)
				}
				if reads != 0 || len(f.renames) != 0 {
					t.Fatalf("unproved %s: reads=%d renames=%v err=%v", refusal, reads, f.renames, err)
				}
				if refusal != "retired" && (err == nil || !strings.Contains(err.Error(), "ownership unproved")) {
					t.Fatalf("unproved cause must be named: %v", err)
				}
			})
		}
	}
}

func newTabPainter(t *testing.T, call func(context.Context, string, ...[]byte) ([]json.RawMessage, error)) (
	*indicatorPainter,
	*bytes.Buffer,
) {
	t.Helper()
	stderr := &bytes.Buffer{}
	return &indicatorPainter{
		socket: "endpoint", lockDir: t.TempDir(), stderr: stderr, call: call,
		clock: &steppingClock{now: phaseOn}, reported: map[string]bool{},
	}, stderr
}

// A bridge tab keeps painting while another tab's proof fails, a retained
// tab without pfm's own label record is refused, or a tab's read fails.
func TestTabFailureNeverStopsOtherTabs(t *testing.T) {
	f := newTabStatusFixture()
	unproved := tabObservation("blocked")
	unproved.Tab, unproved.Pane = "w1:t9", "w1:p9"
	unproved.Validate = func(context.Context) (bool, error) { return false, errors.New("bridge client gone") }
	retained := tabObservation("dead")
	retained.Tab, retained.Validate, retained.Retained = "w1:t8", nil, true
	bridge := tabObservation("blocked")
	bridge.Identity = "/fixture/tmux/cc-1:cc-1:s1"
	painter, stderr := newTabPainter(t, f.call)
	for range 2 {
		wrote, err := painter.paintTabs(context.Background(), []TabDecoration{unproved, retained, bridge}, true)
		if err != nil {
			t.Fatalf("ownership refusals stopped the paint: wrote=%t err=%v", wrote, err)
		}
	}
	if strings.Join(f.renames, ",") != "🔴 Chat" {
		t.Fatalf("bridge tab renames=%v; want one blocked marker", f.renames)
	}
	for _, tab := range []string{"w1:t9", "w1:t8"} {
		if strings.Count(stderr.String(), "tab "+tab) != 1 {
			t.Fatalf("refusal of %s must be named once:\n%s", tab, stderr)
		}
	}

	failing := newTabStatusFixture()
	calls := 0
	refuseFirst := func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("pane read refused")
		}
		return failing.call(ctx, socket, lines...)
	}
	painter, stderr = newTabPainter(t, refuseFirst)
	broken := tabObservation("blocked")
	broken.Tab = "w1:t7"
	_, err := painter.paintTabs(context.Background(), []TabDecoration{broken, bridge}, true)
	if err == nil || !strings.Contains(err.Error(), "pane read refused") ||
		!strings.Contains(stderr.String(), "pane read refused") || strings.Join(failing.renames, ",") != "🔴 Chat" {
		t.Fatalf("read failure: err=%v renames=%v log=%s", err, failing.renames, stderr)
	}
}

// After its live owner is gone, a retained tab is acted on only through pfm's
// own label record: it names the view's identity and the tab still shows the
// label pfm rendered; anything else leaves the tab untouched, unread.
func TestRetainedTabActsOnlyOnOwnRecord(t *testing.T) {
	for _, tc := range []struct {
		name, owner, userLabel, want, restored string
		refused                                bool
	}{
		{name: "own-record", owner: "seat1:s1", want: "⬛ Chat", restored: "Chat"},
		{name: "renamed-by-user", owner: "seat1:s1", userLabel: "Mine", want: "Mine", restored: "Mine", refused: true},
		{name: "other-identity", owner: "seat2:s1", want: "🔴 Chat", restored: "🔴 Chat", refused: true},
		{name: "no-record", want: "Chat", restored: "Chat", refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTabStatusFixture()
			painter, stderr := newTabPainter(t, f.call)
			if tc.owner != "" {
				live := tabObservation("blocked")
				live.Identity = tc.owner
				if _, err := painter.paintTabs(context.Background(), []TabDecoration{live}, true); err != nil {
					t.Fatal(err)
				}
			}
			if tc.userLabel != "" {
				f.label = tc.userLabel
			}
			retained := tabObservation("dead")
			retained.Validate, retained.Retained = nil, true
			reads := 0
			f.before = func(method string) {
				if method == "pane.get" {
					reads++
				}
			}
			if _, err := painter.paintTabs(context.Background(), []TabDecoration{retained}, true); err != nil {
				t.Fatalf("retained refusal stopped the paint: %v", err)
			}
			if f.label != tc.want {
				t.Fatalf("retained label=%q want %q", f.label, tc.want)
			}
			if tc.refused != strings.Contains(stderr.String(), "left untouched") {
				t.Fatalf("refused=%t but log:\n%s", tc.refused, stderr)
			}
			if tc.refused && tc.name != "renamed-by-user" && reads != 0 {
				t.Fatalf("a tab without pfm's record was read %d times", reads)
			}
			if err := painter.restoreTabs(context.Background()); err != nil {
				t.Fatalf("retained restore: %v", err)
			}
			if f.label != tc.restored {
				t.Fatalf("restored label=%q want %q", f.label, tc.restored)
			}
		})
	}
}

// A tab.rename Herdr applied but whose answer was lost leaves a pending
// rename in the tab's label record: the next tick knows the label as pfm's own
// and decorates the chat's name once, never the decorated label again.
func TestTabStatusLostRenameReplyNeverStacksThePrefix(t *testing.T) {
	f := newTabStatusFixture()
	dir := t.TempDir()
	d := tabObservation("blocked")
	d.Name = "Chat"
	if _, err := reconcileTab(context.Background(), "endpoint", dir, d, true, f.call); err != nil {
		t.Fatal(err)
	}
	lost := func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
		results, err := f.call(ctx, socket, lines...)
		if err == nil && strings.Contains(string(lines[0]), `"tab.rename"`) {
			return nil, errors.New("read herdr reply: i/o timeout")
		}
		return results, err
	}
	if _, err := reconcileTab(context.Background(), "endpoint", dir, d, false, lost); err == nil {
		t.Fatal("a rename whose answer was lost must be an error")
	}
	for _, on := range []bool{true, false} {
		if _, err := reconcileTab(context.Background(), "endpoint", dir, d, on, f.call); err != nil {
			t.Fatal(err)
		}
		if want := DecoratedTabLabel("Chat", "blocked", "emoji", on, TabGlyphs{}); f.label != want {
			t.Fatalf("after a lost rename answer the tab reads %q, want one prefix: %q", f.label, want)
		}
	}
	// The same holds for a name writer's rename.
	if err := writeSoleTabLabel(
		context.Background(), "endpoint", dir, d.Tab, "Named", true, func() uint64 { return 1 }, lost,
	); err == nil {
		t.Fatal("a name writer's rename whose answer was lost must be an error")
	}
	d.Name = "Named"
	if _, err := reconcileTab(context.Background(), "endpoint", dir, d, true, f.call); err != nil {
		t.Fatal(err)
	}
	if want := DecoratedTabLabel("Named", "blocked", "emoji", true, TabGlyphs{}); f.label != want {
		t.Fatalf("after a name writer's lost rename answer the tab reads %q, want one prefix: %q", f.label, want)
	}
}
