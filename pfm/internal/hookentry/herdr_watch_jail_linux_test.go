//go:build jail && linux

package hookentry

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
// Jail: the subject is a real child process and its controlling terminal.
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
	_, number := openPTY(t)
	transcript := filepath.Join(dir, "s1.jsonl")
	if err := os.Symlink("/dev/pts/"+number, transcript); err != nil {
		t.Fatal(err)
	}
	watch := &herdrWatch{transcript: transcript}
	if _, found, err := watch.scan(); found || err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("scan() = %v, %v, want an error naming a file that is not regular", found, err)
	}
	if tty, err := os.Open("/dev/tty"); err == nil {
		_ = tty.Close()
		t.Fatalf("the scan's session has a controlling terminal, want none: the transcript's terminal was taken")
	}
}

// openPTY opens a pty master the test owns, unlocks its slave and returns the
// master with the slave's /dev/pts number, which the kernel's ioctl reports.
func openPTY(t *testing.T) (*os.File, string) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open a pty: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock the pty: %v", err)
	}
	number, err := unix.IoctlGetUint32(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("name the pty: %v", err)
	}
	return master, strconv.FormatUint(uint64(number), 10)
}
