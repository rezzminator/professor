package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

func TestTabStatusLabels(t *testing.T) {
	for _, tc := range []struct {
		state, glyph, text string
		pulse              bool
	}{
		{"working", "🟡", "[W]", false},
		{"idle", "🟢", "[I]", false},
		{"blocked", "🔴", "[B]", true},
		{"error", "🟥", "[E]", true},
		{"dead", "⬛", "[D]", false},
		{"unknown", "❔", "[?]", false},
		{"read failure", "❔", "[?]", false},
	} {
		for _, mode := range []string{"emoji", "text"} {
			for _, on := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/%t", tc.state, mode, on), func(t *testing.T) {
					marker := tc.glyph
					if mode == "text" {
						marker = tc.text
					}
					if tc.pulse && !on {
						marker = "⬜"
						if mode == "text" {
							marker = "[ ]"
						}
					}
					got := DecoratedTabLabel(" \x1b\n日本\t chat ", tc.state, mode, on)
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
					long := DecoratedTabLabel(strings.Repeat("界", 100), tc.state, mode, on)
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
		nameDone <- writeSoleTabLabel(context.Background(), "endpoint", dir, d.Tab, "Newest", func() uint64 { return 1 }, func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
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
	if f.label != "⬜ Newest" {
		t.Fatalf("stale name restored: %q", f.label)
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
	for _, on := range []bool{true, true, false, false} {
		if _, err := reconcileTab(context.Background(), socket, dir, d, on, Call); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(f.renames, ",") != "🔴 Chat,⬜ Chat" {
		t.Fatalf("wire tab.rename requests=%v", f.renames)
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
