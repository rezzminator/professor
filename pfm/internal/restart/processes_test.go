package restart

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
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
		"chat-helpers: harvester sidecar converter.py pid 12",
		"chat-helpers: harvester sidecar browser.py pid 13",
		"chat-helpers: cost sink pid 17",
		"pfm: pfm process pid 15",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("units =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if detail := finder.plan.Findings[0].Detail; !strings.Contains(detail, "5 helper process(es) of 9 listed") {
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
