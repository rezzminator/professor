package restart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// herdrModel is the herdr binary over one server; stuck keeps it running
// through `server stop`, dead keeps a started one from coming up, and onStop
// plays the server process exiting.
type herdrModel struct {
	mu      sync.Mutex
	running bool
	stuck   bool
	dead    bool
	status  string // an answer that overrides the modelled one
	onStop  func()
}

func (model *herdrModel) run(argv []string) (deps.RunResult, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	switch strings.Join(argv[1:], " ") {
	case "status --json":
		if model.status != "" {
			return answer(model.status)
		}
		if model.running {
			return answer(`{"server":{"running":true,"socket":"/run/herdr.sock"}}`)
		}
		return answer(`{"server":{"running":false,"socket":""}}`)
	case "server stop":
		if !model.stuck {
			model.running = false
			if model.onStop != nil {
				model.onStop()
			}
		}
		return answer("")
	}
	return exitWith(64, "unknown herdr verb")
}

func (model *herdrModel) started([]string) {
	model.mu.Lock()
	defer model.mu.Unlock()
	if !model.dead {
		model.running = true
	}
}

// herdrServerPID is the server process a herdrHost's table lists.
const herdrServerPID = 30

// herdrHost is a host whose process table holds the Herdr server (pid 30)
// and extra, and whose server process exits on `server stop`.
func herdrHost(t *testing.T, model *herdrModel, extra ...process) (*discoverer, *hostRunner, *fakeKernel) {
	t.Helper()
	runner := &hostRunner{
		handle:  model.run,
		onStart: model.started,
		paths:   map[string]string{"herdr": "/opt/bin/herdr"},
	}
	kernel := newKernel(append(extra, process{PID: herdrServerPID, Argv: []string{"/opt/bin/herdr", "server"}})...)
	model.onStop = func() {
		kernel.mu.Lock()
		defer kernel.mu.Unlock()
		delete(kernel.procs, herdrServerPID)
	}
	return &discoverer{deps: testDeps(t, runner, kernel, &fakeFleet{})}, runner, kernel
}

func herdrPlan(t *testing.T, model *herdrModel, extra ...process) (Plan, *hostRunner, *fakeKernel) {
	t.Helper()
	finder, runner, kernel := herdrHost(t, model, extra...)
	table, err := finder.readTable()
	finder.findHerdr(context.Background(), table, err)
	return finder.plan, runner, kernel
}

func TestFindHerdrNamesAnAbsentBinaryAndAStoppedServer(t *testing.T) {
	finder := &discoverer{deps: testDeps(t, &hostRunner{}, newKernel(), &fakeFleet{})}
	finder.findHerdr(context.Background(), processTable{}, nil)
	if len(finder.plan.Units) != 0 || !strings.Contains(finder.plan.Findings[0].Detail, "herdr is not installed") {
		t.Fatalf("absent herdr = %+v", finder.plan)
	}
	plan, _, _ := herdrPlan(t, &herdrModel{})
	if len(plan.Units) != 1 || !plan.Units[0].StaysStopped ||
		!strings.Contains(plan.Findings[0].Detail, "no server running — it stays stopped") {
		t.Fatalf("stopped herdr = %+v", plan)
	}
}

func TestFindHerdrAnUnreadableStatusIsAFailedFind(t *testing.T) {
	plan, _, _ := herdrPlan(t, &herdrModel{status: `{"server":{}}`})
	if len(plan.Units) != 0 || plan.Findings[0].Err == nil ||
		!strings.Contains(plan.Findings[0].Err.Error(), "no server.running") {
		t.Fatalf("unreadable status = %+v", plan.Findings)
	}
}

func TestHerdrStopsAndStartsVerified(t *testing.T) {
	model := &herdrModel{running: true}
	plan, runner, _ := herdrPlan(t, model)
	if len(plan.Units) != 1 || plan.Units[0].Name != "herdr server /run/herdr.sock" {
		t.Fatalf("units = %v", unitNames(plan.Units))
	}
	unit := plan.Units[0]
	ctx := context.Background()
	if detail, err := unit.Stop(ctx); err != nil || !strings.Contains(detail, "reads not running") || model.running {
		t.Fatalf("stop = %q, %v (running %v)", detail, err, model.running)
	}
	detail, err := unit.Start(ctx)
	if err != nil || detail != "server pid 4242 up; herdr status reads running" {
		t.Fatalf("start = %q, %v", detail, err)
	}
	if len(runner.starts) != 1 || !runner.starts[0].Opts.Detach ||
		strings.Join(runner.starts[0].Argv, " ") != "/opt/bin/herdr server" {
		t.Fatalf("starts = %+v, want one detached herdr server", runner.starts)
	}
}

func TestHerdrFailuresAreNamed(t *testing.T) {
	model := &herdrModel{running: true, stuck: true, dead: true}
	plan, _, _ := herdrPlan(t, model)
	ctx := context.Background()
	if _, err := plan.Units[0].Stop(ctx); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("a stop that did not take = %v", err)
	}
	model.running = false
	if _, err := plan.Units[0].Start(ctx); err == nil || !strings.Contains(err.Error(), "not running") ||
		!strings.Contains(err.Error(), "herdr-server.log") {
		t.Fatalf("a server that never came up = %v", err)
	}
}

// TestHerdrStopProvesEveryProcessItRanGone: a process the old server started
// that outlives `herdr server stop` is stopped and named, never left behind
// under a verified line; the front that launched the restart is spared.
func TestHerdrStopProvesEveryProcessItRanGone(t *testing.T) {
	finder, _, kernel := herdrHost(t, &herdrModel{running: true},
		process{PID: 31, Parent: herdrServerPID, Argv: []string{"-zsh"}},
		process{PID: 32, Parent: 31, Argv: []string{"curl", "https://herdr.example/latest.json"}},
		process{PID: 33, Parent: 31, Argv: []string{"/opt/pfm/bin/pfm", "restart"}},
		process{PID: 40, Parent: 1, Argv: []string{"vim"}},
	)
	finder.deps.Spare = []int{33}
	table, err := finder.readTable()
	finder.findHerdr(context.Background(), table, err)
	plan := finder.plan
	if !strings.Contains(plan.Units[0].FoundBy, "3 process(es), the server and every one under it") {
		t.Fatalf("found by = %q, want the server's tree counted", plan.Units[0].FoundBy)
	}
	detail, err := plan.Units[0].Stop(context.Background())
	if err != nil || !strings.Contains(detail, "2 of its 3 process(es) outlived it, each stopped") {
		t.Fatalf("stop = %q, %v", detail, err)
	}
	if got := kernel.sent(); !slices.Equal(got, []string{"31 terminated", "32 terminated"}) {
		t.Fatalf("signals = %v, want the two outlivers only (pid 33, the front, and 40 are not Herdr's)", got)
	}
}

func TestHerdrStopNamesAProcessItRanThatWillNotDie(t *testing.T) {
	plan, _, kernel := herdrPlan(t, &herdrModel{running: true},
		process{PID: 31, Parent: herdrServerPID, Argv: []string{"-zsh"}})
	kernel.stubborn[31], kernel.unkillable[31] = true, true
	_, err := plan.Units[0].Stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "may still run") || !strings.Contains(err.Error(), "pid 31") {
		t.Fatalf("stop = %v, want the survivor named and the step failed", err)
	}
}

func TestHerdrStopWithAnUnknownTreeIsUnverified(t *testing.T) {
	finder, _, kernel := herdrHost(t, &herdrModel{running: true})
	kernel.mu.Lock()
	delete(kernel.procs, herdrServerPID)
	kernel.mu.Unlock()
	table, err := finder.readTable()
	finder.findHerdr(context.Background(), table, err)
	_, stopErr := finder.plan.Units[0].Stop(context.Background())
	if stopErr == nil || !strings.Contains(stopErr.Error(), "unverified: no herdr server process") {
		t.Fatalf("stop with no server process in the table = %v, want it named unverified", stopErr)
	}
	finder, _, _ = herdrHost(t, &herdrModel{running: true})
	finder.findHerdr(context.Background(), processTable{}, errors.New("list processes: permission denied"))
	if _, stopErr := finder.plan.Units[0].Stop(context.Background()); stopErr == nil ||
		!strings.Contains(stopErr.Error(), "unverified: list processes: permission denied") {
		t.Fatalf("stop over an unreadable table = %v, want it named unverified", stopErr)
	}
}

func TestOpenStartLogCreatesItsDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "restart")
	log, path, err := openStartLog(dir, "x.log")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.WriteString("line\n"); err != nil {
		t.Fatal(err)
	}
	_ = log.Close()
	if body, err := os.ReadFile(path); err != nil || string(body) != "line\n" {
		t.Fatalf("log = %q, %v", body, err)
	}
}

// TestHerdrAServerNotRunningBeforeStaysStopped: an installed Herdr whose
// server was not running is named as staying stopped and never started.
func TestHerdrAServerNotRunningBeforeStaysStopped(t *testing.T) {
	plan, runner, _ := herdrPlan(t, &herdrModel{})
	printed, executed, code := printAndRun(t, plan.Units)
	if code != 0 {
		t.Fatalf("Execute = %d, want 0:\n%s", code, executed)
	}
	if len(runner.starts) != 0 {
		t.Fatalf("a Herdr server that was not running before was started: %+v", runner.starts)
	}
	for _, argv := range runner.ran() {
		if strings.Join(argv[1:], " ") != "status --json" {
			t.Fatalf("a Herdr server that was not running before was touched: %v", argv)
		}
	}
	assertStaysStopped(t, printed, executed, "herdr server", "server.running=false")
}
