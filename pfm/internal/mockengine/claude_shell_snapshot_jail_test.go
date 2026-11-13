package mockengine

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/creack/pty"
)

// snapshotTerminalOut names the file TestToolShellSnapshotLeavesTerminal's child writes its result to.
const snapshotTerminalOut = "MOCK_ENGINE_SNAPSHOT_TERMINAL_OUT"

// TestToolShellSnapshotLeavesTerminal: a chat engine on its own terminal takes the zsh snapshot of the
// owner's interactive shell without that zsh taking job control of the terminal, which would leave the
// engine a background job of its own pane. The owner's ~/.zshrc defines job by whether zsh holds the
// terminal (the monitor option); the engine, the session leader of a fresh terminal, snapshots and runs it.
func TestToolShellSnapshotLeavesTerminal(t *testing.T) {
	if out := os.Getenv(snapshotTerminalOut); out != "" {
		snapshotOnTerminal(t, out)
		return
	}
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Fatalf("the snapshot needs zsh: %v", err)
	}
	dotdir, out := t.TempDir(), filepath.Join(t.TempDir(), "job.out")
	rc := "if [[ -o monitor ]]; then job() { print took-the-terminal }; else job() { print left-the-terminal }; fi\n"
	if err := os.WriteFile(filepath.Join(dotdir, ".zshrc"), []byte(rc), 0o600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestToolShellSnapshotLeavesTerminal$")
	child.Env = append(os.Environ(), "SHELL="+zsh, "ZDOTDIR="+dotdir, snapshotTerminalOut+"="+out)
	terminal, err := pty.StartWithAttrs(child, nil, &syscall.SysProcAttr{Setsid: true, Setctty: true})
	if err != nil {
		t.Fatalf("start the engine on a terminal: %v", err)
	}
	screen, _ := io.ReadAll(terminal) // ends in EIO once the child closes its side
	if err := child.Wait(); err != nil {
		t.Fatalf("the engine on its terminal failed: %v\n%s", err, screen)
	}
	if err := terminal.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != "left-the-terminal\n" {
		t.Fatalf("job in the snapshot of %s = %q (%v), want %q; terminal:\n%s", dotdir, got, err,
			"left-the-terminal\n", screen)
	}
}

// snapshotOnTerminal is the engine's side: it snapshots the owner's shell as its first Bash call does,
// runs job under the snapshot and writes the output to out.
func snapshotOnTerminal(t *testing.T, out string) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	session := &claudeSession{
		proc:      &process{ctx: context.Background(), env: os.Getenv, cwd: cwd},
		configDir: t.TempDir(), sessionID: fixtureSession, environ: os.Environ(),
	}
	argv, err := session.toolShell("job")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("run job under the snapshot: %v: %s", err, output)
	}
	if err := os.WriteFile(out, output, 0o600); err != nil {
		t.Fatal(err)
	}
}
