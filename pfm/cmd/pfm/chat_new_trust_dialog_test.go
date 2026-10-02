package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestChatNewRefusesAtTheClaudeFolderTrustDialogWithoutAKey: the dialog's
// default row is "No, exit", so an Escape or Enter sent to it ends the chat,
// and trusting a folder is the human's call. Whether the dialog is on screen
// when the chat boots ("now") or arrives after the composer ("late", met by the
// delivery-proof retry), chat new names the dialog, never claims the prompt
// went unrecorded, and presses nothing.
func TestChatNewRefusesAtTheClaudeFolderTrustDialogWithoutAKey(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, arrival := range []string{"now", "late"} {
		t.Run(arrival, func(t *testing.T) {
			jail := newRunJail(t)
			defer jail.killSockets(t)
			keys := filepath.Join(jail.root, "stub-keys.log")
			t.Setenv("CC_STUB_TRUST", arrival)
			t.Setenv("CC_STUB_KEYS", keys)
			restoreGrace, restoreWindow := launchGrace, launchProofWindow
			restoreRescue := launchRescueWindow
			launchGrace, launchProofWindow = 50*time.Millisecond, 2500*time.Millisecond
			launchRescueWindow = 250 * time.Millisecond
			t.Cleanup(func() {
				launchGrace, launchProofWindow = restoreGrace, restoreWindow
				launchRescueWindow = restoreRescue
			})

			var stdout, stderr bytes.Buffer
			code := run([]string{
				"chat", "new",
				"--name", "untrusted worker",
				"--cwd", jail.root + "/work",
				"a prompt for an untrusted folder",
			}, &stdout, &stderr)
			if code != codeUndelivered {
				t.Fatalf("run exit=%d, want %d (stdout=%q stderr=%q)",
					code, codeUndelivered, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "folder-trust dialog") ||
				!strings.Contains(stderr.String(), "Yes, I trust this folder") {
				t.Fatalf("refusal = %q, want it to name the dialog and how to answer it", stderr.String())
			}
			if strings.Contains(stderr.String(), "never recorded the prompt") {
				t.Fatalf("refusal = %q, want the dialog named, not an unrecorded prompt", stderr.String())
			}
			// Let a key sent just before the refusal reach the stub's log.
			time.Sleep(300 * time.Millisecond)
			if logged, err := os.ReadFile(keys); err == nil {
				t.Fatalf("the dialog was sent %d key(s); pfm must press nothing there",
					strings.Count(string(logged), "key"))
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
}
