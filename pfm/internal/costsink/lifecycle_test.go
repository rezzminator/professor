package costsink

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/costledger"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// fakeProc answers Stat for one pid from a script the test mutates.
type fakeProc struct {
	gather.ProcFS
	mu    sync.Mutex
	start uint64
	err   error
	calls int
}

func (f *fakeProc) Stat(_ int) (gather.ProcStat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return gather.ProcStat{}, f.err
	}
	return gather.ProcStat{StartTime: f.start}, nil
}

func (f *fakeProc) set(start uint64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.start, f.err = start, err
}

func runConfig(t *testing.T, proc *fakeProc) (Config, string, *syncBuffer) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "ledger")
	logs := &syncBuffer{}
	return Config{
		Listener: listener, LedgerDir: dir, Log: logs, PID: 4242, Start: 777, Proc: proc,
		PollEvery: 10 * time.Millisecond, FlushEvery: 10 * time.Millisecond, DrainIdle: 50 * time.Millisecond,
		DrainMax: 2 * time.Second, Wait: 20 * time.Second, MaxBody: 1 << 20, PruneAfter: 90 * 24 * time.Hour,
	}, dir, logs
}

func postHTTP(t *testing.T, url string, body []byte) {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST %s = %d", url, response.StatusCode)
	}
}

// The sink serves while its chat process lives, keeps accepting through a drain window after the
// process is gone, writes the halves still pending, and exits.
func TestRunServesUntilTheWatchedProcessIsGoneThenDrainsAndExits(t *testing.T) {
	proc := &fakeProc{start: 777}
	cfg, dir, _ := runConfig(t, proc)
	url := "http://" + cfg.Listener.Addr().String()
	done := make(chan error, 1)
	go func() { done <- Meter(context.Background(), cfg) }()

	postHTTP(t, url+"/v1/logs", logBody(apiRequest(testSession, "req_1", "repl_main_thread", 100)))
	select {
	case err := <-done:
		t.Fatalf("Run returned while the watched process lives: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	proc.set(0, fs.ErrNotExist)
	postHTTP(t, url+"/v1/logs", logBody(apiRequest(testSession, "req_2", "repl_main_thread", 200)))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit after the watched process went away")
	}
	ledger, err := costledger.ForSession(dir, testSession)
	if err != nil || len(ledger.Rows) != 2 {
		t.Fatalf(
			"ledger = %d rows, %v; want both the pending log-only rows written by the final drain",
			len(ledger.Rows),
			err,
		)
	}
	if _, err := (&http.Client{Timeout: time.Second}).Post(url+"/v1/logs", "application/json", nil); err == nil {
		t.Fatal("the listener still answers after Run returned")
	}
}

// A sink terminated by a signal (logout, shutdown) still drains: the half waiting for its partner
// is written alone instead of dying with the process.
func TestRunDrainsPendingHalvesWhenTerminated(t *testing.T) {
	proc := &fakeProc{start: 777}
	cfg, dir, _ := runConfig(t, proc)
	ctx, stop := sinkContext()
	defer stop()
	done := make(chan error, 1)
	go func() { done <- Meter(ctx, cfg) }()
	postHTTP(t, "http://"+cfg.Listener.Addr().String()+"/v1/logs",
		logBody(apiRequest(testSession, "req_1", "repl_main_thread", 100)))
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept serving after SIGTERM")
	}
	ledger, err := costledger.ForSession(dir, testSession)
	if err != nil || len(ledger.Rows) != 1 || ledger.Rows[0].Join != costledger.JoinLog {
		t.Fatalf("ledger = %+v, %v; want the pending half written alone", ledger.Rows, err)
	}
}

// A reused pid with another start time is another process: the chat is gone.
func TestRunTreatsAChangedStartTimeAsTheChatBeingGone(t *testing.T) {
	proc := &fakeProc{start: 999}
	cfg, _, _ := runConfig(t, proc)
	done := make(chan error, 1)
	go func() { done <- Meter(context.Background(), cfg) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept serving for a pid whose start time changed")
	}
	proc.mu.Lock()
	defer proc.mu.Unlock()
	if proc.calls == 0 {
		t.Fatal("Run exited without ever reading the watched process's start time")
	}
}

// A process table read that fails for another reason is not "gone": the sink keeps serving, logs
// the failure, and only gives up after a run of consecutive failures.
func TestRunKeepsServingThroughAnUnreadableProcessTableThenGivesUpLoudly(t *testing.T) {
	proc := &fakeProc{err: errors.New("sysctl: operation not permitted")}
	cfg, _, logs := runConfig(t, proc)
	cfg.MaxStatFailures = 5
	done := make(chan error, 1)
	go func() { done <- Meter(context.Background(), cfg) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "operation not permitted") {
			t.Fatalf("Run = %v, want the stat failure named", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run never gave up on an unreadable process table")
	}
	if !strings.Contains(logs.String(), "operation not permitted") {
		t.Fatalf("sink log = %q, want the cause", logs.String())
	}
}

func TestRunPrunesOldLedgerFilesAtStart(t *testing.T) {
	proc := &fakeProc{err: fs.ErrNotExist}
	cfg, dir, _ := runConfig(t, proc)
	old := filepath.Join(dir, "old-session.jsonl")
	if err := costledger.Append(dir, costledger.Row{RequestID: "r", SessionID: "old-session"}); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-100 * 24 * time.Hour)
	if err := chtimes(old, past); err != nil {
		t.Fatal(err)
	}
	if err := Meter(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if fileExists(old) {
		t.Fatal("the 100-day-old ledger file survived the prune at sink start")
	}
}
