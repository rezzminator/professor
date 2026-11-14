//go:build jail

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

var (
	ladderMockOnce sync.Once
	ladderMock     string
	ladderMockErr  error
)

func newWatchLadder(t *testing.T) *watchLadder {
	t.Helper()
	jail := newRunJail(t)
	ladderMockOnce.Do(func() {
		dir := testBinaryDir
		if dir == "" {
			dir, ladderMockErr = os.MkdirTemp(paths.TempRoot(), "pfm-ladder-mock-")
			if ladderMockErr != nil {
				return
			}
		}
		ladderMock, ladderMockErr = testjail.MockEngineBinary(filepath.Join("..", ".."), dir)
	})
	if ladderMockErr != nil {
		t.Fatalf("build mock-engine: %v", ladderMockErr)
	}
	claude := filepath.Join(jail.binDir, "claude")
	if err := os.Remove(claude); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ladderMock, claude); err != nil {
		t.Fatal(err)
	}
	ladder := &watchLadder{
		t:         t,
		jail:      jail,
		configDir: filepath.Join(jail.root, "cc"),
		scenario:  filepath.Join(jail.root, "scenario.json"),
	}
	for _, directory := range []string{filepath.Join(ladder.configDir, "projects"), filepath.Join(jail.root, "holds")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	scenario, err := json.Marshal(map[string]any{
		"jail": map[string]string{
			"sid_dir":   filepath.Join(jail.root, "sid"),
			"proc_root": filepath.Join(jail.root, "proc"),
		},
		"model": "claude-fixture",
		"pane":  map[string]string{"busy": "Working (1s · esc to interrupt) · esc interrupt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ladder.scenario, scenario, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(ladder.configDir, "projects"))
	t.Setenv("PFM_TMUX_CONF", os.DevNull)
	t.Cleanup(func() { jail.killSockets(t) })
	return ladder
}

// Rungs: a turn ending mid-poll, idle then busy again, death.
func TestWatchLadderFollowsOneSeatThroughTurnsToItsExit(t *testing.T) {
	testjail.TmuxServer(t)
	ladder := newWatchLadder(t)
	seat := ladder.start("lad-a")
	watch := ladder.watch("lad-a", "--transitions")
	watch.expect("SEEN lad-a idle idle_seconds=")
	release := seat.hold()
	watch.poll()
	watch.expect("WORKING lad-a")
	release()
	watch.poll()
	watch.expect("IDLE lad-a idle_seconds=")
	seat.turn() // begins and ends between two polls
	watch.poll()
	watch.expect("WORKING lad-a", "IDLE lad-a idle_seconds=")
	watch.poll()
	watch.expect()
	seat.exit()
	watch.poll()
	watch.expect("EXIT lad-a")
	watch.expectEnded(codeDeadChat)
}

// Rung: --once announces the first idle and stops.
func TestWatchLadderOnceAnnouncesTheFirstIdleAndStops(t *testing.T) {
	testjail.TmuxServer(t)
	ladder := newWatchLadder(t)
	seat := ladder.start("lad-a")
	release := seat.hold()
	watch := ladder.watch("lad-a", "--once")
	watch.expect()
	release()
	watch.poll()
	watch.expect("IDLE lad-a idle_seconds=")
	watch.expectEnded(0)
}

// Rung: compaction is a turn, never an end.
func TestWatchLadderCompactionIsNotAnEnd(t *testing.T) {
	testjail.TmuxServer(t)
	ladder := newWatchLadder(t)
	seat := ladder.start("lad-a")
	watch := ladder.watch("lad-a", "--transitions")
	watch.expect("SEEN lad-a idle")
	seat.typeLine("/compact")
	seat.waitTranscript("compact_boundary")
	watch.poll()
	lines := strings.Fields(watch.stdout.String())
	for _, word := range []string{"EXIT", "DEAD", "ERROR"} {
		for _, got := range lines {
			if got == word {
				t.Fatalf("compaction ended the watch: %q", watch.stdout.String())
			}
		}
	}
	watch.read = len(strings.Split(strings.TrimSuffix(watch.stdout.String(), "\n"), "\n"))
	seat.turn()
	watch.poll()
	watch.expect("WORKING lad-a", "IDLE lad-a idle_seconds=")
}

// Rung: the tmux socket vanishing. A server killed with it is the seat's
// EXIT, once. A socket file unlinked under a running server leaves the engine
// working: the watch keeps following its transcript, and the EXIT comes when
// the engine ends.
func TestWatchLadderASocketThatVanishes(t *testing.T) {
	testjail.TmuxServer(t)
	t.Run("kill-server", func(t *testing.T) {
		ladder := newWatchLadder(t)
		seat := ladder.start("lad-a")
		watch := ladder.watch("lad-a", "--transitions")
		watch.expect("SEEN lad-a idle")
		if output, err := seat.tmux("kill-server"); err != nil {
			t.Fatalf("kill-server: %v: %s", err, output)
		}
		seat.waitGone()
		watch.poll()
		watch.expect("EXIT lad-a")
		watch.expectEnded(codeDeadChat)
	})
	t.Run("unlink", func(t *testing.T) {
		ladder := newWatchLadder(t)
		seat := ladder.start("lad-a")
		watch := ladder.watch("lad-a", "--transitions")
		watch.expect("SEEN lad-a idle")
		release := seat.hold()
		watch.poll()
		watch.expect("WORKING lad-a")
		if err := os.Remove(seat.socketPath()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if syscall.Kill(seat.pid, 0) == nil {
				seat.signalServer(syscall.SIGTERM)
			}
		})
		watch.poll()
		watch.expect()
		release()
		watch.poll()
		watch.expect("IDLE lad-a idle_seconds=")
		seat.signalServer(syscall.SIGTERM)
		seat.waitGone()
		watch.poll()
		watch.expect("EXIT lad-a")
		watch.expectEnded(codeDeadChat)
	})
}

// Rung: a rename. A named watch follows the seat it resolved, so a rename
// never ends it and its turns are still announced; a glob follows names, so
// the old name leaves (DEAD) and the new one is adopted (SEEN).
func TestWatchLadderARenameKeepsTheNamedWatchAndMovesTheGlob(t *testing.T) {
	testjail.TmuxServer(t)
	ladder := newWatchLadder(t)
	seat := ladder.start("lad-a")
	named := ladder.watch("lad-a", "--transitions")
	glob := ladder.watch("lad-*", "--transitions")
	named.expect("SEEN lad-a idle")
	glob.expect("SEEN lad-a idle")
	seat.typeLine("/rename lad-b")
	seat.waitTranscript(`"customTitle":"lad-b"`)
	named.poll()
	named.expect()
	glob.poll()
	glob.expect("DEAD lad-a", "SEEN lad-b idle")
	seat.turn()
	named.poll()
	named.expect("WORKING lad-a", "IDLE lad-a idle_seconds=")
	glob.poll()
	glob.expect("WORKING lad-b", "IDLE lad-b idle_seconds=")
}

// Rung: a respawn under the same name between two polls is the old seat's
// EXIT, never a silent switch to the newcomer.
func TestWatchLadderARespawnUnderTheSameNameIsTheOldSeatsExit(t *testing.T) {
	testjail.TmuxServer(t)
	ladder := newWatchLadder(t)
	first := ladder.start("lad-a")
	named := ladder.watch("lad-a", "--transitions")
	glob := ladder.watch("lad-*", "--transitions")
	named.expect("SEEN lad-a idle")
	glob.expect("SEEN lad-a idle")
	first.exit()
	ladder.start("lad-a")
	named.poll()
	named.expect("EXIT lad-a")
	named.expectEnded(codeDeadChat)
	glob.poll()
	glob.expect("EXIT lad-a", "SEEN lad-a idle")
}

// Rung: two seats with one name. A named watch refuses the ambiguity; a glob
// follows both seats, and each one's events and hooks are its own.
func TestWatchLadderTwoSeatsWithOneName(t *testing.T) {
	testjail.TmuxServer(t)
	ladder := newWatchLadder(t)
	first := ladder.start("lad-a")
	second := ladder.start("lad-a")
	named := ladder.watch("lad-a", "--transitions")
	named.expect("ERROR lad-a ")
	named.expectEnded(1)
	hooks := filepath.Join(ladder.jail.root, "hooks.log")
	hook := `echo "$CC_CHAT_STATE $CC_CHAT_SESSION_ID" >> ` + hooks
	glob := ladder.watch("lad-*", "--transitions", "--on-idle", hook, "--on-exit", hook)
	glob.expect("SEEN lad-a idle", "SEEN lad-a idle")
	release := second.hold()
	glob.poll()
	glob.expect("WORKING lad-a")
	release()
	glob.poll()
	glob.expect("IDLE lad-a idle_seconds=")
	first.exit()
	glob.poll()
	glob.expect("EXIT lad-a")
	logged, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	want := "idle " + second.sid + "\ndead " + first.sid + "\n"
	if string(logged) != want {
		t.Fatalf("hooks ran with %q, want each event's own seat: %q", logged, want)
	}
}

// Rung: a glob adopts a seat born after the watch began.
func TestWatchLadderAGlobAdoptsASeatBornLater(t *testing.T) {
	testjail.TmuxServer(t)
	ladder := newWatchLadder(t)
	ladder.start("lad-a")
	glob := ladder.watch("lad-*", "--transitions")
	glob.expect("SEEN lad-a idle")
	born := ladder.start("lad-b")
	glob.poll()
	glob.expect("SEEN lad-b idle")
	born.turn()
	glob.poll()
	glob.expect("WORKING lad-b", "IDLE lad-b idle_seconds=")
}

// Rung: a transcript rotated (/clear) or truncated never ends the watch, and
// the next turn is still announced.
func TestWatchLadderARotatedOrTruncatedTranscriptKeepsTheWatch(t *testing.T) {
	testjail.TmuxServer(t)
	for _, fault := range []string{"clear", "truncate-to-title", "truncate-mid-record"} {
		t.Run(fault, func(t *testing.T) {
			ladder := newWatchLadder(t)
			seat := ladder.start("lad-a")
			watch := ladder.watch("lad-a", "--transitions")
			watch.expect("SEEN lad-a idle")
			switch fault {
			case "clear":
				seat.typeLine("/clear")
				seat.waitPane("❯")
			case "truncate-to-title":
				content, err := os.ReadFile(seat.transcript)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Truncate(seat.transcript, int64(bytes.IndexByte(content, '\n')+1)); err != nil {
					t.Fatal(err)
				}
			case "truncate-mid-record":
				info, err := os.Stat(seat.transcript)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Truncate(seat.transcript, info.Size()-40); err != nil {
					t.Fatal(err)
				}
			}
			watch.poll()
			for _, line := range strings.Split(watch.stdout.String(), "\n") {
				for _, word := range []string{"EXIT ", "DEAD ", "ERROR "} {
					if strings.HasPrefix(line, word) {
						t.Fatalf(
							"%s ended the watch: %q (stderr %q)",
							fault,
							watch.stdout.String(),
							watch.stderr.String(),
						)
					}
				}
			}
			// The fault itself may read as a turn (the newest answer changed);
			// what must hold is that the next real turn is announced, once.
			before := watch.stdout.String()
			seat.ladder.replies++
			reply := fmt.Sprintf("reply-%d", seat.ladder.replies)
			seat.typeLine(`mock-engine: [{"type":"turn","reply":"` + reply + `","busy_ms":0}]`)
			waitForReply(t, filepath.Dir(seat.transcript), reply)
			watch.poll()
			after := strings.TrimPrefix(watch.stdout.String(), before)
			if strings.Count(after, "IDLE lad-a idle_seconds=") != 1 || !strings.HasPrefix(
				after[strings.LastIndex(strings.TrimSuffix(after, "\n"), "\n")+1:], "IDLE lad-a") {
				t.Fatalf("after %s the next turn was not announced once, last: %q (all %q, stderr %q)",
					fault, after, watch.stdout.String(), watch.stderr.String())
			}
		})
	}
}

// waitForReply waits until any transcript in a project directory holds reply.
func waitForReply(t *testing.T, project, reply string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		matches, _ := filepath.Glob(filepath.Join(project, "*.jsonl"))
		for _, path := range matches {
			if content, err := os.ReadFile(path); err == nil && strings.Contains(string(content), answered(reply)) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no transcript in %s ever held %q", project, reply)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// Rung: a tmux server that stops answering is never read as the chat ending:
// the poll still finishes, nothing is announced while it is unreadable, and a
// server that stays unreadable is an ERROR, never an EXIT.
func TestWatchLadderAFrozenTmuxServerIsNeverAnExit(t *testing.T) {
	testjail.TmuxServer(t)
	t.Run("thaws", func(t *testing.T) {
		ladder := newWatchLadder(t)
		seat := ladder.start("lad-a")
		watch := ladder.watch("lad-a", "--transitions")
		watch.expect("SEEN lad-a idle")
		seat.signalServer(syscall.SIGSTOP)
		thawed := false
		t.Cleanup(func() {
			if !thawed {
				seat.signalServer(syscall.SIGCONT)
			}
		})
		watch.poll()
		watch.expect()
		seat.signalServer(syscall.SIGCONT)
		thawed = true
		watch.poll()
		watch.expect()
		seat.turn()
		watch.poll()
		watch.expect("WORKING lad-a", "IDLE lad-a idle_seconds=")
	})
	t.Run("stays frozen", func(t *testing.T) {
		ladder := newWatchLadder(t)
		seat := ladder.start("lad-a")
		bystander := ladder.start("lad-b")
		watch := ladder.watch("lad-a", "lad-b", "--transitions")
		watch.expect("SEEN lad-a idle", "SEEN lad-b idle")
		release := seat.hold()
		watch.poll()
		watch.expect("WORKING lad-a")
		// The turn ends on disk before the freeze: a frozen server stops
		// draining the pane, so an engine painting into it blocks once the pty
		// buffer fills (at once on macOS). The poll against the frozen server
		// still reads the turn's end from the transcript and announces it.
		release()
		seat.signalServer(syscall.SIGSTOP)
		t.Cleanup(func() { seat.signalServer(syscall.SIGCONT) })
		watch.poll()
		watch.expect("IDLE lad-a idle_seconds=")
		// Now nothing about the seat can be read: no line, then an ERROR —
		// never an EXIT nobody saw — while the bystander is still followed.
		seat.killEngine()
		for poll := 1; poll < headless.UnreadablePolls; poll++ {
			watch.poll()
			watch.expect()
		}
		bystander.turn()
		watch.poll()
		got := watch.stdout.String()
		if got != `SEEN lad-a idle idle_seconds=0
SEEN lad-b idle idle_seconds=0
WORKING lad-a
IDLE lad-a idle_seconds=0
ERROR lad-a tmux server unreadable, so whether the chat lives is unknown: cc-1700000000-1-1: tmux server did not answer within 2s: cc-1700000000-1-1 (3 polls in a row)
WORKING lad-b
IDLE lad-b idle_seconds=2
` {
			t.Fatalf(
				"a server unreadable for %d polls: lines %q, want lad-a's ERROR (never EXIT) and lad-b still followed",
				headless.UnreadablePolls,
				got,
			)
		}
	})
}

// Rung: two watcher processes on one target each hear every event once.
func TestWatchLadderTwoWatchersOnOneTargetBothHearEveryEvent(t *testing.T) {
	testjail.TmuxServer(t)
	ladder := newWatchLadder(t)
	seat := ladder.start("lad-a")
	first := ladder.watch("lad-a", "--transitions")
	second := ladder.watch("lad-a", "--transitions")
	first.expect("SEEN lad-a idle")
	second.expect("SEEN lad-a idle")
	seat.turn()
	first.poll()
	second.poll()
	first.expect("WORKING lad-a", "IDLE lad-a idle_seconds=")
	second.expect("WORKING lad-a", "IDLE lad-a idle_seconds=")
	seat.exit()
	second.poll()
	first.poll()
	first.expect("EXIT lad-a")
	second.expect("EXIT lad-a")
}

// Rung: --quiet-after announces a hung turn once, and its end.
func TestWatchLadderQuietAfterAnnouncesAHungTurnOnce(t *testing.T) {
	testjail.TmuxServer(t)
	ladder := newWatchLadder(t)
	seat := ladder.start("lad-a")
	watch := ladder.watch("lad-a", "--transitions", "--quiet-after", "1")
	watch.expect("SEEN lad-a idle")
	release := seat.hold()
	watch.poll()
	watch.expect("WORKING lad-a")
	time.Sleep(1100 * time.Millisecond)
	watch.poll()
	watch.expect("QUIET lad-a quiet_seconds=")
	watch.poll()
	watch.expect()
	release()
	watch.poll()
	watch.expect("IDLE lad-a idle_seconds=")
}

// Rung: --on-idle and --on-exit run once per event, with the event's seat.
func TestWatchLadderHooksRunOncePerEvent(t *testing.T) {
	testjail.TmuxServer(t)
	ladder := newWatchLadder(t)
	seat := ladder.start("lad-a")
	hooks := filepath.Join(ladder.jail.root, "hooks.log")
	hook := `echo "$CC_CHAT_NAME $CC_CHAT_STATE $CC_CHAT_SESSION_ID" >> ` + hooks
	watch := ladder.watch("lad-a", "--on-idle", hook, "--on-exit", hook)
	watch.expect("IDLE lad-a idle_seconds=")
	watch.poll()
	watch.expect()
	seat.exit()
	watch.poll()
	watch.expect("EXIT lad-a")
	logged, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	want := "lad-a idle " + seat.sid + "\nlad-a dead " + seat.sid + "\n"
	if string(logged) != want {
		t.Fatalf("hooks ran with %q, want %q", logged, want)
	}
}

// Rung: a cancelled context ends the watch at once, with no event.
func TestWatchLadderCancelEndsTheWatchWithoutAnEvent(t *testing.T) {
	testjail.TmuxServer(t)
	ladder := newWatchLadder(t)
	ladder.start("lad-a")
	watch := ladder.watch("lad-*", "--transitions")
	watch.expect("SEEN lad-a idle")
	watch.cancel()
	select {
	case watch.code = <-watch.done:
		watch.ended = true
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not return within 5s of its context ending")
	}
	watch.expect()
	if watch.code == 0 || watch.stderr.String() != "pfm chat watch: context canceled\n" {
		t.Fatalf("cancelled watch rc %d stderr %q, want a non-zero rc naming the cancellation",
			watch.code, watch.stderr.String())
	}
}
