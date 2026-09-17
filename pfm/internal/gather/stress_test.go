package gather

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGatherStress(t *testing.T) {
	if os.Getenv("PFM_STRESS") != "1" {
		t.Skip("set PFM_STRESS=1 to run the WP4 stress phase")
	}

	t.Run("crumb_flood", stressCrumbFlood)
	t.Run("socket_storm", stressSocketStorm)
	t.Run("procfs_scale", stressProcFSScale)
}

func stressCrumbFlood(t *testing.T) {
	strict := os.Getenv("PFM_STRESS_STRICT") == "1"
	root := t.TempDir()
	setGatherTestEnv(t, root, filepath.Join(root, "tmux"))
	sidDir := filepath.Join(root, "sid")

	for index := 0; index < 5000; index++ {
		name := fmt.Sprintf("cc-%d-1-1", index+1)
		if err := os.WriteFile(
			filepath.Join(sidDir, name),
			[]byte("/transcripts/"+name+".jsonl"),
			0o600,
		); err != nil {
			t.Fatalf("write valid crumb %d: %v", index, err)
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
			t.Fatalf("write malformed crumb %d: %v", index, err)
		}
	}
	for _, name := range []string{
		"../cc-1-2-3",
		strings.Repeat("x", 1024),
	} {
		if _, _, ok := ParseCrumbName(name); ok {
			t.Fatalf("ParseCrumbName(%q) accepted adversarial name", name)
		}
	}

	started := time.Now()
	probe, err := ReadCrumbs(sidDir, nil)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("ReadCrumbs(10k) error = %v", err)
	}
	if probe.FilesSeen != 10000 ||
		probe.Accepted != 5000 ||
		probe.Ignored != 5000 ||
		len(probe.Crumbs) != 5000 {
		t.Fatalf("ReadCrumbs(10k) counters = %+v", probe)
	}
	limit := time.Minute
	if strict {
		limit = 5 * time.Second
	}
	if elapsed > limit {
		t.Fatalf(
			"ReadCrumbs(10k) took %s, want under %s (strict=%t)",
			elapsed,
			limit,
			strict,
		)
	}
	t.Logf(
		"stress crumb flood: 10,000 files (5,000 accepted, 5,000 rejected) in %s (strict=%t)",
		elapsed.Round(time.Microsecond),
		strict,
	)
}

type gatedTmuxClient struct {
	inner   TmuxClient
	started chan<- string
	release <-chan struct{}
}

func (client gatedTmuxClient) ListPanes(
	ctx context.Context,
	socket string,
) ([]Pane, error) {
	select {
	case client.started <- socket:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-client.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return client.inner.ListPanes(ctx, socket)
}

func stressSocketStorm(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}

	jail := newTmuxJail(t)
	strict := os.Getenv("PFM_STRESS_STRICT") == "1"
	timeout := 2 * time.Minute
	if strict {
		timeout = 15 * time.Second
	}
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
	client := CommandTmux{Binary: "tmux", TmuxTmpDir: jail.root}

	firstContext, cancelFirst := context.WithTimeout(context.Background(), timeout)
	defer cancelFirst()
	started := time.Now()
	firstProbe, err := ProbeTmux(firstContext, jail.tmuxDir, client, time.Now())
	firstElapsed := time.Since(started)
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
		probe TmuxProbe
		err   error
	}
	resultChannel := make(chan probeResult, 1)
	secondContext, cancelSecond := context.WithTimeout(context.Background(), timeout)
	defer cancelSecond()
	secondStarted := time.Now()
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
	secondElapsed := time.Since(secondStarted)
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
	if secondElapsed > timeout {
		t.Fatalf(
			"second socket storm took %s, want under %s (strict=%t)",
			secondElapsed,
			timeout,
			strict,
		)
	}
	t.Logf(
		"stress socket storm: 30 live servers in %s; 15 mid-probe kills in %s (strict=%t)",
		firstElapsed.Round(time.Microsecond),
		secondElapsed.Round(time.Microsecond),
		strict,
	)
}

func stressProcFSScale(t *testing.T) {
	strict := os.Getenv("PFM_STRESS_STRICT") == "1"
	const (
		processCount = 5000
		codexCount   = 200
		fdCount      = 100
	)
	codexHome := "/fixture/codex"
	processes := make(map[int]fakeProcess, processCount)
	for pid := 1; pid <= processCount; pid++ {
		processes[pid] = fakeProcess{
			cmdline: []string{"/usr/bin/worker"},
			stat:    ProcStat{ParentPID: 1, StartTime: uint64(pid)},
		}
	}

	panes := make([]Pane, 0, codexCount)
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
		panes = append(panes, Pane{
			Socket: fmt.Sprintf("cx-%d-1-1", index+1),
			PaneID: fmt.Sprintf("%%%d", index),
			PID:    panePID,
		})
	}
	proc := &fakeProcFS{processes: processes}

	started := time.Now()
	live, err := DetectCodex(proc, codexHome, panes)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("DetectCodex(5k/20k fds) error = %v", err)
	}
	if len(live) != codexCount {
		t.Fatalf("DetectCodex(5k/20k fds) found %d, want %d", len(live), codexCount)
	}
	limit := time.Minute
	if strict {
		limit = 5 * time.Second
	}
	if elapsed > limit {
		t.Fatalf(
			"DetectCodex(5k/20k fds) took %s, want under %s (strict=%t)",
			elapsed,
			limit,
			strict,
		)
	}
	t.Logf(
		"stress ProcFS scale: 5,000 processes, 200 Codex processes, 20,000 fds in %s (strict=%t)",
		elapsed.Round(time.Microsecond),
		strict,
	)
}
