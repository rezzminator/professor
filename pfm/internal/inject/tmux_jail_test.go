package inject

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// injectTmuxJail runs real tmux servers on a scratch TMUX_TMPDIR so no test
// ever reaches a live fleet socket.
type injectTmuxJail struct {
	root    string
	home    string
	tmuxDir string
	sockets []string
}

type verifiedChatTmux struct {
	TmuxInjector
	command string
}

func (tmux verifiedChatTmux) PaneCommand(
	context.Context,
	string,
	string,
) (string, error) {
	return tmux.command, nil
}

func newInjectTmuxJail(t *testing.T) *injectTmuxJail {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	tmuxJailRoot := filepath.Join(os.TempDir(), "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmuxJailRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(tmuxJailRoot, "probe-pfm-inject-")
	if err != nil {
		t.Fatal(err)
	}
	jail := &injectTmuxJail{
		root:    root,
		home:    filepath.Join(root, "home"),
		tmuxDir: filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())),
	}
	for _, directory := range []string{jail.home, jail.tmuxDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", jail.home)
	t.Setenv("TMUX", "")
	// The waiter exports CHAT_INJECT_SOCKET the way chat.sh does; claiming it
	// here restores the caller's value when the test ends.
	t.Setenv("CHAT_INJECT_SOCKET", "")
	t.Setenv("TMUX_TMPDIR", jail.root)
	t.Setenv("TMPDIR", jail.root)
	t.Setenv("PFM_HOME", jail.home)
	t.Setenv("PFM_TMUX_DIR", jail.tmuxDir)
	t.Setenv("PFM_PROC_ROOT", filepath.Join(root, "proc"))
	t.Cleanup(func() {
		for _, socket := range jail.sockets {
			_ = jail.command("-L", socket, "kill-server").Run()
		}
		if err := os.RemoveAll(jail.root); err != nil {
			t.Errorf("remove inject tmux jail: %v", err)
		}
	})
	return jail
}

func (jail *injectTmuxJail) command(arguments ...string) *exec.Cmd {
	command := exec.Command("tmux", arguments...)
	command.Env = append(
		os.Environ(),
		"HOME="+jail.home,
		"TMUX=",
		"TMUX_TMPDIR="+jail.root,
	)
	return command
}

// busyThenIdleUI is a fake chat TUI: it renders the busy spinner detail chat.sh
// keys on, then clears to an idle composer after a delay, and echoes a
// submitted line as "USER:<text>". A line typed while it is still busy is
// echoed as "BUSY-TYPED:", so a premature steer is visible in the capture.
const busyThenIdleUI = `import os, select, sys, time, tty
tty.setraw(0)
delay = float(sys.argv[1])
start = time.time()
sys.stdout.write("Working (2s · 9 tokens)\r\n")
sys.stdout.flush()
idle = False
buf = bytearray()
while True:
    if not idle and time.time() - start >= delay:
        idle = True
        sys.stdout.write("\x1b[2J\x1b[H❯ ")
        sys.stdout.flush()
    ready, _, _ = select.select([0], [], [], 0.05)
    if not ready:
        continue
    ch = os.read(0, 1)
    if not ch:
        break
    if ch == b"\x13":
        if not idle:
            sys.stdout.write("\r\nCONTROL-S-ERROR\r\n")
        buf.clear()
        sys.stdout.write("\r\x1b[2K❯ ")
        sys.stdout.flush()
    elif ch in (b"\r", b"\n"):
        if buf:
            text = bytes(buf).decode("utf-8", "replace")
            tag = "USER:" if idle else "BUSY-TYPED:"
            sys.stdout.write("\r\n" + tag + text + "\r\n❯ ")
            sys.stdout.flush()
            buf.clear()
    elif ch == b"\x1b":
        if not idle:
            sys.stdout.write("\r\nESCAPE-ERROR\r\n")
        buf.clear()
        sys.stdout.write("\r\x1b[2K❯ ")
        sys.stdout.flush()
    else:
        buf.extend(ch)
        sys.stdout.buffer.write(ch)
        sys.stdout.flush()
`

// compactTranscriptUI is a deliberately small TUI probe. It emits output
// while bytes are arriving, but keeps the logical composer in buf and records
// every submitted command to a transcript file. The assertion below therefore
// proves the full focus reached the command handler; a pane glance cannot
// substitute for that proof when the body is wider than the terminal.
const compactTranscriptUI = `import os, select, sys, time, tty
tty.setraw(0)
transcript = sys.argv[1]
buf = bytearray()
event_emitted = False
prompt = "❯ ".encode("utf-8")
os.write(1, b"Working (10s \xc2\xb7 9 tokens)\r\n" + prompt)
while True:
    if not event_emitted and len(buf) >= 256:
        os.write(1, b"\r\nEVENT:busy-output\r\n" + prompt + bytes(buf))
        event_emitted = True
    ready, _, _ = select.select([0], [], [], 0.005)
    if not ready:
        continue
    ch = os.read(0, 1)
    if not ch:
        break
    if ch in (b"\r", b"\n"):
        if buf:
            with open(transcript, "ab") as record:
                record.write(b"FIRED-BEGIN\n")
                record.write(bytes(buf))
                record.write(b"\nFIRED-END\n")
            os.write(1, b"\r\nFIRED\r\nPress up to edit queued messages\r\n" + prompt)
            buf.clear()
        continue
    buf.extend(ch)
    os.write(1, ch)
    time.sleep(0.00005)
`

func (jail *injectTmuxJail) startBusyPane(
	t *testing.T,
	socket, session string,
	busyFor time.Duration,
) string {
	t.Helper()
	script := filepath.Join(jail.root, "ui.py")
	if err := os.WriteFile(script, []byte(busyThenIdleUI), 0o700); err != nil {
		t.Fatal(err)
	}
	command := jail.command(
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		session,
		"-n",
		"steer-window",
		"python3",
		script,
		strconv.FormatFloat(busyFor.Seconds(), 'f', 2, 64),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start jailed pane: %v: %s", err, output)
	}
	jail.sockets = append(jail.sockets, socket)
	output, err := jail.command("-L", socket, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	pane := strings.TrimSpace(string(output))
	// The pane is only busy once its UI has rendered the spinner detail line;
	// asserting before that would measure the fixture's startup, not the guard.
	started := time.Now()
	deadline := started.Add(jailPaneReadyBudget)
	for {
		capture, err := TmuxInjector{}.Capture(
			context.Background(),
			filepath.Join(jail.tmuxDir, socket),
			pane,
			false,
			0,
		)
		if err == nil && IsBusy(capture) {
			return pane
		}
		if time.Now().After(deadline) {
			// The capture error was consulted above and then thrown away here,
			// so a capture that never succeeded reported itself as an empty
			// pane — an error rendering as absence. Say which happened, and
			// for how long, because the recoveries differ: a failing capture
			// is a broken jail, a blank pane is a fixture that never rendered.
			waited := time.Since(started)
			if err != nil {
				t.Fatalf("jailed pane capture kept failing for %s; last error: %v: %q", waited, err, capture)
			}
			t.Fatalf(
				"jailed pane never became busy within %s; capture succeeded throughout and the fixture never rendered a spinner: %q",
				waited,
				capture,
			)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// jailPaneReadyBudget bounds how long a jailed fixture pane may take to render
// its first frame. These fixtures start a python3 interpreter inside tmux, not
// a shell, so readiness pays interpreter startup plus a first render; under
// `go test ./...` on macOS that ran past the previous 10s and the pane was
// still blank when the deadline fired. It is scaffolding readiness, never the
// contract under test — the guards these tests assert on run after the pane is
// ready — and it is only ever paid in full when a fixture genuinely never
// starts.
const jailPaneReadyBudget = 60 * time.Second

func (jail *injectTmuxJail) startCompactTranscriptPane(
	t *testing.T,
	socket, session, transcript string,
) string {
	t.Helper()
	script := filepath.Join(jail.root, "compact-ui.py")
	if err := os.WriteFile(script, []byte(compactTranscriptUI), 0o700); err != nil {
		t.Fatal(err)
	}
	command := jail.command(
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		session,
		"python3",
		script,
		transcript,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start compact transcript pane: %v: %s", err, output)
	}
	jail.sockets = append(jail.sockets, socket)
	output, err := jail.command("-L", socket, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	pane := strings.TrimSpace(string(output))
	started := time.Now()
	deadline := started.Add(jailPaneReadyBudget)
	for {
		capture, captureErr := TmuxInjector{}.Capture(
			context.Background(),
			filepath.Join(jail.tmuxDir, socket),
			pane,
			false,
			0,
		)
		if captureErr == nil && strings.Contains(capture, "Working (10s · 9 tokens)") {
			return pane
		}
		if time.Now().After(deadline) {
			// Same asymmetry as startBusyPane: captureErr gated the success
			// test and then vanished from the report, so a jail that could not
			// be read at all looked identical to a pane that stayed blank.
			waited := time.Since(started)
			if captureErr != nil {
				t.Fatalf(
					"compact transcript pane capture kept failing for %s; last error: %v: %q",
					waited,
					captureErr,
					capture,
				)
			}
			t.Fatalf(
				"compact transcript pane never became ready within %s; capture succeeded throughout and the fixture never rendered its working line: %q",
				waited,
				capture,
			)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestJailedThenWaiterDeliversAfterIdleExactlyOnce is the --then contract in a
// real tmux server: the waiter rides out the busy turn (chat.sh:1062-1077) and
// the steer lands once, after idle, with the submit confirmed.
func TestJailedThenWaiterDeliversAfterIdleExactlyOnce(t *testing.T) {
	jail := newInjectTmuxJail(t)
	socket := "probe-pfm-inject-then"
	pane := jail.startBusyPane(t, socket, "steer-session", 2500*time.Millisecond)
	socketPath := filepath.Join(jail.tmuxDir, socket)

	engine, err := New(Dependencies{
		Resolver: fakeResolver{socket: socketPath, target: pane},
		Tmux:     verifiedChatTmux{command: "claude"},
		Spawner:  &fakeSpawner{},
		Options: Options{
			Poll:            50 * time.Millisecond,
			EnterSettle:     200 * time.Millisecond,
			ProofSettle:     150 * time.Millisecond,
			BusyTries:       2,
			InterruptTries:  2,
			StashTries:      2,
			SettleTries:     10,
			EnterTries:      6,
			LockTimeout:     5 * time.Second,
			LockPoll:        20 * time.Millisecond,
			LockMaxHold:     30 * time.Second,
			LockRoot:        filepath.Join(jail.root, "chat-inject-locks"),
			ThenLogRoot:     jail.root,
			ThenMin:         100 * time.Millisecond,
			ThenBusyTries:   10,
			ThenIdlePoll:    100 * time.Millisecond,
			ThenIdleTries:   80,
			ThenIdleStable:  3,
			ThenSettle:      100 * time.Millisecond,
			Sender:          &Sender{Session: "sender", Label: "Operator", UUID: "abcdef123456"},
			ClaudeInlineMax: ClaudeInlineMax,
			CodexInlineMax:  CodexInlineMax,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// DeliverThen is deliberately different from an ordinary inject: normal
	// busy delivery queues in the composer, while a post-turn steer must wait
	// until the primary turn has settled before it starts the next turn.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	steer := "post compact steer landed"
	result, err := engine.DeliverThen(ctx, ThenWait{SocketPath: socketPath, Target: pane, Steers: []string{steer}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status != "delivered" || !result.Typed {
		t.Fatalf("DeliverThen() = %+v", result)
	}
	capture, err := TmuxInjector{}.Capture(ctx, socketPath, pane, false, FullScrollback)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(capture, "BUSY-TYPED:") {
		t.Fatalf("steer landed while the pane was busy:\n%s", capture)
	}
	if occurrences := strings.Count(capture, "USER:"+steer); occurrences != 1 {
		t.Fatalf("steer landed %d times, want exactly once:\n%s", occurrences, capture)
	}
	if !strings.Contains(capture, "to reply: chat_inject Operator <message>") {
		t.Fatalf("steer was delivered unsigned:\n%s", capture)
	}
	if !strings.Contains(result.Proof, "USER:"+steer) {
		t.Fatalf("delivery proof missing the steer: %q", result.Proof)
	}
}

// TestJailedBusyCodexQueuesAndLongFileDeliversByPaste is the real-tmux
// acceptance fixture for a --file body queued while the pane is busy.
// Renamed from ...UsesAutoFilePointer: the long body now reaches the busy
// pane byte-exact through bracketed paste, proven by the fixture's own
// "BUSY-TYPED:" echo, and the auto-file store is never touched — see the
// comment at the long-body assertion below.
func TestJailedBusyCodexQueuesAndLongFileDeliversByPaste(t *testing.T) {
	jail := newInjectTmuxJail(t)
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	// Deliberately keep a non-Codex socket spelling: the verified foreground
	// process, not the immutable address, decides queue semantics.
	socket := "probe-pfm-inject-codex-queue"
	pane := jail.startBusyPane(t, socket, "queue-session", 30*time.Second)
	socketPath := filepath.Join(jail.tmuxDir, socket)
	engine, err := New(Dependencies{
		Resolver: fakeResolver{socket: socketPath, target: pane},
		Tmux:     verifiedChatTmux{command: "codex"},
		Spawner:  &fakeSpawner{},
		Options: Options{
			Poll:            20 * time.Millisecond,
			EnterSettle:     100 * time.Millisecond,
			ProofSettle:     50 * time.Millisecond,
			SettleTries:     30,
			EnterTries:      4,
			LockTimeout:     5 * time.Second,
			LockPoll:        20 * time.Millisecond,
			LockMaxHold:     30 * time.Second,
			LockRoot:        filepath.Join(jail.root, "queue-locks"),
			ThenLogRoot:     jail.root,
			Sender:          &Sender{Session: "sender", UUID: "abcdef123456"},
			ClaudeInlineMax: ClaudeInlineMax,
			CodexInlineMax:  CodexInlineMax,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	queued, err := engine.Inject(ctx, Request{Target: "queue-session", Message: "queued while working"})
	if err != nil {
		t.Fatal(err)
	}
	if queued.Code != 0 || queued.Status != "queued" || !queued.Typed || queued.Interrupted {
		t.Fatalf("busy Codex queue result = %+v", queued)
	}
	if !strings.Contains(queued.Message, "queued into") {
		t.Fatalf("queue receipt is not distinct: %q", queued.Message)
	}
	capture, err := TmuxInjector{}.Capture(ctx, socketPath, pane, false, FullScrollback)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(capture, "BUSY-TYPED:queued while working") {
		t.Fatalf("busy fixture did not receive queued body: %q", capture)
	}

	// A file-backed body over the inline threshold now travels byte-exact
	// through bracketed paste instead of being replaced by an auto-file
	// pointer: the transport ladder proves it via the busy fixture's own
	// "BUSY-TYPED:<text>" echo, so no pointer and no spilled file are
	// expected here — the auto-file store is a fallback for an UNPROVEN
	// delivery, not this proven one.
	long := strings.Repeat("file-backed body ", 140)
	fileQueued, err := engine.Inject(ctx, Request{
		Target:     "queue-session",
		Message:    long,
		FileBacked: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fileQueued.Code != 0 || fileQueued.Status != "queued" ||
		strings.Contains(fileQueued.Message, "AUTO-FILE") ||
		!strings.Contains(fileQueued.Message, "FILE-BACKED") ||
		fileQueued.AutoFilePath != "" {
		t.Fatalf("long file-backed queue result = %+v", fileQueued)
	}
	capture, err = TmuxInjector{}.Capture(ctx, socketPath, pane, false, FullScrollback)
	if err != nil || !strings.Contains(capture, long) {
		t.Fatalf("busy fixture did not receive the full file-backed body byte-exact: err=%v capture=%q", err, capture)
	}
}

func TestJailedBusyClaudeQueuesWithoutControlKeys(t *testing.T) {
	jail := newInjectTmuxJail(t)
	socket := "probe-pfm-inject-claude-queue"
	pane := jail.startBusyPane(t, socket, "claude-queue-session", 30*time.Second)
	socketPath := filepath.Join(jail.tmuxDir, socket)
	engine, err := New(Dependencies{
		Resolver: fakeResolver{socket: socketPath, target: pane},
		Tmux:     verifiedChatTmux{command: "claude"},
		Spawner:  &fakeSpawner{},
		Options: Options{
			Poll:            20 * time.Millisecond,
			EnterSettle:     100 * time.Millisecond,
			ProofSettle:     50 * time.Millisecond,
			SettleTries:     30,
			EnterTries:      4,
			LockTimeout:     5 * time.Second,
			LockPoll:        20 * time.Millisecond,
			LockMaxHold:     30 * time.Second,
			LockRoot:        filepath.Join(jail.root, "claude-queue-locks"),
			ThenLogRoot:     jail.root,
			Sender:          &Sender{Session: "sender", UUID: "abcdef123456"},
			ClaudeInlineMax: ClaudeInlineMax,
			CodexInlineMax:  CodexInlineMax,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	queued, err := engine.Inject(ctx, Request{
		Target:  "claude-queue-session",
		Message: "queued into claude while working",
	})
	if err != nil {
		t.Fatal(err)
	}
	if queued.Code != 0 || queued.Status != "queued" ||
		!queued.Typed || queued.Interrupted {
		t.Fatalf("busy Claude queue result = %+v", queued)
	}
	if !strings.Contains(queued.Message, "busy Claude accepted") {
		t.Fatalf("queue receipt does not identify Claude: %q", queued.Message)
	}
	capture, err := TmuxInjector{}.Capture(ctx, socketPath, pane, false, FullScrollback)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(capture, "BUSY-TYPED:queued into claude while working") {
		t.Fatalf("busy Claude fixture did not receive queued body: %q", capture)
	}
	if strings.Contains(capture, "CONTROL-S-ERROR") ||
		strings.Contains(capture, "ESCAPE-ERROR") {
		t.Fatalf("busy Claude queue sent a control key: %q", capture)
	}
}

// TestJailedLongCompactFocusFiresWithFullTranscript is the real-tmux
// acceptance fixture for the no-user-visible-size-limit rule. The probe emits
// busy output during input, so an implementation that interleaves output into
// its accumulated command or falls back to a paste is caught by the exact
// transcript record, not by a lucky visible tail of the pane. It drives
// delivery through injectChain (engine.inject with Chain: true): the public
// Inject() now refuses a /compact primary outright (Task C), so the
// paced-literal/full-transcript guarantee is proven the way DeliverThen's
// waiter actually reaches this transaction.
func TestJailedLongCompactFocusFiresWithFullTranscript(t *testing.T) {
	jail := newInjectTmuxJail(t)
	socket := "probe-pfm-inject-compact-focus"
	session := "compact-focus-session"
	transcript := filepath.Join(jail.root, "compact-transcript.log")
	pane := jail.startCompactTranscriptPane(t, socket, session, transcript)
	socketPath := filepath.Join(jail.tmuxDir, socket)
	engine, err := New(Dependencies{
		Resolver: fakeResolver{socket: socketPath, target: pane},
		Tmux:     verifiedChatTmux{TmuxInjector: TmuxInjector{}, command: "claude"},
		Spawner:  &fakeSpawner{},
		Options: Options{
			Poll:            10 * time.Millisecond,
			EnterSettle:     100 * time.Millisecond,
			ProofSettle:     100 * time.Millisecond,
			SettleTries:     500,
			EnterTries:      6,
			LockTimeout:     5 * time.Second,
			LockPoll:        20 * time.Millisecond,
			LockMaxHold:     30 * time.Second,
			LockRoot:        filepath.Join(jail.root, "compact-locks"),
			ThenLogRoot:     jail.root,
			ThenMin:         100 * time.Millisecond,
			ThenBusyTries:   10,
			ThenIdlePoll:    100 * time.Millisecond,
			ThenIdleTries:   80,
			ThenIdleStable:  3,
			ThenSettle:      100 * time.Millisecond,
			Sender:          &Sender{Session: "sender", Label: "Operator", UUID: "abcdef123456"},
			ClaudeInlineMax: ClaudeInlineMax,
			CodexInlineMax:  CodexInlineMax,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	const focusRunes = 2147
	// Keep the tail unique so render-settle proves the final byte reached the
	// deliberately slow fixture instead of matching an earlier repeated run.
	focus := strings.Repeat("f", focusRunes-1) + "z"
	want := "/compact " + focus
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := engine.injectChain(ctx, Request{
		Target:  session,
		Message: want,
		Then:    []string{"resume after compaction"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status != "queued" || !result.Typed ||
		result.AutoFilePath != "" || result.LiteralChunks < 2 {
		capture, captureErr := TmuxInjector{}.Capture(
			context.Background(),
			socketPath,
			pane,
			false,
			FullScrollback,
		)
		recorded, recordErr := os.ReadFile(transcript)
		t.Fatalf(
			"long compact result=%+v capture_err=%v capture=%q transcript_err=%v transcript=%q",
			result,
			captureErr,
			capture,
			recordErr,
			recorded,
		)
	}
	deadline := time.Now().Add(10 * time.Second)
	var recorded []byte
	for {
		recorded, err = os.ReadFile(transcript)
		if err == nil && strings.Contains(string(recorded), "FIRED-END\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("compact command never fired: err=%v transcript=%q", err, recorded)
		}
		time.Sleep(20 * time.Millisecond)
	}
	wantRecord := "FIRED-BEGIN\n" + want + "\nFIRED-END\n"
	if string(recorded) != wantRecord {
		t.Fatalf(
			"compact transcript changed or interleaved command: got %d bytes, want %d",
			len(recorded),
			len(wantRecord),
		)
	}
	if !strings.Contains(result.Proof, "FIRED") {
		t.Fatalf("delivery proof did not show the fired command: %q", result.Proof)
	}
}

// TestJailedCaptureReturnsWholeScrollback covers chat.sh:1215-1219: capture
// reads the entire retained buffer, not only the visible fold.
func TestJailedCaptureReturnsWholeScrollback(t *testing.T) {
	jail := newInjectTmuxJail(t)
	socket := "probe-pfm-inject-scrollback"
	session := "scrollback-session"
	command := jail.command(
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		session,
		"-x",
		"80",
		"-y",
		"10",
		"sh",
		"-c",
		"i=1; while [ $i -le 60 ]; do echo scrollrow-$i; i=$((i+1)); done; sleep 60",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start scrollback pane: %v: %s", err, output)
	}
	jail.sockets = append(jail.sockets, socket)
	socketPath := filepath.Join(jail.tmuxDir, socket)
	engine, err := New(Dependencies{
		Resolver: fakeResolver{socket: socketPath, target: session},
		Tmux:     TmuxInjector{},
		Spawner:  &fakeSpawner{},
		Options:  Options{LockRoot: filepath.Join(jail.root, "locks")},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, text, code, detail, captureErr := engine.Capture(ctx, session, 0)
		if captureErr != nil {
			t.Fatal(captureErr)
		}
		if code != 0 {
			t.Fatalf("Capture() code=%d detail=%q", code, detail)
		}
		if strings.Contains(text, "scrollrow-1\n") &&
			strings.Contains(text, "scrollrow-60") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture never reached the top of the scrollback:\n%s", text)
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, tailed, _, _, err := engine.Capture(ctx, session, 5)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tailed, "scrollrow-1\n") ||
		!strings.Contains(tailed, "scrollrow-60") {
		t.Fatalf("tail bound was not applied after the capture:\n%s", tailed)
	}
}

// TestJailedClientActivityReportsUnattendedAndDeadSocket pins
// CommandTmux.ClientActivity (Task A.1) against a REAL tmux server: an
// unattended session (no client ever attached — every jailed fixture in
// this package, since none of them run a real terminal client) answers
// ok=false with no error, the honest "nobody's there" reading; a socket
// that never had a server answers a real error, which must never be read
// as "unattended" — an error is a failure to look, not an answer.
func TestJailedClientActivityReportsUnattendedAndDeadSocket(t *testing.T) {
	jail := newInjectTmuxJail(t)
	socket := "probe-pfm-client-activity"
	session := "probe-pfm-client-activity-session"
	command := jail.command(
		"-f", "/dev/null", "-L", socket, "new-session", "-d", "-s", session,
		"sleep", "30",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start jailed pane: %v: %s", err, output)
	}
	jail.sockets = append(jail.sockets, socket)
	socketPath := filepath.Join(jail.tmuxDir, socket)

	tmux := TmuxInjector{}
	last, ok, err := tmux.ClientActivity(context.Background(), socketPath, session)
	if err != nil {
		t.Fatalf("ClientActivity() on an unattended live session returned an error instead of ok=false: %v", err)
	}
	if ok {
		t.Fatalf("ClientActivity() on an unattended session reported a client attached: last=%v", last)
	}

	deadSocket := filepath.Join(jail.tmuxDir, "probe-pfm-client-activity-dead")
	_, deadOK, deadErr := tmux.ClientActivity(context.Background(), deadSocket, session)
	if deadErr == nil {
		t.Fatalf(
			"ClientActivity() against a dead socket returned no error (ok=%v) — a failure to look must never read as \"nobody typing\"",
			deadOK,
		)
	}
}
