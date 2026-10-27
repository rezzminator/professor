package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/restart"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// scheduleRestartClock advances the real caller's bounded waits without making
// this fake transport test spend ninety wall-clock seconds on a broken capture.
type scheduleRestartClock struct {
	clock.Clock
	now     time.Time
	elapsed atomic.Int64
}

func (c *scheduleRestartClock) Now() time.Time { return c.now.Add(time.Duration(c.elapsed.Load())) }
func (c *scheduleRestartClock) Sleep(ctx context.Context, d time.Duration) error {
	c.elapsed.Add(int64(d))
	return ctx.Err()
}

// TestHerdrScheduleSurvivesRestart exercises the actual restart Fleet.End and
// CLI schedule tick, not an injected deliverer. A fake Herdr implements only the
// external terminal/agent protocol; pfm owns recovery, readiness and recording.
// Serial: the CLI jail and clock.Real are process-wide.
func TestHerdrScheduleSurvivesRestart(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		t.Run(fmt.Sprintf("delayed-proof=%t", delayed), func(t *testing.T) {
			id := indexScheduleChat(t)
			runtime, err := pfmconfig.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			db := fleetdb.OpenSharedState(ctx, runtime.Paths)
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			oldClock := clock.Real
			ticks := &scheduleRestartClock{Clock: oldClock, now: time.Now()}
			clock.Real = ticks
			t.Cleanup(func() { clock.Real = oldClock })

			account := pfmconfig.DefaultAccountDir(runtime.Paths.Home, 2)
			if err := os.MkdirAll(account, 0o700); err != nil {
				t.Fatal(err)
			}
			configBody, err := json.Marshal(map[string]any{
				"version": 2,
				"accounts": []map[string]any{
					{"id": 1, "configDir": pfmconfig.DefaultAccountDir(runtime.Paths.Home, 1)},
					{"id": 2, "configDir": account},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(runtime.Config.Path, configBody, 0o600); err != nil {
				t.Fatal(err)
			}
			runtime, err = pfmconfig.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(runtime.Paths.Home, "fake-claude")
			if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(testjail.ShortRoot(t), "h.sock")
			seat := fleetdb.Seat{
				SeatID: "scheduled-seat", SessionID: id, Name: "SCHEDULED", Engine: string(pfmengine.Claude),
				Backend: "herdr", State: "live", HerdrSocket: socket, HerdrPane: "w6:p3",
				HerdrWorkspace: "w6", HerdrTerminalID: "fixture-w6:p3", SeatHostPID: 42, SeatHostStart: 100,
			}
			if err := db.PutSeat(ctx, seat); err != nil {
				t.Fatal(err)
			}
			if err := db.WriteSeatRun(seat.SeatID, fleetdb.SeatRun{
				Binary: binary, CWD: runtime.Paths.Home,
				Args: []string{"--session-id", id}, Env: []string{"CLAUDE_CONFIG_DIR=" + account},
			}); err != nil {
				t.Fatal(err)
			}
			writeScheduleRestartHost(t, runtime.Paths.ProcRoot, seat.SeatID)
			fixture := &scheduleRestartHerdr{
				t:       t,
				db:      db,
				proc:    runtime.Paths.ProcRoot,
				pane:    seat.HerdrPane,
				seatID:  seat.SeatID,
				session: id,
				account: account,
				delayed: delayed,
				transcript: filepath.Join(
					filepath.Dir(os.Getenv("PFM_CLAUDE_ROOTS")),
					"claude",
					"project",
					id+".jsonl",
				),
			}
			fixture.serve(socket)
			t.Setenv("TMUX", "")
			t.Setenv("TMUX_PANE", "")
			t.Setenv("CLAUDE_CODE_SESSION_ID", id)
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", seat.HerdrPane)
			// No target argument: this is the chat arming its own one-time wake.
			code, stdout, stderr := runSchedule(t, "set", "--in", "1m", "--prompt", "check the build once")
			if code != 0 || strings.TrimSpace(stdout) != "1" {
				t.Fatalf("self-arm code=%d out=%q err=%q", code, stdout, stderr)
			}
			armed, found, err := db.Schedule(ctx, 1)
			if err != nil || !found || !armed.Once || armed.SessionID != id || armed.SetByID != id {
				t.Fatalf("self-armed schedule=%+v found=%t err=%v", armed, found, err)
			}
			if err := restart.LiveDeps(
				runtime,
				&bytes.Buffer{},
				nil,
				struct{ Socket, Pane string }{},
			).Fleet.End(
				ctx,
				headless.Chat{
					ID: id, SeatID: seat.SeatID, Backend: "herdr", Socket: socket, Pane: seat.HerdrPane,
				},
			); err != nil {
				t.Fatal(err)
			}
			retired, found, err := db.SeatBySession(ctx, id)
			if err != nil || !found {
				t.Fatalf("restart discarded native resume identity: found=%t err=%v", found, err)
			}
			if _, err := db.ReadSeatRun(retired.SeatID); err != nil {
				t.Fatalf("restart discarded account2 recipe: %v", err)
			}
			if retired.EndPending || retired.State == "live" {
				t.Fatalf("restart did not retire seat: %+v", retired)
			}
			// The host timer is out of process: no caller identity is inherited.
			t.Setenv("CLAUDE_CODE_SESSION_ID", "")
			t.Setenv("HERDR_PANE_ID", "w6:p1")
			ticks.elapsed.Add(int64(2 * time.Minute))
			var out, diagnostics bytes.Buffer
			code = run([]string{"internal", "schedule-fire"}, &out, &diagnostics)
			wantCode := 0
			if delayed {
				wantCode = 1
			}
			if code != wantCode {
				t.Fatalf("first tick code=%d want=%d out=%q err=%q", code, wantCode, out.String(), diagnostics.String())
			}
			fixture.mu.Lock()
			submitted, reads, layouts := fixture.submitted, fixture.reads, fixture.layouts
			fixture.mu.Unlock()
			if layouts != 1 || reads < 3 || len(submitted) != 1 || !strings.Contains(submitted[0], armed.Prompt) {
				t.Fatalf("revival/readiness/submission: layouts=%d captures=%d prompts=%q", layouts, reads, submitted)
			}
			current, found, err := db.SeatBySession(ctx, id)
			if err != nil || !found || current.SeatID == seat.SeatID || current.Backend != "herdr" {
				t.Fatalf("resumed seat=%+v found=%t err=%v", current, found, err)
			}
			recipe, err := db.ReadSeatRun(current.SeatID)
			if err != nil || !strings.Contains(strings.Join(recipe.Args, " "), "--resume "+id) ||
				!strings.Contains(strings.Join(recipe.Env, "\n"), "CLAUDE_CONFIG_DIR="+account) {
				t.Fatalf("resume lost session/account2: recipe=%+v err=%v", recipe, err)
			}
			after, _, err := db.Schedule(ctx, armed.ID)
			if err != nil {
				t.Fatal(err)
			}
			if delayed {
				if !after.LastFired.IsZero() || !after.NextFire.Equal(armed.NextFire) || after.LastError == "" {
					t.Fatalf("unproved submission consumed wake: %+v", after)
				}
				// Engine transcript arrival after an ambiguous acknowledgement is
				// independent of the scheduler retry. The tag must prevent retyping.
				fixture.appendPrompt(submitted[0])
			} else if after.LastFired.IsZero() || !after.Unseen {
				t.Fatalf("proved wake not marked: %+v", after)
			}
			out.Reset()
			diagnostics.Reset()
			if code := run([]string{"internal", "schedule-fire"}, &out, &diagnostics); code != 0 {
				t.Fatalf("second tick code=%d err=%q", code, diagnostics.String())
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.submitted) != 1 {
				t.Fatalf("second tick duplicated prompt: %q", fixture.submitted)
			}
			after, _, err = db.Schedule(ctx, armed.ID)
			if err != nil || after.LastFired.IsZero() {
				t.Fatalf("proved wake still due: %+v err=%v", after, err)
			}
		})
	}
}

func writeScheduleRestartHost(t *testing.T, root, seat string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "stat"), []byte("btime 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for pid, parent := range map[int]int{41: 1, 42: 41} {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields("S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100")
		fields[1] = strconv.Itoa(parent)
		argv := "/bin/sh\x00"
		if pid == 42 {
			argv = "/fixture/pfm\x00internal\x00seat-host\x00" + seat + "\x00"
		}
		for name, content := range map[string]string{"stat": fmt.Sprintf("%d (pfm) %s", pid, strings.Join(fields, " ")), "cmdline": argv} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

type scheduleRestartHerdr struct {
	t                                                *testing.T
	db                                               *fleetdb.Store
	mu                                               sync.Mutex
	proc, pane, seatID, session, account, transcript string
	delayed                                          bool
	reads, layouts                                   int
	submitted                                        []string
}

func (f *scheduleRestartHerdr) appendPrompt(text string) {
	f.t.Helper()
	body, err := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"content": text}})
	if err != nil {
		f.t.Fatal(err)
	}
	file, err := os.OpenFile(f.transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := file.Write(append(body, '\n')); err != nil {
		f.t.Error(err)
	}
	if err := file.Close(); err != nil {
		f.t.Error(err)
	}
}

func (f *scheduleRestartHerdr) serve(socket string) {
	f.t.Helper()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		f.t.Fatal(err)
	}
	done := make(chan struct{})
	f.t.Cleanup(func() { _ = listener.Close(); <-done })
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string                     `json:"id"`
					Method string                     `json:"method"`
					Params map[string]json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					f.t.Error(err)
					return
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				result := f.respond(req.Method, req.Params)
				if err := json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "result": result}); err != nil {
					f.t.Error(err)
				}
			}()
		}
	}()
}

func (f *scheduleRestartHerdr) respond(method string, params map[string]json.RawMessage) any {
	switch method {
	case "ping":
		return map[string]any{"type": "pong", "version": "0.9.5", "protocol": 22}
	case "pane.list":
		panes := []any{}
		if f.pane != "" {
			panes = append(panes, testjail.HerdrPaneInfo(f.pane)["pane"])
		}
		return map[string]any{"type": "pane_list", "panes": panes}
	case "pane.get":
		var pane string
		_ = json.Unmarshal(params["pane_id"], &pane)
		return testjail.HerdrPaneInfo(pane)
	case "pane.process_info":
		var pane string
		_ = json.Unmarshal(params["pane_id"], &pane)
		return map[string]any{
			"type": "pane_process_info",
			"process_info": map[string]any{
				"pane_id": pane, "shell_pid": 41,
				"foreground_processes": []any{map[string]any{"pid": os.Getpid(), "argv0": "pfm"}},
			},
		}
	case "pane.close":
		if err := os.RemoveAll(filepath.Join(f.proc, "42")); err != nil {
			f.t.Error(err)
		}
		f.pane = ""
		return map[string]any{"type": "ok"}
	case "layout.apply":
		f.layouts++
		var root struct {
			Command []string `json:"command"`
		}
		if err := json.Unmarshal(params["root"], &root); err != nil {
			f.t.Error(err)
		}
		if len(root.Command) != 4 {
			f.t.Errorf("layout launch command=%q", root.Command)
			return nil
		}
		f.seatID, f.pane = root.Command[3], "w6:p9"
		writeScheduleRestartHost(f.t, f.proc, f.seatID)
		return map[string]any{
			"layout": map[string]any{"workspace_id": "w6", "root": map[string]any{"type": "pane", "pane_id": f.pane}},
		}
	case "pane.read":
		f.reads++
		format := "text"
		if raw := params["format"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &format)
		}
		return map[string]any{
			"type": "pane_read",
			"read": map[string]any{
				"pane_id":      f.pane,
				"workspace_id": "w6",
				"text":         "────────\n❯ \n────────",
				"truncated":    false,
				"format":       format,
			},
		}
	case "agent.get", "agent.prompt":
		kind, state, seq := "agent_info", "idle", 1
		if method == "agent.prompt" {
			if f.reads < 3 {
				f.t.Errorf("prompt before readiness: captures=%d", f.reads)
			}
			pending, _, err := f.db.Schedule(context.Background(), 1)
			if err != nil || !pending.LastFired.IsZero() {
				f.t.Errorf("schedule marked before prompt proof: %+v err=%v", pending, err)
			}
			var text string
			if err := json.Unmarshal(params["text"], &text); err != nil {
				f.t.Error(err)
			}
			f.submitted = append(f.submitted, text)
			kind = "agent_prompted"
			if !f.delayed {
				state, seq = "working", 2
				f.appendPrompt(text)
			}
		}
		return map[string]any{
			"type": kind,
			"agent": map[string]any{
				"pane_id":          f.pane,
				"terminal_id":      "fixture-" + f.pane,
				"agent_status":     state,
				"state_change_seq": seq,
				"agent_session": map[string]string{
					"source": "hook",
					"agent":  "claude",
					"kind":   "session_id",
					"value":  f.session,
				},
			},
		}
	default:
		f.t.Errorf("unexpected Herdr method %q", method)
		return nil
	}
}
