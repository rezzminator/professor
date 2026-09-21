package statusline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// countingRunner is the real command runner with a call counter around it, so
// a fixture can assert the render forked NOTHING once the window is converged.
type countingRunner struct {
	inner commandRunner
	tmux  int
}

func (runner *countingRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "tmux" {
		runner.tmux++
	}
	return runner.inner.Output(ctx, name, args...)
}

// A claude /rename reaches the window name on the next render, not on the next
// quarter hour. The fixture runs a real render against a probe tmux server —
// never a fleet socket — and reads the window name back off it.
func TestRenderConvergesTheWindowNameOnAProbeSocket(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.ShortRoot(t)
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}

	const socket = "probe-cc-1800000001-1-1"
	socketPath := filepath.Join(tmuxDir, socket)
	start := exec.Command(
		"tmux", "-S", socketPath, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "-n", "before", "sleep 120",
	)
	start.Env = append(os.Environ(), "TMUX=")
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", socketPath, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
	pane := probeWindowField(t, socketPath, "#{pane_id}")

	runner := &countingRunner{}
	runtime := Runtime{
		Now:          time.Now,
		Home:         root,
		ConfigDir:    filepath.Join(root, ".claude"),
		CacheDir:     filepath.Join(root, "cache"),
		RateLimitDir: filepath.Join(root, "rates"),
		SIDDir:       filepath.Join(root, "sid"),
		TmuxDir:      tmuxDir,
		ProcRoot:     filepath.Join(root, "proc"),
		Engine:       pfmengine.Claude,
		Command:      runner,
		Env: map[string]string{
			"TMUX":                    socketPath + ",1,0",
			"TMUX_PANE":               pane,
			"PFM_TEST_PROBE_SOCKETS":  "1",
			"FORCE_PROMPT_CACHING_5M": "",
		},
	}
	payload := `{"session_id":"probe-session","session_name":"RENAMED-BY-STATUSLINE"}`

	if _, err := Render(context.Background(), []byte(payload), runtime); err != nil {
		t.Fatal(err)
	}
	if got := probeWindowField(t, socketPath, "#{window_name}"); got != "RENAMED-BY-STATUSLINE" {
		t.Fatalf("window name = %q, want the rendered 🔖 label", got)
	}
	firstPass := runner.tmux
	if firstPass == 0 {
		t.Fatal("the convergence never asked tmux anything")
	}

	// Second render, same label: already converged, so it must not fork tmux.
	if _, err := Render(context.Background(), []byte(payload), runtime); err != nil {
		t.Fatal(err)
	}
	if runner.tmux != firstPass {
		t.Fatalf("a converged render made %d more tmux calls, want none", runner.tmux-firstPass)
	}

	// A new label converges again on the very next render.
	if _, err := Render(context.Background(), []byte(
		`{"session_id":"probe-session","session_name":"RENAMED-AGAIN"}`,
	), runtime); err != nil {
		t.Fatal(err)
	}
	if got := probeWindowField(t, socketPath, "#{window_name}"); got != "RENAMED-AGAIN" {
		t.Fatalf("window name = %q, want RENAMED-AGAIN", got)
	}
}

// A window holding two panes — /chat:branch siblings — cannot carry both
// labels, so the statusline leaves it exactly as name-sync's own claude half
// does: alone.
func TestRenderLeavesASharedWindowAlone(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.ShortRoot(t)
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const socket = "probe-cc-1800000002-1-1"
	socketPath := filepath.Join(tmuxDir, socket)
	start := exec.Command(
		"tmux", "-S", socketPath, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "-n", "SHARED", "sleep 120",
	)
	start.Env = append(os.Environ(), "TMUX=")
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", socketPath, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
	split := exec.Command("tmux", "-S", socketPath, "split-window", "-d", "sleep 120")
	split.Env = append(os.Environ(), "TMUX=")
	if output, err := split.CombinedOutput(); err != nil {
		t.Fatalf("split the window: %v: %s", err, output)
	}
	pane := probeWindowField(t, socketPath, "#{pane_id}")

	runtime := Runtime{
		Now: time.Now, Home: root, ConfigDir: filepath.Join(root, ".claude"),
		CacheDir: filepath.Join(root, "cache"), RateLimitDir: filepath.Join(root, "rates"),
		SIDDir: filepath.Join(root, "sid"), TmuxDir: tmuxDir,
		ProcRoot: filepath.Join(root, "proc"), Engine: pfmengine.Claude,
		Command: commandRunner{},
		Env: map[string]string{
			"TMUX": socketPath + ",1,0", "TMUX_PANE": pane, "PFM_TEST_PROBE_SOCKETS": "1",
		},
	}
	if _, err := Render(context.Background(), []byte(
		`{"session_id":"probe-shared","session_name":"ONE-SIBLING"}`,
	), runtime); err != nil {
		t.Fatal(err)
	}
	if got := probeWindowField(t, socketPath, "#{window_name}"); got != "SHARED" {
		t.Fatalf("window name = %q, want SHARED — a two-pane window must keep its name", got)
	}
}

// Outside a fleet socket the render must not touch tmux at all: a statusline
// rendered in a bare terminal, or on a codex socket, owns no window name.
func TestRenderOutsideAFleetSocketNeverForksTmux(t *testing.T) {
	root := t.TempDir()
	for _, environment := range []map[string]string{
		{},
		{"TMUX": "/elsewhere/cc-1800000003-1-1,1,0", "TMUX_PANE": "%0"},
		{"TMUX": filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()), "cx-1800000004-1-1") + ",1,0", "TMUX_PANE": "%0"},
	} {
		runner := &countingRunner{}
		runtime := Runtime{
			Now: time.Now, Home: root, ConfigDir: filepath.Join(root, ".claude"),
			CacheDir: filepath.Join(root, "cache"), RateLimitDir: filepath.Join(root, "rates"),
			SIDDir: filepath.Join(root, "sid"), TmuxDir: filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())),
			ProcRoot: filepath.Join(root, "proc"), Engine: pfmengine.Claude,
			Command: runner, Env: environment,
		}
		if _, err := Render(context.Background(), []byte(
			`{"session_id":"probe-none","session_name":"NO-WINDOW-HERE"}`,
		), runtime); err != nil {
			t.Fatal(err)
		}
		if runner.tmux != 0 {
			t.Fatalf("environment %v made %d tmux calls, want none", environment, runner.tmux)
		}
	}
}

func probeWindowField(t *testing.T, socketPath, format string) string {
	t.Helper()
	command := exec.Command("tmux", "-S", socketPath, "display-message", "-p", format)
	command.Env = append(os.Environ(), "TMUX=")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read %s: %v: %s", format, err, output)
	}
	return strings.TrimSpace(string(output))
}

// The two writers must agree to the rune. A label longer than a window name
// converges to gather.WindowNameFor's clip, so the next name-sync pass sees a
// converged window instead of renaming it back every 15 minutes forever.
func TestRenderConvergesToTheSameClipNameSyncUses(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.ShortRoot(t)
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const socket = "probe-cc-1800000007-1-1"
	socketPath := filepath.Join(tmuxDir, socket)
	start := exec.Command(
		"tmux", "-S", socketPath, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "-n", "before", "sleep 120",
	)
	start.Env = append(os.Environ(), "TMUX=")
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", socketPath, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
	pane := probeWindowField(t, socketPath, "#{pane_id}")

	label := strings.Repeat("界", 30)
	runtime := Runtime{
		Now: time.Now, Home: root, ConfigDir: filepath.Join(root, ".claude"),
		CacheDir: filepath.Join(root, "cache"), RateLimitDir: filepath.Join(root, "rates"),
		SIDDir: filepath.Join(root, "sid"), TmuxDir: tmuxDir,
		ProcRoot: filepath.Join(root, "proc"), Engine: pfmengine.Claude,
		Command: commandRunner{},
		Env: map[string]string{
			"TMUX": socketPath + ",1,0", "TMUX_PANE": pane, "PFM_TEST_PROBE_SOCKETS": "1",
		},
	}
	if _, err := Render(context.Background(), []byte(
		`{"session_id":"probe-clip","session_name":"`+label+`"}`,
	), runtime); err != nil {
		t.Fatal(err)
	}
	want := gather.WindowNameFor(label)
	if got := probeWindowField(t, socketPath, "#{window_name}"); got != want {
		t.Fatalf("window name = %q, want the 24-rune clip %q", got, want)
	}
}
