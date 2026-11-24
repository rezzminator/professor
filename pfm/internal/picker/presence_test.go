package picker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testwait"
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

// parkedPresence is a running refresh stream and the fake clock it waits on.
type parkedPresence struct {
	updates <-chan ui.Snapshot
	clock   *parkBoundaryClock
	stderr  *bytes.Buffer
}

// parkedQuietPolls is how many consecutive finished park polls that publish
// nothing make a parked stream quiet.
const parkedQuietPolls = 6

// startParkedStream runs the real refresh loop on a fake clock and returns
// once it is parked: the two passes the transition into park allows have
// published. dependencies carries the test's own indexer; the stream's clock
// and activity clock are the fake's.
func startParkedStream(
	t *testing.T,
	ctx context.Context,
	database *store.Store,
	warn func(*bytes.Buffer) fleet.Warn,
	dependencies refreshDependencies,
) *parkedPresence {
	t.Helper()
	epoch := time.Unix(1_800_000_000, 0)
	updates := make(chan ui.Snapshot, 8)
	stream := &parkedPresence{
		updates: updates,
		clock:   &parkBoundaryClock{Fake: clock.NewFake(epoch), boundary: make(chan struct{}, 64)},
		stderr:  &bytes.Buffer{},
	}
	dependencies.activity = ui.NewActivityClock(epoch)
	dependencies.clock = stream.clock
	t.Cleanup(func() {
		for range updates {
		}
	})
	go streamFleetRefreshesWith(ctx, database, scanRequest{}, warn(stream.stderr), stream.stderr, updates, dependencies)
	completed := 0
	stream.await(t, "the stream to park", func(snapshot ui.Snapshot) {
		if !snapshot.Refreshing {
			completed++
		}
	}, func() bool { return completed >= 2 })
	return stream
}

// await receives the stream's snapshots, each handed to seen, until done
// reports true. A stream that waits only on its own timers has each one it
// arms pass at once on the fake clock when nothing is waiting to be read:
// every wait on it is the stream's pause between polls, and what the test
// waits for is there already or made by the stream itself.
func (stream *parkedPresence) await(t *testing.T, what string, seen func(ui.Snapshot), done func() bool) {
	t.Helper()
	testwait.Until(t, what, func() (bool, string) {
		for {
			select {
			case snapshot, ok := <-stream.updates:
				if !ok {
					t.Fatalf("refresh stream closed while waiting for %s: %s", what, stream.stderr.String())
				}
				seen(snapshot)
				continue
			case <-stream.clock.boundary:
				continue
			default:
			}
			break
		}
		if done() {
			return true, ""
		}
		if step, due := stream.clock.NextDue(); due {
			stream.clock.Advance(step)
		}
		return false, stream.clock.String()
	})
}

// parkedPresenceStream runs the real refresh loop until it is parked and has
// gone quiet. It returns the stream.
func parkedPresenceStream(t *testing.T) *parkedPresence {
	t.Helper()
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	ctx, cancel := context.WithCancel(testwait.Context(t))
	t.Cleanup(cancel)
	stream := startParkedStream(t, ctx, database, func(stderr *bytes.Buffer) fleet.Warn {
		return fleet.PrintWarn(stderr)
	}, refreshDependencies{
		newIndexer: func(*store.Store) (indexRunner, error) { return &immediateIndexRunner{}, nil },
	})
	stream.settle(t)
	return stream
}

// settle runs park polls until parkedQuietPolls in a row publish nothing: a
// parked fleet whose pane pids the jail's proc root cannot see owes exactly
// one fresh pass before it is quiet, and a test must start from the quiet. A
// poll is over when the stream is back at its select (one boundary).
func (stream *parkedPresence) settle(t *testing.T) {
	t.Helper()
	for quiet := 0; quiet < parkedQuietPolls; {
		stream.clock.Advance(fleetRefreshParkPollInterval)
		published := false
		testwait.Until(t, "a parked poll to finish", func() (bool, string) {
			select {
			case _, ok := <-stream.updates:
				if !ok {
					t.Fatalf("refresh stream closed while settling: %s", stream.stderr.String())
				}
				published = true
				return false, "publishing"
			case <-stream.clock.boundary:
				return true, ""
			default:
				return false, stream.clock.String()
			}
		})
		for len(stream.updates) > 0 {
			<-stream.updates
			published = true
		}
		if published {
			quiet = 0
		} else {
			quiet++
		}
	}
}

// awaitFullPass waits for a snapshot of a completed pass, advancing the
// stream's park polls one at a time: the change the test made before the call
// is already there for the poll to find.
func (stream *parkedPresence) awaitFullPass(t *testing.T, why string) ui.Snapshot {
	t.Helper()
	var found ui.Snapshot
	got := false
	stream.await(t, "a completed pass while parked and "+why, func(snapshot ui.Snapshot) {
		if !snapshot.Refreshing && snapshot.RefreshFailure == nil {
			found, got = snapshot, true
		}
	}, func() bool { return got })
	return found
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

// goneProcessPID returns the nth pid no process holds: above every kernel's pid limit (Linux caps at 2^22,
// macOS at 99999), so no child is needed to produce one.
func goneProcessPID(n int) int {
	return 1<<30 + n
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
		ClaudeProcesses: []gather.ClaudeProcess{{PID: goneProcessPID(0), PanePID: pane.PID}},
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
	live.ClaudeProcesses = append(live.ClaudeProcesses, gather.ClaudeProcess{PID: goneProcessPID(1), PanePID: pane.PID})
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
