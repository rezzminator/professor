//go:build jail

package action

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/mockengine"
	"github.com/rezzminator/professor/pfm/internal/mockengine/control"
	"github.com/rezzminator/professor/pfm/internal/mockengine/control/controltest"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
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

// launchPaneLooks counts the launch watch's looks at its pane, so a test moves
// the engine's screen only once the watch has seen the engine running.
type launchPaneLooks struct {
	TmuxClient
	count atomic.Int64
}

func (looks *launchPaneLooks) LaunchPane(ctx context.Context, socket string) (LaunchPaneState, error) {
	looks.count.Add(1)
	return looks.TmuxClient.LaunchPane(ctx, socket)
}

// paneGate is a FIFO a pane command blocks on (`read -r _ < gate`) until the
// test releases it, so the command acts when the test says, never after a delay.
type paneGate string

func newPaneGate(t *testing.T) paneGate {
	t.Helper()
	path := filepath.Join(testjail.ShortRoot(t), "gate")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	return paneGate(path)
}

// wait is the shell text that blocks until the gate is released.
func (gate paneGate) wait() string { return "read -r _ < " + shellQuoteValue(string(gate)) }

// release lets the command blocked on the gate go on.
func (gate paneGate) release(t *testing.T) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- os.WriteFile(string(gate), []byte("go\n"), 0o600) }()
	if err, _ := testwait.Receive(t, "the pane command to take its gate "+string(gate), done); err != nil {
		t.Fatalf("release the pane gate: %v", err)
	}
}

// awaitPaneWords waits until the pane of socket shows words.
func awaitPaneWords(t *testing.T, read func(...string) (string, error), socket, words string) {
	t.Helper()
	testwait.Until(t, "the pane to show "+words, func() (bool, string) {
		screen, err := read("capture-pane", "-p", "-t", socket+":")
		if err != nil {
			return false, err.Error()
		}
		return strings.Contains(screen, words), screen
	})
}

// watchResult is what a launch watch run on its own goroutine returned.
type watchResult struct {
	warnings []string
	err      error
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
	testjail.TmuxServer(t)
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
		retired := recordRetires(t)
		warnings, err := executor.watchLaunch(context.Background(), server, "boot probe", "claude")
		if err != nil || len(warnings) != 0 {
			t.Fatalf("watch of a booted chat = %q, %v; want no warning", warnings, err)
		}
		if len(*retired) != 0 {
			t.Fatalf("a booted chat's seat was retired: %q", *retired)
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
		// The launch prompt's turn repaints the working footer as the mock's
		// clock moves, a change the engine already running makes its own.
		const socket = "cc-911-1-1"
		fifo := filepath.Join(testjail.ShortRoot(t), "control")
		executor, server, read := launchWatchServer(
			t,
			socket,
			mockClaudeRun(t, `{"busy_ms":3600000,"control":`+strconv.Quote(fifo)+`}`)+" 'a launch prompt' || true",
		)
		executor.processes = RealProcesses{}
		looks := &launchPaneLooks{TmuxClient: executor.tmux}
		executor.tmux = looks
		quiet, deadline := launchQuiet, launchDeadline
		launchQuiet, launchDeadline = time.Hour, time.Hour
		t.Cleanup(func() { launchQuiet, launchDeadline = quiet, deadline })
		watched := make(chan watchResult, 1)
		go func() {
			warnings, err := executor.watchLaunch(testwait.Context(t), server, "shell probe", "claude")
			watched <- watchResult{warnings, err}
		}()
		// The engine painted its footer, then the watch began a look after one
		// that saw it running: the footer's next repaint is the engine's own.
		awaitPaneWords(t, read, socket, "Thinking… (0s")
		seen := looks.count.Load()
		testwait.Until(t, "the launch watch to look twice after the footer showed", func() (bool, string) {
			return looks.count.Load() >= seen+2, fmt.Sprintf(
				"%d looks, %d when the footer showed",
				looks.count.Load(),
				seen,
			)
		})
		controltest.AdvanceBy(t, fifo, time.Second)
		result, _ := testwait.Receive(t, "the launch watch to report the engine up", watched)
		if result.err != nil || len(result.warnings) != 0 {
			t.Fatalf(
				"watch of an engine under its shell = %q, %v; want it up with no warning",
				result.warnings,
				result.err,
			)
		}
		// The busy stretch ends by signal once the watch has seen it.
		controltest.AdvanceBy(t, fifo, time.Hour)
	})
	t.Run("an engine that dies under an earlier run's words", func(t *testing.T) {
		// A resume that died leaves its words and the fallback's on the pane, and
		// the engine the fallback starts runs on the pane's terminal without
		// painting before it dies too: the held screen is not that engine's, so
		// the open fails quoting the pane rather than reporting the chat up.
		const socket = "cc-912-1-1"
		// The gates stand where the pane's process used to pause: after its
		// words, and after the engine's last line, until tmux has read each.
		afterWords, afterCrash := newPaneGate(t), newPaneGate(t)
		fifo := filepath.Join(testjail.ShortRoot(t), "control")
		executor, server, read := launchWatchServer(t, socket, "echo 'resume refused — earlier words'; "+
			afterWords.wait()+"; "+
			mockClaudeRun(t, `{"launch_crash":"Error: crashed at launch","control":`+strconv.Quote(fifo)+`}`)+"; "+
			afterCrash.wait()+"; exit 1")
		executor.processes = RealProcesses{}
		quiet, deadline := launchQuiet, launchDeadline
		launchQuiet, launchDeadline = time.Hour, time.Hour
		t.Cleanup(func() { launchQuiet, launchDeadline = quiet, deadline })
		retired := recordRetires(t)
		watched := make(chan watchResult, 1)
		go func() {
			warnings, err := executor.watchLaunch(testwait.Context(t), server, "crash probe", "claude")
			watched <- watchResult{warnings, err}
		}()
		awaitPaneWords(t, read, socket, "resume refused — earlier words")
		afterWords.release(t)
		// The engine's runtime loads on the mock's clock before the fault it prints.
		if moved := controltest.Next(t, fifo); moved != 500*time.Millisecond {
			t.Fatalf(
				"the crashing engine's boot moved the clock %s, want 500ms (control log %s)",
				moved,
				control.LogPath(fifo),
			)
		}
		awaitPaneWords(t, read, socket, "Error: crashed at launch")
		afterCrash.release(t)
		result, _ := testwait.Receive(t, "the launch watch to report the dead pane", watched)
		warnings, err := result.warnings, result.err
		// tmux's own line on the dead pane, stamped with the time it died, ends the words once tmux drew it.
		want := regexp.MustCompile(`^the chat crash probe exited at launch — its pane said: resume refused — ` +
			`earlier words \| Error: crashed at launch( \| Pane is dead \(status 1, [^)]+\))?$`)
		if err == nil || !want.MatchString(err.Error()) {
			t.Fatalf("watch of an engine that dies unpainted = %q, %v; want the error %q", warnings, err, want)
		}
		if want := []string{socket}; !slices.Equal(*retired, want) {
			t.Fatalf("seats retired after the dead launch = %q, want %q", *retired, want)
		}
	})
	t.Run("a pane that exits at launch", func(t *testing.T) {
		const socket = "cc-910-1-1"
		// The first gate holds the words back until the watch has set
		// remain-on-exit; the second holds the exit after them: tmux 3.5 drops
		// output a process writes in the same instant it exits, remain-on-exit
		// or not, so only words tmux has read can be quoted.
		beforeWords, afterWords := newPaneGate(t), newPaneGate(t)
		script := beforeWords.wait() + "; echo 'pfm internal agent-open: refusing the double-resume' >&2; " +
			afterWords.wait() + "; exit 1"
		executor, server, read := launchWatchServer(t, socket, script)
		retired := recordRetires(t)
		watched := make(chan watchResult, 1)
		go func() {
			warnings, err := executor.watchLaunch(testwait.Context(t), server, "exit probe", "claude")
			watched <- watchResult{warnings, err}
		}()
		testwait.Until(t, "the watch to set remain-on-exit", func() (bool, string) {
			remain, err := read("display-message", "-p", "-t", socket+":", "#{remain-on-exit}")
			return strings.TrimSpace(remain) == "on", fmt.Sprintf("remain-on-exit %q, %v", remain, err)
		})
		beforeWords.release(t)
		awaitPaneWords(t, read, socket, "refusing the double-resume")
		afterWords.release(t)
		result, _ := testwait.Receive(t, "the launch watch to report the dead pane", watched)
		warnings, err := result.warnings, result.err
		if want := []string{socket}; !slices.Equal(*retired, want) {
			t.Fatalf("seats retired after a dead launch = %q, want %q", *retired, want)
		}
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
