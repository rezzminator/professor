package reload

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRunHerdrRejectsMissingTarget(t *testing.T) {
	_, err := RunHerdr(context.Background(), Request{}, Options{}, io.Discard)
	if err == nil || err.Error() != "native Herdr reload: socket and pane are required" {
		t.Fatalf("RunHerdr empty target error = %v, want explicit target validation", err)
	}
}

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
	mode      string
	onRead    func()
	onExit    func()
	prompted  string
}

func newReloadHerdrFixture(t *testing.T) *reloadHerdrFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv(paths.EnvStateDB, filepath.Join(home, "state", "pfm.db"))
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
	f := &reloadHerdrFixture{t: t, db: db, text: "❯ "}
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
	}
	f.seat = fleetdb.Seat{
		SeatID:      "reload-seat",
		Engine:      string(pfmengine.Claude),
		Backend:     "herdr",
		HerdrSocket: socket,
		HerdrPane:   f.request.Pane,
		SessionID:   f.request.SessionID,
		State:       "live",
		Name:        "REVIEW",
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
					response["error"] = map[string]string{"code": "reload_test_failure", "message": callErr}
				}
				_ = json.NewEncoder(conn).Encode(response)
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	return f
}

func (f *reloadHerdrFixture) respond(method string, p map[string]json.RawMessage) (any, string) {
	var pane string
	_ = json.Unmarshal(p["pane_id"], &pane)
	if method == "agent.get" || method == "agent.prompt" {
		_ = json.Unmarshal(p["target"], &pane)
	}
	if pane != f.request.Pane {
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
		if f.mode == "null-argv" {
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
				pid = 202
			}
			if f.mode == "wrong-model" {
				pid = 202
			}
		}
		argv := append([]string{pfmengine.MustLookup(f.request.Engine).Binary}, run.Args...)
		return f.processResult(
			[]any{map[string]any{"pid": pid, "name": "versioned-binary", "argv0": argv[0], "argv": argv}},
		), ""
	case "pane.read":
		if f.onRead != nil {
			fn := f.onRead
			f.onRead = nil
			fn()
		}
		if f.mode == "read-error" {
			return nil, "snapshot unavailable"
		}
		return map[string]any{
			"type": "pane_read",
			"read": map[string]any{"pane_id": f.request.Pane, "workspace_id": "w6", "text": f.text, "truncated": false},
		}, ""
	case "agent.get":
		status := "idle"
		if f.mode == "blocked" {
			status = "blocked"
		}
		return map[string]any{
			"type":  "agent_info",
			"agent": map[string]any{"pane_id": f.request.Pane, "agent_status": status},
		}, ""
	case "pane.send_text":
		var text string
		_ = json.Unmarshal(p["text"], &text)
		f.typed = append(f.typed, text)
		f.text = f.composer() + text
		return map[string]string{"type": "ok"}, ""
	case "pane.send_keys":
		var keys []string
		_ = json.Unmarshal(p["keys"], &keys)
		f.keys = append(f.keys, keys...)
		for _, key := range keys {
			switch key {
			case "C-s":
				f.text = f.composer()
			case "BSpace":
				f.text = strings.TrimSuffix(f.text, "t")
				if len(f.text) > len("❯ ") {
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
			"type":  "agent_prompted",
			"agent": map[string]any{"pane_id": f.request.Pane, "agent_status": "working"},
		}, ""
	default:
		return nil, "unexpected method " + method
	}
}

func (f *reloadHerdrFixture) processResult(processes []any) any {
	return map[string]any{
		"type":         "pane_process_info",
		"process_info": map[string]any{"pane_id": f.request.Pane, "foreground_processes": processes},
	}
}

func TestRunHerdrLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, mode, want string
		draft, then      bool
	}{
		{name: "model-and-account"},
		{name: "pending-cleared-before-new-process", mode: "pending-race"},
		{name: "stashed-draft", draft: true},
		{name: "same-target-then", then: true},
		{name: "failed-start", mode: "failed-start", want: "permission denied"},
		{name: "old-process-is-not-success", mode: "old-process", want: "new engine"},
		{name: "new-process-wrong-model", mode: "wrong-model", want: "new engine"},
		{name: "process-error", mode: "process-error", want: "process unavailable"},
		{name: "malformed-process", mode: "malformed-process", want: "process"},
		{name: "wrong-pane", mode: "wrong-pane", want: "pane"},
		{name: "empty-process", mode: "empty-process", want: "engine"},
		{name: "null-argv", mode: "null-argv", want: "argv"},
		{name: "snapshot-error", mode: "read-error", want: "snapshot unavailable"},
		{name: "blocked", mode: "blocked", want: "blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			f.mode = tc.mode
			if tc.draft {
				f.text = "❯ parked draft"
			}
			if tc.then {
				f.request.Then = "resume the port"
			}
			result, err := RunHerdr(context.Background(), f.request, f.options, io.Discard)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("RunHerdr error=%v, want %q", err, tc.want)
				}
				if tc.mode == "failed-start" {
					current, found, readErr := f.db.Seat(context.Background(), f.seat.SeatID)
					if readErr != nil || !found || current.State != "failed" || !current.ReloadPending ||
						!strings.Contains(f.text, "permission denied") {
						t.Fatalf("failed row=%+v screen=%q error=%v", current, f.text, readErr)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("RunHerdr: %v", err)
			}
			if result.Account != 2 {
				t.Fatalf("result=%+v", result)
			}
			current, found, err := f.db.Seat(context.Background(), f.seat.SeatID)
			if err != nil || !found || current.HerdrPane != f.request.Pane || current.ReloadPending ||
				current.State != "live" {
				t.Fatalf("seat=%+v found=%t err=%v", current, found, err)
			}
			run, err := f.db.ReadSeatRun(f.seat.SeatID)
			if err != nil {
				t.Fatal(err)
			}
			spawn, err := claudeSpawn(f.request)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := spawn.SeatRun(f.request.CWD)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(run, canonical) {
				t.Fatalf("recipe=%+v, canonical=%+v", run, canonical)
			}
			if tc.draft && !slices.Contains(f.keys, "C-s") {
				t.Fatalf("draft not stashed: %v", f.keys)
			}
			if tc.then && !strings.Contains(f.prompted, f.request.Then) {
				t.Fatalf("then=%q", f.prompted)
			}
			if tc.mode == "pending-race" && f.processes < 3 {
				t.Fatalf("returned on old process after %d probes", f.processes)
			}
		})
	}
}

func TestRunHerdrPublicationAndCancellation(t *testing.T) {
	for _, kind := range []string{"missing-recipe", "missing-seat", "publish-failure", "cancel-before", "cancel-after", "pending-write-failure", "already-pending", "failed-retry", "failed-retry-delayed", "failed-retry-failed", "concurrent"} {
		t.Run(kind, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := ""
			switch kind {
			case "missing-recipe":
				if err := f.db.DeleteSeatRun(f.seat.SeatID); err != nil {
					t.Fatal(err)
				}
				want = "seat run"
			case "missing-seat":
				if err := f.db.DeleteSeat(ctx, f.seat.SeatID); err != nil {
					t.Fatal(err)
				}
				want = "seat"
			case "publish-failure":
				f.onRead = func() {
					if err := f.db.DeleteSeat(ctx, f.seat.SeatID); err != nil {
						t.Error(err)
					}
				}
				want = "seat"
			case "cancel-before":
				cancel()
				want = "canceled"
			case "cancel-after":
				f.mode = "pending-kept"
				f.onExit = cancel
				want = "canceled"
			case "pending-write-failure":
				db, err := sqlitedb.OpenStore(ctx, filepath.Join(f.request.Home, "state", "pfm.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				}()
				if _, err := db.ExecContext(
					ctx,
					`CREATE TRIGGER deny_reload BEFORE UPDATE OF reload_pending ON seat WHEN new.reload_pending=1 BEGIN SELECT RAISE(FAIL, 'pending publication denied'); END`,
				); err != nil {
					t.Fatal(err)
				}
				want = "pending publication denied"
			case "already-pending":
				f.seat.ReloadPending = true
				if err := f.db.PutSeat(ctx, f.seat); err != nil {
					t.Fatal(err)
				}
				want = "pending"
			case "failed-retry", "failed-retry-delayed", "failed-retry-failed":
				f.mode = kind
				if kind == "failed-retry-failed" {
					want = "permission denied"
				}
				f.seat.State = "failed"
				f.seat.ReloadPending = true
				f.seat.LostNote = "previous failure"
				if err := f.db.PutSeat(ctx, f.seat); err != nil {
					t.Fatal(err)
				}
				f.text = "press Enter to retry, q to close"
			case "concurrent":
				// The public mutex remains observable; keep a real first owner until this request refuses.
				if err := os.MkdirAll(f.options.SIDDir, 0o700); err != nil {
					t.Fatal(err)
				}
				// Filled by the native lock path, not the tmux basename lock.
				key := LockPath(
					f.options.SIDDir,
					fmt.Sprintf("herdr-%x", sha256.Sum256([]byte(f.request.SocketPath+"\x00"+f.request.Pane))),
					"seat",
				)
				lock, err := os.OpenFile(key, os.O_CREATE|os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = lock.Close() }()
				if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
				want = "another reload"
			}
			_, err := RunHerdr(ctx, f.request, f.options, io.Discard)
			if want != "" {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error=%v want=%q", err, want)
				}
				if strings.HasPrefix(kind, "cancel") && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation identity lost: %v", err)
				}
				if kind == "publish-failure" || kind == "pending-write-failure" {
					run, readErr := f.db.ReadSeatRun(f.seat.SeatID)
					if readErr != nil || !reflect.DeepEqual(run, f.old) {
						t.Fatalf("unpublished recipe=%+v err=%v", run, readErr)
					}
				}
				if kind == "cancel-after" {
					current, found, readErr := f.db.Seat(context.Background(), f.seat.SeatID)
					if readErr != nil || !found || !current.ReloadPending {
						t.Fatalf("canceled reload lost pending: %+v %v", current, readErr)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			current, found, err := f.db.Seat(ctx, f.seat.SeatID)
			if err != nil || !found || current.State != "live" || current.ReloadPending {
				t.Fatalf("retried row=%+v found=%t err=%v", current, found, err)
			}
		})
	}
}

func TestRunHerdrUnsafePublication(t *testing.T) {
	for _, kind := range []string{"failed-seat-with-live-engine", "corrupt-current-recipe", "binding-changes-during-idle"} {
		t.Run(kind, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			want := ""
			switch kind {
			case "failed-seat-with-live-engine":
				f.seat.State = "failed"
				f.text = "press Enter to retry, q to close"
				if err := f.db.PutSeat(context.Background(), f.seat); err != nil {
					t.Fatal(err)
				}
				want = "engine is still running"
			case "corrupt-current-recipe":
				f.old.CWD = "relative-path"
				if err := f.db.WriteSeatRun(f.seat.SeatID, f.old); err != nil {
					t.Fatal(err)
				}
				want = "current recipe"
			case "binding-changes-during-idle":
				f.onRead = func() {
					before := f.seat
					after := before
					after.HerdrPane = "w9:p9"
					if err := f.db.UpdateSeat(context.Background(), before, after); err != nil {
						t.Error(err)
					}
				}
				want = "binding changed"
			}
			_, err := RunHerdr(context.Background(), f.request, f.options, io.Discard)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error=%v want=%q", err, want)
			}
			run, err := f.db.ReadSeatRun(f.seat.SeatID)
			if err != nil || !reflect.DeepEqual(run, f.old) {
				t.Fatalf("unpublished recipe=%+v want=%+v err=%v", run, f.old, err)
			}
		})
	}
}

func (f *reloadHerdrFixture) composer() string {
	if f.request.Engine == pfmengine.Codex {
		return "› "
	}
	return "❯ "
}

func TestRunHerdrCodex(t *testing.T) {
	for _, tc := range []struct {
		name, mode, want string
		fresh            bool
	}{
		{name: "resume-model-account-effort"},
		{name: "retained-account"},
		{name: "unnamed-seat"},
		{name: "new-session", fresh: true},
		{name: "failed-start", mode: "failed-start", want: "permission denied"},
		{name: "old-process-is-not-success", mode: "old-process", want: "new engine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			f.request.Engine = pfmengine.Codex
			f.request.Model = "native-model"
			f.request.New = tc.fresh
			f.request.CodexHome = filepath.Join(f.request.Home, "account-two")
			f.request.CodexBinary = filepath.Join(f.request.Home, pfmengine.MustLookup(pfmengine.Codex).Binary)
			f.request.CodexYolo = false
			f.request.PromptChannel = "Keep the seat contract."
			f.request.Machine.Version = pfmconfig.Version
			f.request.Machine.Codex = pfmconfig.CodexPrefs{Binary: f.request.CodexBinary}
			f.request.Machine.CodexAccounts = []pfmconfig.CodexAccount{
				{ID: 1, Home: filepath.Join(f.request.Home, "account-one")},
				{ID: 2, Home: f.request.CodexHome},
			}
			f.seat.Engine = string(pfmengine.Codex)
			if tc.name == "unnamed-seat" {
				f.seat.Name = ""
			}
			f.old.Binary = f.request.CodexBinary
			f.old.Args = []string{"resume", f.request.SessionID}
			wantAccount, wantHome := f.request.Account, f.request.CodexHome
			if tc.name == "retained-account" {
				before := f.request
				before.Account = 1
				var err error
				f.old, err = nativeReloadRun(before, f.seat.SeatID)
				if err != nil {
					t.Fatal(err)
				}
				f.request.AccountGiven = false
				wantAccount, wantHome = 1, f.request.Machine.CodexAccounts[0].Home
			}
			f.mode = tc.mode
			f.text = f.composer()
			if err := f.db.PutSeat(context.Background(), f.seat); err != nil {
				t.Fatal(err)
			}
			if err := f.db.WriteSeatRun(f.seat.SeatID, f.old); err != nil {
				t.Fatal(err)
			}
			result, err := RunHerdr(context.Background(), f.request, f.options, io.Discard)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("RunHerdr error=%v, want %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("RunHerdr: %v", err)
			}
			run, err := f.db.ReadSeatRun(f.seat.SeatID)
			if err != nil {
				t.Fatal(err)
			}
			for _, word := range []string{"--model", f.request.Model, "model_reasoning_effort=\"high\"", "developer_instructions=\"\"\"\nKeep the seat contract.\"\"\""} {
				if !slices.Contains(run.Args, word) {
					t.Fatalf("argv=%q missing %q", run.Args, word)
				}
			}
			if result.Account != wantAccount || run.Binary != f.request.CodexBinary ||
				!slices.Contains(run.Env, "CODEX_HOME="+wantHome) {
				t.Fatalf("recipe=%+v", run)
			}
			if !tc.fresh && (!slices.Contains(run.Args, "resume") || !slices.Contains(run.Args, f.request.SessionID)) {
				t.Fatalf("resume argv=%q", run.Args)
			}
			current, found, err := f.db.Seat(context.Background(), f.seat.SeatID)
			if err != nil || !found || current.HerdrPane != f.request.Pane || current.ReloadPending ||
				current.State != "live" {
				t.Fatalf("seat=%+v found=%t err=%v", current, found, err)
			}
			if tc.fresh && (!result.New || current.SessionID != "" || slices.Contains(run.Args, "resume")) {
				t.Fatalf("new result=%+v seat=%+v args=%q", result, current, run.Args)
			}
		})
	}
}

func TestRunHerdrPreservesBirthDefaults(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		accountGiven, cacheGiven, missing bool
		wantAccount                       int
		wantCache                         bool
		want                              string
	}{
		{name: "retain-account-and-cache", wantAccount: 1, wantCache: true},
		{name: "explicit-account-and-cache", accountGiven: true, cacheGiven: true, wantAccount: 2},
		{name: "explicit-account-retain-cache", accountGiven: true, wantAccount: 2, wantCache: true},
		{name: "explicit-cache-retain-account", cacheGiven: true, wantAccount: 1},
		{name: "missing-birth-metadata", missing: true, want: "account"},
		{name: "unknown-account", want: "account"},
		{name: "invalid-cache", want: "cache"},
		{name: "missing-cache", want: "cache"},
		{name: "stale-launch-record", wantAccount: 1, wantCache: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			if !tc.missing {
				before := f.request
				before.Account = 1
				before.Cache1H = true
				spawn, err := claudeSpawn(before)
				if err != nil {
					t.Fatal(err)
				}
				f.old, err = spawn.SeatRun(before.CWD)
				if err != nil {
					t.Fatal(err)
				}
				if tc.name == "unknown-account" {
					f.request.Machine.Accounts = f.request.Machine.Accounts[1:]
				}
				if tc.name == "invalid-cache" || tc.name == "missing-cache" {
					key := ""
					for _, knob := range claudelaunch.Knobs {
						if knob.Name == "cache1h" {
							key = knob.Target
						}
					}
					for i, entry := range f.old.Env {
						if strings.HasPrefix(entry, key+"=") {
							if tc.name == "invalid-cache" {
								f.old.Env[i] = key + "=not-a-cache"
							} else {
								f.old.Env = append(f.old.Env[:i], f.old.Env[i+1:]...)
							}
							break
						}
					}
				}
				if tc.name == "stale-launch-record" {
					values, err := paths.Resolve()
					if err != nil {
						t.Fatal(err)
					}
					if err := fleetdb.RecordLaunch(
						context.Background(),
						values,
						fleetdb.Launch{SessionID: f.seat.SessionID, Engine: f.request.Engine, Account: 2},
						1,
					); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.db.WriteSeatRun(f.seat.SeatID, f.old); err != nil {
					t.Fatal(err)
				}
			}
			f.request.AccountGiven = tc.accountGiven
			f.request.CacheGiven = tc.cacheGiven
			result, err := RunHerdr(context.Background(), f.request, f.options, io.Discard)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error=%v want %q metadata diagnostic", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Account != tc.wantAccount || result.Cache1H != tc.wantCache {
				t.Fatalf("result=%+v want account=%d cache=%t", result, tc.wantAccount, tc.wantCache)
			}
			expected := f.request
			expected.Account = tc.wantAccount
			expected.Cache1H = tc.wantCache
			spawn, err := claudeSpawn(expected)
			if err != nil {
				t.Fatal(err)
			}
			want, err := spawn.SeatRun(expected.CWD)
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.db.ReadSeatRun(f.seat.SeatID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("recipe=%+v want=%+v", got, want)
			}
		})
	}
}

func TestLockNativeReloadSafety(t *testing.T) {
	for _, kind := range []string{"new-directory", "existing-lock", "leaf-symlink", "dangling-symlink", "directory-symlink", "foreign-directory", "foreign-lock", "writable-directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			options := Options{SIDDir: filepath.Join(root, "sid"), IdleTries: 1, Poll: -1}
			options.defaults()
			request := Request{SocketPath: filepath.Join(root, "herdr.sock"), Pane: "w6:p3"}
			key := fmt.Sprintf("herdr-%x", sha256.Sum256([]byte(request.SocketPath+"\x00"+request.Pane)))
			path := LockPath(options.SIDDir, key, "seat")
			target := filepath.Join(root, "target")
			success := kind == "new-directory" || kind == "existing-lock"
			if kind != "new-directory" {
				if err := os.MkdirAll(options.SIDDir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "existing-lock", "foreign-lock":
				if err := os.WriteFile(path, []byte("lock-sentinel"), 0o600); err != nil {
					t.Fatal(err)
				}
				if kind == "foreign-lock" {
					if err := os.Chown(path, 65534, -1); err != nil {
						t.Fatalf("fenced foreign-owner fixture: %v", err)
					}
				}
			case "leaf-symlink", "dangling-symlink":
				if kind == "leaf-symlink" {
					if err := os.WriteFile(target, []byte("target-sentinel"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				if err := os.Remove(options.SIDDir); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, options.SIDDir); err != nil {
					t.Fatal(err)
				}
			case "foreign-directory":
				if err := os.Chown(options.SIDDir, 65534, -1); err != nil {
					t.Fatalf("fenced foreign-owner fixture: %v", err)
				}
			case "writable-directory":
				if err := os.Chmod(options.SIDDir, 0o777); err != nil {
					t.Fatal(err)
				}
			}
			lock, err := lockNativeReload(context.Background(), request, options)
			if lock != nil {
				if closeErr := lock.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			}
			if success {
				if err != nil {
					t.Fatalf("safe lock refused: %v", err)
				}
				info, statErr := os.Stat(path)
				if statErr != nil || !info.Mode().IsRegular() {
					t.Fatalf("lock info=%v err=%v", info, statErr)
				}
				return
			}
			if err == nil {
				t.Fatalf("%s accepted an unsafe native reload lock", kind)
			}
			if !strings.Contains(err.Error(), "native reload") {
				t.Fatalf("lock error lacks context: %v", err)
			}
			if kind == "leaf-symlink" {
				got, readErr := os.ReadFile(target)
				if readErr != nil || string(got) != "target-sentinel" {
					t.Fatalf("symlink target=%q err=%v", got, readErr)
				}
			}
		})
	}
}
