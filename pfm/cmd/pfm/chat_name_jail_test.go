package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestChatNameConvergesTheWindowInlineOnAProbeSocket(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	base := filepath.Join(os.TempDir(), "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "probe-pfm-name-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove probe jail: %v", err)
		}
	})

	deadRuntime := filepath.Join(root, "dead-runtime")
	if err := os.MkdirAll(deadRuntime, 0o700); err != nil {
		t.Fatal(err)
	}
	proof := exec.Command("systemctl", "--user", "show-environment")
	proof.Env = append(withoutEnv(os.Environ(), "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"),
		"XDG_RUNTIME_DIR="+deadRuntime)
	if err := proof.Run(); err == nil {
		t.Fatal("probe jail can reach the user systemd bus")
	}

	socketPath := filepath.Join(root, "probe-name.sock")
	session := "probe-name"
	start := exec.Command(
		"tmux", "-S", socketPath, "-f", "/dev/null",
		"new-session", "-d", "-s", session, "-n", "before", "sleep 120",
	)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socketPath, "kill-server").Run()
	})

	delivered := false
	deliver := func(
		_ context.Context,
		chat headless.Chat,
		name string,
	) (int, string, error) {
		delivered = chat.Socket == socketPath && name == "after"
		return 0, "", nil
	}
	var stderr bytes.Buffer
	code := applyChatName(context.Background(), headless.Chat{
		ID:      "probe-id",
		Name:    "before",
		Socket:  socketPath,
		Session: session,
		Live:    true,
	}, "after", deliver, &stderr)
	if code != 0 || stderr.Len() != 0 || !delivered {
		t.Fatalf("chat name rc=%d delivered=%t stderr=%q", code, delivered, stderr.String())
	}
	output, err := exec.Command(
		"tmux", "-S", socketPath,
		"display-message", "-p", "-t", session, "#{window_name}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(output)); got != "after" {
		t.Fatalf("window name=%q, want after", got)
	}
}

// TestRenameChatWindowClipsNamesLongerThanWindowNameRunes pins a
// regression: renameChatWindow used to hand tmux rename-window the raw chat
// name, unclipped — every OTHER window-name writer clips through
// gather.WindowNameFor (see that function's own doc: two writers that
// clipped differently would rename the window back and forth forever). This
// drives the same real-tmux probe socket as the test above with a name past
// gather.WindowNameRunes AND leading with a multi-byte rune, so a
// byte-index clip and a rune-index clip would disagree, and reads the
// window name back through tmux itself.
func TestRenameChatWindowClipsNamesLongerThanWindowNameRunes(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	base := filepath.Join(os.TempDir(), "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "probe-pfm-rename-clip-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove probe jail: %v", err)
		}
	})

	socketPath := filepath.Join(root, "probe-clip.sock")
	session := "probe-clip"
	start := exec.Command(
		"tmux", "-S", socketPath, "-f", "/dev/null",
		"new-session", "-d", "-s", session, "-n", "before", "sleep 120",
	)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socketPath, "kill-server").Run()
	})

	// 41 runes, well past gather.WindowNameRunes (24), leading with a
	// multi-byte rune so a byte-index clip would slice mid-rune while a
	// rune-index clip does not.
	name := "界" + strings.Repeat("x", 40)
	// chatSocketPath -> paths.SocketPath returns an absolute socket path
	// unchanged, so the jail socket goes straight through.
	if err := renameChatWindow(context.Background(), socketPath, session, name); err != nil {
		t.Fatalf("renameChatWindow: %v", err)
	}

	output, err := exec.Command(
		"tmux", "-S", socketPath,
		"display-message", "-p", "-t", session, "#{window_name}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := gather.WindowNameFor(name)
	if got := strings.TrimSpace(string(output)); got != want {
		t.Fatalf("window name=%q, want clipped %q", got, want)
	}
}

func TestChatNameInjectsThroughTheExactLivePaneNotTranscriptUUID(t *testing.T) {
	chat := headless.Chat{
		ID: "thread-not-indexed", Session: "cx-live-socket", Socket: "cx-live-socket", Pane: "%7",
	}
	if got := pfmchat.PaneTarget(chat); got != "%7" {
		t.Fatalf("PaneTarget() = %q", got)
	}
}

func TestCodexSelfRetainsTheFleetMatchedPane(t *testing.T) {
	jail := newKillCLIJail(t)
	socket := "cx-self-pane-" + strconv.Itoa(os.Getpid())
	if output, err := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "sleep", "120",
	).CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	paneOutput, err := exec.Command(
		"tmux", "-L", socket, "list-panes", "-t", socket, "-F", "#{pane_pid}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(string(paneOutput)))
	if err != nil {
		t.Fatal(err)
	}
	if output, splitErr := exec.Command(
		"tmux", "-L", socket, "split-window", "-d", "-t", socket, "sleep", "120",
	).CombinedOutput(); splitErr != nil {
		t.Fatalf("split tmux: %v: %s", splitErr, output)
	}
	const id = "c6666666-6666-4666-8666-666666666666"
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid: 90103, parentPID: panePID, comm: "codex",
		cmdline: []string{"/usr/local/bin/codex"},
		environ: map[string]string{"CODEX_THREAD_ID": id}, withFD: true,
	})
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", id)
	named, err := pfmchat.Target(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	self, err := pfmchat.Target(context.Background(), "self", nil)
	if err != nil {
		t.Fatal(err)
	}
	if self.Socket != named.Socket || self.Session != named.Session || self.Pane != named.Pane {
		t.Fatalf("self = %+v, want exact fleet address from %+v", self, named)
	}
}

func TestScopedLiveKillConfirmsClosureWithoutExitFlag(t *testing.T) {
	jailTest(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "setsid"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	self := headless.Chat{
		ID: "a1111111-1111-4111-8111-111111111111", Engine: "cc",
		Socket: "cc-new-review", Session: "cc-new-review", Pane: "%0", Live: true,
	}
	ctx := pfmchat.WithResolvedSelf(context.Background(), self)
	var stdout, stderr bytes.Buffer
	if code := runChatKillContext(ctx, []string{"self"}, &stdout, &stderr, paths.OSEnv{}); code != 0 {
		t.Fatalf("kill self code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "closing pane %0 on socket cc-new-review") {
		t.Fatalf("kill self output=%q, want confirmed live-pane closure", stdout.String())
	}
}

func withoutEnv(environment []string, keys ...string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		keep := true
		for _, key := range keys {
			if strings.HasPrefix(entry, key+"=") {
				keep = false
				break
			}
		}
		if keep {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
