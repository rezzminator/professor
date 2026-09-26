package headless

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeChat(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	if err := os.WriteFile(
		path,
		[]byte(strings.Join(lines, "\n")+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return path
}

func userLine(text string) string {
	return `{"type":"user","message":{"role":"user","content":"` + text + `"}}`
}

func assistantLine(text string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` +
		text + `"}]}}`
}

func toolLine(tool string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"` +
		tool + `","input":{}}]}}`
}

// TestStateComesFromTheTranscriptNotAClock is the working/idle rule: a chat
// owes an answer until the assistant speaks, however long its tool runs go
// without writing anything. A timer would call that silence "idle" and a
// watcher would report work finished that never was.
func TestStateComesFromTheTranscriptNotAClock(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		lines []string
		live  bool
		want  string
	}{
		{"answered", []string{userLine("go"), assistantLine("done")}, true, StateIdle},
		{"mid tool run", []string{userLine("go"), toolLine("Bash")}, true, StateWorking},
		{"just asked", []string{userLine("go")}, true, StateWorking},
		{"server gone", []string{userLine("go"), assistantLine("done")}, false, StateDead},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			chat := Chat{
				Name:   "seat",
				Engine: "cc",
				Path:   writeChat(t, testCase.lines...),
				Live:   testCase.live,
			}
			status, err := Inspect(context.Background(), chat, time.Now())
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}
			if status.State != testCase.want {
				t.Fatalf("state = %q, want %q (%#v)", status.State, testCase.want, status)
			}
			if status.Alive() != (testCase.want != StateDead) {
				t.Fatalf("Alive() disagrees with state %q", status.State)
			}
		})
	}
}

// TestAMissingChatIsLoud: the one behaviour the whole package exists for.
func TestAMissingChatIsLoud(t *testing.T) {
	status := Missing("ghost")
	if status.State != StateMissing || status.Alive() {
		t.Fatalf("missing status = %#v", status)
	}
	if !strings.Contains(status.Line(), "not-found") {
		t.Fatalf("missing line = %q", status.Line())
	}
}

// TestALiveChatWithNoTranscriptYetIsWorking covers the window between a spawn
// and the engine's first write — a seat that exists but has said nothing.
func TestALiveChatWithNoTranscriptYetIsWorking(t *testing.T) {
	status, err := Inspect(
		context.Background(),
		Chat{Name: "fresh", Engine: "cx", Live: true},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if status.State != StateWorking {
		t.Fatalf("state = %q, want %q", status.State, StateWorking)
	}
}

func TestALiveChatWithPendingTranscriptPathIsWorking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-not-created-yet.jsonl")
	status, err := Inspect(
		context.Background(),
		Chat{Name: "fresh", Engine: "cx", Path: path, Live: true},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if status.State != StateWorking {
		t.Fatalf("state = %q, want %q", status.State, StateWorking)
	}
}

func TestDeadChatTranscriptReadFailureIsNotRenderedAsAbsence(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "missing-transcript.jsonl")
	if err := os.Symlink(filepath.Join(directory, "does-not-exist.jsonl"), path); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(
		context.Background(),
		Chat{Name: "broken", Engine: "cc", Path: path, Live: false},
		time.Now(),
	)
	if err == nil {
		t.Fatalf("unreadable transcript returned nil error and state %q", status.State)
	}
}

func TestIdleSecondsComeFromTheTranscriptMtime(t *testing.T) {
	path := writeChat(t, userLine("go"), assistantLine("done"))
	stamp := time.Now().Add(-90 * time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(
		context.Background(),
		Chat{Name: "seat", Engine: "cc", Path: path, Live: true},
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if status.IdleSeconds < 89 || status.IdleSeconds > 92 {
		t.Fatalf("idle seconds = %d, want ~90", status.IdleSeconds)
	}
}

// TestStreamFilterWithMargin is the requested grep -C contract: the
// matching line PLUS N lines either side, with non-adjacent hits separated.
func TestStreamFilterWithMargin(t *testing.T) {
	path := writeChat(t,
		assistantLine("one"),
		assistantLine("two"),
		assistantLine("three"),
		assistantLine("DONE here"),
		assistantLine("four"),
		assistantLine("five"),
		assistantLine("six"),
	)
	var out bytes.Buffer
	err := Stream(context.Background(), path, "cc", StreamOptions{
		Filter:    regexp.MustCompile("DONE"),
		Margin:    2,
		FromStart: true,
		Follow:    false,
	}, &out)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	want := []string{
		"A two",
		"A three",
		"A DONE here",
		"A four",
		"A five",
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("filtered stream\n got: %v\nwant: %v", got, want)
	}
}

func TestStreamWithoutAFilterPrintsEverything(t *testing.T) {
	path := writeChat(t, userLine("go"), toolLine("Bash"), assistantLine("done"))
	var out bytes.Buffer
	if err := Stream(context.Background(), path, "cc", StreamOptions{
		FromStart: true,
		Follow:    false,
	}, &out); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	want := "U go\nT Bash|{}\nA done\n"
	if out.String() != want {
		t.Fatalf("stream = %q, want %q", out.String(), want)
	}
}

// TestStreamFollowEndsWhenTheChatDies is the "dead is not quiet" rule for the
// streaming path: the follow must return, not hang forever on a file nobody
// will ever write to again.
func TestStreamFollowEndsWhenTheChatDies(t *testing.T) {
	path := writeChat(t, assistantLine("done"))
	var out bytes.Buffer
	err := Stream(context.Background(), path, "cc", StreamOptions{
		FromStart: true,
		Follow:    true,
		Poll:      time.Millisecond,
		Alive:     func() bool { return false },
	}, &out)
	if err != ErrChatGone {
		t.Fatalf("Stream() error = %v, want ErrChatGone", err)
	}
	if !strings.Contains(out.String(), "A done") {
		t.Fatalf("stream lost what it had already read: %q", out.String())
	}
}

// TestWatchAnnouncesIdleThenExit drives the watcher through a chat that
// answers and then disappears.
func TestWatchAnnouncesIdleThenExit(t *testing.T) {
	path := writeChat(t, userLine("go"), assistantLine("done"))
	// The seat's liveness is written by the goroutine below and read by the
	// watcher's own, so it is atomic: the fixture models a chat that dies
	// under its watcher, which is exactly a cross-goroutine change.
	var live atomic.Bool
	live.Store(true)
	idleHooks := 0
	exitHooks := 0
	watcher := Watcher{
		Name: "seat",
		Resolve: func(context.Context) (Chat, bool, error) {
			return Chat{
				Name:   "seat",
				Engine: "cc",
				Path:   path,
				Live:   live.Load(),
			}, true, nil
		},
	}
	var out bytes.Buffer
	go func() {
		// The chat answers, then its server goes away.
		time.Sleep(20 * time.Millisecond)
		live.Store(false)
	}()
	status, err := watcher.Watch(context.Background(), WatchOptions{
		Poll:   time.Millisecond,
		OnIdle: func(Status) error { idleHooks++; return nil },
		OnExit: func(Status) error { exitHooks++; return nil },
	}, &out)
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 ||
		!strings.HasPrefix(lines[0], "IDLE seat idle_seconds=") ||
		lines[1] != "EXIT seat" {
		t.Fatalf("watch output = %q", out.String())
	}
	if idleHooks != 1 || exitHooks != 1 {
		t.Fatalf("hooks fired idle=%d exit=%d, want 1 and 1", idleHooks, exitHooks)
	}
	if status.Alive() {
		t.Fatalf("final status = %#v", status)
	}
}

// TestWatchReportsAChatThatVanishesAsDead separates the two deaths: a chat
// whose server exited (EXIT) from a name that no longer resolves at all
// (DEAD). A monitor must be able to tell them apart.
func TestWatchReportsAChatThatVanishesAsDead(t *testing.T) {
	watcher := Watcher{
		Name: "ghost",
		Resolve: func(context.Context) (Chat, bool, error) {
			return Chat{}, false, nil
		},
	}
	var out bytes.Buffer
	status, err := watcher.Watch(context.Background(), WatchOptions{
		Poll: time.Millisecond,
	}, &out)
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	if strings.TrimSpace(out.String()) != "DEAD ghost" {
		t.Fatalf("watch output = %q", out.String())
	}
	if status.State != StateMissing {
		t.Fatalf("status = %#v", status)
	}
}

// TestWatchOnceStopsAtTheFirstIdle keeps the composable shape honest.
func TestWatchOnceStopsAtTheFirstIdle(t *testing.T) {
	path := writeChat(t, assistantLine("done"))
	watcher := Watcher{
		Name: "seat",
		Resolve: func(context.Context) (Chat, bool, error) {
			return Chat{Name: "seat", Engine: "cc", Path: path, Live: true}, true, nil
		},
	}
	var out bytes.Buffer
	status, err := watcher.Watch(context.Background(), WatchOptions{
		Poll: time.Millisecond,
		Once: true,
	}, &out)
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	if !strings.HasPrefix(out.String(), "IDLE seat") || !status.Alive() {
		t.Fatalf("watch output = %q status = %#v", out.String(), status)
	}
}

// TestIdleSecondsStayZeroWhileAToolRuns: idle_seconds is idleness, not silence.
// A chat mid tool run writes nothing for minutes; counting that as idle time
// handed a caller "working, idle 400 s" — two facts that contradict each other,
// and a settle rule keyed on the number alone would have called the work done.
func TestIdleSecondsStayZeroWhileAToolRuns(t *testing.T) {
	path := writeChat(t, userLine("go"), toolLine("Bash"))
	stamp := time.Now().Add(-90 * time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(
		context.Background(),
		Chat{Name: "seat", Engine: "cc", Path: path, Live: true},
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateWorking {
		t.Fatalf("state = %q, want %q", status.State, StateWorking)
	}
	if status.IdleSeconds != 0 {
		t.Fatalf("idle seconds = %d while working, want 0", status.IdleSeconds)
	}
}

func TestIdleSecondsStayZeroWhileANewerClaudeSidechainRuns(t *testing.T) {
	path := writeChat(t, userLine("go"), assistantLine("waiting"))
	sessionID := "session-with-agent"
	sidechainDir := filepath.Join(filepath.Dir(path), sessionID, "subagents")
	if err := os.MkdirAll(sidechainDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sidechain := filepath.Join(sidechainDir, "agent-working.jsonl")
	if err := os.WriteFile(sidechain, []byte(assistantLine("working")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentStamp := time.Now().Add(-90 * time.Second)
	sidechainStamp := parentStamp.Add(30 * time.Second)
	if err := os.Chtimes(path, parentStamp, parentStamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sidechain, sidechainStamp, sidechainStamp); err != nil {
		t.Fatal(err)
	}

	status, err := Inspect(context.Background(), Chat{
		Name: "seat", ID: sessionID, Engine: "cc", Path: path, Live: true,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateWorking || status.IdleSeconds != 0 {
		t.Fatalf("status = %#v, want working with zero idle seconds", status)
	}
}

func TestOlderClaudeSidechainKeepsParentIdleVerdict(t *testing.T) {
	path := writeChat(t, userLine("go"), assistantLine("done"))
	sessionID := "session-with-finished-agent"
	sidechainDir := filepath.Join(filepath.Dir(path), sessionID, "subagents")
	if err := os.MkdirAll(sidechainDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sidechain := filepath.Join(sidechainDir, "agent-finished.jsonl")
	if err := os.WriteFile(sidechain, []byte(assistantLine("done")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentStamp := time.Now().Add(-60 * time.Second)
	sidechainStamp := parentStamp.Add(-30 * time.Second)
	if err := os.Chtimes(path, parentStamp, parentStamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sidechain, sidechainStamp, sidechainStamp); err != nil {
		t.Fatal(err)
	}

	status, err := Inspect(context.Background(), Chat{
		Name: "seat", ID: sessionID, Engine: "cc", Path: path, Live: true,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateIdle || status.IdleSeconds < 59 {
		t.Fatalf("status = %#v, want idle from newer parent transcript", status)
	}
}

func TestUnreadableClaudeSidechainDirectoryIsAnInspectError(t *testing.T) {
	path := writeChat(t, userLine("go"), assistantLine("waiting"))
	sessionID := "session-with-unreadable-agents"
	sessionDir := filepath.Join(filepath.Dir(path), sessionID)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "subagents"), []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := Inspect(context.Background(), Chat{
		Name: "seat", ID: sessionID, Engine: "cc", Path: path, Live: true,
	}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "read Claude sidechain directory") {
		t.Fatalf("Inspect() = %#v, %v; want visible sidechain error", status, err)
	}
}
