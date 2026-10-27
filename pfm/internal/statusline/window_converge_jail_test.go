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
			"TMUX":                   socketPath + ",1,0",
			"TMUX_PANE":              pane,
			"PFM_TEST_PROBE_SOCKETS": "1",
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

	// A resume starts the chat on a new tmux server ($TMUX names a new server
	// pid) under the same session id and name: the new server lacks
	// @pfm_chat_name, so the cached name is no convergence there.
	unset := exec.Command("tmux", "-S", socketPath, "set-option", "-u", "-t", socket, "@pfm_chat_name")
	unset.Env = append(os.Environ(), "TMUX=")
	if output, err := unset.CombinedOutput(); err != nil {
		t.Fatalf("unset @pfm_chat_name: %v: %s", err, output)
	}
	runtime.Env["TMUX"] = socketPath + ",2,0"
	if _, err := Render(context.Background(), []byte(payload), runtime); err != nil {
		t.Fatal(err)
	}
	if got := probeWindowField(t, socketPath, "#{@pfm_chat_name}"); got != "RENAMED-BY-STATUSLINE" {
		t.Fatalf("@pfm_chat_name on the resumed chat's server = %q, want the exact chat name", got)
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

// D8: the cache keys on the exact name. A name that differs only past the
// 24-rune window-name clip clips to the SAME label, yet it is a rename for the
// Herdr reporter: the combined rename-window + @pfm_chat_name command runs once
// more (the window-renamed hook fires), and a converged render still forks
// nothing.
func TestRenderRenamesWhenOnlyTheExactNameChangesPastTheClip(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.ShortRoot(t)
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const socket = "probe-cc-1800000008-1-1"
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
	hookLog := filepath.Join(root, "renamed.log")
	hook := exec.Command(
		"tmux", "-S", socketPath, "set-hook", "-g", "window-renamed[50]",
		`run-shell "printf '%s\n' '#{@pfm_chat_name}' >> `+hookLog+`"`,
	)
	hook.Env = append(os.Environ(), "TMUX=")
	if output, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("install the window-renamed probe hook: %v: %s", err, output)
	}
	pane := probeWindowField(t, socketPath, "#{pane_id}")

	runner := &countingRunner{}
	runtime := Runtime{
		Now: time.Now, Home: root, ConfigDir: filepath.Join(root, ".claude"),
		CacheDir: filepath.Join(root, "cache"), RateLimitDir: filepath.Join(root, "rates"),
		SIDDir: filepath.Join(root, "sid"), TmuxDir: tmuxDir,
		ProcRoot: filepath.Join(root, "proc"), Engine: pfmengine.Claude,
		Command: runner,
		Env: map[string]string{
			"TMUX": socketPath + ",1,0", "TMUX_PANE": pane, "PFM_TEST_PROBE_SOCKETS": "1",
		},
	}
	first := "HerDer_Ultimate_Integration"
	second := first + "_v2"
	if gather.WindowNameFor(first) != gather.WindowNameFor(second) {
		t.Fatalf(
			"fixture names must clip to one label: %q vs %q",
			gather.WindowNameFor(first), gather.WindowNameFor(second),
		)
	}
	render := func(name string) {
		t.Helper()
		if _, err := Render(context.Background(), []byte(
			`{"session_id":"probe-exact","session_name":"`+name+`"}`,
		), runtime); err != nil {
			t.Fatal(err)
		}
	}
	hookRuns := func(want []string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			logged, _ := os.ReadFile(hookLog)
			got := strings.Fields(string(logged))
			if strings.Join(got, "\n") == strings.Join(want, "\n") {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("window-renamed hook runs saw @pfm_chat_name %q, want %q", got, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	render(first)
	hookRuns([]string{first})
	render(second)
	if got := probeWindowField(t, socketPath, "#{window_name}"); got != gather.WindowNameFor(second) {
		t.Fatalf("window name = %q, want the shared clip %q", got, gather.WindowNameFor(second))
	}
	show := exec.Command("tmux", "-S", socketPath, "show-options", "-v", "-t", socket, "@pfm_chat_name")
	show.Env = append(os.Environ(), "TMUX=")
	output, err := show.Output()
	if err != nil || strings.TrimSpace(string(output)) != second {
		t.Fatalf("@pfm_chat_name = %q (%v), want the exact new name %q", output, err, second)
	}
	hookRuns([]string{first, second})

	converged := runner.tmux
	render(second)
	if runner.tmux != converged {
		t.Fatalf("a converged render made %d more tmux calls, want none", runner.tmux-converged)
	}
}
