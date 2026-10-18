package chat

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

func TestHerdrTabProviderNativeAndMultipleTmuxViews(t *testing.T) {
	var inspected []string
	provider := herdrTabProvider{
		scan: func(context.Context) (FleetScan, error) {
			return FleetScan{Rows: []compose.Row{
				{
					Backend: "herdr",
					SeatID:  "seat",
					ID:      "s1",
					Socket:  "endpoint",
					PaneID:  "w1:p1",
					Name:    "Native",
					Kind:    compose.LiveClaude,
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
		tmux: func(_ context.Context, _ string, args ...string) (string, error) {
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
	target.Live = false
	got, err = reader.views(context.Background(), target, "endpoint", panes)
	if err != nil || len(got) != 2 {
		t.Fatalf("original retained dead views=%+v err=%v", got, err)
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
