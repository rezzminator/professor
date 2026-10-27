//go:build linux

package hookentry

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// herdrNoTTYHelper names the child of TestHerdrWatchScanTakesNoControllingTerminal
// that scans in a session of its own, carrying its scratch directory.
const herdrNoTTYHelper = "PFM_HERDR_NO_TTY_HELPER"

// The watcher runs detached in a session of its own, with no controlling
// terminal: a transcript replaced by a link to a terminal is refused without
// becoming that session's controlling terminal. The scan runs in a child of
// this test binary that leads its own session, as the watcher does. Linux
// only: the pty is opened through Linux's ptmx ioctls.
func TestHerdrWatchScanTakesNoControllingTerminal(t *testing.T) {
	const name = "TestHerdrWatchScanTakesNoControllingTerminal"
	dir := os.Getenv(herdrNoTTYHelper)
	if dir == "" {
		t.Parallel()
		command := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.v")
		command.Env = append(os.Environ(), herdrNoTTYHelper+"="+t.TempDir())
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "--- PASS: "+name) {
			t.Fatalf("session-leader scan failed: %v\n%s", err, output)
		}
		return
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open a pty: %v", err)
	}
	defer func() { _ = master.Close() }()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock the pty: %v", err)
	}
	// The pty's number, from the master's fdinfo ("tty-index:").
	info, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", master.Fd()))
	_, index, found := strings.Cut(string(info), "tty-index:")
	if err != nil || !found {
		t.Fatalf("name the pty: %v, fdinfo %q", err, info)
	}
	number := strings.TrimSpace(strings.SplitN(index, "\n", 2)[0])
	transcript := filepath.Join(dir, "s1.jsonl")
	if err := os.Symlink("/dev/pts/"+number, transcript); err != nil {
		t.Fatal(err)
	}
	watch := &herdrWatch{transcript: transcript}
	if found, err := watch.scan(); found || err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("scan() = %v, %v, want an error naming a file that is not regular", found, err)
	}
	if tty, err := os.Open("/dev/tty"); err == nil {
		_ = tty.Close()
		t.Fatalf("the scan's session has a controlling terminal, want none: the transcript's terminal was taken")
	}
}
