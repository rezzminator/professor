package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A dead Herdr-born chat opened for no terminal — a schedule's wake
// (OpenDetachedID), an MCP open (OpenDetachedTarget) — revives on its own
// recorded Herdr host whatever process runs the door, never in tmux, and the
// one retire path closes its old pane when Herdr proves it is that seat's
// terminal. A carried Herdr caller still decides as it did.
func TestOpenNativeDeadDetachedRevivesOnItsOwnHerdrHost(t *testing.T) {
	for _, door := range []string{"schedule wake", "mcp open", "carried Herdr caller"} {
		t.Run(door, func(t *testing.T) {
			root := testjail.Fleet(t)
			layouts := make(chan struct{}, 4)
			closed := make(chan string, 4)
			oldPaneLingers := door != "carried Herdr caller"
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				switch req.Method {
				case "ping":
					return map[string]any{"type": "pong", "protocol": 22, "version": "fixture"}, ""
				case "pane.list":
					return map[string]any{"type": "pane_list", "panes": []any{func() any {
						info := testjail.HerdrPaneInfo("w6:p9")["pane"].(map[string]any)
						info["terminal_id"] = "fixture-w6:p3"
						return info
					}()}}, ""
				case "pane.get":
					var pane string
					_ = json.Unmarshal(req.Params["pane_id"], &pane)
					if pane == "w6:p3" {
						if !oldPaneLingers {
							return nil, "pane_not_found"
						}
						info := testjail.HerdrPaneInfo("w6:p3")
						info["pane"].(map[string]any)["terminal_id"] = "fixture-w6:p3"
						return info, ""
					}
					return map[string]any{
						"type": "pane_info",
						"pane": map[string]any{"pane_id": pane, "workspace_id": "w6"},
					}, ""
				case "pane.close":
					var pane string
					_ = json.Unmarshal(req.Params["pane_id"], &pane)
					closed <- pane
					return map[string]any{"type": "ok"}, ""
				case "pane.process_info":
					return map[string]any{
						"type": "pane_process_info",
						"process_info": map[string]any{
							"pane_id": "w6:p9", "shell_pid": 41,
							"foreground_processes": []any{map[string]any{"pid": 42, "name": "claude"}},
						},
					}, ""
				case "pane.focus":
					return nil, "permission_denied"
				case "layout.apply":
					layouts <- struct{}{}
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
			loaded, err := config.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			runtime.Config = loaded.Config
			seedClaudeChat(t, root, seat.SessionID)
			// The replaced seat's reload schedule files go with its row: the
			// new seat has a new id, so nothing else would remove them.
			reloadFiles := plantNativeReloadFiles(t, runtime.Paths.SIDDir, seat.SeatID)
			binary := filepath.Join(runtime.Paths.Home, "native-engine")
			if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := db.WriteSeatRun(
				seat.SeatID,
				fleetdb.SeatRun{Binary: binary, CWD: runtime.Paths.Home, Args: []string{"--resume", seat.SessionID}},
			); err != nil {
				t.Fatal(err)
			}
			// The door runs where no chat is: a timer, a daemon.
			t.Setenv("HERDR_ENV", "")
			t.Setenv("HERDR_SOCKET_PATH", "")
			t.Setenv("HERDR_PANE_ID", "")
			t.Setenv("TMUX", "")
			ctx := context.Background()
			if door == "carried Herdr caller" {
				ctx = WithCaller(
					ctx,
					"open.herdr",
					resolve.Identity{Backend: "herdr", SocketPath: socket, Pane: "w6:p8", HerdrWorkspace: "w6"},
					nil,
				)
			}
			fake := &fakeOpenTmux{alive: map[string]bool{}}
			stubOpenExecutor(t, fake)
			target := headless.Chat{
				ID:      seat.SessionID,
				SeatID:  seat.SeatID,
				Backend: "herdr",
				Engine:  pfmengine.Claude,
				Socket:  seat.HerdrSocket,
				Pane:    seat.HerdrPane,
			}
			var result action.OpenResult
			if door == "schedule wake" {
				result, err = OpenDetachedID(ctx, seat.SessionID, io.Discard, &runtime)
			} else {
				result, err = OpenDetachedTarget(ctx, target, io.Discard, &runtime)
			}
			if err != nil || result.State != "opened" {
				t.Fatalf("dead native result=%+v err=%v", result, err)
			}
			if len(fake.created) != 0 {
				t.Fatalf("a Herdr-born chat was revived in tmux: %+v", fake.created)
			}
			if result.Socket != socket || !strings.Contains(result.Detail, "w6:p9") || len(layouts) != 1 {
				t.Fatalf("native resume=%+v layouts=%d", result, len(layouts))
			}
			close(closed)
			var closes []string
			for pane := range closed {
				closes = append(closes, pane)
			}
			if want := []string{"w6:p3"}; oldPaneLingers && !slices.Equal(closes, want) {
				t.Fatalf("old pane closes=%v, want %v: the chat would show twice", closes, want)
			} else if !oldPaneLingers && len(closes) != 0 {
				t.Fatalf("a pane already gone was closed again: %v", closes)
			}
			assertNativeReloadFilesGone(t, reloadFiles)
			// A stale exact row fails closed; the refreshed row finds the
			// completed birth and lays out no second tab.
			stale := target
			if _, err := OpenDetachedTarget(ctx, stale, io.Discard, &runtime); err == nil {
				t.Fatal("stale exact row unexpectedly resolved")
			}
			current, found, err := db.SeatBySession(ctx, seat.SessionID)
			if err != nil || !found || current.HerdrPane != "w6:p9" {
				t.Fatalf("replacement ownership lost: seat=%+v found=%t err=%v", current, found, err)
			}
			current = nativeChatHost(t, db, current, true)
			stale.SeatID = current.SeatID
			stale.Socket, stale.Pane = current.HerdrSocket, current.HerdrPane
			result, err = OpenDetachedTarget(ctx, stale, io.Discard, &runtime)
			if err != nil || result.State != "live" || len(layouts) != 1 {
				t.Fatalf("retry duplicated birth: result=%+v err=%v layouts=%d", result, err, len(layouts))
			}
		})
	}
}

// placements is every chat.placement decision recorder holds, as door=chosen.
func placements(recorder *obs.Recorder) []string {
	var decisions []string
	for _, record := range recorder.Records() {
		if record.Message != "chat.placement" {
			continue
		}
		door, _ := record.Field("door")
		chosen, _ := record.Field("chosen")
		decisions = append(decisions, fmt.Sprint(door, "=", chosen))
	}
	return decisions
}

// Every door that revives a stored chat asks placement exactly once, under
// its own door name: a door with no terminal (a schedule's wake, an MCP open)
// as "open.detached", a terminal open and the picker as "picker", and a
// restored pane as "restored" — never a second decision further down.
func TestEveryReviveDoorPlacesOnce(t *testing.T) {
	for _, test := range []struct {
		door string
		open func(ctx context.Context, id string, runtime *config.Runtime) error
		want []string
	}{
		{door: "schedule wake", want: []string{"open.detached=tmux"}, open: func(ctx context.Context, id string, runtime *config.Runtime) error {
			_, err := OpenDetachedID(ctx, id, io.Discard, runtime)
			return err
		}},
		{door: "mcp open", want: []string{"open.detached=tmux"}, open: func(ctx context.Context, id string, runtime *config.Runtime) error {
			_, err := OpenDetachedTarget(ctx, headless.Chat{ID: id, Name: id, Engine: pfmengine.Claude, CWD: runtime.Paths.Home}, io.Discard, runtime)
			return err
		}},
		{door: "terminal open", want: []string{"picker=tmux"}, open: func(ctx context.Context, id string, runtime *config.Runtime) error {
			var stderr bytes.Buffer
			if code := OpenID(ctx, id, io.Discard, &stderr, runtime); code != 0 {
				return fmt.Errorf("OpenID=%d: %s", code, stderr.String())
			}
			return nil
		}},
		{door: "picker", want: []string{"picker=tmux"}, open: func(ctx context.Context, id string, runtime *config.Runtime) error {
			var stderr bytes.Buffer
			row := compose.Row{Kind: compose.ResumeClaude, ID: id, Name: id, CWD: runtime.Paths.Home}
			if code := OpenRow(ctx, row, 1, false, "", io.Discard, &stderr, runtime); code != 0 {
				return fmt.Errorf("OpenRow=%d: %s", code, stderr.String())
			}
			return nil
		}},
		{door: "restored", want: []string{"restored=tmux"}, open: func(ctx context.Context, id string, runtime *config.Runtime) error {
			var stderr bytes.Buffer
			if code := OpenRestoredID(ctx, id, io.Discard, &stderr, runtime); code != 0 {
				return fmt.Errorf("OpenRestoredID=%d: %s", code, stderr.String())
			}
			return nil
		}},
	} {
		t.Run(test.door, func(t *testing.T) {
			root := testjail.Fleet(t)
			const id = "c5555555-5555-4555-8555-555555555555"
			seedClaudeChat(t, root, id)
			for _, name := range []string{"HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID", "TMUX"} {
				t.Setenv(name, "")
			}
			runtime, err := config.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			fake := &fakeOpenTmux{alive: map[string]bool{}}
			stubOpenExecutor(t, fake)
			t.Cleanup(launchnotice.Replace(strings.NewReader("\n"), false))
			ctx, recorder := obs.Test(t)
			if err := test.open(ctx, id, &runtime); err != nil {
				t.Fatal(err)
			}
			if got := placements(recorder); !slices.Equal(got, test.want) || len(fake.created) != 1 {
				t.Fatalf("placements=%v created=%d, want %v and one birth", got, len(fake.created), test.want)
			}
		})
	}
}

// A seat no longer live is classified the way the fleet composes it: its
// engine's resumable kind, never a live one the open would try to attach.
func TestNativeRowDemotesASeatThatIsNotLive(t *testing.T) {
	for _, test := range []struct {
		engine pfmengine.ID
		state  string
		want   compose.Kind
	}{
		{pfmengine.Claude, "live", compose.LiveClaude},
		{pfmengine.Claude, "lost", compose.ResumeClaude},
		{pfmengine.Codex, "failed", compose.ResumeCodex},
		{pfmengine.OpenCode, "lost", compose.ResumeOpenCode},
		{pfmengine.OpenCode, "live", compose.LiveOpenCode},
	} {
		row := nativeRow(fleetdb.Seat{Engine: string(test.engine), State: test.state, Backend: "herdr"})
		if row.Kind != test.want {
			t.Errorf("%s %s seat kind=%v, want %v", test.engine, test.state, row.Kind, test.want)
		}
	}
}

// A killed row is not live to the verbs, the same answer the roster gives.
func TestFromRowHonoursKilled(t *testing.T) {
	row := compose.Row{
		Kind:   compose.LiveClaude,
		ID:     "c6666666-6666-4666-8666-666666666666",
		Socket: "cc-1",
		PaneID: "%1",
	}
	if !FromRow(row).Live {
		t.Fatal("a live row is not live")
	}
	row.Killed = true
	if FromRow(row).Live {
		t.Fatal("a killed row is live to the verbs, while the roster calls it dead")
	}
}
