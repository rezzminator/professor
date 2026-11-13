package action

import (
	"context"
	"path/filepath"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// launchScreen is a Screen that records each history read and answers it.
type launchScreen struct {
	pfmcall.Screen // LaunchPane only reads with history.
	targets        []pfmcall.Target
	lines          []int
}

func (screen *launchScreen) CaptureTarget(_ context.Context, target pfmcall.Target, lines int) (string, error) {
	screen.targets = append(screen.targets, target)
	screen.lines = append(screen.lines, lines)
	return "door frame", nil
}

// TestLaunchPaneReadsItsScreenThroughTheScreenDoor: the launch watch's screen
// read is call's Screen door, addressed to the session's pane, with 200 lines
// of history once the pane is dead — never a capture-pane of its own.
func TestLaunchPaneReadsItsScreenThroughTheScreenDoor(t *testing.T) {
	for _, testCase := range []struct {
		dead  string
		lines int
	}{{dead: "0", lines: 0}, {dead: "1", lines: 200}} {
		binary := filepath.Join(t.TempDir(), "tmux")
		script := "#!/bin/sh\ncase \" $* \" in *\" display-message \"*) printf '" + testCase.dead +
			"\\037claude\\037/dev/pts/3\\n';; *) echo \"unexpected tmux $*\" >&2; exit 9;; esac\n"
		if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		tmuxDir := t.TempDir()
		screen := &launchScreen{}
		pane, err := TmuxExecutor{
			Binary:  binary,
			TmuxDir: tmuxDir,
			Screen:  screen,
		}.LaunchPane(
			context.Background(),
			"cc-1",
		)
		if err != nil {
			t.Fatal(err)
		}
		want := pfmcall.Target{SocketPath: filepath.Join(tmuxDir, "cc-1"), Pane: "cc-1:"}
		if len(screen.targets) != 1 || screen.targets[0] != want || screen.lines[0] != testCase.lines {
			t.Fatalf("dead=%s: door reads %+v lines %v, want one read of %+v with %d lines",
				testCase.dead, screen.targets, screen.lines, want, testCase.lines)
		}
		if pane.Screen != "door frame" || pane.Command != "claude" || pane.TTY != "pts/3" ||
			pane.Dead != (testCase.dead == "1") {
			t.Fatalf("dead=%s: pane = %+v", testCase.dead, pane)
		}
	}
}
