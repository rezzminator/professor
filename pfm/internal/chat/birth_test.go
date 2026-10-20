package chat

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestBirthBackendHonorsRequestBoundary(t *testing.T) {
	env := &paths.MapEnv{
		Values: map[string]string{
			"HERDR_ENV":         "1",
			"HERDR_SOCKET_PATH": "/unreachable/herdr.sock",
			"HERDR_PANE_ID":     "w6:p3",
		},
	}
	for _, ctx := range []context.Context{
		spawn.WithBackendEnvironment(context.Background(), nil),
		WithResolvedSelf(context.Background(), headless.Chat{Backend: "tmux"}),
		WithResolvedSelf(context.Background(), headless.Chat{}),
	} {
		choice, err := BirthBackend(ctx, "auto", env)
		if err != nil || choice.Backend != "tmux" || choice.Warning != "" {
			t.Fatalf("request boundary choice=%+v err=%v", choice, err)
		}
	}
}

func TestBirthHerdrAwaitsTranscriptAndAttachesNativePane(t *testing.T) {
	for _, mode := range []string{"await", "attach", "cancel", "timeout", "raw", "codex-prompt", "codex-dash-prompt", "trust"} {
		t.Run(mode, func(t *testing.T) {
			root := testjail.Fleet(t)
			const id = "ad8c2104-88c4-43df-86a9-cd7126949b44"
			if mode != "trust" {
				seedClaudeChat(t, root, id, assistantSaid("native answer"))
			}
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			// Index before birth; native awaiting must consume transcript evidence.
			if _, err := Rows(context.Background(), io.Discard, &config.Runtime{Paths: values}); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(values.Home, "native-engine")
			if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			focused := make(chan string, 2)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				switch req.Method {
				case "ping":
					return map[string]any{"type": "pong", "protocol": 22, "version": "fixture"}, ""
				case "pane.list":
					return map[string]any{
						"type":  "pane_list",
						"panes": []any{testjail.HerdrPaneInfo("w6:p9")["pane"]},
					}, ""
				case "pane.process_info":
					return map[string]any{
						"type":         "pane_process_info",
						"process_info": map[string]any{"pane_id": "w6:p9", "shell_pid": 41},
					}, ""
				case "pane.read":
					screen := "❯ "
					if mode == "trust" {
						screen = "Quick safety check\nYes, I trust this folder\nNo, exit"
					}
					return map[string]any{
						"type": "pane_read",
						"read": map[string]any{
							"pane_id":      "w6:p9",
							"workspace_id": "w6",
							"text":         screen,
							"truncated":    false,
						},
					}, ""
				case "layout.apply":
					db := fleetdb.OpenSharedState(context.Background(), values)
					seats, err := db.Seats(context.Background())
					if err != nil || len(seats) != 1 {
						t.Errorf("birth seats=%v err=%v", seats, err)
						return nil, "fixture_seats"
					}
					seat := nativeChatHost(t, db, seats[0], true)
					seat.HerdrTerminalID = "fixture-w6:p9"
					if err := db.PutSeat(context.Background(), seat); err != nil {
						t.Error(err)
					}
					if err := db.Close(); err != nil {
						t.Error(err)
					}
					return map[string]any{
						"layout": map[string]any{
							"workspace_id": "w6",
							"root":         map[string]any{"type": "pane", "pane_id": "w6:p9"},
						},
					}, ""
				case "agent.focus":
					focused <- "w6:p9"
					return map[string]any{"type": "pane_focused", "pane_id": "w6:p9"}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w6:p3")
			t.Setenv("TMUX", "")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			plan := action.HeadlessPlan{
				SeatRun: &fleetdb.SeatRun{Binary: binary, CWD: values.Home},
				Record:  &fleetdb.Launch{Engine: pfmengine.Claude, SessionID: id},
			}
			prompt := "hello"
			if mode == "codex-dash-prompt" {
				prompt = "--help me"
			}
			if mode == "raw" || strings.HasPrefix(mode, "codex-") {
				plan.Engine = pfmengine.OpenCode
				if strings.HasPrefix(mode, "codex-") {
					plan.Engine = pfmengine.Codex
				}
				plan.Record = nil
			}
			var stdout, stderr bytes.Buffer
			options := headless.AwaitOptions{}
			if mode == "timeout" {
				options.Timeout = time.Nanosecond
			}
			born, code := BirthHerdr(
				ctx,
				spawn.BackendChoice{Backend: "herdr", HerdrSocket: socket, HerdrPane: "w6:p3", HerdrWorkspace: "w6"},
				plan,
				&config.Runtime{Paths: values},
				"INTEGRATION",
				prompt,
				mode == "await" || mode == "cancel" || mode == "timeout" || mode == "trust",
				mode == "attach",
				&stdout,
				&stderr,
				options,
			)
			if mode == "trust" {
				if !born || code != 6 || !strings.Contains(stderr.String(), "folder-trust dialog") ||
					!strings.Contains(stderr.String(), "w6:p9") {
					t.Fatalf("native trust birth born=%v exit=%d err=%q", born, code, stderr.String())
				}
				return
			}
			if mode == "timeout" {
				if code != 5 || !strings.Contains(stderr.String(), "did not answer in time") {
					t.Fatalf("await timeout code=%d err=%q", code, stderr.String())
				}
				return
			}
			if mode == "cancel" {
				if code == 0 {
					t.Fatal("canceled native birth reported success")
				}
				return
			}
			if !born || code != 0 {
				t.Fatalf("birth born=%t code=%d out=%q err=%q", born, code, stdout.String(), stderr.String())
			}
			if strings.HasPrefix(mode, "codex-") {
				state := fleetdb.OpenSharedState(ctx, values)
				seats, err := state.Seats(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(seats) != 1 {
					t.Fatalf("native seats = %+v", seats)
				}
				recipe, err := state.ReadSeatRun(seats[0].SeatID)
				if closeErr := state.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				if err != nil || len(recipe.Args) != 2 || recipe.Args[0] != "--" || recipe.Args[1] != prompt {
					t.Fatalf("native Codex prompt lacks option boundary: args=%q err=%v", recipe.Args, err)
				}
				// Once a transcript exists, restore resumes it without replaying
				// the initial prompt or interpreting its dash-led text as flags.
				recipe, err = resumeNativeRun(
					recipe,
					fleetdb.Seat{Engine: string(pfmengine.Codex), SessionID: id},
					config.Config{}, io.Discard,
				)
				if err != nil || len(recipe.Args) != 2 || recipe.Args[0] != "resume" || recipe.Args[1] != id {
					t.Fatalf("native Codex restore retained prompt as options: args=%q err=%v", recipe.Args, err)
				}
			}
			if mode == "raw" && !strings.HasPrefix(stdout.String(), string(pfmengine.OpenCode)+"\t") {
				t.Fatalf("raw native engine=%q", stdout.String())
			}
			if mode == "await" && stdout.String() != "native answer\n" {
				t.Fatalf("await stdout=%q want only answer", stdout.String())
			}
			if mode == "attach" && (len(focused) != 1 || <-focused != "w6:p9") {
				t.Fatal("native attach did not focus new pane")
			}
		})
	}
}

// A native launch owns a pane before Claude creates a transcript. The same
// persisted seat can be live or positively retired.
func TestNativeBirthAwaitReportsTrustBeforeTranscript(t *testing.T) {
	for _, state := range []string{"live", "dead", "bind-race", "quoted", "selected-trust"} {
		t.Run(state, func(t *testing.T) {
			testjail.Fleet(t)
			var db *fleetdb.Store
			var seat fleetdb.Seat
			var runtime config.Runtime
			raced := false
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				switch req.Method {
				case "pane.list":
					return map[string]any{
						"type":  "pane_list",
						"panes": []any{testjail.HerdrPaneInfo("w6:p3")["pane"]},
					}, ""
				case "pane.process_info":
					if state == "bind-race" && !raced {
						raced = true
						// The seat-host publishes the same birth while await is
						// proving it; its CAS must be retried, not called dead.
						next := seat
						if err := gather.StampHerdrHost(
							gather.NewProcFS(runtime.Paths.ProcRoot), &next, 42,
						); err != nil {
							t.Error(err)
						}
						if err := db.UpdateSeat(context.Background(), seat, next); err != nil {
							t.Error(err)
						}
					}
					return map[string]any{
						"type":         "pane_process_info",
						"process_info": map[string]any{"pane_id": "w6:p3", "shell_pid": 41},
					}, ""
				case "pane.read":
					screen := "Quick safety check\nYes, I trust this folder\nNo, exit"
					switch state {
					case "quoted":
						screen += "\n────────\n❯ \n────────"
					case "selected-trust":
						screen = "Quick safety check\n❯ 1. Yes, I trust this folder\n  2. No, exit"
					}
					return map[string]any{
						"type": "pane_read",
						"read": map[string]any{
							"pane_id":      "w6:p3",
							"workspace_id": "w6",
							"text":         screen,
							"truncated":    false,
						},
					}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, db, seat = nativeChatSeat(t, socket, "live")
			seat = nativeChatHost(t, db, seat, state != "dead")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			turn, err := headless.Await(ctx, func(ctx context.Context) (headless.Chat, bool, error) {
				return nativeBirthTarget(ctx, seat, &runtime)
			}, headless.AwaitOptions{Grace: time.Nanosecond, Timeout: 50 * time.Millisecond, Poll: time.Millisecond})
			if state == "quoted" {
				if !errors.Is(err, headless.ErrAwaitTimeout) || turn.State == headless.StateBlocked {
					t.Fatalf("trust quotes above composer state=%q error=%v, want live timeout", turn.State, err)
				}
				return
			}
			if state == "dead" {
				if !errors.Is(err, headless.ErrChatGone) {
					t.Fatalf("retired seat error=%v, want gone", err)
				}
				return
			}
			if err == nil || errors.Is(err, headless.ErrChatGone) || turn.State != headless.StateBlocked {
				t.Fatalf("live trust seat state=%q error=%v, want blocked, never gone", turn.State, err)
			}
			for _, text := range []string{"folder-trust dialog", "w6:p3", "alive", "open", "trusted directory"} {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("trust refusal=%q, missing %q", err, text)
				}
			}
		})
	}
}
