package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRestoredLegacyRecipeGainsRemoteControl(t *testing.T) {
	for _, session := range []string{"old-session", ""} {
		t.Run(session, func(t *testing.T) {
			machine := config.Defaults(t.TempDir(), nil)
			machine.Accounts = nil // Rosterless native launchers use machine policy.
			run, err := resumeNativeRun(
				fleetdb.SeatRun{
					Binary: "/fixture/claude",
					Args:   []string{"--model", "sonnet", "PRIVATE INITIAL PROMPT"},
				},
				fleetdb.Seat{Engine: string(pfmengine.Claude), SessionID: session, Name: "desk"},
				machine, io.Discard,
			)
			if err != nil {
				t.Fatal(err)
			}
			at := slices.Index(run.Args, "--remote-control")
			if at < 0 || at+1 == len(run.Args) || run.Args[at+1] != "desk" {
				t.Fatalf("restored legacy recipe omitted named Remote Control: %q", run.Args)
			}
			if slices.Contains(run.Args, "PRIVATE INITIAL PROMPT") != (session == "") {
				t.Fatalf("restore mishandled initial prompt: %q", run.Args)
			}
		})
	}
}

// Exercise detached replacement with an actual saved pre-knob recipe, including
// a legacy account identity that cannot be resolved anymore.
func TestDetachedLegacyRecipeRemoteControl(t *testing.T) {
	for _, scenario := range []string{"known on", "known off", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			testjail.Fleet(t)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				switch req.Method {
				case "ping":
					return map[string]any{"type": "pong", "protocol": 22, "version": "fixture"}, ""
				case "pane.list":
					info := testjail.HerdrPaneInfo("w6:p9")["pane"].(map[string]any)
					info["terminal_id"] = "fixture-w6:p3"
					return map[string]any{"type": "pane_list", "panes": []any{info}}, ""
				case "pane.get":
					var pane string
					if err := json.Unmarshal(req.Params["pane_id"], &pane); err != nil {
						t.Error(err)
					}
					if pane == "w6:p3" {
						return nil, "pane_not_found"
					}
					return map[string]any{
						"type": "pane_info",
						"pane": map[string]any{"pane_id": pane, "workspace_id": "w6"},
					}, ""
				case "pane.process_info":
					return map[string]any{
						"type": "pane_process_info",
						"process_info": map[string]any{
							"pane_id":              "w6:p9",
							"shell_pid":            41,
							"foreground_processes": []any{map[string]any{"pid": 42, "name": "claude"}},
						},
					}, ""
				case "layout.apply":
					return map[string]any{
						"layout": map[string]any{
							"workspace_id": "w6",
							"root":         map[string]any{"type": "pane", "pane_id": "w6:p9"},
						},
					}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, db, seat := nativeChatSeat(t, socket, "lost")
			seat = nativeChatHost(t, db, seat, false)
			runtime.Config = config.Defaults(runtime.Paths.Home, nil)
			prefs := runtime.Config.Claude
			prefs.RemoteControl = scenario != "known off"
			account := filepath.Join(runtime.Paths.Home, "account")
			runtime.Config.Accounts = []config.Account{{ID: 7, ConfigDir: account, Claude: &prefs}}
			binary := filepath.Join(runtime.Paths.Home, "fixture-engine")
			if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			run := fleetdb.SeatRun{
				Binary: binary,
				CWD:    runtime.Paths.Home,
				Args:   []string{"--resume", seat.SessionID},
				Env:    []string{"CLAUDE_CONFIG_DIR=" + account},
			}
			if scenario == "unknown" {
				run.Env = nil
				run.Args = append(run.Args, "--remote-control", "stale name")
			}
			if err := db.WriteSeatRun(seat.SeatID, run); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID", "TMUX"} {
				t.Setenv(key, "")
			}
			ctx := WithCaller(
				context.Background(),
				"open.herdr",
				resolve.Identity{Backend: "herdr", SocketPath: socket, Pane: "w6:p8", HerdrWorkspace: "w6"},
				nil,
			)
			var warning bytes.Buffer
			result, err := detachedNative(ctx, seat, &warning, runtime)
			if err != nil || result.State != "opened" {
				t.Fatalf("detached result=%+v err=%v warning=%s", result, err, warning.String())
			}
			current, found, err := db.SeatBySession(ctx, seat.SessionID)
			if err != nil || !found {
				t.Fatalf("replacement absent: found=%t err=%v", found, err)
			}
			run, err = db.ReadSeatRun(current.SeatID)
			if err != nil {
				t.Fatal(err)
			}
			at := slices.Index(run.Args, "--remote-control")
			want := scenario == "known on"
			if (at >= 0) != want || (want && (at+1 >= len(run.Args) || run.Args[at+1] != seat.Name)) ||
				slices.Contains(run.Args, "--name") {
				t.Fatalf("detached Remote Control policy/rename mismatch: %q", run.Args)
			}
			if scenario == "unknown" &&
				(!strings.Contains(warning.String(), "WARN") || !strings.Contains(warning.String(), seat.Name)) {
				t.Fatalf("missing visible warning: %s", warning.String())
			}
		})
	}
}
