package restart

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
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
		process{PID: 18, Argv: []string{"/opt/pfm/bin/pfm", "chat", "watch"}}, // another install's
		process{
			PID:  19,
			Argv: []string{"/opt/pfm/bin/pfm", "ls", "--watch"},
		}, // unmarked, this install's binary
		process{PID: 20, Argv: []string{"/work/other/pfm", "chat", "watch"}},          // unmarked, another binary
		process{PID: 21, Argv: []string{"/opt/pfm/bin/pfm", "internal", "cost-sink"}}, // another install's
	)
	kernel.marks[18] = map[string]string{paths.EnvInstallMark: "another-install-mark"}
	kernel.marks[19] = map[string]string{"HOME": "/srv/operator"}
	kernel.marks[20] = map[string]string{"HOME": "/srv/operator"}
	kernel.marks[21] = map[string]string{paths.EnvInstallMark: "another-install-mark"}
	dependencies := testDeps(t, &hostRunner{}, kernel, &fakeFleet{})
	// Only the unmarked processes are asked for their executable.
	dependencies.Procs = &executableKernel{
		fakeKernel:  kernel,
		executables: map[int]string{19: testBinary, 20: "/work/other/pfm"},
	}
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
		"pfm: pfm process pid 19",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("units =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var details []string
	for _, finding := range finder.plan.Findings {
		if finding.Err != nil {
			t.Fatalf("%s find failed: %v", finding.Family, finding.Err)
		}
		details = append(details, finding.Detail)
	}
	wantDetails := []string{
		"3 helper process(es) of 13 listed — process table: pfm internal cost-sink, pfm mcp serve --stdio" +
			"; 1 of another install kept",
		"2 other pfm process(es) of 13 listed — process table, argv[0] named pfm; this worker (pid 1) is last" +
			"; 3 of another install kept",
	}
	if !slices.Equal(details, wantDetails) {
		t.Fatalf("findings =\n%s\nwant\n%s", strings.Join(details, "\n"), strings.Join(wantDetails, "\n"))
	}
	if len(kernel.sent()) != 0 {
		t.Fatalf("finding signalled: %v", kernel.sent())
	}
}

// TestFindMCPDaemonKeepsAnotherInstallsDaemon: an unsupervised daemon of this
// install is planned for its stop; another install's is kept running and
// claimed, so the pfm family never plans it either.
func TestFindMCPDaemonKeepsAnotherInstallsDaemon(t *testing.T) {
	kernel := newKernel(
		process{PID: 30, Argv: []string{"/opt/pfm/bin/pfm", "mcp", "serve"}},
		process{PID: 31, Argv: []string{"/opt/pfm/bin/pfm", "mcp", "serve"}}, // another install's
	)
	kernel.marks[31] = map[string]string{paths.EnvInstallMark: "another-install-mark"}
	finder := &discoverer{deps: testDeps(t, &hostRunner{}, kernel, &fakeFleet{}), claimed: map[int]bool{}}
	table, err := finder.readTable()
	if err != nil {
		t.Fatal(err)
	}
	finder.findMCPDaemon(table, false, nil)
	finder.findPFM(table)
	got := unitNames(finder.plan.Units)
	if want := []string{"mcp-daemon: pfm mcp serve pid 30"}; !slices.Equal(got, want) {
		t.Fatalf("units = %q, want %q", got, want)
	}
	var details []string
	for _, finding := range finder.plan.Findings {
		if finding.Err != nil {
			t.Fatalf("%s find failed: %v", finding.Family, finding.Err)
		}
		details = append(details, finding.Detail)
	}
	wantDetails := []string{
		"1 unsupervised pfm mcp serve process(es) of 2 listed — process table; 1 of another install kept",
		"0 other pfm process(es) of 2 listed — process table, argv[0] named pfm; this worker (pid 1) is last",
	}
	if !slices.Equal(details, wantDetails) {
		t.Fatalf("findings =\n%s\nwant\n%s", strings.Join(details, "\n"), strings.Join(wantDetails, "\n"))
	}
}

// TestFindMCPDaemonCountsAnExitingDaemonAsNoInstalls pins a daemon that exits
// between the table read and its ownership read: its environment reads torn,
// the install mark gone with the rest, and its executable is gone. It is
// neither this install's daemon nor another install's kept one.
func TestFindMCPDaemonCountsAnExitingDaemonAsNoInstalls(t *testing.T) {
	kernel := newKernel(process{PID: 30, Argv: []string{"/opt/pfm/bin/pfm", "mcp", "serve"}})
	kernel.marks[30] = map[string]string{"HOME": "/srv/operator"}
	deps := testDeps(t, &hostRunner{}, kernel, &fakeFleet{})
	deps.Procs = &executableKernel{fakeKernel: kernel, executables: map[int]string{}}
	finder := &discoverer{deps: deps, claimed: map[int]bool{}}
	table, err := finder.readTable()
	if err != nil {
		t.Fatal(err)
	}
	finder.findMCPDaemon(table, false, nil)
	if got := unitNames(finder.plan.Units); len(got) != 0 {
		t.Fatalf("units = %q, want none for an exiting daemon", got)
	}
	want := "0 unsupervised pfm mcp serve process(es) of 1 listed — process table"
	if len(finder.plan.Findings) != 1 || finder.plan.Findings[0].Err != nil ||
		finder.plan.Findings[0].Detail != want {
		t.Fatalf("findings = %+v, want one %q", finder.plan.Findings, want)
	}
}

// executableKernel is a fakeKernel that also reads executables, ESRCH for a
// pid it has none for.
type executableKernel struct {
	*fakeKernel
	executables map[int]string
}

func (kernel *executableKernel) Executable(pid int) (string, error) {
	if executable, ok := kernel.executables[pid]; ok {
		return executable, nil
	}
	return "", syscall.ESRCH
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

// A process that outlives SIGKILL, or whose live pid cannot be read, fails its stop by name, never reads as gone.
func TestProcessStopNamesAProcessThatSurvivesSIGKILL(t *testing.T) {
	for _, row := range []struct {
		name       string
		proc       process
		unreadable bool
		want       string
	}{
		{"survives SIGKILL", process{PID: 22, Argv: []string{"pfm", "chat", "watch"}, Start: 5}, false, "pid 22 still runs pfm chat watch after SIGKILL"},
		{"unreadable live pid", process{PID: 24, Argv: []string{"pfm", "internal", "cost-sink"}, Start: 5}, true, "read pid 24 from the process table: permission denied\npermission denied"},
	} {
		t.Run(row.name, func(t *testing.T) {
			kernel := newKernel(row.proc)
			if row.unreadable {
				kernel.unreadable[row.proc.PID] = true
			} else {
				kernel.stubborn[row.proc.PID], kernel.unkillable[row.proc.PID] = true, true
			}
			finder := &discoverer{deps: testDeps(t, &hostRunner{}, kernel, &fakeFleet{})}
			_, err := finder.processUnit(FamilyPFM, "pfm process", row.proc).Stop(context.Background())
			if got := fmt.Sprint(err); got != row.want {
				t.Fatalf("stop error = %q, want %q", got, row.want)
			}
		})
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

// TestProcessStopNeverSignalsAChangedOwner: a planned process that, by the
// stop, runs as another user or carries another install's mark is never
// signalled; the stop names the change.
func TestProcessStopNeverSignalsAChangedOwner(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(kernel *fakeKernel)
		want   string
	}{
		{
			name:   "another user",
			change: func(kernel *fakeKernel) { kernel.foreign[29] = true },
			want:   "pid 29 identity changed before signal",
		},
		{
			name: "another install",
			change: func(kernel *fakeKernel) {
				kernel.marks[29] = map[string]string{paths.EnvInstallMark: "another-install-mark"}
			},
			want: "pid 29 identity changed before signal",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := process{PID: 29, Argv: []string{"/opt/pfm/bin/pfm", "mcp", "serve"}, Start: 5}
			kernel := newKernel(proc)
			tc.change(kernel)
			finder := &discoverer{deps: testDeps(t, &hostRunner{}, kernel, &fakeFleet{})}
			_, err := finder.processUnit(FamilyMCPDaemon, "pfm mcp serve", proc).Stop(context.Background())
			if err == nil || err.Error() != tc.want || len(kernel.sent()) != 0 {
				t.Fatalf("error=%v signals=%v, want %q and no signal", err, kernel.sent(), tc.want)
			}
		})
	}
}

func TestProcessStopRechecksSnapshotAfterOwnershipProbe(t *testing.T) {
	proc := process{PID: 29, Argv: []string{"pfm", "mcp", "serve"}, Start: 5}
	kernel := newKernel(proc)
	stopper := processStopper{
		door:  gather.Door{Procs: kernel, Kernel: kernel, Clock: newTickClock()},
		probe: kernel.probe,
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
