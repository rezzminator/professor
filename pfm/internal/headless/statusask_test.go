package headless

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// assertNoLeakedTempFiles is Ask's cleanup pin (see the package doc comment
// on Ask): there are seven returns between file creation and cleanup, and a
// leaked file under directory is the failure this catches.
func assertNoLeakedTempFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("read prepared temp dir %s: %v", directory, err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for index, entry := range entries {
			names[index] = entry.Name()
		}
		t.Fatalf("leaked prepared temp files in %s: %v", directory, names)
	}
}

// startHeadlessTmuxSession mirrors internal/inject's newInjectTmuxJail
// (tmux_jail_test.go): a real tmux server on a scratch socket rooted at
// the current UID's tmux directory, never t.TempDir() (its test-name-derived path blows past
// the unix socket sun_path limit). TMUX_TMPDIR is still pinned per
// pfm/CLAUDE.md's testing rule even though capturePane below is handed the
// ABSOLUTE socket path and never consults it.
func startHeadlessTmuxSession(t *testing.T) (socketPath, session string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}
	tmuxJailRoot := filepath.Join(os.TempDir(), "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmuxJailRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(tmuxJailRoot, "probe-pfm-headless-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", root)
	socketPath = filepath.Join(root, "sock")
	session = "ask-pane"
	command := exec.Command(
		"tmux", "-S", socketPath, "-f", "/dev/null",
		"new-session", "-d", "-s", session, "-x", "80", "-y", "10",
		"sh", "-c", "echo live pane content; sleep 60",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start tmux session: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socketPath, "kill-server").Run()
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove headless tmux jail: %v", err)
		}
	})
	return socketPath, session
}

// TestAskPaysRunnerEveryCallNeverCaches pins Ask's central invariant (see the
// doc comment on AskOptions): unlike Summarize, Ask never consults or writes
// a cache, because a pane moves every second and a cached answer would be a
// confidently stale claim about a chat that has moved on.
func TestAskPaysRunnerEveryCallNeverCaches(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "exchange.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"what is happening"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"steady state"}}`,
	)
	bin := filepath.Join(root, "bin")
	counter := filepath.Join(root, "calls")
	writeSummaryStub(t, bin, "claude", "printf x >> \"$ASK_COUNTER\"\nprintf 'nothing has changed\\n'")
	t.Setenv("PATH", bin)
	t.Setenv("ASK_COUNTER", counter)
	tempDir := filepath.Join(root, "tmp", "chat-status")
	options := AskOptions{Config: summaryMachine("claude"), TempDir: tempDir}
	chat := Chat{Name: "seat", Engine: "cc", Path: transcriptPath, Live: false}

	first := Ask(context.Background(), chat, options)
	if !strings.HasPrefix(first.Text, "TRANSCRIPT-ONLY (chat is not live: there is no pane to capture): ") ||
		first.Warning != nil {
		t.Fatalf("first ask=%+v", first)
	}
	assertNoLeakedTempFiles(t, tempDir)

	second := Ask(context.Background(), chat, options)
	if second.Text != first.Text || second.Warning != nil {
		t.Fatalf("second ask text drifted: first=%q second=%q", first.Text, second.Text)
	}
	assertNoLeakedTempFiles(t, tempDir)

	if calls, err := os.ReadFile(counter); err != nil || string(calls) != "xx" {
		t.Fatalf("runner calls=%q err=%v, want both calls paid (Ask never caches)", calls, err)
	}
}

func TestAskPreservesAnswerWhenPreparedExchangeCleanupFails(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "exchange.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"status?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"steady state"}}`,
	)
	tempDir := filepath.Join(root, "tmp", "chat-status")
	bin := filepath.Join(root, "bin")
	writeSummaryStub(t, bin, "claude", `
set -eu
set -- "$HEADLESS_TEMP_DIR"/exchange-*.md
[ "$#" -eq 1 ]
[ -f "$1" ]
rm "$1"
printf 'cleanup-resistant answer\n'`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HEADLESS_TEMP_DIR", tempDir)

	result := Ask(context.Background(), Chat{Name: "seat", Engine: "cc", Path: transcriptPath}, AskOptions{
		Config: summaryMachine("claude"), TempDir: tempDir,
	})
	if result.Text != "TRANSCRIPT-ONLY (chat is not live: there is no pane to capture): cleanup-resistant answer" ||
		strings.HasPrefix(result.Text, "failed (") {
		t.Fatalf("ask lost its computed answer after cleanup failure: %+v", result)
	}
	if !errors.Is(result.Warning, fs.ErrNotExist) {
		t.Fatalf("ask cleanup warning=%v, want fs.ErrNotExist", result.Warning)
	}
}

// TestAskDistinguishesNotLiveFromCaptureFailure pins the engine's root law
// (pfm/CLAUDE.md: "a probe that could not run never returns 'nothing
// found'") as Ask states it: a chat that is not live and a chat whose pane
// capture ERRORED are two different facts, worded so a reader cannot
// mistake "we never had a screen" for "we failed to read the screen".
func TestAskDistinguishesNotLiveFromCaptureFailure(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "exchange.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"what is happening"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"steady state"}}`,
	)
	bin := filepath.Join(root, "bin")
	writeSummaryStub(t, bin, "claude", "printf 'answer\\n'")
	// Prepend, not replace: the capture-failure call below still needs the
	// REAL tmux binary to reach (and be refused by) the empty socket path.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	options := AskOptions{Config: summaryMachine("claude"), TempDir: filepath.Join(root, "tmp")}

	notLive := Ask(context.Background(), Chat{Name: "seat", Engine: "cc", Path: transcriptPath, Live: false}, options)
	if !strings.Contains(notLive.Text, "TRANSCRIPT-ONLY (chat is not live: there is no pane to capture)") {
		t.Fatalf("not-live ask=%+v", notLive)
	}
	if strings.Contains(notLive.Text, "pane capture failed") {
		t.Fatalf("not-live text bled into capture-failed wording: %q", notLive.Text)
	}

	tmuxRoot := t.TempDir()
	t.Setenv("TMUX_TMPDIR", tmuxRoot)
	socketPath := filepath.Join(tmuxRoot, "no-server-here")
	captureFailed := Ask(context.Background(), Chat{
		Name: "seat", Engine: "cc", Path: transcriptPath, Live: true,
		Socket: socketPath, Session: "whatever",
	}, options)
	if !strings.Contains(captureFailed.Text, "TRANSCRIPT-ONLY (pane capture failed: ") {
		t.Fatalf("capture-failed ask=%+v", captureFailed)
	}
	if strings.Contains(captureFailed.Text, "executable file not found") {
		t.Fatalf(
			"capture-failed for the wrong reason (tmux itself missing, not a refused connection): %q",
			captureFailed.Text,
		)
	}
	if strings.Contains(captureFailed.Text, "chat is not live") {
		t.Fatalf("capture-failed text bled into not-live wording: %q", captureFailed.Text)
	}
}

// TestAskCapturesLivePaneAndReportsPaneOnlyWithoutExchange pins the third
// distinct wording: a live pane that captured fine but has no recorded human
// exchange yet reads as PANE-ONLY, never as an empty transcript-only answer.
func TestAskCapturesLivePaneAndReportsPaneOnlyWithoutExchange(t *testing.T) {
	socketPath, session := startHeadlessTmuxSession(t)
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	writeSummaryStub(t, bin, "claude", "cat >/dev/null\nprintf 'quietly idling\\n'")
	// Prepend, not replace: capturePane still needs the REAL tmux binary
	// from the ambient PATH to reach the session started above.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tempDir := filepath.Join(root, "tmp")
	options := AskOptions{Config: summaryMachine("claude"), TempDir: tempDir}
	chat := Chat{
		Name: "seat", Engine: "cc", Path: "", Live: true,
		Socket: socketPath, Session: session,
	}

	result := Ask(context.Background(), chat, options)
	if !strings.HasPrefix(result.Text, "PANE-ONLY (no human exchange recorded yet): ") {
		t.Fatalf("pane-only ask=%+v", result)
	}
	assertNoLeakedTempFiles(t, tempDir)
}

// TestAskUnavailableNamesBothReasons pins the fourth outcome: when NEITHER
// probe produced content, the text names both reasons instead of silently
// returning an empty answer indistinguishable from a quiet chat.
func TestAskUnavailableNamesBothReasons(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	// An empty PATH: if Ask ever reached the runner on this path, the
	// runner would fail loudly (no such binary) rather than silently
	// succeed, so a mistaken engine call cannot masquerade as this case.
	t.Setenv("PATH", bin)
	tempDir := filepath.Join(root, "tmp")
	options := AskOptions{Config: summaryMachine("claude"), TempDir: tempDir}
	chat := Chat{Name: "seat", Engine: "cc", Path: "", Live: false}

	result := Ask(context.Background(), chat, options)
	want := "unavailable (chat is not live: there is no pane to capture; no human exchange recorded yet)"
	if result.Text != want {
		t.Fatalf("unavailable ask=%+v, want text=%q", result, want)
	}
	assertNoLeakedTempFiles(t, tempDir)
}

// TestAskReportsEngineBinaryMissingAndCleansUpOnEveryErrorReturn pins the
// fifth outcome (a configured engine binary absent from PATH reads as
// MISSING, not as a generic failure) and, across its four calls, exercises
// four of the seven returns between file creation and cleanup: engine
// binary MISSING, Config.DefaultEngine() failure, a crashed runner, and the
// happy path. Every one of them must leave TempDir empty.
func TestAskReportsEngineBinaryMissingAndCleansUpOnEveryErrorReturn(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "exchange.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"status?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"answer"}}`,
	)
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	tempDir := filepath.Join(root, "tmp", "chat-status")
	chat := Chat{Name: "seat", Engine: "cc", Path: transcriptPath, Live: false}

	machine := summaryMachine("codex")
	machine.Codex.Binary = "absent-codex"
	missing := Ask(context.Background(), chat, AskOptions{Config: machine, TempDir: tempDir})
	if missing.Text != "unavailable (codex binary MISSING)" {
		t.Fatalf("missing ask=%+v", missing)
	}
	assertNoLeakedTempFiles(t, tempDir)

	noEngine := Ask(context.Background(), chat, AskOptions{Config: pfmconfig.Config{}, TempDir: tempDir})
	if !strings.HasPrefix(noEngine.Text, "failed (") || !strings.Contains(noEngine.Text, "no engines configured") {
		t.Fatalf("no-engine ask=%+v", noEngine)
	}
	assertNoLeakedTempFiles(t, tempDir)

	writeSummaryStub(t, bin, "claude", "printf 'provider crashed\\n' >&2\nexit 9")
	failed := Ask(context.Background(), chat, AskOptions{Config: summaryMachine("claude"), TempDir: tempDir})
	if !strings.HasPrefix(failed.Text, "failed (") || !strings.Contains(failed.Text, "provider crashed") {
		t.Fatalf("failed ask=%+v", failed)
	}
	assertNoLeakedTempFiles(t, tempDir)

	writeSummaryStub(t, bin, "claude", "printf 'all clear\\n'")
	ok := Ask(context.Background(), chat, AskOptions{Config: summaryMachine("claude"), TempDir: tempDir})
	if !strings.HasPrefix(ok.Text, "TRANSCRIPT-ONLY (chat is not live: there is no pane to capture): ") {
		t.Fatalf("ok ask=%+v", ok)
	}
	assertNoLeakedTempFiles(t, tempDir)
}
