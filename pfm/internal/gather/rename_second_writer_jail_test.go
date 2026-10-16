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
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close file: %v", err)
		}
	}()
	if _, err := file.WriteString(sequence); err != nil {
		t.Fatalf("write pane escape: %v", err)
	}
}

// windowNameSettles waits for a pane title after the subject escape. tmux
// processes pane output in order, so seeing the title proves it saw the escape.
func windowNameSettles(t *testing.T, socket, tty, title string) string {
	t.Helper()
	if title == "" {
		title = "PFM-ESCAPE-PROCESSED"
		writePaneEscape(t, tty, "\033]2;"+title+"\007")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := renameProbeTmux(t, socket, "display-message", "-p", "#{pane_title}")
		if got == title {
			return renameProbeTmux(t, socket, "display-message", "-p", "#{window_name}")
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane_title = %q; processed signal %q not seen within 2s", got, title)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Candidate 1: tmux's automatic-rename. RULED OUT — rename-window turns it off
// for the window it renames, even while the server-wide default is on.
func TestAutomaticRenameIsNotTheSecondWriter(t *testing.T) {
	socket, windowID, _ := startRenameProbeServer(t, "autorename")
	renameProbeTmux(t, socket, "set-window-option", "-g", "automatic-rename", "on")

	if err := (TmuxProbe{TmuxTmpDir: "/tmp"}).RenameWindow(context.Background(), WindowRename{
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
	// tmux 3.4 tmux.h NAME_INTERVAL is 500000 us (names.c checks once per
	// interval); one automatic-rename check is enough for this negative case.
	time.Sleep(500 * time.Millisecond)
	if got := renameProbeTmux(t, socket, "display-message", "-p", "#{window_name}"); got != "WANTED" {
		t.Fatalf("window name = %q, want WANTED — automatic-rename took it back", got)
	}
}

// Candidate 2: an OSC title write (\e]2;…\a), which every harness and shell
// emits. RULED OUT — inside tmux it sets pane_title and never the window name,
// with allow-rename on or off.
func TestOSCTitleWriteNeverTouchesTheWindowName(t *testing.T) {
	socket, windowID, tty := startRenameProbeServer(t, "osc")
	if err := (TmuxProbe{TmuxTmpDir: "/tmp"}).RenameWindow(context.Background(), WindowRename{
		Socket: socket, WindowID: windowID, CurrentName: "before", TargetName: "WANTED",
	}); err != nil {
		t.Fatal(err)
	}
	renameProbeTmux(t, socket, "set-window-option", "-g", "allow-rename", "on")
	writePaneEscape(t, tty, "\033]2;OSC-TITLE\007")
	if got := windowNameSettles(t, socket, tty, "OSC-TITLE"); got != "WANTED" {
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
	if got := windowNameSettles(t, socket, tty, ""); got != "SECOND-WRITER" {
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

	if err := (TmuxProbe{TmuxTmpDir: "/tmp"}).RenameWindow(context.Background(), WindowRename{
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
	if got := windowNameSettles(t, socket, tty, ""); got != "WANTED" {
		t.Fatalf("window name = %q, want WANTED — the latch did not hold", got)
	}
}

// A rename that knows the chat's exact name states it in the SAME tmux command
// as the rename, set BEFORE the rename so the window-renamed hook that fires on
// it already reads the exact name. The window name stays the clipped form; a
// writer that does not know the name (ChatName empty) leaves the option unset.
func TestRenameWindowSetsTheExactChatNameInTheSameCommand(t *testing.T) {
	socket, windowID, _ := startRenameProbeServer(t, "chatname")
	hookLog := fmt.Sprintf("/tmp/%s-hook.log", socket)
	t.Cleanup(func() { _ = os.Remove(hookLog) })
	renameProbeTmux(t, socket, "set-hook", "-g", "window-renamed[50]",
		fmt.Sprintf(`run-shell "printf '%%s\n' '#{@pfm_chat_name}' >> %s"`, hookLog))

	// A trailing `;` is the one character tmux would read as a command separator.
	exact := "界" + strings.Repeat("x", 40) + ";"
	probe := TmuxProbe{TmuxTmpDir: "/tmp"}
	if err := probe.RenameWindow(context.Background(), WindowRename{
		Socket: socket, WindowID: windowID, CurrentName: "before",
		TargetName: WindowNameFor(exact), ChatName: exact,
	}); err != nil {
		t.Fatal(err)
	}
	if got := renameProbeTmux(t, socket, "show-options", "-v", "-t", socket, "@pfm_chat_name"); got != exact {
		t.Fatalf("@pfm_chat_name = %q, want the exact name %q", got, exact)
	}
	if got := renameProbeTmux(t, socket, "display-message", "-p", "#{window_name}"); got != WindowNameFor(exact) {
		t.Fatalf("window name = %q, want the clipped %q", got, WindowNameFor(exact))
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if logged, _ := os.ReadFile(hookLog); strings.TrimSpace(string(logged)) == exact {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("window-renamed hook saw @pfm_chat_name %q, want %q set before the rename", logged, exact)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// An unnamed writer sets nothing and does not clear a name already stored.
	if err := probe.RenameWindow(context.Background(), WindowRename{
		Socket: socket, WindowID: windowID, CurrentName: WindowNameFor(exact), TargetName: "other",
	}); err != nil {
		t.Fatal(err)
	}
	if got := renameProbeTmux(t, socket, "show-options", "-v", "-t", socket, "@pfm_chat_name"); got != exact {
		t.Fatalf("@pfm_chat_name after a nameless rename = %q, want it kept as %q", got, exact)
	}
}
