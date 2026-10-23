package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Every state plays its frames in order, wrapping past the last: emoji mode
// the default frames, text mode the three-rune markers; each frame keeps the
// cleaned base and the label cap.
func TestTabStatusLabels(t *testing.T) {
	for _, tc := range []struct {
		state       string
		emoji, text []string
	}{
		{"working", []string{"🔵"}, []string{"[W]"}},
		{"idle", []string{"⚪"}, []string{"[I]"}},
		{"blocked", []string{"🔴", "⭕"}, []string{"[B]", "[ ]"}},
		{"done", []string{"🟡"}, []string{"[C]"}},
		{"error", []string{"🟥", "❌"}, []string{"[E]", "[ ]"}},
		{"dead", []string{"⬛"}, []string{"[D]"}},
		{"unknown", []string{"❔"}, []string{"[?]"}},
		{"read failure", []string{"❔"}, []string{"[?]"}},
	} {
		for _, mode := range []string{"emoji", "text"} {
			frames, wantWidth := tc.emoji, 2
			if mode == "text" {
				frames, wantWidth = tc.text, 3
			}
			for tick := range len(frames) + 1 {
				t.Run(fmt.Sprintf("%s/%s/%d", tc.state, mode, tick), func(t *testing.T) {
					marker := frames[tick%len(frames)]
					if frame := tabFrame(tc.state, mode, int64(tick), TabGlyphs{}); frame != tick%len(frames) {
						t.Fatalf("tick %d plays frame %d, want %d", tick, frame, tick%len(frames))
					}
					got := DecoratedTabLabel(" \x1b\n日本\t chat ", tc.state, mode, tick, TabGlyphs{})
					if got != marker+" 日本 chat" {
						t.Fatalf("label=%q want %q", got, marker+" 日本 chat")
					}
					if ansi.StringWidth(marker) != wantWidth {
						t.Fatalf("marker width=%d want %d", ansi.StringWidth(marker), wantWidth)
					}
					if mode == "emoji" && utf8.RuneCountInString(marker) != 1 {
						t.Fatalf("default marker %q is not a single code point", marker)
					}
					long := DecoratedTabLabel(strings.Repeat("界", 100), tc.state, mode, tick, TabGlyphs{})
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
	focused                         bool
	revision                        uint64
	renames                         []string
	before                          func(string)
}

func newTabStatusFixture() *tabStatusFixture {
	return &tabStatusFixture{
		label:    "Chat",
		terminal: "term1",
		state:    "blocked",
		session:  "s1",
		count:    1,
		revision: 7,
		focused:  true,
	}
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
				`{"type":"pane_info","pane":{"pane_id":"w1:p1","terminal_id":%q,"workspace_id":"w1","tab_id":"w1:t1","focused":%t,"revision":%d,"agent_status":%q,"agent_session":{"source":"pfm","agent":"claude","kind":"id","value":%q}}}`,
				f.terminal,
				f.focused,
				f.revision,
				f.state,
				f.session,
			)
		case "tab.get":
			body = fmt.Sprintf(
				`{"type":"tab_info","tab":{"tab_id":"w1:t1","workspace_id":"w1","number":1,"focused":%t,"label":%q,"pane_count":%d,"agent_status":%q}}`,
				f.focused,
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

func TestTabStatusDoneUntilFirstFocus(t *testing.T) {
	f := newTabStatusFixture()
	f.state, f.focused = "done", false
	d := tabObservation("idle") // Stop/idle from the transcript; Herdr owns unseen completion.
	d.AgentStatus, d.Mode = "done", "text"
	dir := t.TempDir()
	for tick, focused := range []bool{false, false, true, false} {
		f.focused = focused
		if tick >= 2 {
			// Herdr's effective done becomes idle when focus marks it seen.
			f.state, d.AgentStatus = "idle", "idle"
		}
		if _, err := reconcileTab(context.Background(), "endpoint", dir, d, int64(tick), f.call); err != nil {
			t.Fatal(err)
		}
		want := "[I] Chat"
		switch tick {
		case 0, 1:
			want = "[C] Chat"
		}
		if f.label != want {
			t.Fatalf("tick %d focused=%v label=%q want=%q", tick, focused, f.label, want)
		}
	}
	// A later completion gets its own unseen state, even if it says the same thing.
	f.state, d.AgentStatus = "done", "done"
	if _, err := reconcileTab(context.Background(), "endpoint", dir, d, 0, f.call); err != nil {
		t.Fatal(err)
	}
	if f.label != "[C] Chat" {
		t.Fatalf("new completion did not become done: %q", f.label)
	}
}

func TestTabStatusFocusedCompletionIsAlreadySeen(t *testing.T) {
	f := newTabStatusFixture()
	f.state = "done"
	d := tabObservation("idle")
	d.AgentStatus, d.Mode = "done", "text"
	if _, err := reconcileTab(context.Background(), "endpoint", t.TempDir(), d, 0, f.call); err != nil {
		t.Fatal(err)
	}
	if f.label != "[I] Chat" {
		t.Fatalf("focused completed tab was marked unseen: %q", f.label)
	}
}

func TestTabStatusPulseAndNoop(t *testing.T) {
	for _, state := range []string{"working", "idle", "blocked", "done", "error", "dead"} {
		t.Run(state, func(t *testing.T) {
			f := newTabStatusFixture()
			f.focused = false
			dir := t.TempDir()
			d := tabObservation(state)
			for _, tick := range []int64{0, 0, 1, 1, 0} {
				if _, err := reconcileTab(context.Background(), "endpoint", dir, d, tick, f.call); err != nil {
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
	if _, err := reconcileTab(context.Background(), "endpoint", dir, d, 0, f.call); err != nil {
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
		_, err := reconcileTab(context.Background(), "endpoint", dir, d, 1, f.call)
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
		if _, err := reconcileTab(context.Background(), "endpoint", dir, d, 0, f.call); err != nil {
			t.Fatal(err)
		}
		if want := DecoratedTabLabel(want, "blocked", "emoji", 0, TabGlyphs{}); f.label != want {
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
	if want := DecoratedTabLabel("Mine", "blocked", "emoji", 0, TabGlyphs{}); f.label != want {
		t.Fatalf("after a re-announce the tab reads %q, want the user's %q", f.label, want)
	}
	// An explicit pfm rename wins over the user's label ...
	if err := writeSoleTabLabel(
		context.Background(), "endpoint", dir, d.Tab, "Explicit", true, func() uint64 { return 1 }, f.call,
	); err != nil {
		t.Fatal(err)
	}
	if want := DecoratedTabLabel("Explicit", "blocked", "emoji", 0, TabGlyphs{}); f.label != want {
		t.Fatalf("after an explicit rename the tab reads %q, want %q", f.label, want)
	}
	// ... without the painter's next tick, still seeing the older composed name, undoing it ...
	tick("Fourth", "Explicit")
	// ... and the tab follows the chat's name again.
	tick("Fifth", "Fifth")

	legacy := newTabStatusFixture()
	legacyDir := t.TempDir()
	rendered := DecoratedTabLabel("Old", "blocked", "emoji", 0, TabGlyphs{})
	legacy.label = rendered
	if err := withTabLabelLock(context.Background(), legacyDir, "endpoint", d.Tab, func(path string) error {
		return writeTabLabel(path, tabLabelRecord{
			Base: "Old", Rendered: rendered, Identity: d.Identity, State: "blocked", Mode: "emoji",
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
		if _, err := reconcileTab(context.Background(), "endpoint", dir, d, 0, f.call); err != nil {
			t.Fatal(err)
		}
		if want := DecoratedTabLabel(want, "blocked", "emoji", 0, TabGlyphs{}); f.label != want {
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
	rendered := DecoratedTabLabel("Mine", "blocked", "emoji", 0, TabGlyphs{})
	legacy.label = rendered
	if err := withTabLabelLock(context.Background(), legacyDir, "endpoint", d.Tab, func(path string) error {
		return writeTabLabel(path, tabLabelRecord{
			Base: "Mine", Rendered: rendered, Identity: d.Identity, State: "blocked", Mode: "emoji",
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

// A launch writes the tab before the painter has seen a source name. A
// /rename arriving before that first paint must replace the launch label,
// not merely become Seen while Base stays stuck on the launch name forever.
func TestTabStatusRenameBeforeFirstPaintAndWithoutEvent(t *testing.T) {
	f := newTabStatusFixture()
	f.state = "idle"
	painter, step, _ := newTestPainter(t, &fakeIndicatorHerdr{}, phaseOn)
	indicatorCall := painter.call
	painter.call = func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
		if bytes.Contains(lines[0], []byte(`"pane.list"`)) {
			return indicatorCall(ctx, socket, lines...)
		}
		return f.call(ctx, socket, lines...)
	}
	var seq uint64
	next := func() uint64 { seq++; return seq }
	if err := writeSoleTabLabel(context.Background(), painter.socket, painter.lockDir,
		"w1:t1", "Launch", false, next, f.call); err != nil {
		t.Fatal(err)
	}
	name := "Renamed" // The first source row already carries the /rename.
	polls := 0
	painter.tabs = func(context.Context, string) ([]TabDecoration, error) {
		polls++
		d := tabObservation("idle")
		d.AgentStatus = "idle"
		d.Name, d.UserName, d.UserKnown = name, name, true
		return []TabDecoration{d}, nil
	}
	step.onSleep = func(context.Context) error {
		want := "Renamed"
		if polls > 1 {
			want = "Renamed again"
		}
		if want := DecoratedTabLabel(want, "idle", "emoji", 0, TabGlyphs{}); f.label != want {
			t.Errorf("paint %d label = %q, want %q", polls, f.label, want)
		}
		if polls == 1 {
			// Only the source name changes: no Herdr status or focus event.
			name = "Renamed again"
			return nil
		}
		return context.Canceled
	}
	if err := painter.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if polls != 2 {
		t.Fatalf("source polls = %d, want the first paint and one eventless tick", polls)
	}
}

func TestTabStatusExplicitRenameBeforeFirstPaintWaitsForSource(t *testing.T) {
	f := newTabStatusFixture()
	dir := t.TempDir()
	d := tabObservation("blocked")
	if err := writeSoleTabLabel(context.Background(), "endpoint", dir, d.Tab,
		"Chosen", true, func() uint64 { return 1 }, f.call); err != nil {
		t.Fatal(err)
	}
	// A session start re-announces the same name before the first paint: the
	// explicit rename stays protected from the stale source title.
	if err := writeSoleTabLabel(context.Background(), "endpoint", dir, d.Tab,
		"Chosen", false, func() uint64 { return 1 }, f.call); err != nil {
		t.Fatal(err)
	}
	for _, tick := range []struct{ source, want string }{
		{"Old source", "Chosen"}, // The engine has not flushed /rename yet.
		{"Chosen", "Chosen"},
		{"Later rename", "Later rename"},
	} {
		d.Name, d.UserName, d.UserKnown = tick.source, tick.source, true
		if _, err := reconcileTab(context.Background(), "endpoint", dir, d, 0, f.call); err != nil {
			t.Fatal(err)
		}
		if want := DecoratedTabLabel(tick.want, "blocked", "emoji", 0, TabGlyphs{}); f.label != want {
			t.Fatalf("source %q: label = %q, want %q", tick.source, f.label, want)
		}
	}
}

func TestTabStatusAutomaticTitleBeforeFirstPaintReplacesLaunchLabel(t *testing.T) {
	f := newTabStatusFixture()
	dir := t.TempDir()
	d := tabObservation("idle")
	if err := writeSoleTabLabel(context.Background(), "endpoint", dir, d.Tab,
		"New Claude chat", false, func() uint64 { return 1 }, f.call); err != nil {
		t.Fatal(err)
	}
	for _, tick := range []struct{ source, want string }{
		{"CPU pegging investigation", "CPU pegging investigation"}, // AI title, no user name.
		{"CPU pegging investigation", "CPU pegging investigation"},
		{"Later title", "Later title"},
	} {
		d.Name, d.UserName, d.UserKnown = tick.source, "", true
		if _, err := reconcileTab(context.Background(), "endpoint", dir, d, 0, f.call); err != nil {
			t.Fatal(err)
		}
		if want := DecoratedTabLabel(tick.want, "idle", "emoji", 0, TabGlyphs{}); f.label != want {
			t.Fatalf("source %q: label = %q, want %q", tick.source, f.label, want)
		}
	}
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
			if _, err := reconcileTab(context.Background(), "a", dir, d, 0, f.call); err != nil {
				t.Fatal(err)
			}
			tc.change(f, &d)
			before := len(f.renames)
			if _, err := reconcileTab(context.Background(), "a", dir, d, 1, f.call); err != nil {
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
	if _, err := reconcileTab(context.Background(), "a", dir, d, 0, a.call); err != nil {
		t.Fatal(err)
	}
	d.Name = "Other"
	if _, err := reconcileTab(context.Background(), "b", dir, d, 0, b.call); err != nil {
		t.Fatal(err)
	}
	d.Mode = "off"
	if _, err := reconcileTab(context.Background(), "a", dir, d, 1, a.call); err != nil {
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
			if _, err := reconcileTab(context.Background(), "endpoint", dir, d, 0, f.call); err != nil {
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
	for _, tick := range []int64{0, 0, 1, 1} {
		if _, err := reconcileTab(context.Background(), socket, dir, d, tick, Call); err != nil {
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
					_, err = reconcileTab(context.Background(), "endpoint", t.TempDir(), d, 0, f.call)
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
		wrote, err := painter.paintTabs(context.Background(), []TabDecoration{unproved, retained, bridge}, 0)
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
	_, err := painter.paintTabs(context.Background(), []TabDecoration{broken, bridge}, 0)
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
				if _, err := painter.paintTabs(context.Background(), []TabDecoration{live}, 0); err != nil {
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
			if _, err := painter.paintTabs(context.Background(), []TabDecoration{retained}, 0); err != nil {
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
	if _, err := reconcileTab(context.Background(), "endpoint", dir, d, 0, f.call); err != nil {
		t.Fatal(err)
	}
	lost := func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
		results, err := f.call(ctx, socket, lines...)
		if err == nil && strings.Contains(string(lines[0]), `"tab.rename"`) {
			return nil, errors.New("read herdr reply: i/o timeout")
		}
		return results, err
	}
	if _, err := reconcileTab(context.Background(), "endpoint", dir, d, 1, lost); err == nil {
		t.Fatal("a rename whose answer was lost must be an error")
	}
	for _, tick := range []int64{0, 1} {
		if _, err := reconcileTab(context.Background(), "endpoint", dir, d, tick, f.call); err != nil {
			t.Fatal(err)
		}
		if want := DecoratedTabLabel("Chat", "blocked", "emoji", int(tick), TabGlyphs{}); f.label != want {
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
	if _, err := reconcileTab(context.Background(), "endpoint", dir, d, 0, f.call); err != nil {
		t.Fatal(err)
	}
	if want := DecoratedTabLabel("Named", "blocked", "emoji", 0, TabGlyphs{}); f.label != want {
		t.Fatalf("after a name writer's lost rename answer the tab reads %q, want one prefix: %q", f.label, want)
	}
}

// tabPainterFixture serves the painter's list and report calls from an empty
// indicator fake and every pane and tab call from the tab fixture.
func tabPainterFixture(t *testing.T, tabs *tabStatusFixture) (*indicatorPainter, *steppingClock) {
	t.Helper()
	lists := &fakeIndicatorHerdr{}
	painter, stepping, _ := newTestPainter(t, lists, phaseOn)
	painter.call = func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
		var request struct {
			Method string `json:"method"`
		}
		if len(lines) > 0 && json.Unmarshal(lines[0], &request) == nil &&
			(request.Method == "pane.list" || request.Method == "workspace.list" ||
				request.Method == "pane.report_metadata" || request.Method == "workspace.report_metadata") {
			return lists.call(ctx, socket, lines...)
		}
		return tabs.call(ctx, socket, lines...)
	}
	return painter, stepping
}

var blockedFrames = []string{"🔴", "⭕"}

// A blocked tab plays every attention frame, one per half-second tick,
// and the painter stops once no decorated tab is left.
func TestTabPainterAnimatesBlockedAndStopsWhenNoTabAnimates(t *testing.T) {
	f := newTabStatusFixture()
	painter, stepping := tabPainterFixture(t, f)
	polls := 0
	painter.tabs = func(context.Context, string) ([]TabDecoration, error) {
		polls++
		if polls > len(blockedFrames) {
			return nil, nil
		}
		d := tabObservation("blocked")
		d.Glyphs.Blocked = blockedFrames
		return []TabDecoration{d}, nil
	}
	stepping.onSleep = func(context.Context) error {
		if len(stepping.sleeps) > 20 {
			return context.Canceled // a painter that never stops ends here
		}
		return nil
	}
	if err := painter.run(context.Background()); err != nil {
		t.Fatalf("run = %v", err)
	}
	var want []string
	for _, frame := range blockedFrames {
		want = append(want, frame+" Chat")
	}
	if !slices.Equal(f.renames, want) {
		t.Fatalf("renames = %q, want every blocked frame once in order %q", f.renames, want)
	}
	if polls != len(blockedFrames)+1 || len(stepping.sleeps) != len(blockedFrames) {
		t.Fatalf(
			"polls %d sleeps %d: want the painter stopped at the first tick with no tab",
			polls,
			len(stepping.sleeps),
		)
	}
}

// A label caught on any frame of its state is pfm's own: the next tick paints
// its own frame over it, never stacking a second prefix as if a user renamed it.
func TestTabLabelOnAnyFrameIsPfmsOwn(t *testing.T) {
	for i, caught := range blockedFrames {
		f := newTabStatusFixture()
		painter, stepping := tabPainterFixture(t, f)
		polls := 0
		painter.tabs = func(context.Context, string) ([]TabDecoration, error) {
			polls++
			if polls > 2 {
				return nil, nil
			}
			d := tabObservation("blocked")
			d.Glyphs.Blocked = blockedFrames
			return []TabDecoration{d}, nil
		}
		stepping.onSleep = func(context.Context) error {
			if len(stepping.sleeps) == 1 {
				f.label = caught + " Chat" // another painter's frame, mid-animation
			}
			return nil
		}
		if err := painter.run(context.Background()); err != nil {
			t.Fatalf("frame %d: run = %v", i, err)
		}
		if f.label != "⭕ Chat" {
			t.Errorf("frame %d: a label caught on %q reads %q after the next tick, want pfm's frame %q",
				i, caught, f.label, "⭕ Chat")
		}
		for _, rename := range f.renames {
			if strings.Count(rename, " ") != 1 {
				t.Errorf("frame %d: rename %q stacks a second prefix", i, rename)
			}
		}
	}
}
