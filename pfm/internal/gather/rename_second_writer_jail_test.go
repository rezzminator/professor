package gather

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A window name that does not stick has exactly one interesting question
// behind it: who else writes it. These fixtures answer it on a real tmux
// server — a probe-* socket in the scratch namespace, never a fleet socket —
// by trying each candidate writer in turn.

func startRenameProbeServer(t *testing.T, name string) (socket, windowID, tty string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socket = fmt.Sprintf("probe-pfm-rename-%s-%d", name, os.Getpid())
	start := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "-n", "before", "sleep 120",
	)
	start.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR=/tmp")
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", socket, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR=/tmp")
		_ = kill.Run()
	})
	windowID = renameProbeTmux(t, socket, "display-message", "-p", "#{window_id}")
	tty = renameProbeTmux(t, socket, "display-message", "-p", "#{pane_tty}")
	return socket, windowID, tty
}

func renameProbeTmux(t *testing.T, socket string, arguments ...string) string {
	t.Helper()
	command := exec.Command("tmux", append([]string{"-L", socket}, arguments...)...)
	command.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR=/tmp")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// writePaneEscape writes an escape sequence to the pane's own tty, which is
// how a shell or a harness inside the pane sets a title — the one shape
// send-keys cannot reproduce, because send-keys is input and a title is output.
func writePaneEscape(t *testing.T, tty, sequence string) {
	t.Helper()
	file, err := os.OpenFile(tty, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("pane tty %s is not writable here: %v", tty, err)
	}
	defer file.Close()
	if _, err := file.WriteString(sequence); err != nil {
		t.Fatalf("write pane escape: %v", err)
	}
}

// windowNameSettles polls until tmux has drained the pane output, so a name
// that is going to change has had its chance before the assertion runs.
func windowNameSettles(t *testing.T, socket string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	name := renameProbeTmux(t, socket, "display-message", "-p", "#{window_name}")
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		next := renameProbeTmux(t, socket, "display-message", "-p", "#{window_name}")
		if next != name {
			return next
		}
	}
	return name
}

// Candidate 1: tmux's automatic-rename. RULED OUT — rename-window turns it off
// for the window it renames, even while the server-wide default is on.
func TestAutomaticRenameIsNotTheSecondWriter(t *testing.T) {
	socket, windowID, _ := startRenameProbeServer(t, "autorename")
	renameProbeTmux(t, socket, "set-window-option", "-g", "automatic-rename", "on")

	if err := (CommandTmux{TmuxTmpDir: "/tmp"}).RenameWindow(context.Background(), WindowRename{
		Socket: socket, WindowID: windowID, CurrentName: "before", TargetName: "WANTED",
	}); err != nil {
		t.Fatal(err)
	}
	if got := renameProbeTmux(
		t,
		socket,
		"show-window-options",
		"-t",
		windowID,
		"automatic-rename",
	); got != "automatic-rename off" {
		t.Fatalf("automatic-rename after rename-window = %q, want it disabled for this window", got)
	}
	if got := windowNameSettles(t, socket); got != "WANTED" {
		t.Fatalf("window name = %q, want WANTED — automatic-rename took it back", got)
	}
}

// Candidate 2: an OSC title write (\e]2;…\a), which every harness and shell
// emits. RULED OUT — inside tmux it sets pane_title and never the window name,
// with allow-rename on or off.
func TestOSCTitleWriteNeverTouchesTheWindowName(t *testing.T) {
	socket, windowID, tty := startRenameProbeServer(t, "osc")
	if err := (CommandTmux{TmuxTmpDir: "/tmp"}).RenameWindow(context.Background(), WindowRename{
		Socket: socket, WindowID: windowID, CurrentName: "before", TargetName: "WANTED",
	}); err != nil {
		t.Fatal(err)
	}
	renameProbeTmux(t, socket, "set-window-option", "-g", "allow-rename", "on")
	writePaneEscape(t, tty, "\033]2;OSC-TITLE\007")
	if got := windowNameSettles(t, socket); got != "WANTED" {
		t.Fatalf("window name = %q, want WANTED — an OSC title write must not rename a window", got)
	}
	if got := renameProbeTmux(t, socket, "display-message", "-p", "#{pane_title}"); got != "OSC-TITLE" {
		t.Fatalf("pane_title = %q, want OSC-TITLE — the escape never reached the pane", got)
	}
}

// Candidate 3: the screen title escape (\ek…\e\\) with allow-rename on. This
// IS a second writer: a bare rename-window is taken back by the next write.
func TestScreenTitleEscapeIsTheSecondWriterWhenAllowRenameIsOn(t *testing.T) {
	socket, windowID, tty := startRenameProbeServer(t, "esck")
	renameProbeTmux(t, socket, "set-window-option", "-g", "allow-rename", "on")
	renameProbeTmux(t, socket, "rename-window", "-t", windowID, "WANTED")

	writePaneEscape(t, tty, "\033kSECOND-WRITER\033\\")
	if got := windowNameSettles(t, socket); got != "SECOND-WRITER" {
		t.Skipf(
			"this tmux does not honour \\ek with allow-rename on (name=%q); the latch below is then simply inert",
			got,
		)
	}
}

// …and RenameWindow's latch stops it: the same write against a window pfm
// renamed leaves the fleet's name in place.
func TestRenameWindowLatchSurvivesAScreenTitleEscape(t *testing.T) {
	socket, windowID, tty := startRenameProbeServer(t, "latch")
	renameProbeTmux(t, socket, "set-window-option", "-g", "allow-rename", "on")

	if err := (CommandTmux{TmuxTmpDir: "/tmp"}).RenameWindow(context.Background(), WindowRename{
		Socket: socket, WindowID: windowID, CurrentName: "before", TargetName: "WANTED",
	}); err != nil {
		t.Fatal(err)
	}
	if got := renameProbeTmux(
		t,
		socket,
		"show-window-options",
		"-t",
		windowID,
		"allow-rename",
	); got != "allow-rename off" {
		t.Fatalf("allow-rename after RenameWindow = %q, want it latched off for this window", got)
	}
	writePaneEscape(t, tty, "\033kSECOND-WRITER\033\\")
	if got := windowNameSettles(t, socket); got != "WANTED" {
		t.Fatalf("window name = %q, want WANTED — the latch did not hold", got)
	}
}
