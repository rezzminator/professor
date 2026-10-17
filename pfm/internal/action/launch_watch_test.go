package action

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/mockengine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// launchWatchServer creates a chat server running run on a jailed tmux and
// returns an executor over it plus a reader of that server's pane.
func launchWatchServer(t *testing.T, socket, run string) (*Executor, ChatServer, func(...string) (string, error)) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jailAction(t)
	root := testjail.ShortRoot(t)
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
	tmux := TmuxExecutor{TmuxDir: tmuxDir}
	socketPath := filepath.Join(tmuxDir, socket)
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", socketPath, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
	executor, err := New(Dependencies{
		Tmux: tmux, Processes: &fakeProcesses{}, Gate: fixedGate(false), Runner: &captureRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := ChatServer{Socket: socket, CWD: root, Window: "Claude", Run: run}
	if err := tmux.CreateChatServer(context.Background(), server); err != nil {
		t.Fatalf("create chat server: %v", err)
	}
	read := func(arguments ...string) (string, error) {
		command := exec.Command("tmux", append([]string{"-S", socketPath}, arguments...)...)
		command.Env = append(os.Environ(), "TMUX=")
		output, err := command.Output()
		return string(output), err
	}
	return executor, server, read
}

// mockClaudeRun is the pane command of the fake engine playing Claude with
// the given scenario, by its wire form.
func mockClaudeRun(t *testing.T, scenario string) string {
	t.Helper()
	directory := t.TempDir()
	binary, err := testjail.MockEngineBinary("../..", directory)
	if err != nil {
		t.Fatalf("build the mock engine: %v", err)
	}
	claude := filepath.Join(directory, "claude")
	if err := os.Symlink(binary, claude); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "scenario.json")
	if err := os.WriteFile(path, []byte(scenario), 0o600); err != nil {
		t.Fatal(err)
	}
	return mockengine.EnvScenario + "=" + shellQuoteValue(path) + " " + shellQuoteValue(claude) +
		" --session-id 55555555-5555-4555-8555-555555555555"
}

func shellQuoteValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// TestWatchLaunchOnARealPane plays the launch watch's three verdicts on a real
// jailed tmux server: the fake engine held at Claude Code's folder-trust
// dialog stays up with the dialog untouched and comes back as the refusal
// warning, the fake engine that boots is up with remain-on-exit released,
// and a pane whose command prints and exits is a failed open quoting it, its
// server gone.
func TestWatchLaunchOnARealPane(t *testing.T) {
	t.Run("held at the folder-trust dialog", func(t *testing.T) {
		const socket = "cc-908-1-1"
		executor, server, read := launchWatchServer(
			t, socket, mockClaudeRun(t, `{"busy_ms":0,"trust_dialog":true}`),
		)
		warnings, err := executor.watchLaunch(context.Background(), server, "trust probe", "claude")
		if err != nil {
			t.Fatalf("watch failed a chat held at the dialog: %v", err)
		}
		joined := strings.Join(warnings, "\n")
		for _, want := range []string{
			"trust probe is held at Claude Code's folder-trust dialog for " + server.CWD,
			"tmux -L " + socket + " attach -t " + socket,
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("warnings %q lack %q", warnings, want)
			}
		}
		screen, err := read("capture-pane", "-p", "-t", socket+":")
		if err != nil {
			t.Fatalf("the held chat's server is gone: %v", err)
		}
		if !pfmengine.ClaudeTrustDialog(screen) {
			t.Fatalf("the pane left the dialog — something answered it:\n%s", screen)
		}
	})
	t.Run("a chat that boots", func(t *testing.T) {
		const socket = "cc-909-1-1"
		executor, server, read := launchWatchServer(t, socket, mockClaudeRun(t, `{"busy_ms":0}`))
		warnings, err := executor.watchLaunch(context.Background(), server, "boot probe", "claude")
		if err != nil || len(warnings) != 0 {
			t.Fatalf("watch of a booted chat = %q, %v; want no warning", warnings, err)
		}
		remain, err := read("display-message", "-p", "-t", socket+":", "#{remain-on-exit}")
		if err != nil {
			t.Fatalf("the booted chat's server is gone: %v", err)
		}
		if strings.TrimSpace(remain) != "off" {
			t.Fatalf("remain-on-exit = %q after the watch, want it released to off", remain)
		}
	})
	t.Run("a chat under a shell that stays in front", func(t *testing.T) {
		// A resume row's compound line: the shell never execs, so the pane's
		// command is the shell and the engine is found on the pane's terminal.
		const socket = "cc-911-1-1"
		executor, server, _ := launchWatchServer(t, socket, mockClaudeRun(t, `{"busy_ms":0}`)+" || true")
		executor.processes = RealProcesses{}
		quiet, deadline := launchQuiet, launchDeadline
		launchQuiet, launchDeadline = time.Hour, time.Hour
		t.Cleanup(func() { launchQuiet, launchDeadline = quiet, deadline })
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		warnings, err := executor.watchLaunch(ctx, server, "shell probe", "claude")
		if err != nil || len(warnings) != 0 {
			t.Fatalf("watch of an engine under its shell = %q, %v; want it up with no warning", warnings, err)
		}
	})
	t.Run("a pane that exits at launch", func(t *testing.T) {
		const socket = "cc-910-1-1"
		executor, server, read := launchWatchServer(
			// The pause after the words: tmux 3.5 drops output a process writes
			// in the same instant it exits, remain-on-exit or not, so only words
			// tmux has read can be quoted.
			t, socket, "sleep 0.5; echo 'pfm internal agent-open: refusing the double-resume' >&2; sleep 0.3; exit 1",
		)
		warnings, err := executor.watchLaunch(context.Background(), server, "exit probe", "claude")
		if err == nil {
			t.Fatalf("watch reported a dead launch as up; warnings %q", warnings)
		}
		for _, want := range []string{"exit probe exited at launch", "refusing the double-resume"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q lacks %q", err, want)
			}
		}
		if output, err := read("list-sessions"); err == nil {
			t.Fatalf("the dead launch's server still runs: %q", output)
		}
	})
}

// fakeLaunch is one created pane as the launch watch reads it: its screen,
// its foreground command, whether its process exited, and whether its whole
// server is already gone.
type fakeLaunch struct {
	screen  string
	command string
	tty     string
	dead    bool
	gone    bool
	// unreadable: the server runs but its pane cannot be read.
	unreadable bool
}

func (tmux *fakeActionTmux) LaunchPane(
	ctx context.Context,
	socket string,
) (LaunchPaneState, error) {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		// The real reads are context-bound tmux commands: a cancelled caller
		// kills them before they answer.
		return LaunchPaneState{}, err
	}
	tmux.launchReads++
	launch, found := tmux.launches[socket]
	if !tmux.alive[socket] || launch.gone {
		return LaunchPaneState{}, errors.New("no server running on " + socket)
	}
	if !found {
		return LaunchPaneState{Screen: "fake chat ready", Command: "claude"}, nil
	}
	if launch.unreadable {
		return LaunchPaneState{}, errors.New("tmux socket " + socket + " returned 1 launch pane fields")
	}
	return LaunchPaneState{Screen: launch.screen, Command: launch.command, TTY: launch.tty, Dead: launch.dead}, nil
}

func (tmux *fakeActionTmux) SetRemainOnExit(
	_ context.Context,
	socket string,
	on bool,
) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	state := "off"
	if on {
		state = "on"
	}
	tmux.remain = append(tmux.remain, socket+"\t"+state)
	return nil
}
