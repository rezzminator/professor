package restart

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

func unitNames(units []Unit) []string {
	names := make([]string, 0, len(units))
	for index := range units {
		unit := &units[index]
		names = append(names, unit.Family+": "+unit.Name)
	}
	return names
}

func TestFindHelpersAndPFMFindEachKindOnce(t *testing.T) {
	kernel := newKernel(
		process{PID: 1, Argv: []string{"/opt/pfm/bin/pfm", "restart", "--worker"}}, // this worker: Self
		process{PID: 10, Argv: []string{"/opt/pfm/bin/pfm", "internal", "cost-sink", "--session", "s"}},
		process{PID: 11, Argv: []string{"pfm", "--config", "/c.json", "mcp", "serve", "--stdio", "--family", "chat"}},
		process{PID: 12, Argv: []string{"/venv/bin/python", "/rt/project/converter.py"}},
		process{PID: 13, Argv: []string{"/venv/bin/python", "/rt/project/browser.py"}},
		process{PID: 14, Argv: []string{"/usr/bin/python3", "/elsewhere/converter.py"}},
		process{PID: 15, Argv: []string{"/opt/pfm/bin/pfm", "chat", "watch"}},
		process{PID: 16, Argv: []string{"/opt/pfm/bin/pfm", "restart"}}, // the front: Spare
		process{PID: 17, Argv: []string{"/work/pfm-dev", "--config=/c.json", "internal", "cost-sink"}},
	)
	dependencies := testDeps(t, &hostRunner{}, kernel, &fakeFleet{})
	dependencies.Spare = []int{16}
	dependencies.Executable = "/work/pfm-dev"
	finder := &discoverer{deps: dependencies, claimed: map[int]bool{}}
	table, err := finder.readTable()
	if err != nil {
		t.Fatal(err)
	}
	finder.findHelpers(table)
	finder.findPFM(table)
	got := unitNames(finder.plan.Units)
	want := []string{
		"chat-helpers: cost sink pid 10",
		"chat-helpers: stdio MCP server pid 11",
		"chat-helpers: cost sink pid 17",
		"pfm: pfm process pid 15",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("units =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if detail := finder.plan.Findings[0].Detail; !strings.Contains(detail, "3 helper process(es) of 9 listed") {
		t.Fatalf("helpers finding = %q, want the count and what the table listed", detail)
	}
	if len(kernel.sent()) != 0 {
		t.Fatalf("finding signalled: %v", kernel.sent())
	}
}

func TestProcessStopExitsOnSIGTERM(t *testing.T) {
	proc := process{PID: 20, Argv: []string{"pfm", "internal", "cost-sink"}, Start: 5}
	kernel := newKernel(proc)
	finder := &discoverer{deps: testDeps(t, &hostRunner{}, kernel, &fakeFleet{})}
	detail, err := finder.processUnit(FamilyHelpers, "cost sink", proc).Stop(context.Background())
	if err != nil || detail != "exited on SIGTERM" {
		t.Fatalf("stop = %q, %v", detail, err)
	}
	if got := kernel.sent(); !slices.Equal(got, []string{"20 terminated"}) {
		t.Fatalf("signals = %v, want one SIGTERM", got)
	}
}

func TestProcessStopKillsWhatIgnoresSIGTERM(t *testing.T) {
	proc := process{PID: 21, Argv: []string{"pfm", "chat", "watch"}, Start: 5}
	kernel := newKernel(proc)
	kernel.stubborn[21] = true
	finder := &discoverer{deps: testDeps(t, &hostRunner{}, kernel, &fakeFleet{})}
	detail, err := finder.processUnit(FamilyPFM, "pfm process", proc).Stop(context.Background())
	if err != nil || !strings.HasPrefix(detail, "killed (SIGKILL after") {
		t.Fatalf("stop = %q, %v", detail, err)
	}
	if got := kernel.sent(); !slices.Equal(got, []string{"21 terminated", "21 killed"}) {
		t.Fatalf("signals = %v, want SIGTERM then SIGKILL", got)
	}
}

func TestProcessStopNamesAProcessThatSurvivesSIGKILL(t *testing.T) {
	proc := process{PID: 22, Argv: []string{"pfm", "chat", "watch"}, Start: 5}
	kernel := newKernel(proc)
	kernel.stubborn[22], kernel.unkillable[22] = true, true
	finder := &discoverer{deps: testDeps(t, &hostRunner{}, kernel, &fakeFleet{})}
	_, err := finder.processUnit(FamilyPFM, "pfm process", proc).Stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "pid 22 still runs pfm chat watch after SIGKILL") {
		t.Fatalf("stop error = %v, want the survivor named", err)
	}
}

func TestProcessStopNeverSignalsARecycledPid(t *testing.T) {
	seen := process{PID: 23, Argv: []string{"pfm", "internal", "cost-sink"}, Start: 5}
	kernel := newKernel(process{PID: 23, Argv: []string{"vim"}, Start: 9})
	finder := &discoverer{deps: testDeps(t, &hostRunner{}, kernel, &fakeFleet{})}
	detail, err := finder.processUnit(FamilyHelpers, "cost sink", seen).Stop(context.Background())
	if err != nil || detail != "already gone" {
		t.Fatalf("stop = %q, %v", detail, err)
	}
	if len(kernel.sent()) != 0 {
		t.Fatalf("a recycled pid was signalled: %v", kernel.sent())
	}
}

func TestProcessStopAnUnreadableLivePidIsAnErrorNotGone(t *testing.T) {
	proc := process{PID: 24, Argv: []string{"pfm", "internal", "cost-sink"}, Start: 5}
	kernel := newKernel(proc)
	kernel.unreadable[24] = true
	finder := &discoverer{deps: testDeps(t, &hostRunner{}, kernel, &fakeFleet{})}
	_, err := finder.processUnit(FamilyHelpers, "cost sink", proc).Stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "read pid 24 from the process table") {
		t.Fatalf("stop error = %v, want the unreadable pid named", err)
	}
}

func TestReadTableFailureFailsEveryProcessFamily(t *testing.T) {
	kernel := newKernel()
	kernel.listErr = errors.New("permission denied")
	plan := BuildPlan(context.Background(), testDeps(t, &hostRunner{}, kernel, &fakeFleet{}))
	var failed []string
	for _, finding := range plan.Findings {
		if finding.Err != nil && strings.Contains(finding.Err.Error(), "list processes: permission denied") {
			failed = append(failed, finding.Family)
		}
	}
	if !slices.Equal(failed, []string{FamilyHelpers, FamilyMCPDaemon, FamilyPFM}) {
		t.Fatalf("families failed on the unreadable table = %v", failed)
	}
}

// TestReadTableAnUnreadableProcessIsAFailedFind: a live pid of this user the
// table cannot read may be a helper, a daemon or a pfm process, so every
// process family's find fails naming it, never "nothing to stop".
func TestReadTableAnUnreadableProcessIsAFailedFind(t *testing.T) {
	kernel := newKernel(
		process{PID: 10, Argv: []string{"pfm", "internal", "cost-sink"}},
		process{PID: 40, Argv: []string{"pfm", "mcp", "serve"}},
	)
	kernel.unreadable[40] = true
	plan := BuildPlan(context.Background(), testDeps(t, &hostRunner{}, kernel, &fakeFleet{}))
	var failed []string
	for _, finding := range plan.Findings {
		if finding.Err != nil && strings.Contains(finding.Err.Error(), "pid 40: permission denied") {
			failed = append(failed, finding.Family)
		}
	}
	if !slices.Equal(failed, []string{FamilyHelpers, FamilyMCPDaemon, FamilyPFM}) {
		t.Fatalf("families failed on the unreadable pid = %v; findings %+v", failed, plan.Findings)
	}
	if got := unitNames(plan.Units); !slices.Equal(got, []string{"chat-helpers: cost sink pid 10"}) {
		t.Fatalf("units = %v, want the readable helper still planned", got)
	}
}

// TestReadTableSkipsWhatIsProvablyNotOurs: another user's protected process,
// a pid gone mid-read and a kernel thread are no failed find.
func TestReadTableSkipsWhatIsProvablyNotOurs(t *testing.T) {
	kernel := newKernel(
		process{PID: 50, Argv: []string{"/usr/sbin/sshd"}},
		process{PID: 51},
	)
	kernel.unreadable[50], kernel.foreign[50] = true, true
	finder := &discoverer{deps: testDeps(t, &hostRunner{}, kernel, &fakeFleet{})}
	table, err := finder.readTable()
	if err != nil || len(table.Unreadable) != 0 || len(table.Processes) != 0 || table.Listed != 2 {
		t.Fatalf("table = %+v, %v", table, err)
	}
	if !finder.skippable(60, fs.ErrNotExist) {
		t.Fatal("a pid gone mid-read is not skipped")
	}
}

func TestClipArgvKeepsAPlanLineShort(t *testing.T) {
	long := strings.Repeat("x", 200)
	if got := clipArgv(long); len(got) != 160+len("…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("clipArgv kept %d bytes", len(got))
	}
	if got := clipArgv("pfm ls"); got != "pfm ls" {
		t.Fatalf("clipArgv clipped a short line: %q", got)
	}
}

func TestProcessStopNeverSignalsAChangedOwner(t *testing.T) {
	proc := process{PID: 29, Argv: []string{"/opt/pfm/bin/pfm", "mcp", "serve"}, Start: 5}
	kernel := newKernel(proc)
	kernel.foreign[29] = true
	finder := &discoverer{deps: testDeps(t, &hostRunner{}, kernel, &fakeFleet{})}
	_, err := finder.processUnit(FamilyMCPDaemon, "pfm mcp serve", proc).Stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "identity changed") || len(kernel.sent()) != 0 {
		t.Fatalf("error=%v signals=%v, want changed ownership rejected before signalling", err, kernel.sent())
	}
}

type handoffKernel struct {
	*fakeKernel
	executable    string
	configPath    string
	executableErr error
	envErr        error
	identityErr   error
	recycle       bool
	reads         int
}

func (kernel *handoffKernel) Executable(int) (string, error) {
	return kernel.executable, kernel.executableErr
}

func (kernel *handoffKernel) Environ(int) (map[string]string, error) {
	return map[string]string{"PFM_CONFIG": kernel.configPath, "HOME": "/fixture/home"}, kernel.envErr
}

func (kernel *handoffKernel) ProcessIdentity(pid int) (gather.ProcessIdentity, error) {
	if kernel.identityErr != nil {
		return gather.ProcessIdentity{}, kernel.identityErr
	}
	return kernel.fakeKernel.ProcessIdentity(pid)
}

func (kernel *handoffKernel) Stat(pid int) (gather.ProcStat, error) {
	kernel.reads++
	if kernel.recycle && kernel.reads == 2 {
		kernel.mu.Lock()
		row := kernel.procs[pid]
		row.Start++
		kernel.procs[pid] = row
		kernel.mu.Unlock()
	}
	return kernel.fakeKernel.Stat(pid)
}

func TestConvergeMCPDaemonUnmanagedOwnershipAndLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name        string
		enabled     bool
		argv        []string
		change      func(*handoffKernel, *hostRunner)
		wantSignals []string
		starts      int
		wantErr     string
	}{
		{name: "disabled stops owned bare argv", wantSignals: []string{"29 terminated"}},
		{name: "enabled restarts after stopping", enabled: true, wantSignals: []string{"29 terminated"}, starts: 1},
		{name: "explicit config", argv: []string{"/opt/pfm/bin/pfm", "--config", "/fixture/config", "mcp", "serve"}, wantSignals: []string{"29 terminated"}},
		{name: "equals config overrides environment", argv: []string{"pfm", "--config=/fixture/config", "mcp", "serve"}, change: func(k *handoffKernel, _ *hostRunner) { k.configPath = "/foreign/config" }, wantSignals: []string{"29 terminated"}},
		{name: "foreign executable", change: func(k *handoffKernel, _ *hostRunner) { k.executable = "/foreign/pfm" }},
		{name: "foreign config", change: func(k *handoffKernel, _ *hostRunner) { k.configPath = "/foreign/config" }},
		{name: "foreign owner", change: func(k *handoffKernel, _ *hostRunner) { k.foreign[29] = true }},
		{name: "extra arguments", argv: []string{"pfm", "mcp", "serve", "--extra"}},
		{name: "recycled pid", change: func(k *handoffKernel, _ *hostRunner) { k.recycle = true }},
		{name: "identity failure", change: func(k *handoffKernel, _ *hostRunner) { k.identityErr = errors.New("identity refused") }, wantErr: "identity refused"},
		{name: "executable failure", change: func(k *handoffKernel, _ *hostRunner) { k.executableErr = errors.New("executable refused") }, wantErr: "executable refused"},
		{name: "environment failure", change: func(k *handoffKernel, _ *hostRunner) { k.envErr = errors.New("environment refused") }, wantErr: "environment refused"},
		{name: "enumeration failure", change: func(k *handoffKernel, _ *hostRunner) { k.listErr = errors.New("enumeration refused") }, wantErr: "enumeration refused"},
		{name: "unreadable process", change: func(k *handoffKernel, _ *hostRunner) { k.unreadable[29] = true }, wantErr: "pid 29"},
		{name: "missing start identity", change: func(k *handoffKernel, _ *hostRunner) { row := k.procs[29]; row.Start = 0; k.procs[29] = row }, wantErr: "start identity unavailable"},
		{name: "term ignored", change: func(k *handoffKernel, _ *hostRunner) { k.stubborn[29] = true }, wantSignals: []string{"29 terminated", "29 killed"}},
		{name: "survives kill", enabled: true, change: func(k *handoffKernel, _ *hostRunner) { k.stubborn[29], k.unkillable[29] = true, true }, wantSignals: []string{"29 terminated", "29 killed"}, wantErr: "after SIGKILL"},
		{name: "start fails", enabled: true, change: func(_ *handoffKernel, r *hostRunner) { r.startErr = errors.New("start refused") }, wantSignals: []string{"29 terminated"}, starts: 1, wantErr: "start refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := tc.argv
			if argv == nil {
				argv = []string{"pfm", "mcp", "serve"}
			}
			kernel := &handoffKernel{
				fakeKernel: newKernel(process{PID: 29, Argv: argv, Start: 5}),
				executable: "/opt/pfm/bin/pfm",
				configPath: "/fixture/config",
			}
			runner := &hostRunner{}
			dependencies := testDeps(t, runner, kernel.fakeKernel, &fakeFleet{})
			dependencies.Procs = kernel
			dependencies.ConfigPath = "/fixture/config"
			dependencies.DaemonProbe = func(context.Context) error { return nil }
			runner.onStart = func(argv []string) {
				if len(kernel.sent()) == 0 {
					t.Error("started before stopping old owned daemon")
				}
				if !slices.Equal(
					argv,
					[]string{dependencies.Executable, "--config", dependencies.ConfigPath, "mcp", "serve"},
				) {
					t.Errorf("start argv=%v", argv)
				}
			}
			if tc.change != nil {
				tc.change(kernel, runner)
			}
			_, err := ConvergeMCPDaemon(context.Background(), dependencies, tc.enabled)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error=%v want=%s", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(kernel.sent(), tc.wantSignals) || len(runner.starts) != tc.starts {
				t.Fatalf(
					"signals=%v starts=%d want=%v/%d",
					kernel.sent(),
					len(runner.starts),
					tc.wantSignals,
					tc.starts,
				)
			}
		})
	}
}

type daemonRaceIdentity struct {
	identity gather.ProcessIdentity
	err      error
}

type daemonRaceKernel struct {
	*handoffKernel
	identities        map[int]daemonRaceIdentity
	executableErrors  map[int]error
	environmentErrors map[int]error
	executablePaths   map[int]string
	configPaths       map[int]string
	identityCalls     int
	executableCalls   int
	environmentCalls  int
}

func (kernel *daemonRaceKernel) ProcessIdentity(pid int) (gather.ProcessIdentity, error) {
	kernel.identityCalls++
	if scripted, ok := kernel.identities[kernel.identityCalls]; ok {
		return scripted.identity, scripted.err
	}
	return kernel.handoffKernel.ProcessIdentity(pid)
}

func (kernel *daemonRaceKernel) Executable(pid int) (string, error) {
	kernel.executableCalls++
	if err := kernel.executableErrors[kernel.executableCalls]; err != nil {
		return "", err
	}
	if path, ok := kernel.executablePaths[kernel.executableCalls]; ok {
		return path, nil
	}
	return kernel.handoffKernel.Executable(pid)
}

func (kernel *daemonRaceKernel) Environ(pid int) (map[string]string, error) {
	kernel.environmentCalls++
	if err := kernel.environmentErrors[kernel.environmentCalls]; err != nil {
		return nil, err
	}
	if path, ok := kernel.configPaths[kernel.environmentCalls]; ok {
		return map[string]string{"PFM_CONFIG": path}, nil
	}
	return kernel.handoffKernel.Environ(pid)
}

func TestConvergeMCPDaemonExitRacesPreserveLiveFailures(t *testing.T) {
	for _, tc := range []struct {
		name              string
		identities        map[int]daemonRaceIdentity
		executableErrors  map[int]error
		environmentErrors map[int]error
		executablePaths   map[int]string
		configPaths       map[int]string
		recycle           bool
		stubborn          bool
		enabled           bool
		wantSignals       []string
		starts            int
		wantErr           string
	}{
		{name: "owned snapshot exits before stop still restarts", enabled: true, starts: 1, identities: map[int]daemonRaceIdentity{3: {err: syscall.ESRCH}}},
		{name: "post TERM zombie restarts enabled chat", stubborn: true, enabled: true, starts: 1, wantSignals: []string{"29 terminated"}, identities: map[int]daemonRaceIdentity{5: {identity: gather.ProcessIdentity{Zombie: true}}}},
		{name: "post TERM exit stops disabled chat", stubborn: true, wantSignals: []string{"29 terminated"}, identities: map[int]daemonRaceIdentity{5: {err: syscall.ESRCH}}},
		{name: "post TERM executable disappears after exit", stubborn: true, enabled: true, starts: 1, wantSignals: []string{"29 terminated"}, executableErrors: map[int]error{3: fs.ErrNotExist}, identities: map[int]daemonRaceIdentity{6: {err: syscall.ESRCH}}},
		{name: "zombie between stopper and ownership checks", identities: map[int]daemonRaceIdentity{3: {identity: gather.ProcessIdentity{Zombie: true}}}},
		{name: "ESRCH between stopper and ownership checks", identities: map[int]daemonRaceIdentity{3: {err: syscall.ESRCH}}},
		{name: "identity missing between checks", identities: map[int]daemonRaceIdentity{3: {err: fs.ErrNotExist}}},
		{name: "executable missing during discovery after exit", executableErrors: map[int]error{1: fs.ErrNotExist}, identities: map[int]daemonRaceIdentity{2: {err: syscall.ESRCH}}},
		{name: "executable missing during verify after exit", executableErrors: map[int]error{2: fs.ErrNotExist}, identities: map[int]daemonRaceIdentity{4: {err: syscall.ESRCH}}},
		{name: "executable missing during verify after zombie", executableErrors: map[int]error{2: fs.ErrNotExist}, identities: map[int]daemonRaceIdentity{4: {identity: gather.ProcessIdentity{Zombie: true}}}},
		{name: "environment missing during verify after exit", environmentErrors: map[int]error{2: fs.ErrNotExist}, identities: map[int]daemonRaceIdentity{4: {err: syscall.ESRCH}}},
		{name: "transient executable missing recovers", executableErrors: map[int]error{2: fs.ErrNotExist}, wantSignals: []string{"29 terminated"}},
		{name: "transient environment missing recovers", environmentErrors: map[int]error{2: fs.ErrNotExist}, wantSignals: []string{"29 terminated"}},
		{name: "identity permission remains error", identities: map[int]daemonRaceIdentity{3: {err: fs.ErrPermission}}, wantErr: "permission"},
		{name: "executable permission remains error", executableErrors: map[int]error{2: fs.ErrPermission}, wantErr: "permission"},
		{name: "environment permission remains error", environmentErrors: map[int]error{2: fs.ErrPermission}, wantErr: "permission"},
		{name: "gone proof unreadable remains error", executableErrors: map[int]error{2: fs.ErrNotExist}, identities: map[int]daemonRaceIdentity{4: {err: fs.ErrPermission}}, wantErr: "permission"},
		{name: "live UID change refused", identities: map[int]daemonRaceIdentity{3: {identity: gather.ProcessIdentity{EffectiveUID: foreignUID}}}, wantErr: "identity changed"},
		{name: "live executable change refused", executablePaths: map[int]string{2: "/foreign/pfm"}, wantErr: "identity changed"},
		{name: "live config change refused", configPaths: map[int]string{2: "/foreign/config"}, wantErr: "identity changed"},
		{name: "PID reuse does not signal", recycle: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kernel := &daemonRaceKernel{
				handoffKernel: &handoffKernel{
					fakeKernel: newKernel(process{PID: 29, Argv: []string{"pfm", "mcp", "serve"}, Start: 5}),
					executable: "/opt/pfm/bin/pfm",
					configPath: "/fixture/config",
					recycle:    tc.recycle,
				},
				identities:        tc.identities,
				executableErrors:  tc.executableErrors,
				environmentErrors: tc.environmentErrors,
				executablePaths:   tc.executablePaths,
				configPaths:       tc.configPaths,
			}
			runner := &hostRunner{}
			dependencies := testDeps(t, runner, kernel.fakeKernel, &fakeFleet{})
			dependencies.Procs = kernel
			dependencies.ConfigPath = "/fixture/config"
			kernel.stubborn[29] = tc.stubborn
			dependencies.DaemonProbe = func(context.Context) error { return nil }
			_, err := ConvergeMCPDaemon(context.Background(), dependencies, tc.enabled)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error=%v want=%s", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(kernel.sent(), tc.wantSignals) || len(runner.starts) != tc.starts {
				t.Fatalf(
					"signals=%v starts=%d want=%v/%d",
					kernel.sent(),
					len(runner.starts),
					tc.wantSignals,
					tc.starts,
				)
			}
		})
	}
}

type delayedDaemonKernel struct {
	*handoffKernel
	door          string
	missingErr    error
	missingAt     int
	calls         int
	missing       bool
	terminal      bool
	gone          bool
	proofErr      error
	afterReadable func()
}

func (kernel *delayedDaemonKernel) missingRead(door string) error {
	if kernel.door != door {
		return nil
	}
	kernel.calls++
	if kernel.calls == kernel.missingAt {
		kernel.missing = true
	}
	if kernel.missing {
		return kernel.missingErr
	}
	return nil
}

func (kernel *delayedDaemonKernel) Cmdline(pid int) ([]string, error) {
	if err := kernel.missingRead("command line"); err != nil {
		return nil, nil
	}
	return kernel.handoffKernel.Cmdline(pid)
}

func (kernel *delayedDaemonKernel) Executable(pid int) (string, error) {
	if err := kernel.missingRead("executable"); err != nil {
		return "", err
	}
	return kernel.handoffKernel.Executable(pid)
}

func (kernel *delayedDaemonKernel) Environ(pid int) (map[string]string, error) {
	if err := kernel.missingRead("environment"); err != nil {
		return nil, err
	}
	env, err := kernel.handoffKernel.Environ(pid)
	if !kernel.missing && kernel.afterReadable != nil {
		after := kernel.afterReadable
		kernel.afterReadable = nil
		after()
	}
	return env, err
}

func (kernel *delayedDaemonKernel) ProcessIdentity(pid int) (gather.ProcessIdentity, error) {
	if kernel.missing && kernel.proofErr != nil {
		return gather.ProcessIdentity{}, kernel.proofErr
	}
	if kernel.gone {
		return gather.ProcessIdentity{}, syscall.ESRCH
	}
	if kernel.terminal {
		return gather.ProcessIdentity{Zombie: true}, nil
	}
	return kernel.handoffKernel.ProcessIdentity(pid)
}

type daemonProofClock struct {
	tickClock
	afterSleep func()
	sleeps     int
}

func (clk *daemonProofClock) Sleep(ctx context.Context, duration time.Duration) error {
	clk.sleeps++
	if clk.afterSleep != nil {
		clk.afterSleep()
	}
	return clk.tickClock.Sleep(ctx, duration)
}

func TestConvergeMCPDaemonWaitsForLateExitWithoutRefreshingIdentity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		door        string
		enabled     bool
		discovery   bool
		readErr     error
		proofErr    error
		afterSleep  func(*delayedDaemonKernel, context.CancelFunc)
		wantErr     string
		wantSignals []string
		starts      int
		immediate   bool
	}{
		{name: "empty argv live must wait", door: "command line", enabled: true, starts: 1, afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) { k.gone = true }},
		{name: "empty argv persists live times out", door: "command line", wantErr: "deadline"},
		{name: "empty argv becomes readable", door: "command line", wantSignals: []string{"29 terminated"}, afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) { k.missing = false }},
		{name: "missing executable then exited restarts", enabled: true, starts: 1, afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) { k.gone = true }},
		{name: "missing executable then zombie stops", afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) { k.terminal = true }},
		{name: "missing environment then terminal restarts", door: "environment", enabled: true, starts: 1, afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) { k.terminal = true }},
		{name: "missing executable becomes readable", wantSignals: []string{"29 terminated"}, afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) { k.missing = false }},
		{name: "missing environment becomes readable", door: "environment", wantSignals: []string{"29 terminated"}, afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) { k.missing = false }},
		{name: "discovery missing then gone", discovery: true, afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) { k.gone = true }},
		{name: "persistent missing executable live times out", wantErr: "deadline"},
		{name: "persistent missing environment live times out", door: "environment", wantErr: "deadline"},
		{name: "cancellation", wantErr: "canceled", afterSleep: func(_ *delayedDaemonKernel, cancel context.CancelFunc) { cancel() }},
		{name: "permission immediate", readErr: fs.ErrPermission, wantErr: "permission", immediate: true},
		{name: "IO immediate", readErr: syscall.EIO, wantErr: "input/output", immediate: true},
		{name: "identity proof failure immediate", proofErr: fs.ErrPermission, wantErr: "permission", immediate: true},
		{name: "PID reused before executable readable", afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) {
			k.missing = false
			row := k.procs[29]
			row.Start++
			k.procs[29] = row
		}},
		{name: "argv replaced before executable readable", wantErr: "identity changed", afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) {
			k.missing = false
			row := k.procs[29]
			row.Argv = []string{"pfm", "mcp", "serve", "--extra"}
			k.procs[29] = row
		}},
		{name: "UID replaced before executable readable", wantErr: "identity changed", afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) { k.missing = false; k.foreign[29] = true }},
		{name: "executable replaced after wait", wantErr: "identity changed", afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) { k.missing = false; k.executable = "/foreign/pfm" }},
		{name: "executable replaced during final probe after wait", wantErr: "identity changed", afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) {
			k.missing = false
			k.afterReadable = func() { k.executable = "/foreign/pfm" }
		}},
		{name: "config replaced during final probe after wait", wantErr: "identity changed", afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) {
			k.missing = false
			k.afterReadable = func() { k.configPath = "/foreign/config" }
		}},
		{name: "config replaced after wait", wantErr: "identity changed", afterSleep: func(k *delayedDaemonKernel, _ context.CancelFunc) {
			k.missing = false
			k.configPath = "/foreign/config"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			kernel := &delayedDaemonKernel{
				handoffKernel: &handoffKernel{
					fakeKernel: newKernel(process{PID: 29, Argv: []string{"pfm", "mcp", "serve"}, Start: 5}),
					executable: "/opt/pfm/bin/pfm",
					configPath: "/fixture/config",
				},
				door:       tc.door,
				missingErr: tc.readErr,
				missingAt:  2,
				proofErr:   tc.proofErr,
			}
			if kernel.door == "" {
				kernel.door = "executable"
			}
			if kernel.door == "command line" {
				kernel.missingAt = 3
			}
			if kernel.missingErr == nil {
				kernel.missingErr = fs.ErrNotExist
			}
			if tc.discovery {
				kernel.missingAt = 1
			}
			clk := &daemonProofClock{tickClock: newTickClock()}
			if tc.afterSleep != nil {
				clk.afterSleep = func() { tc.afterSleep(kernel, cancel) }
			}
			runner := &hostRunner{}
			dependencies := testDeps(t, runner, kernel.fakeKernel, &fakeFleet{})
			dependencies.Procs, dependencies.Clock = kernel, clk
			dependencies.ConfigPath = "/fixture/config"
			dependencies.DaemonProbe = func(context.Context) error { return nil }
			_, err := ConvergeMCPDaemon(ctx, dependencies, tc.enabled)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error=%v want=%s", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(kernel.sent(), tc.wantSignals) || len(runner.starts) != tc.starts {
				t.Fatalf(
					"signals=%v starts=%d want=%v/%d",
					kernel.sent(),
					len(runner.starts),
					tc.wantSignals,
					tc.starts,
				)
			}
			if !tc.immediate && tc.afterSleep != nil && clk.sleeps == 0 {
				t.Fatal("transient missing state bypassed bounded proof")
			}
			if tc.immediate && clk.sleeps != 0 {
				t.Fatalf("immediate error slept %d times", clk.sleeps)
			}
		})
	}
}

func TestProcessStopRechecksSnapshotAfterOwnershipProbe(t *testing.T) {
	proc := process{PID: 29, Argv: []string{"pfm", "mcp", "serve"}, Start: 5}
	kernel := newKernel(proc)
	stopper := processStopper{
		procs:  kernel,
		signal: kernel.signal,
		clock:  newTickClock(),
		verify: func(process) (bool, error) {
			replacement := proc
			replacement.Start++
			kernel.procs[29] = replacement
			return true, nil
		},
	}
	_, err := stopper.stop(context.Background(), proc)
	if err != nil || len(kernel.sent()) != 0 {
		t.Fatalf("error=%v signals=%v; snapshot replacement must never be signalled", err, kernel.sent())
	}
}
