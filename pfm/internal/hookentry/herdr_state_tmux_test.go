package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A real tmux server proves what the fake cannot: the chat-server options make
// a client attach copy the Herdr variables into the session environment (and a
// client without them remove them), the client-attached and client-detached
// hooks run the reporter with the client's pid and intact quoting, the
// reporter's tmux calls read what tmux prints, and the stored state and the
// client registry survive to a later attach.
func TestHerdrStateFollowsTheClientThatAttachedOnARealTmuxServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.ShortRoot(t)
	server := newFakeHerdr(t)
	server.addPane("w7:p3", "term_7")
	server.addPane("w8:p1", "term_8")
	environment := append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	tmux := func(args ...string) string {
		t.Helper()
		command := exec.Command("tmux", append([]string{"-L", "cc-herdr"}, args...)...)
		command.Env = environment
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	tmux("-f", "/dev/null", "new-session", "-d", "-s", "cc-herdr", "sleep", "120")
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", "cc-herdr", "kill-server")
		kill.Env = environment
		_ = kill.Run()
	})
	// The pfm binary the client-attached hook runs is a stub recording its
	// arguments one per line, so the hook's quoting is proven as tmux and the
	// shell actually split it.
	hookLog := filepath.Join(root, "hook.log")
	stub := filepath.Join(root, "pf m")
	if err := testjail.WriteExecutable(
		stub,
		[]byte(
			"#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done >> '"+hookLog+"'\necho END >> '"+hookLog+"'\n",
		),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	for _, option := range pfmconfig.ChatServerOptions(nil, stub) {
		if option[0] == "set-hook" || strings.HasPrefix(option[2], "update-environment[") {
			tmux(option...)
		}
	}
	socket := tmux("display-message", "-p", "-t", "cc-herdr", "#{socket_path}")

	attach := func(extra ...string) (detach func()) {
		t.Helper()
		before := tmux("display-message", "-p", "-t", "cc-herdr", "#{session_attached}")
		command := exec.Command("tmux", "-L", "cc-herdr", "-C", "attach", "-t", "cc-herdr")
		command.Env = append(append([]string{}, environment...), extra...)
		stdin, err := command.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Start(); err != nil {
			t.Fatalf("attach control client: %v", err)
		}
		waitForAttached(t, tmux, before, false)
		return func() {
			_ = stdin.Close()
			_ = command.Wait()
			waitForAttached(t, tmux, before, true)
		}
	}
	paneID := tmux("display-message", "-p", "-t", "cc-herdr", "#{pane_id}")
	panePID, err := strconv.Atoi(tmux("display-message", "-p", "-t", "cc-herdr", "#{pane_pid}"))
	if err != nil {
		t.Fatalf("pane pid of the chat pane: %v", err)
	}
	lockDir := filepath.Join(root, "lock")
	// The engine of the chat is the pane's own process; the hook is a pfm child.
	processes := map[int]fakeProc{100: {panePID, "pfm"}, panePID: {1, "sleep"}}
	report := func(args ...string) herdrRun {
		var stderr bytes.Buffer
		clock := time.Now().UnixNano()
		rc := reportToHerdr(
			append([]string{"--socket", socket, "--session", "cc-herdr"}, args...),
			strings.NewReader(`{"session_id":"s1"}`),
			&stderr,
			herdrDeps{
				getenv: func(name string) string {
					return map[string]string{"TMUX_PANE": paneID, "HERDR_BIN_PATH": "/usr/bin/herdr"}[name]
				},
				tmux:     runTmuxForHerdr,
				now:      func() time.Time { clock += 1000; return time.Unix(0, clock) },
				lockDir:  lockDir,
				lookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
				logError: func(session string, at time.Time, message string) error {
					return herdr.AppendError(lockDir, session, at, message)
				},
				procInfo: func(pid int) (int, string, error) {
					proc, ok := processes[pid]
					if !ok {
						return 0, "", fmt.Errorf("no pid %d in the fixture process table", pid)
					}
					return proc.parent, proc.command, nil
				},
				getpid: func() int { return 100 },
				// The tmux client's own environment, read from the real process.
				environ: gather.NewProcFS("").Environ,
			},
		)
		return herdrRun{rc: rc, stderr: stderr.String()}
	}
	herdrEnv := func(pane string) []string {
		return []string{
			"HERDR_ENV=1",
			"HERDR_PANE_ID=" + pane,
			"HERDR_SOCKET_PATH=" + server.path,
			"HERDR_BIN_PATH=/usr/bin/herdr",
		}
	}
	calls := func() []string { return server.calls() }
	clientPID := func() string {
		t.Helper()
		pids := strings.Fields(tmux("list-clients", "-t", "cc-herdr", "-F", "#{client_pid}"))
		if len(pids) != 1 {
			t.Fatalf("attached clients = %q, want exactly the one this step attached", pids)
		}
		return pids[0]
	}
	registry := func() map[string]herdrClient {
		t.Helper()
		var state herdrStored
		raw := tmux("show-options", "-qv", "-t", "cc-herdr", herdrStoredOption)
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			t.Fatalf("decode stored option %q: %v", raw, err)
		}
		return state.Clients
	}

	if got := report("--engine", "claude", "working"); got.rc != 0 || got.stderr != "" || len(calls()) != 0 {
		t.Fatalf("detached chat: rc=%d stderr=%q requests=%q, want a silent no-op", got.rc, got.stderr, calls())
	}
	if stored := tmux(
		"show-options",
		"-qv",
		"-t",
		"cc-herdr",
		herdrStoredOption,
	); !strings.Contains(
		stored,
		`"state":"working"`,
	) {
		t.Fatalf("stored option = %q, want the working state kept while detached", stored)
	}

	detach := attach(herdrEnv("w7:p3")...)
	first := clientPID()
	server.runIn("w7:p3", atoi(t, first))
	if got := report("attach", "--client", first); got.rc != 0 || got.stderr != "" {
		t.Fatalf("attach of %s: rc=%d stderr=%q", first, got.rc, got.stderr)
	}
	if got := registry()[first]; got.Pane != "w7:p3" || got.Terminal != "term_7" {
		t.Fatalf("registry[%s] = %+v, want pane w7:p3 of terminal term_7", first, got)
	}
	if got := calls(); len(got) != 3 || got[0] != listCall || !strings.Contains(got[2], `"pane_id":"w7:p3"`) ||
		!strings.Contains(got[2], `"state":"working"`) {
		t.Fatalf("attached from w7:p3: requests = %q, want pane.list and the stored working state to w7:p3", got)
	}
	if got := report("--engine", "claude", "idle"); got.rc != 0 || got.stderr != "" {
		t.Fatalf("idle event: rc=%d stderr=%q", got.rc, got.stderr)
	}
	if got := calls(); !strings.Contains(got[len(got)-1], `"pane_id":"w7:p3"`) ||
		!strings.Contains(got[len(got)-1], `"state":"idle"`) {
		t.Fatalf("requests = %q, want the idle report to w7:p3 last", got)
	}
	detach()
	before := len(calls())
	if got := report("detach"); got.rc != 0 || got.stderr != "" {
		t.Fatalf("detach sweep after %s left: rc=%d stderr=%q", first, got.rc, got.stderr)
	}
	released := calls()[before:]
	if len(released) != 3 || !strings.HasPrefix(released[1], "pane.release_agent") ||
		!strings.Contains(released[2], `"clear_title":true`) || !strings.Contains(released[1], `"pane_id":"w7:p3"`) {
		t.Fatalf("requests after the detach = %q, want pane.list, then w7:p3 released and its title cleared", released)
	}
	if clients := registry(); len(clients) != 0 {
		t.Fatalf("registry after the detach = %+v, want it empty", clients)
	}

	before = len(calls())
	if got := report("--engine", "claude", "working"); got.rc != 0 || len(calls()) != before {
		t.Fatalf(
			"after detach: rc=%d new=%d, want no report to a pane nobody is attached from",
			got.rc,
			len(calls())-before,
		)
	}

	detach = attach(herdrEnv("w8:p1")...)
	second := clientPID()
	server.runIn("w8:p1", atoi(t, second))
	before = len(calls())
	if got := report("attach", "--client", second); got.rc != 0 || got.stderr != "" {
		t.Fatalf("attach of %s: rc=%d stderr=%q", second, got.rc, got.stderr)
	}
	if got := calls()[before:]; len(got) != 3 || !strings.Contains(got[2], `"pane_id":"w8:p1"`) ||
		!strings.Contains(got[2], `"state":"working"`) {
		t.Fatalf("re-attached from w8:p1: requests = %q, want the stored working state synced to w8:p1 only", got)
	}
	detach()
	if got := report("detach"); got.rc != 0 || got.stderr != "" {
		t.Fatalf("detach sweep after %s left: rc=%d stderr=%q", second, got.rc, got.stderr)
	}

	detach = attach()
	third := clientPID()
	before = len(calls())
	if got := report("attach", "--client", third); got.rc != 0 || got.stderr != "" ||
		len(calls()) != before || len(registry()) != 0 {
		t.Fatalf(
			"attached from a terminal without Herdr: rc=%d stderr=%q new=%d registry=%+v, want the removed variables to register nothing",
			got.rc,
			got.stderr,
			len(calls())-before,
			registry(),
		)
	}
	detach()

	// A client that attached while no hook ran for it (a chat born before the
	// hooks, or across a Herdr restart) is adopted by the next event: its own
	// environment says Herdr, the pane running its pid is the pane reported to.
	detach = attach(herdrEnv("w7:p3")...)
	fourth := clientPID()
	server.foreground("w7:p3", atoi(t, fourth))
	before = len(calls())
	if got := report("--engine", "claude", "working"); got.rc != 0 || got.stderr != "" {
		t.Fatalf("working event with %s unregistered: rc=%d stderr=%q", fourth, got.rc, got.stderr)
	}
	if got := calls()[before:]; len(got) != 3 || got[0] != listCall || !strings.Contains(got[2], `"pane_id":"w7:p3"`) ||
		!strings.Contains(got[2], `"state":"working"`) {
		t.Fatalf(
			"event with an unregistered client: requests = %q, want pane.list, its pane probed and w7:p3 reported",
			got,
		)
	}
	if got := registry()[fourth]; got.Pane != "w7:p3" || got.Terminal != "term_7" {
		t.Fatalf("registry[%s] = %+v, want the adopted pane w7:p3 of terminal term_7", fourth, got)
	}
	detach()
	if got := report("detach"); got.rc != 0 || got.stderr != "" {
		t.Fatalf("detach sweep after %s left: rc=%d stderr=%q", fourth, got.rc, got.stderr)
	}

	// The hooks tmux itself ran: an attach (and a resize's sync) carries the
	// client's pid, a detach cannot
	// (tmux expands #{client_pid} to nothing once the client is gone), and the
	// quoting is intact in both.
	var seen map[string]bool
	deadline := time.Now().Add(5 * time.Second)
	for {
		logged, _ := os.ReadFile(hookLog)
		seen = map[string]bool{}
		for _, call := range strings.Split(strings.TrimSuffix(string(logged), "END\n"), "END\n") {
			if len(logged) == 0 {
				break
			}
			argv := strings.Split(strings.TrimSuffix(call, "\n"), "\n")
			if len(argv) < 7 || argv[0] != "internal" || argv[1] != "herdr-state" || argv[3] != "--socket" ||
				argv[4] != socket || argv[5] != "--session" || argv[6] != "cc-herdr" {
				t.Fatalf(
					"client hook ran the pfm binary with %q, want internal herdr-state {attach|sync|detach} --socket %s --session cc-herdr ...",
					argv,
					socket,
				)
			}
			switch {
			case (argv[2] == "attach" || argv[2] == "sync") && len(argv) == 9 && argv[7] == "--client":
				seen[argv[2]+" "+argv[8]] = true
			case argv[2] == "detach" && (len(argv) == 7 || len(argv) == 8 && argv[7] == "--client"):
				seen["detach"] = true
			default:
				t.Fatalf(
					"client hook ran the pfm binary with %q, want an attach or sync with the client pid or a detach without one",
					argv,
				)
			}
		}
		if seen["attach "+first] && seen["attach "+second] && seen["detach"] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("client hooks seen = %v, want an attach for clients %s and %s and a detach", seen, first, second)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForAttached polls session_attached until it differs from before (a
// client attached) or equals it again (the client left).
func waitForAttached(t *testing.T, tmux func(...string) string, before string, wantBack bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		now := tmux("display-message", "-p", "-t", "cc-herdr", "#{session_attached}")
		if (now == before) == wantBack {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session_attached never reached the wanted state (before=%s, back=%t)", before, wantBack)
}

func atoi(t *testing.T, text string) int {
	t.Helper()
	value, err := strconv.Atoi(text)
	if err != nil {
		t.Fatalf("parse %q as a pid: %v", text, err)
	}
	return value
}

// The cost the adoption adds to every reporter run when the registry holds no
// client for a live one (D2): one tmux list-clients and one environment read
// of the unregistered client, measured on a real tmux server and a real
// process. The logged median is what the docs print; the bound is the D2 flip
// (20 ms) with room for a loaded fence.
func TestHerdrStateAdoptionCostPerEvent(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.ShortRoot(t)
	environment := append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	tmux := func(args ...string) string {
		t.Helper()
		command := exec.Command("tmux", append([]string{"-L", "cc-herdr-cost"}, args...)...)
		command.Env = environment
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	tmux("-f", "/dev/null", "new-session", "-d", "-s", "cc-herdr-cost", "sleep", "120")
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", "cc-herdr-cost", "kill-server")
		kill.Env = environment
		_ = kill.Run()
	})
	socket := tmux("display-message", "-p", "-t", "cc-herdr-cost", "#{socket_path}")
	environ := gather.NewProcFS("").Environ
	const runs = 21
	took := make([]time.Duration, 0, runs)
	for range runs {
		start := time.Now()
		if _, err := runTmuxForHerdr(
			context.Background(), socket, "list-clients", "-t", "cc-herdr-cost", "-F", "#{client_pid}",
		); err != nil {
			t.Fatal(err)
		}
		if _, err := environ(os.Getpid()); err != nil {
			t.Fatalf("read the environment of the test process: %v", err)
		}
		took = append(took, time.Since(start))
	}
	slices.Sort(took)
	median := took[runs/2]
	t.Logf("adoption cost per event, median of %d: %v (list-clients + one environment read)", runs, median)
	if median > 100*time.Millisecond {
		t.Fatalf("adoption cost per event = %v, want well under the 20 ms D2 flip", median)
	}
}
