package gather

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// The claude half of window naming against a REAL tmux server on a scratch
// socket: a pane rendering a 🔖 statusline, captured and converged the way a
// picker run does it. The table tests prove the rules; this proves the two
// tmux calls behind them — capture-pane and rename-window — actually work
// together on a live server.
func TestClaudeWindowNameConvergesOnARealServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newTmuxJail(t)
	const socket = "cc-1800000001-42-1"
	// The pane paints a statusline and then holds: a chat's label lives on
	// its own screen and nowhere else.
	command := jail.command(
		"-f", "/dev/null", "-L", socket,
		"new-session", "-d", "-s", socket, "-n", "zsh",
		"printf '🥇 acct  🔖 FLEET_LABEL │ 42%%\\n'; sleep 120",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start jailed server: %v: %s", err, output)
	}
	jail.sockets = append(jail.sockets, socket)

	tmux := TmuxProbe{TmuxTmpDir: jail.root}
	gatherer, err := New(Dependencies{
		Tmux:       tmux,
		TmuxTmpDir: jail.root,
		CodexName:  func(string) string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	var snapshot Snapshot
	for attempt := 0; attempt < 40; attempt++ {
		snapshot, err = gatherer.Gather(ctx)
		if err != nil {
			t.Fatalf("Gather() error = %v", err)
		}
		if len(snapshot.Renames) == 1 {
			break
		}
	}
	if len(snapshot.Renames) != 1 {
		t.Fatalf("Gather() planned %#v, want one claude rename", snapshot.Renames)
	}
	rename := snapshot.Renames[0]
	if rename.TargetName != "FLEET_LABEL" {
		t.Fatalf("target name = %q, want FLEET_LABEL", rename.TargetName)
	}
	if err := tmux.RenameWindow(ctx, rename); err != nil {
		t.Fatalf("RenameWindow() error = %v", err)
	}

	output, err := jail.command(
		"-L", socket, "list-windows", "-F", "#{window_name}",
	).Output()
	if err != nil {
		t.Fatalf("read back the window name: %v", err)
	}
	if got := strings.TrimSpace(string(output)); got != "FLEET_LABEL" {
		t.Fatalf("window name = %q, want FLEET_LABEL", got)
	}

	// Converged: a second pass has nothing left to do, so the picker does not
	// rename the same window on every refresh.
	snapshot, err = gatherer.Gather(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Renames) != 0 {
		t.Fatalf("a converged window was renamed again: %#v", snapshot.Renames)
	}
}
