package main

// The watch fault ladder: `pfm chat watch` against real mock-engine Claude
// seats on real tmux servers in a run jail. Each rung changes real seats
// between two polls and then runs exactly one more poll, so "the event
// arrives within one poll" is what every assertion checks.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// ladderRoundLimit bounds one poll round: a round that has not finished by
// then is a hung watch, which is the failure a frozen tmux server causes.
const ladderRoundLimit = 20 * time.Second

var (
	ladderMockOnce sync.Once
	ladderMock     string
	ladderMockErr  error
)

// ladderClock is the ladder's poll clock: Now is the real clock, and a poll
// timer fires only when the test fires it.
type ladderClock struct{ armed chan chan time.Time }

func newLadderClock() *ladderClock { return &ladderClock{armed: make(chan chan time.Time, 1)} }

func (*ladderClock) Now() time.Time { return time.Now() }

func (*ladderClock) Sleep(ctx context.Context, duration time.Duration) error {
	return clock.Real.Sleep(ctx, duration)
}

func (*ladderClock) After(duration time.Duration) <-chan time.Time { return clock.Real.After(duration) }

func (*ladderClock) NewTicker(duration time.Duration) clock.Ticker {
	return clock.Real.NewTicker(duration)
}

func (ladder *ladderClock) NewTimer(time.Duration) clock.Timer {
	fire := make(chan time.Time, 1)
	ladder.armed <- fire
	return ladderTimer(fire)
}

type ladderTimer chan time.Time

func (timer ladderTimer) C() <-chan time.Time { return timer }
func (ladderTimer) Stop() bool                { return true }
func (ladderTimer) Reset(time.Duration) bool  { return true }

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (locked *lockedBuffer) Write(data []byte) (int, error) {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.buffer.Write(data)
}

func (locked *lockedBuffer) String() string {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.buffer.String()
}

// watchLadder is one run jail holding mock Claude seats.
type watchLadder struct {
	t         *testing.T
	jail      *runJail
	configDir string
	scenario  string
	seats     int
	replies   int
}

type ladderSeat struct {
	ladder     *watchLadder
	name       string
	socket     string
	sid        string
	cwd        string
	transcript string
	pid        int // the seat's tmux server
	enginePID  int // the mock engine running in its pane
}

func newWatchLadder(t *testing.T) *watchLadder {
	t.Helper()
	jail := newRunJail(t)
	ladderMockOnce.Do(func() {
		dir := testBinaryDir
		if dir == "" {
			dir, ladderMockErr = os.MkdirTemp("", "pfm-ladder-mock-")
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

// start brings up one mock Claude seat titled name and waits until the fleet
// lists it live.
func (ladder *watchLadder) start(name string) *ladderSeat {
	t := ladder.t
	t.Helper()
	ladder.seats++
	seat := &ladderSeat{
		ladder: ladder,
		name:   name,
		// A spawn-shaped socket name, so the seat's crumbs are honored and
		// the fleet finds it the way it finds a chat pfm started.
		socket: fmt.Sprintf("cc-1700000000-%d-1", ladder.seats),
		sid:    fmt.Sprintf("c1a00000-0000-4000-8000-%012d", ladder.seats),
	}
	seat.cwd = filepath.Join(ladder.jail.root, "work", seat.socket)
	project := filepath.Join(ladder.configDir, "projects", strings.NewReplacer("/", "-", ".", "-").Replace(seat.cwd))
	for _, directory := range []string{seat.cwd, project} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	seat.transcript = filepath.Join(project, seat.sid+".jsonl")
	stamp := time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	records := []map[string]any{
		{"type": "custom-title", "customTitle": name, "sessionId": seat.sid},
		{
			"type": "user", "uuid": seat.sid[:8] + "-u", "sessionId": seat.sid, "timestamp": stamp,
			"cwd": seat.cwd, "isSidechain": false, "message": map[string]any{"role": "user", "content": "warm up"},
		},
		{
			"type": "assistant", "uuid": seat.sid[:8] + "-a", "sessionId": seat.sid, "timestamp": stamp,
			"cwd": seat.cwd, "isSidechain": false, "message": map[string]any{
				"role": "assistant", "model": "claude-fixture",
				"content": []map[string]string{{"type": "text", "text": "ready"}},
				"usage":   map[string]int{"input_tokens": 10, "output_tokens": 10},
			},
		},
	}
	var body bytes.Buffer
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		body.Write(append(line, '\n'))
	}
	if err := os.WriteFile(seat.transcript, body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		"tmux", "-S", seat.socketPath(), "-f", os.DevNull, "new-session", "-d", "-s", "main",
		"-x", "160", "-y", "48", "-c", seat.cwd, "exec claude --resume "+seat.sid,
	)
	command.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+ladder.configDir, "MOCK_ENGINE_SCENARIO="+ladder.scenario)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start seat %s: %v: %s", name, err, output)
	}
	pid, err := seat.tmux("display-message", "-p", "#{pid}")
	if err != nil {
		t.Fatalf("seat %s server pid: %v", name, err)
	}
	seat.pid, err = strconv.Atoi(strings.TrimSpace(pid))
	if err != nil {
		t.Fatalf("seat %s server pid %q: %v", name, pid, err)
	}
	engine, err := seat.tmux("display-message", "-p", "-t", "main", "#{pane_pid}")
	if err != nil {
		t.Fatalf("seat %s engine pid: %v", name, err)
	}
	seat.enginePID, err = strconv.Atoi(strings.TrimSpace(engine))
	if err != nil {
		t.Fatalf("seat %s engine pid %q: %v", name, engine, err)
	}
	seat.waitPane("❯")
	ladder.waitRow(name, seat.socket, true)
	return seat
}

// waitRow waits until the fleet lists (or no longer lists) a live seat titled
// name on socket.
func (ladder *watchLadder) waitRow(name, socket string, live bool) {
	ladder.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		rows, err := pfmchat.Rows(context.Background(), io.Discard, nil)
		found := false
		for index := range rows {
			row := pfmchat.FromRow(rows[index])
			if row.Live && row.Name == name && row.Socket == socket {
				found = true
			}
		}
		if err == nil && found == live {
			return
		}
		if time.Now().After(deadline) {
			var seen []string
			for index := range rows {
				row := pfmchat.FromRow(rows[index])
				seen = append(
					seen,
					fmt.Sprintf("%s@%s/%s live=%v id=%s", row.Name, row.Socket, row.Pane, row.Live, row.ID),
				)
			}
			crumbs, _ := os.ReadDir(filepath.Join(ladder.jail.root, "sid"))
			var crumbNames []string
			for _, crumb := range crumbs {
				crumbNames = append(crumbNames, crumb.Name())
			}
			var warnings bytes.Buffer
			_, _ = pfmchat.Rows(context.Background(), &warnings, nil)
			probe := exec.Command("tmux", "-L", socket, "list-panes", "-a")
			probe.Env = append(os.Environ(), "TMUX=")
			listed, probeErr := probe.CombinedOutput()
			sockets, _ := os.ReadDir(ladder.jail.tmuxDir)
			for _, entry := range sockets {
				info, _ := entry.Info()
				fmt.Fprintf(&warnings, "; socket %s mode %v", entry.Name(), info.Mode())
			}
			fmt.Fprintf(&warnings, "; list-panes -L %s: %q err %v; TMUX_TMPDIR=%s PFM_TMUX_DIR=%s",
				socket, listed, probeErr, os.Getenv("TMUX_TMPDIR"), os.Getenv("PFM_TMUX_DIR"))
			ladder.t.Fatalf("fleet never listed %s on %s live=%v (err %v); rows %q; crumbs %q; scan warnings %q",
				name, socket, live, err, seen, crumbNames, warnings.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (seat *ladderSeat) socketPath() string {
	return filepath.Join(seat.ladder.jail.tmuxDir, seat.socket)
}

func (seat *ladderSeat) tmux(arguments ...string) (string, error) {
	output, err := exec.Command("tmux", append([]string{"-S", seat.socketPath()}, arguments...)...).CombinedOutput()
	return string(output), err
}

func (seat *ladderSeat) waitPane(want string) {
	seat.ladder.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		frame, _ := seat.tmux("capture-pane", "-p", "-t", "main")
		if strings.Contains(frame, want) {
			return
		}
		if time.Now().After(deadline) {
			seat.ladder.t.Fatalf("seat %s pane never showed %q: %q", seat.name, want, frame)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// waitTranscript waits until the seat's current transcript holds want.
func (seat *ladderSeat) waitTranscript(want string) {
	seat.ladder.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		content, _ := os.ReadFile(seat.transcript)
		if strings.Contains(string(content), want) {
			return
		}
		if time.Now().After(deadline) {
			seat.ladder.t.Fatalf("seat %s transcript never held %q", seat.name, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (seat *ladderSeat) typeLine(line string) {
	seat.ladder.t.Helper()
	if output, err := seat.tmux("send-keys", "-t", "main", "-l", line); err != nil {
		seat.ladder.t.Fatalf("type into %s: %v: %s", seat.name, err, output)
	}
	if output, err := seat.tmux("send-keys", "-t", "main", "Enter"); err != nil {
		seat.ladder.t.Fatalf("press Enter in %s: %v: %s", seat.name, err, output)
	}
}

// answered is the transcript text of the assistant's answer reply: the
// typed prompt names the reply too, so only this marks the turn as ended.
func answered(reply string) string { return `"text":"` + reply + `"` }

// turn plays one whole turn and waits until its answer is on disk.
func (seat *ladderSeat) turn() {
	seat.ladder.t.Helper()
	seat.ladder.replies++
	reply := fmt.Sprintf("reply-%d", seat.ladder.replies)
	seat.typeLine(`mock-engine: [{"type":"turn","reply":"` + reply + `","busy_ms":0}]`)
	seat.waitTranscript(answered(reply))
}

// hold starts a turn that stays open until release, and waits until the
// prompt is on disk.
func (seat *ladderSeat) hold() func() {
	seat.ladder.t.Helper()
	seat.ladder.replies++
	reply := fmt.Sprintf("reply-%d", seat.ladder.replies)
	flag := filepath.Join(seat.ladder.jail.root, "holds", reply)
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		seat.ladder.t.Fatal(err)
	}
	seat.typeLine(`mock-engine: [{"type":"hold","until_gone":"` + flag + `"},{"type":"turn","reply":"` + reply +
		`","busy_ms":0}]`)
	seat.waitTranscript(flag)
	return func() {
		seat.ladder.t.Helper()
		if err := os.Remove(flag); err != nil {
			seat.ladder.t.Fatal(err)
		}
		seat.waitTranscript(answered(reply))
	}
}

// exit quits the engine; its server ends with its only pane.
func (seat *ladderSeat) exit() {
	seat.ladder.t.Helper()
	seat.typeLine("/exit")
	seat.waitGone()
}

func (seat *ladderSeat) waitGone() {
	seat.ladder.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for syscall.Kill(seat.pid, 0) == nil {
		if time.Now().After(deadline) {
			seat.ladder.t.Fatalf("seat %s server %d never ended", seat.name, seat.pid)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// processArgs is pid's full command line as the kernel's process table holds
// it, which a signal checks before it is sent.
func processArgs(pid int) (string, error) {
	args, err := exec.Command("ps", "-ww", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", fmt.Errorf("ps -p %d: %w", pid, err)
	}
	return strings.TrimSpace(string(args)), nil
}

// signalServer sends sig to the seat's own tmux server, after checking the pid
// is still that server.
func (seat *ladderSeat) signalServer(sig syscall.Signal) {
	seat.ladder.t.Helper()
	args, err := processArgs(seat.pid)
	if err != nil || !strings.Contains(args, seat.socketPath()) {
		seat.ladder.t.Fatalf("pid %d is not seat %s's server (args %q, err %v)", seat.pid, seat.name, args, err)
	}
	if err := syscall.Kill(seat.pid, sig); err != nil {
		seat.ladder.t.Fatalf("signal %v to %s's server: %v", sig, seat.name, err)
	}
}

// killEngine kills the mock engine in the seat's pane, after checking the pid
// is still that engine.
func (seat *ladderSeat) killEngine() {
	seat.ladder.t.Helper()
	args, err := processArgs(seat.enginePID)
	if err != nil || !strings.Contains(args, seat.sid) {
		seat.ladder.t.Fatalf("pid %d is not seat %s's engine (args %q, err %v)", seat.enginePID, seat.name, args, err)
	}
	if err := syscall.Kill(seat.enginePID, syscall.SIGKILL); err != nil {
		seat.ladder.t.Fatalf("kill %s's engine: %v", seat.name, err)
	}
	// A killed mock cannot withdraw its jail /proc entry; the kernel would
	// have dropped the real one, so the test does.
	if err := os.RemoveAll(filepath.Join(seat.ladder.jail.root, "proc", strconv.Itoa(seat.enginePID))); err != nil {
		seat.ladder.t.Fatal(err)
	}
}

// ladderWatch is one `pfm chat watch` running against the jail.
type ladderWatch struct {
	t      *testing.T
	clock  *ladderClock
	stdout *lockedBuffer
	stderr *lockedBuffer
	cancel context.CancelFunc
	done   chan int
	fire   chan time.Time
	read   int
	code   int
	ended  bool
}

// watch starts `pfm chat watch args...` and runs its first poll.
func (ladder *watchLadder) watch(args ...string) *ladderWatch {
	ladder.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	watch := &ladderWatch{
		t: ladder.t, clock: newLadderClock(), stdout: &lockedBuffer{}, stderr: &lockedBuffer{},
		cancel: cancel, done: make(chan int, 1),
	}
	go func() {
		watch.done <- watchCommand(ctx, watch.clock, args, watch.stdout, watch.stderr, deps.RealRunner{})
	}()
	ladder.t.Cleanup(watch.stop)
	watch.settle()
	return watch
}

// settle waits until the round in flight is over: the next poll armed, or the
// watch returned.
func (watch *ladderWatch) settle() {
	watch.t.Helper()
	select {
	case watch.fire = <-watch.clock.armed:
	case watch.code = <-watch.done:
		watch.ended = true
	case <-time.After(ladderRoundLimit):
		watch.t.Fatalf("a poll round did not finish within %s: stdout %q stderr %q\nwatch goroutines:\n%s",
			ladderRoundLimit, watch.stdout.String(), watch.stderr.String(), pfmFrames())
	}
}

// pfmFrames is every goroutine stack that passes through pfm code: where a hung
// round is blocked.
func pfmFrames() string {
	buffer := make([]byte, 1<<22)
	stacks := strings.Split(string(buffer[:runtime.Stack(buffer, true)]), "\n\n")
	var kept []string
	for _, stack := range stacks {
		if strings.Contains(stack, "pfm/internal/") && !strings.Contains(stack, "chat_watch_ladder_test.go") {
			kept = append(kept, stack)
		}
	}
	return strings.Join(kept, "\n\n")
}

// poll runs exactly one more poll round.
func (watch *ladderWatch) poll() {
	watch.t.Helper()
	if watch.ended {
		watch.t.Fatalf("poll after the watch returned rc %d: stdout %q", watch.code, watch.stdout.String())
	}
	watch.fire <- time.Now()
	watch.settle()
}

func (watch *ladderWatch) stop() {
	if watch.ended {
		return
	}
	watch.cancel()
	select {
	case watch.code = <-watch.done:
		watch.ended = true
	case <-time.After(10 * time.Second):
		watch.t.Errorf("watch did not return within 10s of its context ending")
	}
}

// expect checks the event lines written since the last check: one per want,
// in order, each starting with its want.
func (watch *ladderWatch) expect(want ...string) {
	watch.t.Helper()
	all := strings.Split(strings.TrimSuffix(watch.stdout.String(), "\n"), "\n")
	if len(all) == 1 && all[0] == "" {
		all = nil
	}
	got := all[watch.read:]
	watch.read = len(all)
	match := len(got) == len(want)
	for index := 0; match && index < len(want); index++ {
		match = strings.HasPrefix(got[index], want[index])
	}
	if !match {
		watch.t.Fatalf("event lines = %q, want lines starting %q (stderr %q)", got, want, watch.stderr.String())
	}
}

// expectEnded checks the watch returned with rc.
func (watch *ladderWatch) expectEnded(code int) {
	watch.t.Helper()
	if !watch.ended || watch.code != code {
		watch.t.Fatalf("watch ended=%v rc=%d, want it returned with rc %d (stderr %q)",
			watch.ended, watch.code, code, watch.stderr.String())
	}
}

// Rungs: a turn ending mid-poll, idle then busy again, death.
func TestWatchLadderFollowsOneSeatThroughTurnsToItsExit(t *testing.T) {
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
		seat.signalServer(syscall.SIGSTOP)
		t.Cleanup(func() { seat.signalServer(syscall.SIGCONT) })
		// The engine outlives its frozen server: the turn it ends is still
		// read from its transcript and announced.
		release()
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
		if !strings.Contains(got, "ERROR lad-a ") || strings.Contains(got, "EXIT lad-a") ||
			!strings.Contains(got, "IDLE lad-b") {
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
	if watch.code == 0 || !strings.Contains(watch.stderr.String(), context.Canceled.Error()) {
		t.Fatalf("cancelled watch rc %d stderr %q, want a non-zero rc naming the cancellation",
			watch.code, watch.stderr.String())
	}
}
