package gather

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmtmux "hostops/pfm/internal/tmux"
)

// TestProbeTmuxFailsWholeWhenTmuxCannotRun is the regression for the shared
// MCP daemon going deaf: launchd started it with /usr/bin:/bin:/usr/sbin:/sbin,
// tmux lives outside that PATH, and every socket's list-panes failed to START.
// Each failure was filed as a per-socket warning, so the probe returned zero
// panes and no error — every chat_inject from a Codex chat then read "matched
// no live chat" when the truth was "could not look". A tmux that never ran
// read no socket at all, so the pass fails whole and names the cause.
//
// The binary is a bare name absent from PATH, exactly what deps.Executable
// hands back when its lookup fails, so this is the live error shape.
func TestProbeTmuxFailsWholeWhenTmuxCannotRun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, probe := range []struct {
		name string
		run  func(context.Context, string, TmuxClient, time.Time) (TmuxProbe, error)
	}{
		{name: "sweeping", run: ProbeTmux},
		{name: "read-only", run: ProbeTmuxReadOnly},
	} {
		t.Run(probe.name, func(t *testing.T) {
			tmuxDir := t.TempDir()
			now := time.Now()
			createCorpseSocket(t, filepath.Join(tmuxDir, "cc-7-8-9"), now.Add(-2*time.Hour))
			client := CommandTmux{Binary: "pfm-test-missing-tmux", TmuxTmpDir: tmuxDir}

			result, err := probe.run(context.Background(), tmuxDir, client, now)
			if err == nil {
				t.Fatalf(
					"probe with an unstartable tmux returned no error: panes=%d warnings=%q — an empty fleet that means \"could not look\"",
					len(result.Panes),
					result.ProbeWarnings,
				)
			}
			if !pfmtmux.CouldNotRun(err) {
				t.Fatalf("probe error %v does not carry the could-not-run cause", err)
			}
			if !strings.Contains(err.Error(), "pfm-test-missing-tmux") {
				t.Fatalf("probe error %q does not name the binary it could not run", err)
			}
			// A probe that could not read a socket must never sweep it.
			if _, statErr := os.Stat(filepath.Join(tmuxDir, "cc-7-8-9")); statErr != nil {
				t.Fatalf("probe that could not run removed a socket it never read: %v", statErr)
			}
		})
	}
}

// TestProbeTmuxKeepsAFailingServerAsAWarning pins the other side of the
// line: a tmux that RAN and failed against one server is that server's
// problem, reported as a warning while the rest of the fleet still lists.
func TestProbeTmuxKeepsAFailingServerAsAWarning(t *testing.T) {
	tmuxDir := t.TempDir()
	now := time.Now()
	createCorpseSocket(t, filepath.Join(tmuxDir, "cc-7-8-9"), now)
	result, err := ProbeTmuxReadOnly(context.Background(), tmuxDir, alwaysFailTmux{}, now)
	if err != nil {
		t.Fatalf("one failing server failed the whole probe: %v", err)
	}
	if len(result.ProbeWarnings) != 1 {
		t.Fatalf("warnings = %q, want the one failing server named", result.ProbeWarnings)
	}
}

// ConvergeGlobalOptions applies only what diverges and names each change as
// it read it, so a second pass over a converged server changes nothing and
// says nothing; a server that cannot be read is an error naming the option,
// never an empty "nothing to converge".
func TestConvergeGlobalOptionsChangesOnlyWhatDiverges(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root, err := os.MkdirTemp("/tmp", "pfmcv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socket := "cc-1800000041-1-1"
	environment := append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	start := exec.Command("tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", socket, "sleep", "120")
	start.Env = environment
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start tmux fixture: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", socket, "kill-server")
		kill.Env = environment
		_ = kill.Run()
	})
	client := CommandTmux{Binary: "tmux", TmuxTmpDir: root}
	options := [][]string{
		{"set-option", "-g", "set-titles", "off"},
		{"set-window-option", "-g", "automatic-rename", "off"},
	}
	transitions, err := client.ConvergeGlobalOptions(context.Background(), socket, options)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(transitions, "; ") != `automatic-rename "on" -> "off"` {
		t.Fatalf("first pass transitions = %q, want only automatic-rename changed", transitions)
	}
	if again, err := client.ConvergeGlobalOptions(
		context.Background(),
		socket,
		options,
	); err != nil ||
		len(again) != 0 {
		t.Fatalf("second pass = %q (%v), want a converged server left alone", again, err)
	}
	_, err = client.ConvergeGlobalOptions(context.Background(), "cc-missing", options)
	if err == nil || !strings.Contains(err.Error(), "set-titles") {
		t.Fatalf("unreadable server error = %v, want one naming the option it could not read", err)
	}
}
