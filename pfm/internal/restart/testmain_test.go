package restart

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain jails PFM_HOME for the package: no restart test reaches the
// operator's home, fleet or services.
func TestMain(m *testing.M) {
	if role := os.Getenv(frontRoleEnv); role != "" {
		os.Exit(runFrontRole(role))
	}
	os.Exit(testjail.Run(m))
}

// tickClock is a fake clock whose Sleep advances time at once, so a bounded
// wait runs to its deadline without blocking the test.
type tickClock struct{ *clock.Fake }

func newTickClock() tickClock { return tickClock{clock.NewFake(time.Unix(1_700_000_000, 0))} }

func (clk tickClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clk.Advance(d)
	return nil
}

// hostRunner is a stateful fake deps.Runner: handle answers every Run (a test
// keeps the state it models), paths answers LookPath, and every call is on
// the ledger.
type hostRunner struct {
	mu       sync.Mutex
	handle   func(argv []string) (deps.RunResult, error)
	paths    map[string]string
	lookErr  map[string]error
	runs     [][]string
	starts   []deps.StartCall
	startErr error
	onStart  func(argv []string)
}

func (runner *hostRunner) Run(_ context.Context, argv []string, _ deps.RunOptions) (deps.RunResult, error) {
	runner.mu.Lock()
	runner.runs = append(runner.runs, slices.Clone(argv))
	handle := runner.handle
	runner.mu.Unlock()
	if handle == nil {
		return deps.RunResult{}, fmt.Errorf("unscripted run: %s", strings.Join(argv, " "))
	}
	return handle(argv)
}

func (runner *hostRunner) LookPath(name string) (string, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if err := runner.lookErr[name]; err != nil {
		return "", err
	}
	if path, ok := runner.paths[name]; ok {
		return path, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func (runner *hostRunner) Start(_ context.Context, argv []string, opts deps.StartOptions) (deps.Process, error) {
	runner.mu.Lock()
	runner.starts = append(runner.starts, deps.StartCall{Argv: slices.Clone(argv), Opts: opts})
	onStart, startErr := runner.onStart, runner.startErr
	runner.mu.Unlock()
	if startErr != nil {
		return nil, startErr
	}
	if onStart != nil {
		onStart(argv)
	}
	return fakeProcess{pid: 4242}, nil
}

func (runner *hostRunner) ran() [][]string {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return slices.Clone(runner.runs)
}

// fakeProcess is the started process a hostRunner hands back.
type fakeProcess struct{ pid int }

func (proc fakeProcess) Pid() int                      { return proc.pid }
func (fakeProcess) Wait() error                        { return nil }
func (fakeProcess) Release() error                     { return nil }
func (fakeProcess) StdinPipe() (io.WriteCloser, error) { return nil, errors.New("no stdin") }
func (fakeProcess) StdoutPipe() (io.ReadCloser, error) { return nil, errors.New("no stdout") }
func (fakeProcess) Kill() error                        { return nil }
func (fakeProcess) KillGroup() error                   { return nil }
func answer(stdout string) (deps.RunResult, error) {
	return deps.RunResult{Stdout: []byte(stdout)}, nil
}

func exitWith(code int, stderr string) (deps.RunResult, error) {
	return deps.RunResult{ExitCode: code, Stderr: []byte(stderr)}, nil
}

// foreignUID is the uid of another user's process in a fakeKernel.
const foreignUID = 1000

// fakeKernel is the process table and the kernel's signal delivery: a pid in
// stubborn ignores SIGTERM, one in unkillable survives SIGKILL too, one in
// unreadable cannot be read, and one in foreign is another user's.
type fakeKernel struct {
	mu         sync.Mutex
	procs      map[int]process
	stubborn   map[int]bool
	unkillable map[int]bool
	unreadable map[int]bool
	foreign    map[int]bool
	listErr    error
	signals    []string
}

func newKernel(procs ...process) *fakeKernel {
	kernel := &fakeKernel{
		procs:      map[int]process{},
		stubborn:   map[int]bool{},
		unkillable: map[int]bool{},
		unreadable: map[int]bool{},
		foreign:    map[int]bool{},
	}
	for _, proc := range procs {
		kernel.procs[proc.PID] = proc
	}
	return kernel
}

func (kernel *fakeKernel) PIDs() ([]int, error) {
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	if kernel.listErr != nil {
		return nil, kernel.listErr
	}
	pids := make([]int, 0, len(kernel.procs))
	for pid := range kernel.procs {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids, nil
}

func (kernel *fakeKernel) lookup(pid int) (process, error) {
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	if kernel.unreadable[pid] {
		return process{}, fs.ErrPermission
	}
	proc, ok := kernel.procs[pid]
	if !ok {
		return process{}, fs.ErrNotExist
	}
	return proc, nil
}

func (kernel *fakeKernel) Cmdline(pid int) ([]string, error) {
	proc, err := kernel.lookup(pid)
	return proc.Argv, err
}

func (kernel *fakeKernel) Stat(pid int) (gather.ProcStat, error) {
	proc, err := kernel.lookup(pid)
	return gather.ProcStat{ParentPID: proc.Parent, StartTime: proc.Start}, err
}

// ProcessIdentity plays the kernel's uid of a pid: foreignUID for a pid in
// foreign, the test user (0) otherwise.
func (kernel *fakeKernel) ProcessIdentity(pid int) (gather.ProcessIdentity, error) {
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	if _, ok := kernel.procs[pid]; !ok {
		return gather.ProcessIdentity{}, fs.ErrNotExist
	}
	if kernel.foreign[pid] {
		return gather.ProcessIdentity{EffectiveUID: foreignUID}, nil
	}
	return gather.ProcessIdentity{}, nil
}

func (*fakeKernel) Environ(int) (map[string]string, error) { return nil, nil }
func (*fakeKernel) FDLinks(int) ([]gather.FDLink, error)   { return nil, nil }

func (kernel *fakeKernel) signal(pid int, sig syscall.Signal) error {
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	if _, ok := kernel.procs[pid]; !ok {
		return syscall.ESRCH
	}
	if sig == 0 {
		return nil
	}
	kernel.signals = append(kernel.signals, fmt.Sprintf("%d %s", pid, sig))
	switch {
	case sig == syscall.SIGTERM && !kernel.stubborn[pid]:
		delete(kernel.procs, pid)
	case sig == syscall.SIGKILL && !kernel.unkillable[pid]:
		delete(kernel.procs, pid)
	}
	return nil
}

func (kernel *fakeKernel) sent() []string {
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	return slices.Clone(kernel.signals)
}

// fakeFleet is the chat roster: the chats it lists and every End it took.
type fakeFleet struct {
	mu         sync.Mutex
	chats      []headless.Chat
	unreadable map[string]string
	scanErr    error
	endErr     map[string]error
	ended      []string
}

func (fleet *fakeFleet) Live(context.Context) ([]headless.Chat, map[string]string, error) {
	return slices.Clone(fleet.chats), fleet.unreadable, fleet.scanErr
}

func (fleet *fakeFleet) End(_ context.Context, chat headless.Chat) error {
	fleet.mu.Lock()
	defer fleet.mu.Unlock()
	fleet.ended = append(fleet.ended, chat.ID)
	return fleet.endErr[chat.ID]
}

// testDeps is a Deps whose every door is a fake; a test overrides the doors it drives.
func testDeps(t *testing.T, runner *hostRunner, kernel *fakeKernel, fleet *fakeFleet) Deps {
	t.Helper()
	return Deps{
		Runner:     runner,
		Procs:      kernel,
		Signal:     kernel.signal,
		Clock:      newTickClock(),
		Fleet:      fleet,
		Self:       1,
		Executable: "/opt/pfm/bin/pfm",
		LogDir:     t.TempDir(),
	}
}
