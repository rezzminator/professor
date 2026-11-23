//go:build jail

// Subject: a real tail held by a real shell, its stderr a pipe the test reads.

package testwait_test

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// inotifyWarning is what GNU and uutils tail print on stderr when the host has
// no inotify instance to spare, as a GitHub runner printed it.
const inotifyWarning = "tail: inotify cannot be used, reverting to polling: Too many open files"

// TestHoldWritesNothingAndKeepsTheCallersStderr runs each shell form of the
// hold behind a tail that prints the runner's warning, backgrounded by a
// shell that then exits, as a fake's grandchild is: the hold writes nothing,
// yet keeps the caller's stderr open until it is killed.
func TestHoldWritesNothingAndKeepsTheCallersStderr(t *testing.T) {
	realTail, err := exec.LookPath("tail")
	if err != nil {
		t.Fatalf("look up tail: %v", err)
	}
	for _, row := range []struct{ name, shell, hold string }{
		{"Hold", "sh", testwait.Hold},
		{"HoldAs", "bash", testwait.HoldAs("hold-fixture")},
	} {
		t.Run(row.name, func(t *testing.T) {
			dir := t.TempDir()
			warned, pidFile := filepath.Join(dir, "warned"), filepath.Join(dir, "pid")
			shim := "#!/bin/sh\necho '" + inotifyWarning + "' >&2\n: > '" + warned + "'\nexec '" + realTail + "' \"$@\"\n"
			if err := testjail.WriteExecutable(filepath.Join(dir, "tail"), []byte(shim), 0o700); err != nil {
				t.Fatal(err)
			}
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = read.Close() }()
			shell := exec.Command(row.shell, "-c", row.hold+" & echo $! > '"+pidFile+"'")
			shell.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			shell.Stderr = write
			runErr := shell.Run()
			_ = write.Close()
			if runErr != nil {
				t.Fatalf("%s -c %q: %v", row.shell, row.hold, runErr)
			}
			testwait.Until(t, "the warning tail to run ("+warned+")", func() (bool, string) {
				_, err := os.Stat(warned)
				return err == nil, fmt.Sprintf("stat: %v", err)
			})
			body, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
			if err != nil {
				t.Fatalf("pid file %s = %q: %v", pidFile, body, err)
			}
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			drained := make(chan string, 1)
			go func() {
				output, _ := io.ReadAll(read)
				drained <- string(output)
			}()
			// A read parks in the poller only while a writer holds the pipe; with
			// none, it sees EOF at once.
			testwait.Until(t, "the read of the hold's stderr to park while the hold runs", func() (bool, string) {
				select {
				case output := <-drained:
					t.Fatalf(
						"the hold's stderr reached EOF while it ran: it dropped the caller's stderr (read %q)",
						output,
					)
				default:
				}
				return testwait.Blocked("poll.(*pollDesc).waitRead", "poll.(*FD).Read")
			})
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
				t.Fatalf("kill the hold %d: %v", pid, err)
			}
			output, _ := testwait.Receive(t, "the hold's stderr to close once it is killed", drained)
			if output != "" {
				t.Fatalf("the hold wrote %q on its stderr, want nothing", output)
			}
		})
	}
}
