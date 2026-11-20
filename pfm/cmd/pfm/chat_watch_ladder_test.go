//go:build jail

package main

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
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// ladderRoundLimit bounds one poll round: a round that has not finished by
// then is a hung watch, which is the failure a frozen tmux server causes.
const ladderRoundLimit = 20 * time.Second

// newLadderTime is a ladder's time: a fake clock every watch of the ladder
// polls on and every transcript of it is pinned to, so a second a watch prints
// is true by construction, never the wall clock's. It starts on a whole minute
// just behind the wall clock, so each poll timer is due a whole poll later and
// the transcripts stay inside the fleet scan's recency caps, which are real.
func newLadderTime() *clock.Fake { return clock.NewFake(time.Unix(time.Now().Unix()/60*60, 0)) }

// ladderClock is one watch's poll clock: Now reads the ladder's time, and a
// poll timer fires only when the test fires it (poll). Sleep, After and
// tickers stay real: they time waits on other processes, never an instant.
type ladderClock struct {
	fake  *clock.Fake
	armed chan ladderTimer
}

func newLadderClock(fake *clock.Fake) *ladderClock {
	return &ladderClock{fake: fake, armed: make(chan ladderTimer, 1)}
}

func (ladder *ladderClock) Now() time.Time { return ladder.fake.Now() }

func (*ladderClock) Sleep(ctx context.Context, duration time.Duration) error {
	return clock.Real.Sleep(ctx, duration)
}

func (*ladderClock) After(duration time.Duration) <-chan time.Time { return clock.Real.After(duration) }

func (*ladderClock) NewTicker(duration time.Duration) clock.Ticker {
	return clock.Real.NewTicker(duration)
}

func (ladder *ladderClock) NewTimer(duration time.Duration) clock.Timer {
	timer := ladderTimer{fire: make(chan time.Time, 1), due: ladder.fake.Now().Add(duration)}
	ladder.armed <- timer
	return timer
}

// ladderTimer is one armed poll timer: the instant it is due on the ladder's
// time, and the channel poll fires it on.
type ladderTimer struct {
	fire chan time.Time
	due  time.Time
}

func (timer ladderTimer) C() <-chan time.Time { return timer.fire }
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

// watchLadder is one run jail holding mock Claude seats, on its own time.
type watchLadder struct {
	t         *testing.T
	jail      *runJail
	clock     *clock.Fake
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
	stamp := ladder.clock.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
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
	seat.pin()
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

// pin sets every transcript in the seat's project directory to the ladder's
// time, so the idle and quiet seconds a watch prints count the ladder's time
// since the last write. Each wait for a write pins once the write is on disk.
func (seat *ladderSeat) pin() {
	seat.ladder.t.Helper()
	project := filepath.Dir(seat.transcript)
	transcripts, err := filepath.Glob(filepath.Join(project, "*.jsonl"))
	if err != nil || len(transcripts) == 0 {
		seat.ladder.t.Fatalf("pin seat %s: no transcript in %s (%v)", seat.name, project, err)
	}
	now := seat.ladder.clock.Now()
	for _, path := range transcripts {
		if err := os.Chtimes(path, now, now); err != nil {
			seat.ladder.t.Fatalf("pin %s to the ladder's time: %v", path, err)
		}
	}
}

// waitTranscript waits until the seat's current transcript holds want.
func (seat *ladderSeat) waitTranscript(want string) {
	seat.ladder.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		content, _ := os.ReadFile(seat.transcript)
		if strings.Contains(string(content), want) {
			seat.pin()
			return
		}
		if time.Now().After(deadline) {
			seat.ladder.t.Fatalf("seat %s transcript never held %q", seat.name, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// waitProject waits until a transcript in the seat's project directory other
// than skip holds want: a reply that may land in a /clear successor, or the
// successor's title.
func (seat *ladderSeat) waitProject(want, skip string) {
	seat.ladder.t.Helper()
	project := filepath.Dir(seat.transcript)
	deadline := time.Now().Add(15 * time.Second)
	for {
		matches, _ := filepath.Glob(filepath.Join(project, "*.jsonl"))
		for _, path := range matches {
			if content, err := os.ReadFile(path); err == nil && path != skip &&
				strings.Contains(string(content), want) {
				seat.pin()
				return
			}
		}
		if time.Now().After(deadline) {
			seat.ladder.t.Fatalf("no transcript in %s besides %q ever held %q", project, skip, want)
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
	timer  ladderTimer
	read   int
	code   int
	ended  bool
}

// watch starts `pfm chat watch args...` and runs its first poll.
func (ladder *watchLadder) watch(args ...string) *ladderWatch {
	ladder.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	watch := &ladderWatch{
		t: ladder.t, clock: newLadderClock(ladder.clock), stdout: &lockedBuffer{}, stderr: &lockedBuffer{},
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
	case watch.timer = <-watch.clock.armed:
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

// poll runs exactly one more poll round: the ladder's time moves on to the
// instant the armed timer is due, as the wall clock would, and it fires.
func (watch *ladderWatch) poll() {
	watch.t.Helper()
	if watch.ended {
		watch.t.Fatalf("poll after the watch returned rc %d: stdout %q", watch.code, watch.stdout.String())
	}
	if gap := watch.timer.due.Sub(watch.clock.fake.Now()); gap > 0 {
		watch.clock.fake.Advance(gap)
	}
	watch.timer.fire <- watch.clock.fake.Now()
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

// expect checks the event lines written since the last check are exactly
// want, in order.
func (watch *ladderWatch) expect(want ...string) {
	watch.t.Helper()
	all := strings.Split(strings.TrimSuffix(watch.stdout.String(), "\n"), "\n")
	if len(all) == 1 && all[0] == "" {
		all = nil
	}
	got := all[watch.read:]
	watch.read = len(all)
	if !slices.Equal(got, want) {
		watch.t.Fatalf("event lines = %q, want %q (stderr %q)", got, want, watch.stderr.String())
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
