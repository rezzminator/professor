package costsink

import (
	"context"
	"errors"
	"fmt"
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

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/costledger"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testwait"
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

func (f *fakeProc) statCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
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
		// The ledger's file times are the host's: the fake starts at the host's now.
		Clock: clock.NewFake(time.Now()),
	}, dir, logs
}

// meterClock is the fake clock runConfig gave cfg.
func meterClock(t *testing.T, cfg Config) *clock.Fake {
	t.Helper()
	fake, ok := cfg.Clock.(*clock.Fake)
	if !ok {
		t.Fatalf("cfg.Clock = %T, want the *clock.Fake runConfig set", cfg.Clock)
	}
	return fake
}

// meterPoll moves the sink one poll on, once its poll and flush tickers are armed.
func meterPoll(t *testing.T, cfg Config) {
	t.Helper()
	fake := meterClock(t, cfg)
	testwait.Until(t, "the sink to arm its poll and flush tickers", func() (bool, string) {
		return fake.Pending() >= 2, fmt.Sprintf("%d waits armed", fake.Pending())
	})
	fake.Advance(cfg.PollEvery)
}

// meterReturned drives the sink's fake clock to each wait it parks on until Meter returns on done:
// every event the test makes (a POST, a signal) has happened by then, so the polls, the flushes and
// the drain window run on fake time alone. Meter runs on a goroutine the test started, a member of
// the driven family, so the clock moves only while the sink is parked.
func meterReturned(t *testing.T, condition string, cfg Config, done <-chan error) error {
	t.Helper()
	var err error
	testwait.Drive(t, condition, meterClock(t, cfg), func() { err = <-done })
	return err
}

func postHTTP(t *testing.T, url string, body []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		testwait.Context(t), http.MethodPost, url, strings.NewReader(string(body)),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
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
	// The sink has polled the watched process three times and is still serving.
	for polls := 1; polls <= 3; polls++ {
		meterPoll(t, cfg)
		testwait.Until(t, fmt.Sprintf("the sink to poll the watched process %d times", polls), func() (bool, string) {
			return proc.statCalls() >= polls, fmt.Sprintf("%d polls", proc.statCalls())
		})
	}
	select {
	case err := <-done:
		t.Fatalf("Run returned while the watched process lives: %v", err)
	default:
	}
	proc.set(0, fs.ErrNotExist)
	postHTTP(t, url+"/v1/logs", logBody(apiRequest(testSession, "req_2", "repl_main_thread", 200)))
	if err := meterReturned(t, "Run to exit after the watched process went away", cfg, done); err != nil {
		t.Fatalf("Run = %v", err)
	}
	ledger, err := costledger.ForSession(dir, testSession)
	if err != nil || len(ledger.Rows) != 2 {
		t.Fatalf(
			"ledger = %d rows, %v; want both the pending log-only rows written by the final drain",
			len(ledger.Rows),
			err,
		)
	}
	closedRequest, err := http.NewRequestWithContext(testwait.Context(t), http.MethodPost, url+"/v1/logs", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	closedRequest.Header.Set("Content-Type", "application/json")
	if _, err := http.DefaultClient.Do(closedRequest); err == nil {
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
	if err := meterReturned(t, "Run to stop serving after SIGTERM", cfg, done); err != nil {
		t.Fatalf("Run = %v", err)
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
	if err := meterReturned(t, "Run to stop serving for a pid whose start time changed", cfg, done); err != nil {
		t.Fatalf("Run = %v", err)
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
	err := meterReturned(t, "Run to give up on an unreadable process table", cfg, done)
	if err == nil ||
		err.Error() != "cost sink: read process 4242 5 times in a row: sysctl: operation not permitted" {
		t.Fatalf("Run = %v, want the stat failure named", err)
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
	done := make(chan error, 1)
	go func() { done <- Meter(context.Background(), cfg) }()
	if err := meterReturned(t, "Run to exit for a watched process already gone", cfg, done); err != nil {
		t.Fatal(err)
	}
	if fileExists(old) {
		t.Fatal("the 100-day-old ledger file survived the prune at sink start")
	}
}
