package chat

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// SessionEnd releases the agent while the engine and its pane still exist.
// Repeated observations must distinguish that transition from a broken agent.
func TestHerdrTabProviderReleasedAgentTeardown(t *testing.T) {
	for _, outcome := range []string{"exit", "closed", "alive", "recovered", "unreadable", "moved-exit", "replaced-pane"} {
		t.Run(outcome, func(t *testing.T) {
			t.Setenv("TMUX_TMPDIR", t.TempDir())
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
					if phase > 0 && outcome == "closed" {
						panes = nil
					}
					return map[string]any{"type": "pane_list", "panes": panes}, ""
				case "pane.process_info":
					name := "claude"
					if phase > 0 && (outcome == "exit" || outcome == "moved-exit") {
						name = "bash"
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
				mode: func() (string, error) { return "emoji", nil },
				warn: tabIndicatorWarnings{dir: dir, stderr: &warnings},
			}
			for phase = 0; phase < 3; phase++ {
				views, err := provider.tabs(context.Background(), socket)
				if err != nil {
					t.Fatal(err)
				}
				wantError := outcome == "unreadable" || phase > 0 && outcome == "alive"
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
					if phase > 0 && (outcome == "exit" || outcome == "moved-exit") {
						want = headless.StateDead
					}
					if phase == 1 && outcome == "recovered" {
						want = headless.StateIdle
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
