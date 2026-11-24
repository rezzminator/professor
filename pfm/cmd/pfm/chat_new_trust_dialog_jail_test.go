//go:build jail

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestChatNewRefusesAtTheClaudeFolderTrustDialogWithoutAKey: the dialog's
// default row is "No, exit", so an Escape or Enter sent to it ends the chat,
// and trusting a folder is the human's call. Whether the dialog is on screen
// when the chat boots ("now") or arrives after the composer ("late", met by the
// delivery-proof retry), chat new names the dialog, never claims the prompt
// went unrecorded, and presses nothing.
//
// The delivery proof waits on the clock chat new is handed, a fake here: "late"
// draws the dialog once that wait is armed (after the proof's only fleet
// lookup), and the retry runs because the test steps the fake past the
// proof window, never because time passed.
func TestChatNewRefusesAtTheClaudeFolderTrustDialogWithoutAKey(t *testing.T) {
	testjail.TmuxServer(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, arrival := range []string{"now", "late"} {
		t.Run(arrival, func(t *testing.T) {
			jail := newRunJail(t)
			defer jail.killSockets(t)
			keys := filepath.Join(jail.root, "stub-keys.log")
			release := filepath.Join(jail.root, "trust-release")
			if err := syscall.Mkfifo(release, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CC_STUB_TRUST", arrival)
			t.Setenv("CC_STUB_KEYS", keys)
			t.Setenv("CC_STUB_TRUST_RELEASE", release)
			// Shorter than the proof's fleet-lookup cadence (3 s), so its one
			// lookup is the one at its start; it runs on the fake clock alone.
			restoreWindow := launchProofWindow
			launchProofWindow = 2500 * time.Millisecond
			t.Cleanup(func() { launchProofWindow = restoreWindow })
			runtime, err := pfmconfig.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			fake := clock.NewFake(time.Unix(1_800_000_000, 0))

			var stdout, stderr bytes.Buffer
			done := make(chan int, 1)
			go func() {
				done <- runRun(testwait.Context(t), []string{
					"--name", "untrusted worker",
					"--cwd", jail.root + "/work",
					"a prompt for an untrusted folder",
				}, &stdout, &stderr, runtime, paths.OSEnv{}, fake)
			}()
			code := -1
			if arrival == "late" {
				proofWaiting := func() (bool, string) {
					return testwait.Blocked("headless.waitForNextPoll", "headless.Await")
				}
				testwait.Until(t, "the delivery proof to arm its wait", proofWaiting)
				writer, err := os.OpenFile(release, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := writer.WriteString("draw\n"); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				testwait.Until(t, "the stub to draw the trust dialog", func() (bool, string) {
					_, statErr := os.Stat(keys + ".drawn")
					return statErr == nil, "dialog not drawn yet"
				})
				// Step the fake one armed wait at a time until chat new returns.
				testwait.Until(t, "chat new to return after the proof window", func() (bool, string) {
					select {
					case code = <-done:
						return true, ""
					default:
					}
					if waiting, _ := proofWaiting(); waiting {
						if step, due := fake.NextDue(); due {
							fake.Advance(step)
						}
					}
					return false, fake.String()
				})
			} else {
				code, _ = testwait.Receive(t, "chat new to return", done)
			}
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
			// The tty delivers keys in order: once the stub reads a sentinel typed after
			// the refusal, every key pfm sent before it has reached the stub's log.
			sockets, err := os.ReadDir(jail.tmuxDir)
			if err != nil || len(sockets) != 1 {
				t.Fatalf("jailed socket count=%d err=%v", len(sockets), err)
			}
			sentinel := exec.Command("tmux", "-S", filepath.Join(jail.tmuxDir, sockets[0].Name()),
				"send-keys", "-t", sockets[0].Name(), "-l", "Z")
			if output, err := sentinel.CombinedOutput(); err != nil {
				t.Fatalf("type the sentinel: %v: %s", err, output)
			}
			testwait.Until(t, "the stub to read the sentinel key", func() (bool, string) {
				_, statErr := os.Stat(keys + ".sentinel")
				return statErr == nil, "no sentinel yet"
			})
			if logged, err := os.ReadFile(keys); err == nil {
				t.Fatalf("the dialog was sent %d key(s); pfm must press nothing there",
					strings.Count(string(logged), "key"))
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
}
