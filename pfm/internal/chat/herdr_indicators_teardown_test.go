package chat

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// SessionEnd releases the agent while the engine and its pane still exist.
// Repeated observations must distinguish that transition from a broken agent.
func TestHerdrTabProviderReleasedAgentTeardown(t *testing.T) {
	for _, outcome := range []string{"exit", "closed", "alive", "recovered", "unreadable", "moved-exit", "replaced-pane", "kept-exit", "exit-beside-incomplete", "own-pane-incomplete"} {
		t.Run(outcome, func(t *testing.T) {
			t.Setenv("TMUX_TMPDIR", t.TempDir())
			// kept-exit: herdr-keep's holder 5 runs shell 10, whose terminal's
			// foreground group 41 runs the engine, then a bare shell.
			proc := t.TempDir()
			if outcome == "kept-exit" {
				t.Setenv(paths.EnvProcRoot, proc)
			}
			foreground := func(argv0 string) {
				zeros := strings.Repeat("0 ", 13)
				for pid, file := range map[string]string{
					"5/stat":     "5 (herdr-keep) S 1 5 5 0 -1 " + zeros + "2 0\n",
					"10/stat":    "10 (zsh) S 5 10 10 0 41 " + zeros + "3 0\n",
					"41/stat":    "41 (" + argv0 + ") S 10 41 10 0 41 " + zeros + "4 0\n",
					"41/cmdline": argv0 + "\x00",
				} {
					path := filepath.Join(proc, pid)
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			clk := clock.NewFake(time.Now())
			phase := 0
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				switch req.Method {
				case "pane.list":
					pane := testjail.HerdrPaneInfo("w1:p1")["pane"]
					if phase > 0 && outcome == "moved-exit" {
						pane = testjail.HerdrPaneInfo("w2:p2")["pane"]
						pane.(map[string]any)["terminal_id"] = "fixture-w1:p1"
					}
					if phase > 0 && outcome == "replaced-pane" {
						pane.(map[string]any)["terminal_id"] = "different-terminal"
					}
					panes := []any{pane}
					if phase > 0 && outcome == "exit-beside-incomplete" {
						// Another pane Herdr lists without its identity is skipped alone.
						panes = append(panes, map[string]any{"pane_id": "w1:p9"})
					}
					if phase > 0 && outcome == "own-pane-incomplete" {
						panes = []any{map[string]any{"pane_id": "w1:p1"}}
					}
					if phase > 0 && outcome == "closed" {
						panes = []any{} // Herdr lists no pane as [], never null
					}
					return map[string]any{"type": "pane_list", "panes": panes}, ""
				case "pane.process_info":
					name := "claude"
					if phase > 0 && (outcome == "exit" || outcome == "moved-exit" ||
						outcome == "exit-beside-incomplete" || outcome == "own-pane-incomplete") {
						name = "bash"
					}
					if outcome == "kept-exit" {
						// Herdr sees only herdr-keep's attach client.
						name = "herdr-keep"
					}
					if outcome == "unreadable" {
						return nil, "process_read_failed"
					}
					return map[string]any{"type": "pane_process_info", "process_info": map[string]any{
						"pane_id": func() string {
							if phase > 0 && outcome == "moved-exit" {
								return "w2:p2"
							}
							return "w1:p1"
						}(), "foreground_processes": []any{map[string]any{"pid": 41, "name": name}},
					}}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			dir := t.TempDir()
			var warnings bytes.Buffer
			provider := herdrTabProvider{
				clock: clk,
				scan: func(context.Context) (FleetScan, error) {
					return FleetScan{Rows: []compose.Row{
						{Backend: "herdr", ID: "s1", Socket: socket, PaneID: "w1:p1", Kind: compose.LiveClaude},
					}}, nil
				},
				views: func(context.Context, headless.Chat, string) ([]herdr.TabDecoration, error) {
					return []herdr.TabDecoration{{Pane: "w1:p1", Terminal: "fixture-w1:p1", Identity: "s1"}}, nil
				},
				inspect: func(context.Context, headless.Chat, time.Time) (headless.Status, error) {
					if phase == 1 && outcome == "recovered" {
						return headless.Status{State: headless.StateIdle}, nil
					}
					return headless.Status{}, &herdr.Refusal{
						Request: "agent.get pfm-seat",
						Code:    herdr.AgentNotFound,
						Message: "target missing",
					}
				},
				// Past the exit grace an engine still alive in an agent-less pane (a
				// native seat Herdr tracks no agent for) is read from its own screen.
				screen: func(_ context.Context, chat headless.Chat) (string, error) {
					if chat.Socket != socket || chat.Pane != "w1:p1" || chat.Engine != pfmengine.Claude {
						t.Errorf("screen read of %+v, want claude pane w1:p1 on %s", chat, socket)
					}
					return claudeBusyPane, nil
				},
				mode: func() (string, error) { return "emoji", nil },
				warn: tabIndicatorWarnings{dir: dir, stderr: &warnings},
			}
			if outcome == "kept-exit" {
				testjail.StageHerdrKeep(t, testjail.HerdrKeepListing(5, 2, 10, 3, "w1:p1", socket), 0)
			}
			for phase = 0; phase < 3; phase++ {
				if outcome == "kept-exit" && phase == 0 {
					foreground("claude")
				} else if outcome == "kept-exit" {
					foreground("bash")
				}
				views, err := provider.tabs(context.Background(), socket)
				if err != nil {
					t.Fatal(err)
				}
				wantError := outcome == "unreadable" || outcome == "own-pane-incomplete" && phase > 0
				records, err := herdr.RecentErrors(dir, time.Time{})
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "unreadable" && !strings.Contains(warnings.String(), "process_read_failed") {
					t.Fatalf("lost process lookup failure: %s", warnings.String())
				}
				if (len(records) > 0) != wantError {
					t.Fatalf(
						"phase=%d doctor-count=%d warnings=%q wantError=%v",
						phase,
						len(records),
						warnings.String(),
						wantError,
					)
				}
				if phase > 0 && (outcome == "closed" || outcome == "replaced-pane") && len(views) != 0 {
					t.Fatalf("closed pane painted: %+v", views)
				}
				if len(views) > 0 {
					want := "unknown"
					if phase > 0 && (outcome == "exit" || outcome == "moved-exit" || outcome == "kept-exit" ||
						outcome == "exit-beside-incomplete") {
						want = headless.StateDead
					}
					if phase == 1 && outcome == "recovered" {
						want = headless.StateIdle
					}
					if phase > 0 && outcome == "alive" {
						want = headless.StateWorking
					}
					if views[0].State != want {
						t.Fatalf("state=%q want %q", views[0].State, want)
					}
				}
				clk.Advance(6 * time.Second)
			}
		})
	}
}
