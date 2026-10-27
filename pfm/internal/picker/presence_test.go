package picker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/themesync"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

// presenceJail jails the tmux socket directory and returns the jail root (the
// TMUX_TMPDIR of any server a test starts) and the real tmux binary, resolved
// before any test puts a shim on PATH.
func presenceJail(t *testing.T) (string, string) {
	t.Helper()
	tmuxBinary, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	t.Setenv("PFM_TMUX_DIR", filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())))
	return root, tmuxBinary
}

// parkedPresenceStream runs the real refresh loop until it is parked: the two
// passes the transition into park allows have published and the loop has gone
// quiet. It returns the stream's updates.
func parkedPresenceStream(t *testing.T) <-chan ui.Snapshot {
	t.Helper()
	shortenRefreshIntervals(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan ui.Snapshot, 1)
	var stderr bytes.Buffer
	t.Cleanup(func() {
		cancel()
		for range updates {
		}
	})
	go streamFleetRefreshesWith(
		ctx,
		database,
		scanRequest{},
		fleet.PrintWarn(&stderr),
		&stderr,
		updates,
		refreshDependencies{
			newIndexer: func(*store.Store) (indexRunner, error) { return &immediateIndexRunner{}, nil },
			activity:   ui.NewActivityClock(time.Now()),
		},
	)
	completed := 0
	for completed < 2 {
		select {
		case snapshot, ok := <-updates:
			if !ok {
				t.Fatalf("refresh stream closed before parking: %s", stderr.String())
			}
			if !snapshot.Refreshing {
				completed++
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("refresh stream never parked: %s", stderr.String())
		}
	}
	settleParked(t, updates, &stderr)
	return updates
}

// settleParked drains the stream until it has been silent for 300ms: a parked
// fleet whose pane pids the jail's proc root cannot see owes exactly one fresh
// pass before it is quiet, and a test must start from the quiet.
func settleParked(t *testing.T, updates <-chan ui.Snapshot, stderr *bytes.Buffer) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case _, ok := <-updates:
			if !ok {
				t.Fatalf("refresh stream closed while settling: %s", stderr.String())
			}
		case <-time.After(300 * time.Millisecond):
			return
		case <-deadline:
			t.Fatal("parked stream never went quiet")
		}
	}
}

// awaitFullPass waits for a snapshot of a completed pass.
func awaitFullPass(t *testing.T, updates <-chan ui.Snapshot, within time.Duration, why string) ui.Snapshot {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case snapshot, ok := <-updates:
			if !ok {
				t.Fatalf("refresh stream closed while %s", why)
			}
			if !snapshot.Refreshing && snapshot.RefreshFailure == nil {
				return snapshot
			}
		case <-deadline:
			t.Fatalf("no snapshot within %s while parked and %s: the picker froze at the moment it parked", within, why)
		}
	}
}

func TestParkedPickerPublishesThemeWithoutLiveRowsOrFacts(t *testing.T) {
	presenceJail(t)
	updates := parkedPresenceStream(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	variant := pickerThemeVariant(true)
	writePickerTheme(t, home, themesync.Applied{Primary: variant}, themesync.Light, true)
	snapshot := awaitFullPass(t, updates, 5*time.Second, "the theme changed without live rows or a facts reader")
	if got := ui.NewModel(snapshot).
		View().
		BackgroundColor; !reflect.DeepEqual(
		got,
		lipgloss.Color(variant.Background),
	) {
		t.Fatalf("parked theme background = %v, want %s", got, variant.Background)
	}
}

// TestParkedPickerPublishesAChatThatLaunchesWithoutAKeystroke pins root cause
// B for a launch: a picker that parked while nobody touched it did no pass
// until a key arrived, so a chat that started under it never reached the rows,
// the header counts or the cosmos. The parked poll now sees the new tmux socket
// without forking anything and runs a full pass for it.
func TestParkedPickerPublishesAChatThatLaunchesWithoutAKeystroke(t *testing.T) {
	root, _ := presenceJail(t)
	updates := parkedPresenceStream(t)
	startCodexStatusPane(t, root, "cx-1800000021-1-1", `  NEW_CHAT · /work/example · Full Access\n`)
	awaitFullPass(t, updates, 5*time.Second, "a new chat's socket appeared")
}

// TestParkedPickerPublishesAChatThatDiesWithoutAKeystroke is the same law for a
// death: a chat whose server is killed (or whose host rebooted) must leave the
// rows, not stay on them as a live star until somebody presses a key.
func TestParkedPickerPublishesAChatThatDiesWithoutAKeystroke(t *testing.T) {
	root, tmuxBinary := presenceJail(t)
	// The chat exists before the picker opens, so the picker parks WITH it.
	const socket = "cx-1800000022-1-1"
	startCodexStatusPane(t, root, socket, `  DOOMED · /work/example · Full Access\n`)
	updates := parkedPresenceStream(t)

	kill := exec.Command(tmuxBinary, "-L", socket, "kill-server")
	kill.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	if output, err := kill.CombinedOutput(); err != nil {
		t.Fatalf("kill chat server: %v: %s", err, output)
	}
	awaitFullPass(t, updates, 5*time.Second, "a chat's server died")
}

// presenceEnv is a fleet whose socket and crumb directories are empty scratch
// directories and whose processes are the kernel's own (an absent proc root
// falls back to the native reader).
func presenceEnv(t *testing.T) fleet.Env {
	t.Helper()
	root := t.TempDir()
	return fleet.Env{Paths: paths.Values{
		TmuxDir:  filepath.Join(root, "tmux"),
		SIDDir:   filepath.Join(root, "sid"),
		ProcRoot: filepath.Join(root, "no-proc"),
	}}
}

// goneProcessPID returns the pid of a process that has exited and been reaped.
func goneProcessPID(t *testing.T) int {
	t.Helper()
	command := exec.Command("true")
	if err := command.Run(); err != nil {
		t.Fatalf("run a process to reap: %v", err)
	}
	return command.Process.Pid
}

// TestPresenceBaselineOwesOnePassPerNewlyGonePid pins the parked poll's verdict
// on pids its own snapshot lists already gone. Each such pid buys exactly one
// full pass and then the fleet reads unchanged (a pane pid the proc root can
// never see must not cost a pass every poll); a chat that dies while a later
// pass runs is a NEW gone pid and still owes its own pass, or it stays a live
// row on screen until some other change.
func TestPresenceBaselineOwesOnePassPerNewlyGonePid(t *testing.T) {
	environment := presenceEnv(t)
	now := time.Now()
	pane := gather.ProbePane{Socket: "cc-1800000031-1-1", PaneID: "%1", PID: os.Getpid()}
	live := gather.Snapshot{
		Panes:           []gather.ProbePane{pane},
		ClaudeProcesses: []gather.ClaudeProcess{{PID: goneProcessPID(t), PanePID: pane.PID}},
	}
	var baseline presenceBaseline
	pass := func(live gather.Snapshot) {
		t.Helper()
		baseline.begin(environment, now)
		if err := baseline.record(environment, live); err != nil {
			t.Fatalf("record a readable fleet: %v", err)
		}
	}
	verdicts := func(live gather.Snapshot, polls int) []bool {
		t.Helper()
		out := make([]bool, 0, polls)
		for range polls {
			moved, err := baseline.moved(environment, live, now)
			if err != nil {
				t.Fatalf("probe a readable fleet: %v", err)
			}
			out = append(out, moved)
		}
		return out
	}

	pass(live)
	if got := verdicts(live, 4); !slices.Equal(got, []bool{false, true, false, false}) {
		t.Fatalf("a pid already gone at record: polls = %v, want one owed pass (held one poll), then unchanged", got)
	}
	// That pass ran and the pid is still listed gone (the host's fact); now a
	// second chat dies while a pass runs.
	live.ClaudeProcesses = append(live.ClaudeProcesses, gather.ClaudeProcess{PID: goneProcessPID(t), PanePID: pane.PID})
	pass(live)
	if got := verdicts(live, 3); !slices.Equal(got, []bool{false, true, false}) {
		t.Fatalf("a newly gone pid beside a permanently gone one: polls = %v, want its own owed pass, then unchanged",
			got)
	}
}

// TestPresenceBaselineBlindBuysAPassOncePerParkThreshold pins the parked poll
// with no baseline (the last pass could not take one): record reports why, so
// the picker can mark the fleet stale, and a full pass is due once per park
// threshold since the last one began — never on every 2 s poll, which would
// turn a persistent error into a multi-second pass every poll.
func TestPresenceBaselineBlindBuysAPassOncePerParkThreshold(t *testing.T) {
	environment := presenceEnv(t)
	if err := os.WriteFile(environment.Paths.SIDDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var baseline presenceBaseline
	baseline.begin(environment, start)
	if err := baseline.record(environment, gather.Snapshot{}); err == nil {
		t.Fatal("an unreadable crumb directory recorded a baseline: could not look read as watched")
	}
	for _, at := range []time.Duration{0, fleetRefreshParkPollInterval, fleetRefreshParkThreshold - time.Millisecond} {
		if moved, err := baseline.moved(environment, gather.Snapshot{}, start.Add(at)); err != nil || moved {
			t.Fatalf("blind poll %s after the pass: moved=%v err=%v, want no pass yet", at, moved, err)
		}
	}
	due := start.Add(fleetRefreshParkThreshold)
	if moved, err := baseline.moved(environment, gather.Snapshot{}, due); err != nil || !moved {
		t.Fatalf("blind poll a park threshold after the pass: moved=%v err=%v, want the pass it is owed", moved, err)
	}
}

// TestRowFactsMovedSeesBusyIdleAndUnreadableFacts pins what a parked re-read of
// the rows' facts publishes: a chat that went busy or idle, or facts that
// stopped being readable, and nothing else — an unchanged re-read sends nothing.
func TestRowFactsMovedSeesBusyIdleAndUnreadableFacts(t *testing.T) {
	idle := ui.Snapshot{Rows: []compose.Row{{Kind: compose.LiveClaude, ID: "a", Socket: "cc-1-1-1"}}}
	same := ui.Snapshot{Rows: []compose.Row{{Kind: compose.LiveClaude, ID: "a", Socket: "cc-1-1-1"}}}
	if rowFactsMoved(idle, same) {
		t.Fatal("an identical re-read must publish nothing")
	}
	busy := ui.Snapshot{Rows: []compose.Row{{Kind: compose.LiveClaude, ID: "a", Socket: "cc-1-1-1", Working: true}}}
	if !rowFactsMoved(idle, busy) {
		t.Fatal("a chat that went busy must publish")
	}
	if !rowFactsMoved(idle, ui.Snapshot{Rows: idle.Rows, FactsError: "1 unreadable"}) {
		t.Fatal("facts that became unreadable must publish: could not look is not nothing to show")
	}
	if !hasLiveRow(idle.Rows) || hasLiveRow([]compose.Row{{Kind: compose.ResumeClaude, ID: "r"}}) {
		t.Fatal("only a live seat has working state that can change under a parked picker")
	}
}

// TestPresenceBaselineHoldsAChangeOnePollSoTwoStepsBuyOnePass pins the held
// poll: a chat's socket and its crumb land a poll apart, and the pass the first
// step would buy waits for the second. A change that reverts before the next
// poll buys nothing, and a held change always buys its pass at the next poll.
func TestPresenceBaselineHoldsAChangeOnePollSoTwoStepsBuyOnePass(t *testing.T) {
	environment := presenceEnv(t)
	if err := os.MkdirAll(environment.Paths.SIDDir, 0o700); err != nil {
		t.Fatalf("make crumb dir: %v", err)
	}
	now := time.Now()
	var baseline presenceBaseline
	baseline.begin(environment, now)
	if err := baseline.record(environment, gather.Snapshot{}); err != nil {
		t.Fatalf("record an empty fleet: %v", err)
	}
	poll := func() bool {
		t.Helper()
		moved, err := baseline.moved(environment, gather.Snapshot{}, now)
		if err != nil {
			t.Fatalf("probe a readable fleet: %v", err)
		}
		return moved
	}
	crumb := func(socket string) string { return filepath.Join(environment.Paths.SIDDir, socket) }
	write := func(socket string) {
		t.Helper()
		if err := os.WriteFile(crumb(socket), nil, 0o600); err != nil {
			t.Fatalf("write crumb %s: %v", socket, err)
		}
	}

	if poll() {
		t.Fatal("an unchanged fleet bought a pass")
	}
	write("cc-1800000041-1-1")
	if poll() {
		t.Fatal("the first step of a change bought its pass at once: the second step would buy another")
	}
	if err := os.Remove(crumb("cc-1800000041-1-1")); err != nil {
		t.Fatalf("remove crumb: %v", err)
	}
	if poll() {
		t.Fatal("a change that reverted before the next poll bought a pass")
	}
	write("cc-1800000041-1-1")
	if poll() {
		t.Fatal("a reverted change left its hold behind: the next change was not held")
	}
	write("cc-1800000042-1-1")
	if !poll() {
		t.Fatal("two steps a poll apart bought no pass at the second poll")
	}
	baseline.begin(environment, now)
	if err := baseline.record(environment, gather.Snapshot{}); err != nil {
		t.Fatalf("record after the pass: %v", err)
	}
	if poll() {
		t.Fatal("the pass left the fleet changed")
	}
}
