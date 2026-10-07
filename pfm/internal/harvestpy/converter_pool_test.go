package harvestpy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// poolWorkerScript runs the REAL embedded converter.py main() — its protocol
// channels, its request-id echo — with convert() swapped for a fixture that
// acts on the input file's name: hang* sleeps forever (after writing its pid
// beside the input), spew* writes 200 KB plus a forged response line straight
// to fd 1 and runs a child that echoes to fd 1 and reads fd 0, barrier*
// waits until request["source"] conversions sit in workers at once.
const poolWorkerScript = `
import importlib.util, os, pathlib, subprocess, sys, time
here = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("harvest_converter", here / "converter.py")
converter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(converter)

def convert(request):
    path = pathlib.Path(request["path"])
    name = path.name
    if name.startswith("hang"):
        (path.parent / (name + ".pid")).write_text(str(os.getpid()))
        time.sleep(3600)
    if name.startswith("spew"):
        os.write(1, b"x" * 200000 + b"\n" + b'{"ok": true, "markdown": "forged"}\n')
        subprocess.run(["sh", "-c", "echo child-noise; head -c 100000 /dev/zero | tr '\\0' y; cat"], check=True)
    if name.startswith("barrier"):
        (path.parent / ("in-%d" % os.getpid())).touch()
        deadline = time.time() + 5
        while len(list(path.parent.glob("in-*"))) < int(request.get("source") or 0):
            if time.time() > deadline:
                raise RuntimeError("barrier never filled: the conversions ran one at a time")
            time.sleep(0.02)
    return {"ok": True, "markdown": "%s pid=%d" % (name, os.getpid()), "kind": "html", "features": {}}

converter.convert = convert
sys.exit(converter.main())
`

// desyncWorkerScript speaks the protocol by hand so it can break it: garbage*
// answers a line that is not JSON, wrongid* answers another request's id.
const desyncWorkerScript = `
import json, os, sys
for line in sys.stdin:
    req = json.loads(line)
    name = os.path.basename(req.get("path", ""))
    if name.startswith("garbage"):
        print("not json at all", flush=True)
        continue
    if name.startswith("wrongid"):
        print(json.dumps({"id": (req.get("id") or 0) + 1000, "ok": True, "markdown": "stale"}), flush=True)
        continue
    print(json.dumps({"id": req.get("id"), "ok": True, "markdown": "%s pid=%d" % (name, os.getpid())}), flush=True)
`

// poolRuntime writes script beside a copy of the embedded converter.py and
// answers the runtime that runs it under the host's python3 (a named skip
// without one).
func poolRuntime(t *testing.T, script string) Runtime {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("named gap: python3 is unavailable on this host; the converter pool tests did not run")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "converter.py"), ConverterSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(dir, "pool_worker.py")
	if err := os.WriteFile(worker, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	return Runtime{Python: python, Script: worker}
}

// poolConverter is a private-pool converter over runtime, closed at cleanup.
func poolConverter(t *testing.T, runtime Runtime) *Converter {
	t.Helper()
	converter := NewConverter(runtime)
	t.Cleanup(func() { _ = converter.Close() })
	return converter
}

// poolInput writes one fixture input named name into dir.
func poolInput(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("<p>fixture</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// convertWithin runs one conversion under a test-level ceiling, so a
// regression that serializes or hangs fails on an assertion, never a timeout
// of the whole package.
func convertWithin(converter *Converter, ceiling time.Duration, request Request) (Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ceiling)
	defer cancel()
	return converter.Convert(ctx, request)
}

// waitForPid polls for the pid file a hang* conversion writes when it starts.
func waitForPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path + ".pid"); err == nil && len(raw) > 0 {
			var pid int
			if _, err := fmt.Sscanf(string(raw), "%d", &pid); err == nil {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the conversion of %s never started", path)
	return 0
}

// processGone polls until pid no longer exists (killed and reaped).
func processGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// pidOf reads the worker pid a fixture conversion reports in its markdown.
func pidOf(t *testing.T, result Result) int {
	t.Helper()
	_, after, found := strings.Cut(result.Markdown, "pid=")
	var pid int
	if !found {
		t.Fatalf("markdown %q carries no pid", result.Markdown)
	}
	if _, err := fmt.Sscanf(after, "%d", &pid); err != nil {
		t.Fatalf("markdown %q: %v", result.Markdown, err)
	}
	return pid
}

// TestConverterPoolRunsConversionsInParallel is the incident's second half:
// one mutex around one worker serialized every conversion of every chat. Three
// conversions that each wait until all three sit in a worker at once can only
// finish on a pool of three.
func TestConverterPoolRunsConversionsInParallel(t *testing.T) {
	t.Parallel()
	runtime := poolRuntime(t, poolWorkerScript)
	runtime.Workers = 3
	converter := poolConverter(t, runtime)
	gate := t.TempDir()
	results := make([]Result, 3)
	errs := make([]error, 3)
	var wait sync.WaitGroup
	for index := range 3 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			input := poolInput(t, gate, fmt.Sprintf("barrier-%d.html", index))
			results[index], errs[index] = convertWithin(converter, 15*time.Second,
				Request{Path: input, Kind: "html", Source: "3"})
		}()
	}
	wait.Wait()
	pids := map[int]bool{}
	for index := range 3 {
		if errs[index] != nil {
			t.Fatalf("conversion %d: %v", index, errs[index])
		}
		pids[pidOf(t, results[index])] = true
	}
	if len(pids) != 3 {
		t.Fatalf("worker pids = %v, want three distinct workers", pids)
	}
}

// TestConverterPoolReplacesAHungWorker: one conversion hangs; the others
// complete on the rest of the pool, the hung one fails on its OWN deadline
// (the caller's context has a far later one) as ErrConverterTimeout, its
// process is killed, and the pool serves the next request.
func TestConverterPoolReplacesAHungWorker(t *testing.T) {
	t.Parallel()
	runtime := poolRuntime(t, poolWorkerScript)
	runtime.Workers = 3
	runtime.Timeout = 1500 * time.Millisecond
	converter := poolConverter(t, runtime)
	dir := t.TempDir()
	hang := poolInput(t, dir, "hang.html")
	hung := make(chan error, 1)
	go func() {
		_, err := convertWithin(converter, 20*time.Second, Request{Path: hang, Kind: "html"})
		hung <- err
	}()
	hungPid := waitForPid(t, hang)
	errs := make([]error, 6)
	var wait sync.WaitGroup
	for index := range 6 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			input := poolInput(t, dir, fmt.Sprintf("page-%d.html", index))
			_, errs[index] = convertWithin(converter, 10*time.Second, Request{Path: input, Kind: "html"})
		}()
	}
	wait.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("conversion %d beside the hung one: %v", index, err)
		}
	}
	select {
	case err := <-hung:
		if !errors.Is(err, ErrConverterTimeout) {
			t.Fatalf("hung conversion error = %v, want ErrConverterTimeout", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the hung conversion never hit its own deadline")
	}
	if !processGone(hungPid, 5*time.Second) {
		t.Fatalf("the hung worker %d is still alive after its deadline", hungPid)
	}
	result, err := convertWithin(converter, 10*time.Second,
		Request{Path: poolInput(t, dir, "after.html"), Kind: "html"})
	if err != nil || pidOf(t, result) == hungPid {
		t.Fatalf("after the timeout: %+v, %v; want a conversion on a live worker", result, err)
	}
}

// TestConverterProtocolSurvivesNativeStdoutSpew: native code and children
// write straight to fd 1 and read fd 0. On the protocol pipe that output was
// read as the response (a desync) and, past 64 KiB, blocked the worker
// forever; a child reading fd 0 stole the next request. The worker answers
// its own response and then the next request, on the same process.
func TestConverterProtocolSurvivesNativeStdoutSpew(t *testing.T) {
	t.Parallel()
	runtime := poolRuntime(t, poolWorkerScript)
	runtime.Workers = 1
	converter := poolConverter(t, runtime)
	dir := t.TempDir()
	spew, err := convertWithin(converter, 10*time.Second, Request{Path: poolInput(t, dir, "spew.html"), Kind: "html"})
	if err != nil {
		t.Fatalf("spewing conversion: %v", err)
	}
	if !strings.HasPrefix(spew.Markdown, "spew.html pid=") {
		t.Fatalf("spewing conversion answered %q, want its own response", spew.Markdown)
	}
	next, err := convertWithin(converter, 10*time.Second, Request{Path: poolInput(t, dir, "next.html"), Kind: "html"})
	if err != nil {
		t.Fatalf("conversion after the spew: %v", err)
	}
	if !strings.HasPrefix(next.Markdown, "next.html pid=") || pidOf(t, next) != pidOf(t, spew) {
		t.Fatalf("after the spew: %q, want next.html on the same worker as %q", next.Markdown, spew.Markdown)
	}
}

// TestConverterCancelMidConversionKillsOnlyThatWorker: a conversion on the
// other worker completes while one hangs; cancelling the hung one's context
// (an MCP client cancel) returns context.Canceled at once and kills that
// worker only.
func TestConverterCancelMidConversionKillsOnlyThatWorker(t *testing.T) {
	t.Parallel()
	runtime := poolRuntime(t, poolWorkerScript)
	runtime.Workers = 2
	converter := poolConverter(t, runtime)
	dir := t.TempDir()
	hang := poolInput(t, dir, "hang.html")
	// The ceiling only bounds a regression that serializes the two: the
	// cancel below lands long before it.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	hung := make(chan error, 1)
	go func() {
		_, err := converter.Convert(ctx, Request{Path: hang, Kind: "html"})
		hung <- err
	}()
	hungPid := waitForPid(t, hang)
	other, err := convertWithin(converter, 5*time.Second, Request{Path: poolInput(t, dir, "other.html"), Kind: "html"})
	if err != nil {
		t.Fatalf("conversion beside the hung one: %v", err)
	}
	cancel()
	select {
	case err := <-hung:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled conversion error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled conversion stayed blocked")
	}
	if !processGone(hungPid, 5*time.Second) {
		t.Fatalf("the cancelled worker %d is still alive", hungPid)
	}
	again, err := convertWithin(converter, 5*time.Second, Request{Path: poolInput(t, dir, "again.html"), Kind: "html"})
	if err != nil || pidOf(t, again) != pidOf(t, other) {
		t.Fatalf("after the cancel: %+v, %v; want the surviving worker %d", again, err, pidOf(t, other))
	}
}

// TestConverterQueueSaturationFailsFast: with every worker busy and the queue
// full, the next request fails at once with ErrConverterBusy instead of
// hanging behind them.
func TestConverterQueueSaturationFailsFast(t *testing.T) {
	t.Parallel()
	runtime := poolRuntime(t, poolWorkerScript)
	runtime.Workers, runtime.Queue = 1, 1
	converter := poolConverter(t, runtime)
	dir := t.TempDir()
	hang := poolInput(t, dir, "hang.html")
	go func() { _, _ = convertWithin(converter, 20*time.Second, Request{Path: hang, Kind: "html"}) }()
	waitForPid(t, hang)
	go func() {
		_, _ = convertWithin(converter, 20*time.Second, Request{Path: poolInput(t, dir, "queued.html"), Kind: "html"})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for converter.pool.queued() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	started := time.Now()
	_, err := convertWithin(converter, 3*time.Second, Request{Path: poolInput(t, dir, "rejected.html"), Kind: "html"})
	if !errors.Is(err, ErrConverterBusy) {
		t.Fatalf("saturated request error = %v, want ErrConverterBusy", err)
	}
	if waited := time.Since(started); waited > time.Second {
		t.Fatalf("saturated request waited %s, want an immediate refusal", waited)
	}
}

// TestConverterDiscardsADesyncedWorker: a response line that is not JSON, or
// that answers another request's id, means the pipe can no longer be trusted:
// the request fails as ErrConverterDesync and that worker is killed, so the
// next request runs on a fresh process.
func TestConverterDiscardsADesyncedWorker(t *testing.T) {
	t.Parallel()
	runtime := poolRuntime(t, desyncWorkerScript)
	runtime.Workers = 1
	converter := poolConverter(t, runtime)
	dir := t.TempDir()
	first, err := convertWithin(converter, 5*time.Second, Request{Path: poolInput(t, dir, "one.html"), Kind: "html"})
	if err != nil {
		t.Fatal(err)
	}
	previous := pidOf(t, first)
	for _, broken := range []string{"garbage.html", "wrongid.html"} {
		_, err := convertWithin(converter, 5*time.Second, Request{Path: poolInput(t, dir, broken), Kind: "html"})
		if !errors.Is(err, ErrConverterDesync) {
			t.Fatalf("%s: error = %v, want ErrConverterDesync", broken, err)
		}
		if !processGone(previous, 5*time.Second) {
			t.Fatalf("%s: the desynced worker %d is still alive", broken, previous)
		}
		next, err := convertWithin(
			converter,
			5*time.Second,
			Request{Path: poolInput(t, dir, "next.html"), Kind: "html"},
		)
		if err != nil {
			t.Fatalf("after %s: %v", broken, err)
		}
		if pid := pidOf(t, next); pid == previous {
			t.Fatalf("after %s the request ran on the desynced worker %d", broken, pid)
		} else {
			previous = pid
		}
	}
}

// TestConverterReapsIdleWorkers: a worker idle past IdleTimeout is stopped
// (each one holds the docling models, about 2 GB), and the next request
// starts a fresh one.
func TestConverterReapsIdleWorkers(t *testing.T) {
	t.Parallel()
	runtime := poolRuntime(t, poolWorkerScript)
	runtime.Workers = 1
	runtime.IdleTimeout = 100 * time.Millisecond
	converter := poolConverter(t, runtime)
	dir := t.TempDir()
	first, err := convertWithin(converter, 5*time.Second, Request{Path: poolInput(t, dir, "one.html"), Kind: "html"})
	if err != nil {
		t.Fatal(err)
	}
	if !processGone(pidOf(t, first), 5*time.Second) {
		t.Fatalf("the idle worker %d was never reaped", pidOf(t, first))
	}
	second, err := convertWithin(converter, 5*time.Second, Request{Path: poolInput(t, dir, "two.html"), Kind: "html"})
	if err != nil || pidOf(t, second) == pidOf(t, first) {
		t.Fatalf("after the reap: %+v, %v; want a fresh worker", second, err)
	}
}

// TestSharedConverterSharesOnePool: the daemon's loopback and external
// harvester services hold one pool between them, so its worker bound is the
// process's; the workers outlive one holder's Close and stop at the last.
//
// Serial: it registers in the package-level sharedPools.
func TestSharedConverterSharesOnePool(t *testing.T) {
	runtime := poolRuntime(t, poolWorkerScript)
	runtime.Workers = 1
	first, second := SharedConverter(runtime), SharedConverter(runtime)
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	if first.pool != second.pool {
		t.Fatal("two shared converters over one runtime hold two pools")
	}
	dir := t.TempDir()
	before, err := convertWithin(first, 5*time.Second, Request{Path: poolInput(t, dir, "one.html"), Kind: "html"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := convertWithin(second, 5*time.Second, Request{Path: poolInput(t, dir, "two.html"), Kind: "html"})
	if err != nil || pidOf(t, after) != pidOf(t, before) {
		t.Fatalf("after one holder closed: %+v, %v; want the same worker %d", after, err, pidOf(t, before))
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if !processGone(pidOf(t, before), 5*time.Second) {
		t.Fatalf("worker %d outlived the last holder", pidOf(t, before))
	}
	// A call still holding the closed converter (a gateway torn down by a
	// config reload mid-call) is refused rather than starting a worker in a
	// pool no Close will ever drain.
	if _, err := convertWithin(second, 5*time.Second,
		Request{Path: poolInput(t, dir, "late.html"), Kind: "html"}); !errors.Is(err, errConverterClosed) {
		t.Fatalf("conversion after the last Close: %v; want errConverterClosed", err)
	}
}

// TestConverterPoolSize is the default worker bound: a quarter of the CPUs,
// never more workers than a quarter of the memory holds at 2 GiB each, at
// least one, at most eight; with the memory unknown, at most two.
func TestConverterPoolSize(t *testing.T) {
	t.Parallel()
	const gib = uint64(1) << 30
	for _, test := range []struct {
		name   string
		cpus   int
		memory uint64
		want   int
	}{
		{"this workstation", 15, 62 * gib, 4},
		{"memory bound", 32, 16 * gib, 2},
		{"small box", 2, 4 * gib, 1},
		{"tiny memory still one", 8, 2 * gib, 1},
		{"huge box capped", 128, 1024 * gib, 8},
		{"memory unknown", 16, 0, 2},
		{"one cpu", 1, 64 * gib, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := converterPoolSize(test.cpus, test.memory); got != test.want {
				t.Fatalf("converterPoolSize(%d, %d GiB) = %d, want %d", test.cpus, test.memory/gib, got, test.want)
			}
		})
	}
}

// TestConversionBudget is one conversion's own deadline: the base plus a
// per-MiB allowance (four times larger under OCR), capped at 30 minutes or the
// base when the base is larger.
func TestConversionBudget(t *testing.T) {
	t.Parallel()
	const mib = int64(1) << 20
	for _, test := range []struct {
		name string
		base time.Duration
		ocr  bool
		size int64
		want time.Duration
	}{
		{"default base, small page", 0, false, 200 << 10, 180 * time.Second},
		{"default base, 10 MiB pdf", 0, false, 10 * mib, 330 * time.Second},
		{"default base, 10 MiB scan under OCR", 0, true, 10 * mib, 780 * time.Second},
		{"configured base", 60 * time.Second, false, 2 * mib, 90 * time.Second},
		{"capped at 30 minutes", 0, true, 100 * mib, 30 * time.Minute},
		{"a base past the cap is the cap", time.Hour, true, 100 * mib, time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := conversionBudget(test.base, test.ocr, test.size); got != test.want {
				t.Fatalf("conversionBudget(%s, %t, %d) = %s, want %s", test.base, test.ocr, test.size, got, test.want)
			}
		})
	}
}

// TestConverterQueuedCancelGivesBackItsPlace: a conversion cancelled while it
// waits for a worker (its MCP client disconnected) leaves the queue at once,
// so the next request queues in its place instead of being refused as busy.
func TestConverterQueuedCancelGivesBackItsPlace(t *testing.T) {
	t.Parallel()
	runtime := poolRuntime(t, poolWorkerScript)
	runtime.Workers, runtime.Queue = 1, 1
	converter := poolConverter(t, runtime)
	dir := t.TempDir()
	hang := poolInput(t, dir, "hang.html")
	go func() { _, _ = convertWithin(converter, 20*time.Second, Request{Path: hang, Kind: "html"}) }()
	waitForPid(t, hang)
	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	queuedInput := poolInput(t, dir, "queued.html")
	go func() {
		_, err := converter.Convert(queuedCtx, Request{Path: queuedInput, Kind: "html"})
		queued <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for converter.pool.queued() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancelQueued()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled queued conversion: %v; want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled conversion stayed queued")
	}
	next := poolInput(t, dir, "next.html")
	_, err := convertWithin(converter, 300*time.Millisecond, Request{Path: next, Kind: "html"})
	if errors.Is(err, ErrConverterBusy) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("next request after the cancel: %v; want it queued until its own deadline", err)
	}
}

// gatedRunner holds every Start until release closes, so a test can Close
// the pool while a worker is starting (the docling import takes seconds).
type gatedRunner struct {
	deps.RealRunner
	starting chan struct{}
	release  chan struct{}
}

func (runner gatedRunner) Start(ctx context.Context, argv []string, options deps.StartOptions) (deps.Process, error) {
	runner.starting <- struct{}{}
	<-runner.release
	return runner.RealRunner.Start(ctx, argv, options)
}

// TestConverterCloseStopsAWorkerStillStarting: a Close (daemon shutdown, the
// last gateway's teardown) while a worker is starting stops that worker and
// fails its request, as it does a busy one, instead of the worker joining the
// drained pool and idling there for ten minutes with the models loaded.
func TestConverterCloseStopsAWorkerStillStarting(t *testing.T) {
	t.Parallel()
	runtime := poolRuntime(t, poolWorkerScript)
	runner := gatedRunner{starting: make(chan struct{}), release: make(chan struct{})}
	runtime.Runner, runtime.Workers = runner, 1
	converter := poolConverter(t, runtime)
	input := poolInput(t, t.TempDir(), "one.html")
	done := make(chan error, 1)
	go func() {
		_, err := convertWithin(converter, 10*time.Second, Request{Path: input, Kind: "html"})
		done <- err
	}()
	<-runner.starting
	if err := converter.Close(); err != nil {
		t.Fatal(err)
	}
	close(runner.release)
	if err := <-done; !errors.Is(err, errConverterClosed) {
		t.Fatalf("conversion whose worker started across Close: %v; want errConverterClosed", err)
	}
	converter.pool.mu.Lock()
	live, all, idle := converter.pool.live, len(converter.pool.all), len(converter.pool.idle)
	converter.pool.mu.Unlock()
	if live != 0 || all != 0 || idle != 0 {
		t.Fatalf("after Close: live=%d all=%d idle=%d; want no worker kept", live, all, idle)
	}
}

// TestConverterRetriesADeadReusedWorkerInItsOwnSlot: a request that drew an
// idle worker whose pipe broke (its process died while idle) retries on a
// fresh worker in the slot it already holds, never behind the queue: with the
// queue full it would otherwise be refused as busy for nothing it did.
func TestConverterRetriesADeadReusedWorkerInItsOwnSlot(t *testing.T) {
	t.Parallel()
	runtime := poolRuntime(t, poolWorkerScript)
	runtime.Workers, runtime.Queue = 1, 1
	converter := poolConverter(t, runtime)
	dir := t.TempDir()
	first, err := convertWithin(converter, 5*time.Second, Request{Path: poolInput(t, dir, "one.html"), Kind: "html"})
	if err != nil {
		t.Fatal(err)
	}
	converter.pool.mu.Lock()
	_ = converter.pool.idle[0].stdin.Close() // the idle worker's pipe is gone
	// Two conversions arrived meanwhile: the queue is full.
	converter.pool.waiters = append(converter.pool.waiters, make(chan poolGrant, 1), make(chan poolGrant, 1))
	converter.pool.mu.Unlock()
	second, err := convertWithin(converter, 10*time.Second, Request{Path: poolInput(t, dir, "two.html"), Kind: "html"})
	if err != nil || pidOf(t, second) == pidOf(t, first) {
		t.Fatalf("request on the dead reused worker: %+v, %v; want a fresh worker in its slot", second, err)
	}
}
