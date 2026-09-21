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

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
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

func TestChatNameInjectsThroughTheLiveSessionNotTranscriptUUID(t *testing.T) {
	chat := headless.Chat{ID: "thread-not-indexed", Session: "cx-live-socket", Socket: "cx-live-socket"}
	if got := chatNameInjectTarget(chat); got != "cx-live-socket" {
		t.Fatalf("chatNameInjectTarget() = %q", got)
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
