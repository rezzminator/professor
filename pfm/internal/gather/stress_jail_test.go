//go:build jail

package gather

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestGatherStress(t *testing.T) {
	if os.Getenv("PFM_STRESS") != "1" {
		t.Skip("set PFM_STRESS=1 to run the WP4 stress phase")
	}

	t.Run("crumb_flood", stressCrumbFlood)
	t.Run("socket_storm", stressSocketStorm)
	t.Run("procfs_scale", stressProcFSScale)
}

// writeCrumbFlood fills sidDir with 5,000 valid crumbs and 5,000 malformed ones.
func writeCrumbFlood(tb testing.TB, sidDir string) {
	tb.Helper()
	for index := 0; index < 5000; index++ {
		name := fmt.Sprintf("cc-%d-1-1", index+1)
		if err := os.WriteFile(
			filepath.Join(sidDir, name),
			[]byte("/transcripts/"+name+".jsonl"),
			0o600,
		); err != nil {
			tb.Fatalf("write valid crumb %d: %v", index, err)
		}
	}
	for index := 0; index < 5000; index++ {
		var name string
		switch index % 4 {
		case 0:
			name = fmt.Sprintf("cc-%d-1-1.then-failed", index+1)
		case 1:
			name = fmt.Sprintf("cc-%d-1-1\nsentinel", index+1)
		case 2:
			name = fmt.Sprintf("cc-%d-1-1..%%1", index+1)
		default:
			name = fmt.Sprintf("cc-%d-1-1.%%x", index+1)
		}
		if err := os.WriteFile(
			filepath.Join(sidDir, name),
			[]byte("/transcripts/ignored.jsonl"),
			0o600,
		); err != nil {
			tb.Fatalf("write malformed crumb %d: %v", index, err)
		}
	}
}

func stressCrumbFlood(t *testing.T) {
	root := t.TempDir()
	setGatherTestEnv(t, root, filepath.Join(root, "tmux"))
	sidDir := filepath.Join(root, "sid")
	writeCrumbFlood(t, sidDir)
	for _, name := range []string{
		"../cc-1-2-3",
		strings.Repeat("x", 1024),
	} {
		if _, _, ok := ParseCrumbName(name); ok {
			t.Fatalf("ParseCrumbName(%q) accepted adversarial name", name)
		}
	}

	probe, err := ReadCrumbs(sidDir, nil)
	if err != nil {
		t.Fatalf("ReadCrumbs(10k) error = %v", err)
	}
	if probe.FilesSeen != 10000 ||
		probe.Accepted != 5000 ||
		probe.Ignored != 5000 ||
		len(probe.Crumbs) != 5000 {
		t.Fatalf("ReadCrumbs(10k) counters = %+v", probe)
	}
}

// BenchmarkReadCrumbsFlood measures what the crumb flood test no longer judges: ReadCrumbs over
// 10,000 files, half of them malformed.
func BenchmarkReadCrumbsFlood(b *testing.B) {
	sidDir := filepath.Join(b.TempDir(), "sid")
	if err := os.MkdirAll(sidDir, 0o700); err != nil {
		b.Fatal(err)
	}
	writeCrumbFlood(b, sidDir)
	b.ResetTimer()
	for b.Loop() {
		if _, err := ReadCrumbs(sidDir, nil); err != nil {
			b.Fatalf("ReadCrumbs(10k) error = %v", err)
		}
	}
}

func stressSocketStorm(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}

	testjail.TmuxServer(t)
	jail := newTmuxJail(t)
	sockets := make([]string, 30)
	for index := range sockets {
		sockets[index] = fmt.Sprintf("cc-%d-2000-%d", 1000+index, index)
		jail.startServer(
			t,
			sockets[index],
			fmt.Sprintf("session-%d", index),
			"window",
			"pane",
		)
	}
	client := TmuxProbe{Binary: "tmux", TmuxTmpDir: jail.root}

	firstProbe, err := ProbeTmux(testwait.Context(t), jail.tmuxDir, client, time.Now())
	if err != nil {
		t.Fatalf("first 30-server ProbeTmux() error = %v", err)
	}
	if len(firstProbe.Panes) != 30 || len(firstProbe.ProbeWarnings) != 0 {
		t.Fatalf(
			"first 30-server ProbeTmux() got %d panes and %d warnings",
			len(firstProbe.Panes),
			len(firstProbe.ProbeWarnings),
		)
	}

	release := make(chan struct{})
	probeStarted := make(chan string, 30)
	gated := gatedTmuxClient{
		inner:   client,
		started: probeStarted,
		release: release,
	}
	type probeResult struct {
		probe TmuxSnapshot
		err   error
	}
	resultChannel := make(chan probeResult, 1)
	secondContext := testwait.Context(t)
	go func() {
		probe, probeErr := ProbeTmux(
			secondContext,
			jail.tmuxDir,
			gated,
			time.Now(),
		)
		resultChannel <- probeResult{probe: probe, err: probeErr}
	}()

	seen := make(map[string]struct{}, 30)
	for len(seen) < 30 {
		select {
		case socket := <-probeStarted:
			seen[socket] = struct{}{}
		case <-secondContext.Done():
			t.Fatalf("only %d of 30 socket probes started: %v", len(seen), secondContext.Err())
		}
	}
	for _, socket := range sockets[:15] {
		if err := jail.killServer(socket); err != nil {
			t.Fatalf("kill server %q mid-probe: %v", socket, err)
		}
	}
	close(release)

	var secondResult probeResult
	select {
	case secondResult = <-resultChannel:
	case <-secondContext.Done():
		t.Fatalf("second socket storm hung: %v", secondContext.Err())
	}
	if secondResult.err != nil {
		t.Fatalf("second ProbeTmux() escalated dead sockets: %v", secondResult.err)
	}
	if len(secondResult.probe.Panes) != 15 ||
		len(secondResult.probe.ProbeWarnings) != 0 {
		t.Fatalf(
			"second ProbeTmux() got %d panes and %d warnings; want 15 panes and silence for ended servers",
			len(secondResult.probe.Panes),
			len(secondResult.probe.ProbeWarnings),
		)
	}
}

const (
	procFSScaleProcesses = 5000
	procFSScaleCodex     = 200
	procFSScaleFDs       = 100
)

// procFSScaleFixture is 5,000 processes, 200 of them Codex, each holding 100 descriptors.
func procFSScaleFixture() (proc *fakeProcFS, codexHome string, panes []ProbePane) {
	const (
		processCount = procFSScaleProcesses
		codexCount   = procFSScaleCodex
		fdCount      = procFSScaleFDs
	)
	codexHome = "/fixture/codex"
	processes := make(map[int]fakeProcess, processCount)
	for pid := 1; pid <= processCount; pid++ {
		processes[pid] = fakeProcess{
			cmdline: []string{"/usr/bin/worker"},
			stat:    ProcStat{ParentPID: 1, StartTime: uint64(pid)},
		}
	}

	panes = make([]ProbePane, 0, codexCount)
	for index := 0; index < codexCount; index++ {
		codexPID := index + 1
		panePID := 1001 + index
		links := make([]FDLink, 0, fdCount)
		for fd := 0; fd < fdCount-1; fd++ {
			links = append(links, FDLink{
				FD:     fd,
				Target: fmt.Sprintf("/outside/%d/%d", index, fd),
			})
		}
		rollout := filepath.Join(
			codexHome,
			"sessions",
			"stress",
			fmt.Sprintf("rollout-%03d.jsonl", index),
		)
		links = append(links, FDLink{FD: fdCount - 1, Target: rollout})
		processes[codexPID] = fakeProcess{
			cmdline: []string{"/usr/bin/codex"},
			fdLinks: links,
			stat:    ProcStat{ParentPID: panePID, StartTime: uint64(codexPID)},
		}
		panes = append(panes, ProbePane{
			Socket: fmt.Sprintf("cx-%d-1-1", index+1),
			PaneID: fmt.Sprintf("%%%d", index),
			PID:    panePID,
		})
	}
	return &fakeProcFS{processes: processes}, codexHome, panes
}

func stressProcFSScale(t *testing.T) {
	proc, codexHome, panes := procFSScaleFixture()
	live, err := DetectCodex(proc, codexHome, panes)
	if err != nil {
		t.Fatalf("DetectCodex(5k/20k fds) error = %v", err)
	}
	if len(live) != procFSScaleCodex {
		t.Fatalf("DetectCodex(5k/20k fds) found %d, want %d", len(live), procFSScaleCodex)
	}
}

// BenchmarkDetectCodexScale measures what the ProcFS scale test no longer judges: DetectCodex
// over 5,000 processes and 20,000 descriptors.
func BenchmarkDetectCodexScale(b *testing.B) {
	proc, codexHome, panes := procFSScaleFixture()
	b.ResetTimer()
	for b.Loop() {
		if _, err := DetectCodex(proc, codexHome, panes); err != nil {
			b.Fatalf("DetectCodex(5k/20k fds) error = %v", err)
		}
	}
}
