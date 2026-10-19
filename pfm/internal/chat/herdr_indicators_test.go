package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHerdrTabProviderNativeAndMultipleTmuxViews(t *testing.T) {
	var inspected []string
	provider := herdrTabProvider{
		scan: func(context.Context) (FleetScan, error) {
			return FleetScan{Rows: []compose.Row{
				{
					Backend:       "herdr",
					SeatID:        "seat",
					ID:            "s1",
					Socket:        "endpoint",
					PaneID:        "w1:p1",
					Name:          "Native",
					UserName:      "Native",
					UserNameKnown: true,
					Kind:          compose.LiveClaude,
				},
				{ID: "s2", Socket: "tmux", Name: "Tmux", Kind: compose.LiveClaude},
			}}, nil
		},
		inspect: func(_ context.Context, c headless.Chat, _ time.Time) (headless.Status, error) {
			inspected = append(inspected, c.ID)
			return headless.Status{State: "working"}, nil
		},
		views: func(_ context.Context, c headless.Chat, _ string) ([]herdr.TabDecoration, error) {
			if c.Backend == "herdr" {
				return []herdr.TabDecoration{{Pane: c.Pane, Identity: "native"}}, nil
			}
			return []herdr.TabDecoration{{Pane: "w1:p2", Identity: "tmux"}, {Pane: "w1:p3", Identity: "tmux"}}, nil
		}, mode: func() (string, error) { return "emoji", nil }, warn: &bytes.Buffer{},
	}
	got, err := provider.tabs(context.Background(), "endpoint")
	if err != nil || len(got) != 3 || len(inspected) != 2 {
		t.Fatalf("tabs=%+v inspected=%v err=%v", got, inspected, err)
	}
	for _, d := range got {
		if d.State != "working" || d.Mode != "emoji" || d.Name == "" {
			t.Fatalf("decoration=%+v", d)
		}
		// The chat's user name rides with its name: the painter tells a
		// /rename from an automatic name by it.
		want := map[string]string{"native": "Native", "tmux": ""}[d.Identity]
		if d.UserName != want || d.UserKnown != (d.Identity == "native") {
			t.Fatalf("decoration %s user name = %q (known %v), want %q", d.Identity, d.UserName, d.UserKnown, want)
		}
	}
}

func TestHerdrTabProviderMissedHooksAndNewPrompt(t *testing.T) {
	// Inspector is the sole status authority. Repeated polls must not cache a
	// turn verdict by chat name: errors/death need no hook; a prompt replaces it.
	for _, tc := range []struct {
		name   string
		states []string
	}{
		{"error without idle hook", []string{"working", "error"}},
		{"tool failure continues", []string{"working", "working"}},
		{"abrupt death", []string{"working", "dead"}},
		{"new prompt outranks error", []string{"error", "working"}},
		{"replacement session", []string{"error", "idle"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := 0
			provider := herdrTabProvider{
				scan: func(context.Context) (FleetScan, error) {
					return FleetScan{
						Rows: []compose.Row{{ID: fmt.Sprint(i), Name: "Chat", Kind: compose.LiveClaude}},
					}, nil
				},
				inspect: func(context.Context, headless.Chat, time.Time) (headless.Status, error) {
					return headless.Status{State: tc.states[i]}, nil
				},
				views: func(_ context.Context, c headless.Chat, _ string) ([]herdr.TabDecoration, error) {
					return []herdr.TabDecoration{{Pane: "w1:p1", Identity: c.ID}}, nil
				},
				mode: func() (string, error) { return "emoji", nil }, warn: &bytes.Buffer{},
			}
			for i = 0; i < len(tc.states); i++ {
				got, err := provider.tabs(context.Background(), "endpoint")
				if err != nil || len(got) != 1 || got[0].State != tc.states[i] || got[0].Identity != fmt.Sprint(i) {
					t.Fatalf("poll %d tabs=%+v err=%v", i, got, err)
				}
			}
		})
	}
}

func TestHerdrTabProviderReadFailureIsVisible(t *testing.T) {
	warnings := &bytes.Buffer{}
	provider := herdrTabProvider{
		scan: func(context.Context) (FleetScan, error) {
			return FleetScan{Rows: []compose.Row{{ID: "s1", Name: "Chat", Kind: compose.LiveClaude}}}, nil
		},
		inspect: func(context.Context, headless.Chat, time.Time) (headless.Status, error) {
			return headless.Status{}, fmt.Errorf("transcript denied")
		},
		views: func(context.Context, headless.Chat, string) ([]herdr.TabDecoration, error) {
			return []herdr.TabDecoration{{Pane: "w1:p1", Identity: "s1"}}, nil
		},
		mode: func() (string, error) { return "emoji", nil }, warn: warnings,
	}
	got, err := provider.tabs(context.Background(), "endpoint")
	if err != nil || len(got) != 1 || got[0].State != "unknown" ||
		!strings.Contains(warnings.String(), "transcript denied") {
		t.Fatalf("tabs=%+v err=%v warnings=%s", got, err, warnings)
	}
	provider.scan = func(context.Context) (FleetScan, error) { return FleetScan{}, fmt.Errorf("fleet denied") }
	if _, err := provider.tabs(
		context.Background(),
		"endpoint",
	); err == nil ||
		!strings.Contains(err.Error(), "fleet denied") {
		t.Fatalf("fleet failure=%v", err)
	}
}

func TestHerdrTabStartupFailureIsVisible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pfm.config.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"chat":{"herdrTabStatus":"invalid"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_CONFIG", path)
	warnings := &bytes.Buffer{}
	err := PaintHerdrIndicators(context.Background(), "endpoint", t.TempDir(), warnings)
	if err == nil || !strings.Contains(warnings.String(), "herdrTabStatus must be") {
		t.Fatalf("startup failure err=%v diagnostic=%q", err, warnings.String())
	}
}

func TestHerdrTabViewReaderUsesActualClientsAndGeneration(t *testing.T) {
	focused := false
	panes := []herdr.TabPane{
		{
			Pane:      "w1:p1",
			Terminal:  "term1",
			Tab:       "w1:t1",
			Workspace: "w1",
			Focused:   &focused,
			Session:   []byte(`{"source":"pfm","agent":"claude","kind":"id","value":"s1"}`),
		},
		{
			Pane:      "w2:p2",
			Terminal:  "term2",
			Tab:       "w2:t2",
			Workspace: "w2",
			Focused:   &focused,
			Session:   []byte(`{"source":"pfm","agent":"claude","kind":"id","value":"s1"}`),
		},
	}
	generation := `{"session_id":"s1","seq":1,"clients":{"10":{"pane":"stale","socket":"endpoint"}}}`
	reader := tabViewReader{
		runtime: &config.Runtime{Paths: paths.Values{TmuxDir: "/fixture/tmux"}},
		tmux: func(_ context.Context, socket string, args ...string) (string, error) {
			if socket != "/fixture/tmux/tmux" {
				return "", fmt.Errorf("tmux -S %s %s: exit status 1", socket, args[0])
			}
			if args[0] == "list-clients" {
				return "10\n11\n12", nil
			}
			return generation, nil
		},
		resolve: func(_ context.Context, pid int) (herdr.Location, bool, error) {
			switch pid {
			case 10:
				return herdr.Location{Socket: "endpoint", Pane: "w1:p1", Terminal: "term1"}, true, nil
			case 11:
				return herdr.Location{Socket: "endpoint", Pane: "w2:p2", Terminal: "term2"}, true, nil
			default:
				return herdr.Location{Socket: "other", Pane: "w1:p1", Terminal: "term1"}, true, nil
			}
		},
		bridge: func(_ context.Context, socket, pane string) (call.HerdrBridge, error) {
			if socket != "endpoint" {
				return call.HerdrBridge{}, fmt.Errorf("bridge asked on %s", socket)
			}
			return call.HerdrBridge{
				TmuxSocket: "/fixture/tmux/tmux",
				Session:    "Chat",
				ClientPID:  map[string]int{"w1:p1": 10, "w2:p2": 11}[pane],
			}, nil
		},
	}
	target := headless.Chat{ID: "s1", Live: true, Socket: "tmux", Session: "Chat"}
	got, err := reader.views(context.Background(), target, "endpoint", panes)
	if err != nil || len(got) != 2 || got[0].Pane != "w1:p1" || got[1].Pane != "w2:p2" {
		t.Fatalf("actual views=%+v err=%v", got, err)
	}
	if valid, err := got[0].Validate(context.Background()); err != nil || !valid {
		t.Fatalf("current generation valid=%t err=%v", valid, err)
	}
	generation = `{"session_id":"s2","seq":2}`
	if valid, err := got[0].Validate(context.Background()); err != nil || valid {
		t.Fatalf("replaced generation valid=%t err=%v", valid, err)
	}
	if got, err := reader.views(context.Background(), target, "endpoint", panes); err != nil || len(got) != 0 {
		t.Fatalf("replacement views=%+v err=%v", got, err)
	}
	noServer := headless.Chat{ID: "s3", Live: true, Session: "Chat"}
	if got, err := reader.views(context.Background(), noServer, "endpoint", panes); err != nil || len(got) != 0 {
		t.Fatalf("a live row with no tmux socket has no tab: views=%+v err=%v", got, err)
	}
	target.Live = false
	got, err = reader.views(context.Background(), target, "endpoint", panes)
	if err != nil || len(got) != 2 {
		t.Fatalf("original retained dead views=%+v err=%v", got, err)
	}
	for i := range got {
		if !got[i].Retained || got[i].Validate != nil || got[i].Identity != "tmux:Chat:s1" {
			t.Fatalf("dead view %d must be retained, revalidated only by pfm's label record: %+v", i, got[i])
		}
	}
	panes[0].Terminal = "replacement"
	panes[1].Session = []byte(`{"source":"pfm","agent":"claude","kind":"id","value":"s2"}`)
	got, err = reader.views(context.Background(), target, "endpoint", panes)
	if err != nil || len(got) != 0 {
		t.Fatalf("replacement retained old chat=%+v err=%v", got, err)
	}
}

func TestHerdrTabSessionProducerForms(t *testing.T) {
	target := headless.Chat{ID: "s1", Path: "/fixture/session.jsonl"}
	for _, tc := range []struct {
		kind, value   string
		matches, fail bool
	}{
		{"id", "s1", true, false},
		{"id", "s2", false, false},
		{"path", target.Path, true, false},
		{"path", "/fixture/other.jsonl", false, false},
		{"future", "s1", false, true},
	} {
		t.Run(tc.kind+tc.value, func(t *testing.T) {
			body := fmt.Sprintf(`{"source":"pfm","agent":"claude","kind":%q,"value":%q}`, tc.kind, tc.value)
			matches, err := tabPaneSession(herdr.TabPane{Session: []byte(body)}, target)
			if matches != tc.matches || (err != nil) != tc.fail {
				t.Fatalf("session=%s matches=%t err=%v", body, matches, err)
			}
		})
	}
}

// A native tab is painted only for a proved owner. An unproved or failed
// ownership proof leaves that tab untouched with a diagnostic, at discovery and
// at the final validation, and never aborts the paint of every other tab.
func TestHerdrTabNativeOwnershipProof(t *testing.T) {
	testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, values)
	if err := db.PutSeat(ctx, fleetdb.Seat{
		SeatID:         "seat1",
		SessionID:      "s1",
		Engine:         "cc",
		Backend:        "herdr",
		HerdrSocket:    "endpoint",
		HerdrPane:      "w1:p1",
		HerdrWorkspace: "w1",
		State:          "live",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	focused := false
	pane := herdr.TabPane{
		Pane:      "w1:p1",
		Terminal:  "term1",
		Tab:       "w1:t1",
		Workspace: "w1",
		Focused:   &focused,
		Session:   []byte(`{"source":"pfm","agent":"claude","kind":"id","value":"s1"}`),
	}
	target := headless.Chat{ID: "s1", SeatID: "seat1", Backend: "herdr", Socket: "endpoint", Pane: "w1:p1", Live: true}
	for _, tc := range []struct {
		name          string
		owned, engine bool
		engineErr     error
		proof         error
		views         int
		state         string
	}{
		{"proved owner, engine in foreground", true, true, nil, nil, 1, ""},
		// A supervisor outlives an abruptly dead engine until it retires the seat.
		{"proved owner, engine gone", true, false, nil, nil, 1, "dead"},
		{"proved supervisor death", false, true, nil, nil, 1, "dead"},
		{"proved owner, foreground unavailable", true, false, errors.New("foreground evidence unavailable"), nil, 1, "unknown"},
		{"ownership unproved", false, true, nil, errors.New("seat seat1 supervisor ownership unproved"), 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warnings := &bytes.Buffer{}
			proof := tc.proof
			engineErr := tc.engineErr
			reader := tabViewReader{
				runtime: &config.Runtime{Paths: values},
				warn:    warnings,
				prove: func(_ context.Context, seat fleetdb.Seat, _ paths.Values) (fleetdb.Seat, bool, error) {
					return seat, tc.owned, proof
				},
				engine: func(context.Context, fleetdb.Seat) (bool, error) { return tc.engine, engineErr },
			}
			got, err := reader.views(ctx, target, "endpoint", []herdr.TabPane{pane})
			if err != nil || len(got) != tc.views {
				t.Fatalf("views=%+v err=%v warnings=%q", got, err, warnings)
			}
			if tc.proof != nil {
				if !strings.Contains(warnings.String(), tc.proof.Error()) {
					t.Fatalf("unproved owner diagnostic=%q", warnings)
				}
				return
			}
			if got[0].State != tc.state || got[0].Identity != "seat1:s1" ||
				(tc.engineErr == nil && warnings.Len() != 0) {
				t.Fatalf("decoration=%+v warnings=%q", got[0], warnings)
			}
			if tc.engineErr != nil {
				if !strings.Contains(warnings.String(), tc.engineErr.Error()) {
					t.Fatalf("missing foreground diagnostic: %s", warnings)
				}
				provider := herdrTabProvider{
					scan: func(context.Context) (FleetScan, error) {
						return FleetScan{
							Rows: []compose.Row{
								{
									Backend: "herdr",
									SeatID:  "seat1",
									ID:      "s1",
									Socket:  "endpoint",
									PaneID:  "w1:p1",
									Kind:    compose.LiveClaude,
								},
							},
						}, nil
					},
					views: func(ctx context.Context, c headless.Chat, socket string) ([]herdr.TabDecoration, error) {
						return reader.views(ctx, c, socket, []herdr.TabPane{pane})
					},
					inspect: func(context.Context, headless.Chat, time.Time) (headless.Status, error) {
						return headless.Status{State: "idle"}, nil
					},
					mode: func() (string, error) { return "emoji", nil }, warn: warnings,
				}
				if tabs, err := provider.tabs(
					ctx,
					"endpoint",
				); err != nil || len(tabs) != 1 ||
					tabs[0].State != "unknown" {
					t.Fatalf("unavailable evidence became known state: tabs=%+v err=%v", tabs, err)
				}
			}
			if valid, err := got[0].Validate(ctx); err != nil || !valid {
				t.Fatalf("proved owner valid=%t err=%v", valid, err)
			}
			if tc.engineErr != nil {
				engineErr = nil
				if valid, err := got[0].Validate(ctx); err != nil || valid {
					t.Fatalf("changed foreground evidence must invalidate unknown: valid=%t err=%v", valid, err)
				}
				engineErr = tc.engineErr
			}
			proof = errors.New("seat seat1 supervisor birth changed during proof")
			if valid, err := got[0].Validate(ctx); err != nil || valid ||
				!strings.Contains(warnings.String(), "supervisor birth changed") {
				t.Fatalf("unproved at validation valid=%t err=%v warnings=%q", valid, err, warnings)
			}
		})
	}
}

// Herdr emits no agent_session for a pfm-native pane, so a native tab is
// painted when the proved seat's own Herdr terminal is the pane's terminal; a
// pane naming another chat, a seat with no recorded terminal, or another
// terminal leaves the tab untouched, and a terminal changed since discovery
// fails the final validation.
func TestHerdrTabNativePaneWithoutAgentSession(t *testing.T) {
	for _, tc := range []struct {
		name, seatTerminal string
		session            string
		views              int
	}{
		{name: "the seat's terminal proves the pane", seatTerminal: "term1", views: 1},
		{name: "a seat with no recorded terminal", seatTerminal: "", views: 0},
		{name: "another terminal", seatTerminal: "term2", views: 0},
		{
			name: "an agent_session naming another chat", seatTerminal: "term1", views: 0,
			session: `{"source":"pfm","agent":"claude","kind":"id","value":"s2"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			putSeat := func(terminal string) {
				t.Helper()
				db := fleetdb.OpenSharedState(ctx, values)
				if err := db.PutSeat(ctx, fleetdb.Seat{
					SeatID: "seat1", SessionID: "s1", Engine: "cc", Backend: "herdr", HerdrSocket: "endpoint",
					HerdrPane: "w1:p1", HerdrWorkspace: "w1", HerdrTerminalID: terminal, State: "live",
				}); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			putSeat(tc.seatTerminal)
			pane := herdr.TabPane{Pane: "w1:p1", Terminal: "term1", Tab: "w1:t1", Workspace: "w1"}
			if tc.session != "" {
				pane.Session = []byte(tc.session)
			}
			target := headless.Chat{
				ID:      "s1",
				SeatID:  "seat1",
				Backend: "herdr",
				Socket:  "endpoint",
				Pane:    "w1:p1",
				Live:    true,
			}
			warnings := &bytes.Buffer{}
			reader := tabViewReader{
				runtime: &config.Runtime{Paths: values},
				warn:    warnings,
				prove: func(_ context.Context, seat fleetdb.Seat, _ paths.Values) (fleetdb.Seat, bool, error) {
					return seat, true, nil
				},
				engine: func(context.Context, fleetdb.Seat) (bool, error) { return true, nil },
			}
			got, err := reader.views(ctx, target, "endpoint", []herdr.TabPane{pane})
			if err != nil || len(got) != tc.views {
				t.Fatalf("views=%+v err=%v warnings=%q", got, err, warnings)
			}
			if tc.views == 0 {
				return
			}
			if got[0].Identity != "seat1:s1" || got[0].Terminal != "term1" {
				t.Fatalf("decoration=%+v", got[0])
			}
			if valid, err := got[0].Validate(ctx); err != nil || !valid {
				t.Fatalf("proved native pane valid=%t err=%v", valid, err)
			}
			putSeat("term9")
			if valid, err := got[0].Validate(ctx); err != nil || valid {
				t.Fatalf("a seat moved to another terminal must invalidate: valid=%t err=%v", valid, err)
			}
		})
	}
}

// A live bridge tab is acted on only while the bridge proof names the very
// tmux client and chat session the view was built from.
func TestHerdrTabBridgeProofGatesLiveViews(t *testing.T) {
	focused := false
	panes := []herdr.TabPane{{
		Pane: "w1:p1", Terminal: "term1", Tab: "w1:t1", Workspace: "w1", Focused: &focused,
		Session: []byte(`{"source":"pfm","agent":"claude","kind":"id","value":"s1"}`),
	}}
	for _, tc := range []struct {
		name    string
		proof   call.HerdrBridge
		err     error
		valid   bool
		wantErr string
	}{
		{name: "proved", proof: call.HerdrBridge{TmuxSocket: "/fixture/tmux/cc-1", Session: "cc-1", ClientPID: 10}, valid: true},
		{name: "unproved", err: errors.New("herdr pane w1:p1 bridge ownership unproved: not attached"), wantErr: "not attached"},
		{name: "other-client", proof: call.HerdrBridge{TmuxSocket: "/fixture/tmux/cc-1", Session: "cc-1", ClientPID: 99}},
		{name: "other-chat", proof: call.HerdrBridge{TmuxSocket: "/fixture/tmux/cc-2", Session: "cc-2", ClientPID: 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := tabViewReader{
				runtime: &config.Runtime{Paths: paths.Values{TmuxDir: "/fixture/tmux"}},
				tmux: func(_ context.Context, socket string, args ...string) (string, error) {
					if socket != "/fixture/tmux/cc-1" {
						return "", fmt.Errorf("tmux -S %s %s: exit status 1", socket, args[0])
					}
					if args[0] == "list-clients" {
						return "10\n", nil
					}
					return `{"session_id":"s1","seq":1}`, nil
				},
				resolve: func(context.Context, int) (herdr.Location, bool, error) {
					return herdr.Location{Socket: "endpoint", Pane: "w1:p1", Terminal: "term1"}, true, nil
				},
				bridge: func(_ context.Context, socket, pane string) (call.HerdrBridge, error) {
					if socket != "endpoint" || pane != "w1:p1" {
						t.Errorf("bridge proof of %s %s", socket, pane)
					}
					return tc.proof, tc.err
				},
			}
			// headless.Chat carries the tmux socket NAME, as the fleet scan
			// produces it; the reader resolves it under the runtime's TmuxDir.
			target := headless.Chat{ID: "s1", Live: true, Socket: "cc-1", Session: "cc-1"}
			got, err := reader.views(context.Background(), target, "endpoint", panes)
			if err != nil || len(got) != 1 {
				t.Fatalf("views=%+v err=%v", got, err)
			}
			valid, err := got[0].Validate(context.Background())
			if valid != tc.valid || (tc.wantErr == "") != (err == nil) ||
				err != nil && !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("bridge validation = %t, %v; want %t naming %q", valid, err, tc.valid, tc.wantErr)
			}
		})
	}
}

// One chat's view failure is named and skipped; a socket-wide pane listing
// failure still fails the tick.
func TestHerdrTabProviderOneChatFailureKeepsOthers(t *testing.T) {
	warnings := &bytes.Buffer{}
	listErr := error(nil)
	provider := herdrTabProvider{
		scan: func(context.Context) (FleetScan, error) {
			return FleetScan{Rows: []compose.Row{
				{ID: "s1", Name: "Broken", Kind: compose.LiveClaude},
				{ID: "s2", Name: "Bridge", Kind: compose.LiveClaude},
			}}, nil
		},
		inspect: func(context.Context, headless.Chat, time.Time) (headless.Status, error) {
			return headless.Status{State: "blocked"}, nil
		},
		views: func(_ context.Context, target headless.Chat, _ string) ([]herdr.TabDecoration, error) {
			if listErr != nil {
				return nil, listErr
			}
			if target.ID == "s1" {
				return nil, errors.New("list tab tmux clients: server gone")
			}
			return []herdr.TabDecoration{{Pane: "w1:p2", Identity: "s2"}}, nil
		},
		mode: func() (string, error) { return "emoji", nil }, warn: warnings,
	}
	got, err := provider.tabs(context.Background(), "endpoint")
	if err != nil || len(got) != 1 || got[0].Identity != "s2" || !strings.Contains(warnings.String(), "server gone") {
		t.Fatalf("tabs=%+v err=%v warnings=%s", got, err, warnings)
	}
	// A chat failing tick after tick is named once while it lasts, and afresh
	// once it recurs after a tick it was readable.
	persisted := warnings.String()
	provider.warn = &tickWarnings{out: warnings}
	broken := true
	views := provider.views
	provider.views = func(ctx context.Context, target headless.Chat, socket string) ([]herdr.TabDecoration, error) {
		if target.ID == "s1" && !broken {
			return nil, nil
		}
		return views(ctx, target, socket)
	}
	for _, failing := range []bool{true, true, true, false, true} {
		broken = failing
		if _, err := provider.tabs(context.Background(), "endpoint"); err != nil {
			t.Fatal(err)
		}
	}
	if named := strings.Count(strings.TrimPrefix(warnings.String(), persisted), "server gone"); named != 2 {
		t.Fatalf("a recurring chat failure was named %d times over 5 ticks, want 2: %s", named, warnings)
	}
	listErr = fmt.Errorf("%w: socket refused", errTabPaneList)
	_, err = provider.tabs(context.Background(), "endpoint")
	if err == nil || !strings.Contains(err.Error(), "socket refused") {
		t.Fatalf("pane listing failure=%v", err)
	}
}

// Every tab of a tick carries the theme's glyphs; a theme that cannot be read
// is named and the tabs keep painting with the defaults.
func TestHerdrTabProviderCarriesTheThemeGlyphs(t *testing.T) {
	themeGlyphs := herdr.TabGlyphs{Blocked: herdr.TabFrames{"🟣", "🔘"}}
	var readErr error
	warn := &bytes.Buffer{}
	provider := herdrTabProvider{
		scan: func(context.Context) (FleetScan, error) {
			return FleetScan{
				Rows: []compose.Row{{ID: "s1", Socket: "tmux", Name: "Chat", Kind: compose.LiveClaude}},
			}, nil
		},
		inspect: func(context.Context, headless.Chat, time.Time) (headless.Status, error) {
			return headless.Status{State: "blocked"}, nil
		},
		views: func(context.Context, headless.Chat, string) ([]herdr.TabDecoration, error) {
			return []herdr.TabDecoration{{Pane: "w1:p2", Identity: "tmux"}, {Pane: "w1:p3", Identity: "tmux"}}, nil
		},
		mode:   func() (string, error) { return "emoji", nil },
		glyphs: func() (herdr.TabGlyphs, error) { return themeGlyphs, readErr },
		warn:   warn,
	}
	got, err := provider.tabs(context.Background(), "endpoint")
	if err != nil || len(got) != 2 {
		t.Fatalf("tabs=%+v err=%v", got, err)
	}
	for _, d := range got {
		if !reflect.DeepEqual(d.Glyphs, themeGlyphs) {
			t.Fatalf("decoration glyphs=%+v, want the theme's %+v", d.Glyphs, themeGlyphs)
		}
	}
	readErr = errors.New("parse state.json: unexpected end")
	got, err = provider.tabs(context.Background(), "endpoint")
	if err != nil || len(got) != 2 || !reflect.DeepEqual(got[0].Glyphs, herdr.TabGlyphs{}) {
		t.Fatalf("an unreadable theme: tabs=%+v err=%v, want the tabs with the default glyphs", got, err)
	}
	if !strings.Contains(warn.String(), "Herdr tab glyphs of the theme: parse state.json") {
		t.Fatalf("an unreadable theme was not named: %q", warn.String())
	}
}

// Off and text modes draw no theme glyph, so they never read the theme nor
// name its failure.
func TestHerdrTabProviderReadsNoGlyphsOutsideEmojiMode(t *testing.T) {
	for _, mode := range []string{herdr.TabStatusOff, "text"} {
		warn := &bytes.Buffer{}
		read := false
		provider := herdrTabProvider{
			scan: func(context.Context) (FleetScan, error) { return FleetScan{}, nil },
			mode: func() (string, error) { return mode, nil },
			glyphs: func() (herdr.TabGlyphs, error) {
				read = true
				return herdr.TabGlyphs{}, errors.New("parse state.json: unexpected end")
			},
			warn: warn,
		}
		if _, err := provider.tabs(context.Background(), "endpoint"); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if read || warn.Len() > 0 {
			t.Fatalf("%s: glyphs read=%v warn=%q, want neither", mode, read, warn.String())
		}
	}
}
