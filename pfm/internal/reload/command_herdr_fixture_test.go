package reload

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// reloadHerdrFixture is the fake Herdr server and seat store a native reload
// runs against: it answers each socket request the reload sends and records
// what was typed, prompted and reported.
type reloadHerdrFixture struct {
	t         *testing.T
	db        *fleetdb.Store
	request   Request
	options   Options
	seat      fleetdb.Seat
	old       fleetdb.SeatRun
	mu        sync.Mutex
	text      string
	keys      []string
	typed     []string
	submitted bool
	processes int
	// badReads counts the version-named rows without argv served before /exit.
	badReads  int
	mode      string
	canonical string
	terminal  string
	values    paths.Values
	onRead    func()
	onExit    func()
	prompted  string
	// proc is the process table the run reads (PFM_PROC_ROOT).
	proc string

	// agentReads counts agent.get calls.
	agentReads int
	// registered is Herdr having refused one agent read of the reborn chat.
	registered bool
	// sessionReports are the pane.report_agent_session requests received,
	// seq given as its distance from the fixture clock at receipt.
	sessionReports []string
}

// assertLaunchRecorded checks the launch record of the reloaded session: the
// reload's account 2 and 5m cache when recorded, else no record at all.
func (f *reloadHerdrFixture) assertLaunchRecorded(recorded bool) {
	f.t.Helper()
	launches, err := fleetdb.OpenLaunches(context.Background(), f.values)
	if err != nil {
		f.t.Fatal(err)
	}
	record, err := launches.LaunchFor(context.Background(), f.request.SessionID)
	if closeErr := launches.Close(); closeErr != nil {
		f.t.Error(closeErr)
	}
	if !recorded {
		if !errors.Is(err, fleetdb.ErrNoLaunch) {
			f.t.Fatalf(
				"launch record of %s in %s = %+v, %v; want none",
				f.request.SessionID,
				f.values.StateDB,
				record,
				err,
			)
		}
		return
	}
	if err != nil || record.Account != 2 || record.Cache1H || record.Engine != pfmengine.Claude {
		f.t.Fatalf("launch record of %s in %s = %+v, %v; want account 2, 5m cache, claude",
			f.request.SessionID, f.values.StateDB, record, err)
	}
}

// bundledRgRow is the child Claude native runs its bundled rg as: its own
// executable under argv0 rg, so its name is Claude's version.
const bundledRgRow = `{"pid":303,"name":"2.1.330","argv0":"rg","argv":["rg","--files"]}`

func newReloadHerdrFixture(t *testing.T) *reloadHerdrFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv(paths.EnvStateDB, filepath.Join(home, "state", "pfm.db"))
	t.Setenv(paths.EnvCacheDB, filepath.Join(home, "state", "pfm-cache.db"))
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	db := fleetdb.OpenSharedState(context.Background(), values)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	socket := filepath.Join(testjail.ShortRoot(t), "h.sock")
	f := &reloadHerdrFixture{t: t, db: db, text: "❯ ", canonical: "w6:p3", terminal: "reload-terminal", values: values}
	proc := t.TempDir()
	f.proc = proc
	t.Setenv(paths.EnvProcRoot, proc)
	if err := os.WriteFile(filepath.Join(proc, "stat"), []byte("btime 1000000000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(proc, "90")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "stat"),
		[]byte("90 (pfm) S 1 "+strings.Repeat("0 ", 17)+"9 0\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "cmdline"),
		[]byte("pfm\x00internal\x00seat-host\x00reload-seat\x00"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	f.request = Request{
		Engine:       pfmengine.Claude,
		SocketPath:   socket,
		Pane:         "w6:p3",
		SessionID:    "11111111-1111-4111-8111-111111111111",
		CWD:          home,
		Home:         home,
		Account:      2,
		AccountGiven: true, CacheGiven: true,
		AccountIDs: []int{1, 2},
		Model:      "sonnet",
		Effort:     "high",
		Machine:    reloadTestMachine("", home),
	}
	f.options = Options{
		Home:           home,
		SIDDir:         filepath.Join(home, "sid"),
		Delay:          -1,
		Poll:           -1,
		IdleTries:      5,
		ExitTries:      5,
		ExitGraceTries: 3,
		ThenTries:      5,
		Clock:          clock.NewFake(time.Unix(0, 0)),
	}
	f.seat = fleetdb.Seat{
		SeatID:          "reload-seat",
		Engine:          string(pfmengine.Claude),
		Backend:         "herdr",
		HerdrSocket:     socket,
		HerdrPane:       f.request.Pane,
		HerdrWorkspace:  "w6",
		HerdrTerminalID: f.terminal,
		SeatHostPID:     90, SeatHostStart: 9,
		SessionID: f.request.SessionID,
		State:     "live",
		Name:      "REVIEW",
	}
	descriptor := pfmengine.MustLookup(pfmengine.Claude)
	f.old = fleetdb.SeatRun{
		Binary: filepath.Join(home, descriptor.Binary),
		CWD:    home,
		Args:   []string{"--resume", f.request.SessionID, "--model", "opus"},
		Env:    []string{"OLD=recipe"},
	}
	f.request.Machine.Claude.Binary = f.old.Binary
	if err := db.PutSeat(context.Background(), f.seat); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteSeatRun(f.seat.SeatID, f.old); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
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
					ID     json.RawMessage            `json:"id"`
					Method string                     `json:"method"`
					Params map[string]json.RawMessage `json:"params"`
				}
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				if err := json.Unmarshal(line, &req); err != nil {
					return
				}
				f.mu.Lock()
				result, callErr := f.respond(req.Method, req.Params)
				f.mu.Unlock()
				response := map[string]any{"id": req.ID, "result": result, "ok": callErr == ""}
				if callErr != "" {
					code, message := "reload_test_failure", callErr
					if rest, ok := strings.CutPrefix(callErr, herdr.AgentNotFound+": "); ok {
						code, message = herdr.AgentNotFound, rest
					}
					response["error"] = map[string]string{"code": code, "message": message}
				}
				_ = json.NewEncoder(conn).Encode(response)
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	return f
}

func (f *reloadHerdrFixture) respond(method string, p map[string]json.RawMessage) (any, string) {
	if method == "pane.list" {
		info := testjail.HerdrPaneInfo(f.canonical)["pane"].(map[string]any)
		info["terminal_id"] = f.terminal
		return map[string]any{"type": "pane_list", "panes": []any{info}}, ""
	}
	if method == "pane.get" {
		info := testjail.HerdrPaneInfo(f.canonical)
		info["pane"].(map[string]any)["terminal_id"] = f.terminal
		return info, ""
	}
	var pane string
	_ = json.Unmarshal(p["pane_id"], &pane)
	if method == "agent.get" || method == "agent.prompt" {
		_ = json.Unmarshal(p["target"], &pane)
	}
	if pane != f.canonical {
		return nil, "wrong target pane " + pane
	}
	switch method {
	case "pane.process_info":
		if f.mode == "process-error" {
			return nil, "process unavailable"
		}
		if f.mode == "malformed-process" {
			return map[string]any{"type": "ok"}, ""
		}
		if f.mode == "wrong-pane" {
			return map[string]any{"type": "pane_process_info", "process_info": map[string]any{"pane_id": "w9:p9"}}, ""
		}
		if f.mode == "empty-process" || f.mode == "failed-retry-failed" ||
			strings.HasPrefix(f.mode, "failed-retry") && !f.submitted {
			return f.processResult([]any{}), ""
		}
		// Claude native runs its bundled rg from its own executable under argv0
		// rg, so the child carries Claude's version as its name.
		if f.mode == "bundled-rg-alone" {
			return f.processResult([]any{json.RawMessage(bundledRgRow)}), ""
		}
		if f.mode == "omitted-process" {
			return map[string]any{
				"type":         "pane_process_info",
				"process_info": map[string]any{"pane_id": f.canonical, "shell_pid": 90},
			}, ""
		}
		// Herdr now and then lists a live Claude by its version name with an
		// empty argv (Herdr 0.9.5, Claude Code 2.1.330): the two seat proofs, the
		// target refresh and the first engine read before /exit get it.
		if !f.submitted && (f.mode == "version-named-no-argv" ||
			f.mode == "version-named-no-argv-once" && f.badReads < 4) {
			f.badReads++
			return f.processResult([]any{json.RawMessage(`{"pid":3300341,"name":"2.1.330","argv":[]}`)}), ""
		}
		// One startup poll reads the pane's processes three times (two target
		// refreshes, then the engine read): the first whole poll sees it dying.
		dying := f.submitted && (f.mode == "argv-gone" || f.mode == "argv-gone-once" && f.processes < 3)
		if dying {
			f.processes++
		}
		if f.mode == "null-argv" || dying {
			return f.processResult(
				[]any{
					map[string]any{
						"pid":   101,
						"name":  pfmengine.MustLookup(f.request.Engine).Binary,
						"argv0": nil,
						"argv":  nil,
					},
				},
			), ""
		}
		run := f.old
		pid := 101
		if f.submitted {
			f.processes++
			if f.mode == "failed-retry-delayed" && f.processes == 2 {
				before, found, err := f.db.Seat(context.Background(), f.seat.SeatID)
				if err != nil || !found {
					return nil, fmt.Sprintf("retry read: %v found=%t", err, found)
				}
				after := before
				after.State = "live"
				after.ReloadPending = false
				if err := f.db.UpdateSeat(context.Background(), before, after); err != nil {
					return nil, err.Error()
				}
				f.text = f.composer()
			}
			if f.mode != "old-process" && f.mode != "wrong-model" && (f.mode != "pending-race" || f.processes > 2) {
				var err error
				run, err = f.db.ReadSeatRun(f.seat.SeatID)
				if err != nil {
					return nil, err.Error()
				}
				if f.mode == "seat-host-names-remote-control" {
					run.Args = claudelaunch.RefreshRemoteControl(run.Args, true, "tab1")
				}
				pid = 202
			}
			if f.mode == "wrong-model" {
				pid = 202
			}
		}
		argv := append([]string{pfmengine.MustLookup(f.request.Engine).Binary}, run.Args...)
		rows := []any{map[string]any{"pid": pid, "name": "versioned-binary", "argv0": argv[0], "argv": argv}}
		if f.mode == "bundled-rg-first" {
			rows = append([]any{json.RawMessage(bundledRgRow)}, rows...)
		}
		return f.processResult(rows), ""
	case "pane.read":
		if f.onRead != nil {
			fn := f.onRead
			f.onRead = nil
			fn()
		}
		if f.mode == "read-error" {
			return nil, "snapshot unavailable"
		}
		format := "text"
		text := strings.NewReplacer("\x1b[2m", "", "\x1b[0m", "").Replace(f.text)
		if string(p["format"]) == `"ansi"` {
			format = "ansi"
			text = f.text
		}
		if string(p["source"]) != `"visible"` {
			f.t.Errorf("reload composer starts history collection: %s", p["source"])
		}
		if strings.HasPrefix(text, "❯") {
			text = "────────\n" + text + "\n────────"
		}
		return map[string]any{
			"type": "pane_read",
			"read": map[string]any{
				"pane_id":      f.canonical,
				"workspace_id": strings.Split(f.canonical, ":")[0],
				"text":         text,
				"truncated":    false,
				"format":       format,
			},
		}, ""
	case "agent.get":
		f.agentReads++
		// Herdr has not registered the agent: before /exit, or in the moment
		// after the reborn Claude starts, or never.
		if f.mode == "agent-not-found-before-exit" && !f.submitted ||
			f.mode == "agent-not-found-after-reboot" && f.submitted && !f.registered ||
			f.mode == "agent-never-registers" && f.submitted {
			f.registered = true
			return nil, herdr.AgentNotFound + ": agent target " + pane + " not found"
		}
		var status any = "idle"
		switch {
		case f.mode == "blocked":
			status = "blocked"
		case f.mode == "working-behind-idle-composer" || f.mode == "working-turn":
			status = "working"
		case f.mode == "unrecognized-agent" || f.mode == "unrecognized-agent-once" && f.agentReads == 1:
			status = "thinking"
		case f.mode == "undecodable-agent-once" && f.agentReads == 1:
			status = 7
		case f.mode == "unknown-agent" || f.mode == "unknown-agent-once" && f.agentReads == 1:
			status = "unknown" // Herdr's state for a Claude it has not classified yet
		}
		return map[string]any{
			"type":  "agent_info",
			"agent": map[string]any{"pane_id": f.canonical, "terminal_id": f.terminal, "agent_status": status},
		}, ""
	case "pane.send_text":
		var text string
		_ = json.Unmarshal(p["text"], &text)
		f.typed = append(f.typed, text)
		if f.mode != "exit-unrendered" {
			f.text = f.composer() + text
		}
		return map[string]string{"type": "ok"}, ""
	case "pane.send_keys":
		var keys []string
		_ = json.Unmarshal(p["keys"], &keys)
		f.keys = append(f.keys, keys...)
		for _, key := range keys {
			switch key {
			case "ctrl+s":
				if f.mode != "stash-refused" {
					f.text = f.composer()
				}
			case "backspace":
				if len(f.text) > len(f.composer()) {
					f.text = f.text[:len(f.text)-1]
				}
			case "Enter":
				if f.text == f.composer()+"/exit" || strings.Contains(f.text, "press Enter to retry") {
					current, found, err := f.db.Seat(context.Background(), f.seat.SeatID)
					if err != nil || !found || !current.ReloadPending {
						return nil, fmt.Sprintf(
							"exit before pending publication: found=%t row=%+v err=%v",
							found,
							current,
							err,
						)
					}
					run, err := f.db.ReadSeatRun(f.seat.SeatID)
					if err != nil || !slices.Contains(run.Args, f.request.Model) {
						return nil, fmt.Sprintf("exit before canonical recipe: %+v %v", run, err)
					}
					f.submitted = true
					before := current
					switch f.mode {
					case "failed-start", "failed-retry-failed":
						current.State = "failed"
						current.LostNote = "exec new engine: permission denied"
						f.text = "exec new engine: permission denied\npress Enter to retry, q to close"
					case "failed-retry-delayed":
						current.State = "failed"
					default:
						current.ReloadPending = f.mode == "pending-kept"
						current.State = "live"
						f.text = f.composer()
					}
					if err := f.db.UpdateSeat(context.Background(), before, current); err != nil {
						return nil, err.Error()
					}
					if f.onExit != nil {
						f.onExit()
					}
				}
			}
		}
		return map[string]string{"type": "ok"}, ""
	case "agent.prompt":
		_ = json.Unmarshal(p["text"], &f.prompted)
		return map[string]any{
			"type": "agent_prompted",
			"agent": map[string]any{
				"pane_id":      f.canonical,
				"terminal_id":  f.terminal,
				"agent_status": "working",
			},
		}, ""
	case "pane.report_agent_session":
		var report struct {
			Source      string `json:"source"`
			Agent       string `json:"agent"`
			Session     string `json:"agent_session_id"`
			StartSource string `json:"session_start_source"`
			Seq         int64  `json:"seq"`
		}
		raw, err := json.Marshal(p)
		if err == nil {
			err = json.Unmarshal(raw, &report)
		}
		if err != nil {
			return nil, err.Error()
		}
		f.sessionReports = append(f.sessionReports, fmt.Sprintf("source=%s agent=%s session=%s start=%s seq-clock=%d",
			report.Source, report.Agent, report.Session, report.StartSource,
			report.Seq-f.options.Clock.Now().UnixNano()))
		return map[string]string{"type": "ok"}, ""
	default:
		return nil, "unexpected method " + method
	}
}

func (f *reloadHerdrFixture) processResult(processes []any) any {
	return map[string]any{
		"type":         "pane_process_info",
		"process_info": map[string]any{"pane_id": f.canonical, "shell_pid": 90, "foreground_processes": processes},
	}
}
