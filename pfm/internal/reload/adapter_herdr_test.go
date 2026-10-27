package reload

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestNativeReloadAdapterTarget(t *testing.T) {
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	socket := filepath.Join(t.TempDir(), "arbitrary.sock")
	for _, pane := range []string{"w6:p3", "w6:p4"} {
		if err := db.PutSeat(
			ctx,
			fleetdb.Seat{
				SeatID:      pane,
				Backend:     "herdr",
				Engine:      string(pfmengine.Codex),
				State:       "live",
				HerdrSocket: socket,
				HerdrPane:   pane,
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name, sock, pane string
		bad              bool
	}{{"selected", socket, "w6:p3", false}, {"ambiguous", socket, "", true}, {"missing-pane", socket, "w6:p7", true}} {
		t.Run(test.name, func(t *testing.T) {
			seat, native, err := NativeTarget(ctx, values, test.sock, test.pane, &paths.MapEnv{})
			if !native || (err != nil) != test.bad ||
				(!test.bad && (seat == nil || seat.HerdrPane != test.pane || seat.Engine != string(pfmengine.Codex))) {
				t.Fatalf("seat=%+v native=%v err=%v", seat, native, err)
			}
		})
	}
	seat, err := SeatForReload(ctx, values, socket, "w6:p3")
	if err != nil || seat == nil || seat.HerdrPane != "w6:p3" {
		t.Fatalf("seat=%+v err=%v", seat, err)
	}
}

func TestNativeReloadAdapterEngine(t *testing.T) {
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	socket := filepath.Join(t.TempDir(), "arbitrary.sock")
	if err := db.PutSeat(
		ctx,
		fleetdb.Seat{
			SeatID:      "engine-adapter",
			Backend:     "herdr",
			Engine:      string(pfmengine.Codex),
			State:       "live",
			HerdrSocket: socket,
			HerdrPane:   "w6:p3",
		},
	); err != nil {
		t.Fatal(err)
	}
	got, err := EngineForTarget(ctx, values, socket, "w6:p3")
	if err != nil || got != pfmengine.Codex {
		t.Fatalf("engine=%q err=%v", got, err)
	}
}

func TestNotifyWorkerFailureDelivery(t *testing.T) {
	previousDelays := notifyRetryDelays
	notifyRetryDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() { notifyRetryDelays = previousDelays })
	const shown = `{"type":"notification_show","shown":true,"reason":"shown"}`
	const rateLimited = `{"type":"notification_show","shown":false,"reason":"rate_limited"}`
	for _, tc := range []struct {
		name, reply, want string
		// then answers every attempt after the first; attempts is how many
		// notification.show requests Herdr receives (0 means one).
		then     string
		attempts int
		// retired: the seat row is gone (the chat ended) under a native worker.
		retired bool
	}{
		{name: "retired seat of a native worker", reply: shown, retired: true},
		{name: "rate_limited then shown", reply: rateLimited, then: shown, attempts: 2},
		{name: "rate_limited on every retry", reply: rateLimited, want: "not shown (rate_limited)", attempts: 4},
		{"shown", `{"type":"notification_show","shown":true,"reason":"shown"}`, "", "", 0, false},
		{"disabled", `{"type":"notification_show","shown":false,"reason":"disabled"}`, "not shown (disabled)", "", 0, false},
		{"busy", `{"type":"notification_show","shown":false,"reason":"busy"}`, "not shown (busy)", "", 0, false},
		{"no_foreground_client", `{"type":"notification_show","shown":false,"reason":"no_foreground_client"}`, "not shown (no_foreground_client)", "", 0, false},
		{"wrong type", `{"type":"ok","shown":true,"reason":"shown"}`, "acknowledgement", "", 0, false},
		{"missing shown", `{"type":"notification_show","reason":"shown"}`, "acknowledgement", "", 0, false},
		{"missing reason", `{"type":"notification_show","shown":true}`, "acknowledgement", "", 0, false},
		{"contradictory", `{"type":"notification_show","shown":true,"reason":"disabled"}`, "acknowledgement", "", 0, false},
		{"wrong shown shape", `{"type":"notification_show","shown":"true","reason":"shown"}`, "decode", "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "pfm.db"))
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(testjail.ShortRoot(t), "n.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := listener.Close(); err != nil {
					t.Error(err)
				}
			})
			var mu sync.Mutex
			accepted := 0
			serve := func(conn net.Conn, reply string) {
				defer func() {
					if err := conn.Close(); err != nil {
						t.Error(err)
					}
				}()
				if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Error(err)
					return
				}
				var request struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(conn).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request.Method != "notification.show" {
					t.Errorf("method=%s", request.Method)
				}
				if err := json.NewEncoder(conn).
					Encode(map[string]any{"id": request.ID, "result": json.RawMessage(reply)}); err != nil {
					t.Error(err)
				}
			}
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					mu.Lock()
					attempt := accepted
					accepted++
					mu.Unlock()
					reply := tc.reply
					if attempt > 0 && tc.then != "" {
						reply = tc.then
					}
					serve(conn, reply)
				}
			}()
			db := fleetdb.OpenSharedState(t.Context(), values)
			// A retired seat leaves the store empty.
			if !tc.retired {
				if err := db.PutSeat(
					t.Context(),
					fleetdb.Seat{
						SeatID:      "notify",
						Backend:     "herdr",
						HerdrSocket: socket,
						HerdrPane:   "w6:p3",
						State:       "live",
						Engine:      "claude",
					},
				); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			handled, err := NotifyWorkerFailure(t.Context(), values, socket, "w6:p3", "worker failed", true)
			if !handled || (tc.want == "" && err != nil) ||
				(tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) {
				t.Fatalf("handled=%v err=%v, want %q", handled, err, tc.want)
			}
			wantAttempts := max(tc.attempts, 1)
			mu.Lock()
			got := accepted
			mu.Unlock()
			if got != wantAttempts {
				t.Fatalf("notification.show requests = %d, want %d", got, wantAttempts)
			}
		})
	}
}

// TestLockWorkerScheduleOpensOnlyPrivateFilesAndBoundsTheWait: the SID
// directory defaults to a shared /tmp path, so a native seat's reload record
// and worker log open only as the user's private regular files inside a
// directory no one else can write; a planted symlink or FIFO is refused, never
// followed or blocked on, and a record another scheduler holds is refused
// after a bounded wait.
func TestLockWorkerScheduleOpensOnlyPrivateFilesAndBoundsTheWait(t *testing.T) {
	previous := scheduleLockWait
	scheduleLockWait = 50 * time.Millisecond
	t.Cleanup(func() { scheduleLockWait = previous })
	const socket, pane = "/fixture/herdr.sock", "w6:p3"
	for _, tc := range []struct {
		name, want string
		plant      func(t *testing.T, sid, record, log string)
		openLog    bool
	}{
		{name: "private files open", plant: func(*testing.T, string, string, string) {}, openLog: true},
		{
			name: "symlinked record", want: "open reload schedule record",
			plant: func(t *testing.T, sid, record, _ string) {
				if err := os.Symlink(filepath.Join(sid, "elsewhere"), record); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "FIFO record", want: "not a private single-link regular file",
			plant: func(t *testing.T, _, record, _ string) {
				if err := unix.Mkfifo(record, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "directory others can write", want: "not a protected user directory",
			plant: func(t *testing.T, sid, _, _ string) {
				if err := os.Chmod(sid, 0o777); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "record held by another scheduler", want: "another /reload is scheduling this chat right now",
			plant: func(t *testing.T, _, record, _ string) {
				held, err := os.OpenFile(record, os.O_CREATE|os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = held.Close() })
				if err := unix.Flock(int(held.Fd()), unix.LOCK_EX); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlinked log", want: "reload-herdr-lock.log", openLog: true,
			plant: func(t *testing.T, sid, _, log string) {
				if err := os.Symlink(filepath.Join(sid, "elsewhere"), log); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := testjail.ShortRoot(t)
			sid := filepath.Join(root, "sid")
			if err := os.Mkdir(sid, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv(paths.EnvStateDB, filepath.Join(root, "pfm.db"))
			t.Setenv(paths.EnvSIDDir, sid)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			db := fleetdb.OpenSharedState(t.Context(), values)
			err = db.PutSeat(t.Context(), fleetdb.Seat{
				SeatID: "lock", Backend: "herdr", HerdrSocket: socket, HerdrPane: pane, State: "live", Engine: "claude",
			})
			if closeErr := db.Close(); err != nil || closeErr != nil {
				t.Fatalf("seed: %v %v", err, closeErr)
			}
			record := filepath.Join(sid, "reload-herdr-lock.pid")
			wantLog := filepath.Join(sid, "reload-herdr-lock.log")
			tc.plant(t, sid, record, wantLog)
			type locked struct {
				log      string
				schedule *NativeSchedule
				err      error
			}
			done := make(chan locked, 1)
			go func() {
				log, schedule, err := LockWorkerSchedule(t.Context(), values, socket, pane)
				done <- locked{log, schedule, err}
			}()
			var got locked
			select {
			case got = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("LockWorkerSchedule still waiting after 3s: the wait is unbounded")
			}
			if got.log != wantLog {
				t.Fatalf("log = %q, want %q", got.log, wantLog)
			}
			if tc.openLog {
				if got.err != nil {
					t.Fatalf("lock: %v", got.err)
				}
				got.schedule.Close(&strings.Builder{})
				log, err := OpenWorkerLog(got.log)
				if tc.want == "" {
					if err != nil {
						t.Fatalf("open private log: %v", err)
					}
					if err := log.Close(); err != nil {
						t.Fatal(err)
					}
					return
				}
				got.err = err
			}
			if got.err == nil || !strings.Contains(got.err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %q", got.err, tc.want)
			}
			if tc.name == "record held by another scheduler" && !errors.As(got.err, new(*ScheduledError)) {
				t.Fatalf("a held record must refuse as *ScheduledError (rc 2): %v", got.err)
			}
			if _, err := os.Lstat(filepath.Join(sid, "elsewhere")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a symlink target was created or followed: %v", err)
			}
		})
	}
}

// TestStartWorkerCommitsTheStartAndItsRecord holds F2c at the reload door: the
// worker start and its record run inside one commit span of the caller's
// gate, which a deadline closes only between spans.
func TestStartWorkerCommitsTheStartAndItsRecord(t *testing.T) {
	const socket, pane = "/fixture/herdr.sock", "w6:p3"
	pid := os.Getpid()
	for _, tc := range []struct {
		name        string
		gateOpen    bool
		wantStarted bool
		wantRecord  bool
		wantErr     string
	}{
		{name: "an open gate starts, records, then ends the span", gateOpen: true, wantStarted: true, wantRecord: true},
		{
			name: "a gate the deadline closed starts nothing", gateOpen: false,
			wantErr: "the prompt hook's deadline passed before the reload worker started; nothing was scheduled",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := testjail.ShortRoot(t)
			sid := filepath.Join(root, "sid")
			if err := os.Mkdir(sid, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv(paths.EnvStateDB, filepath.Join(root, "pfm.db"))
			t.Setenv(paths.EnvSIDDir, sid)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			db := fleetdb.OpenSharedState(t.Context(), values)
			err = db.PutSeat(t.Context(), fleetdb.Seat{
				SeatID:      "commit",
				Backend:     "herdr",
				HerdrSocket: socket,
				HerdrPane:   pane,
				State:       "live",
				Engine:      "claude",
			})
			if closeErr := db.Close(); err != nil || closeErr != nil {
				t.Fatalf("seed: %v %v", err, closeErr)
			}
			_, schedule, err := LockWorkerSchedule(t.Context(), values, socket, pane)
			if err != nil {
				t.Fatalf("lock: %v", err)
			}
			t.Cleanup(func() { schedule.Close(&strings.Builder{}) })
			record := filepath.Join(sid, paths.SIDReloadHerdrPrefix+"commit.pid")
			recordAtEnd, ends := "", 0
			ctx := deps.WithCommitGate(t.Context(), func() (func(), bool) {
				if !tc.gateOpen {
					return nil, false
				}
				return func() {
					ends++
					data, err := os.ReadFile(record)
					if err != nil {
						t.Errorf("read record at span end: %v", err)
					}
					recordAtEnd = string(data)
				}, true
			})
			started := false
			var stderr strings.Builder
			err = StartWorker(ctx, schedule, &stderr, func() (int, error) {
				started = true
				return pid, nil
			})
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("StartWorker err = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("StartWorker = %v, want nil", err)
			}
			if started != tc.wantStarted {
				t.Fatalf("worker started = %v, want %v", started, tc.wantStarted)
			}
			wantEnds := 0
			if tc.gateOpen {
				wantEnds = 1
			}
			if ends != wantEnds {
				t.Fatalf("span ended %d times, want %d", ends, wantEnds)
			}
			if tc.wantRecord && !strings.HasPrefix(recordAtEnd, strconv.Itoa(pid)+" ") {
				t.Fatalf("record when the span ended = %q, want it to name worker %d", recordAtEnd, pid)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}
