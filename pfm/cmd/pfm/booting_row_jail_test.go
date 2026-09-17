package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestBootingRowJailedTSV is the red-first, gather-through-render proof for
// the booting-chat blind spot (TESTPLAN.md "Residual known divergences" #9):
// a chat still sitting at a startup prompt has a live pane and a live claude
// process, but no SID crumb — the statusline writes that only once boot
// completes. Before this fix, crumb-driven gather was blind to it and
// `pfm ls` showed nothing at all for the socket; run against the
// pre-fix tree this test failed with the socket entirely absent from the TSV
// output. It drives a REAL tmux pane and REAL (jailed) /proc entries — no
// fake ProcFS or TmuxClient — exactly like
// TestKillCLIVouchesEngineForUnindexedButVisibleRows, so the fix under test
// is gather's actual pane-and-process walk, not a stand-in for it.
//
// Covers fixtures (a), (b), and (d): a fresh epoch-shaped socket with no
// crumb rows Booting; the same socket loses its Booting row and gains an
// ordinary live-claude row (never both, never neither) the moment a crumb
// appears; a cc-new-* socket rows Booting too and is untouched by the first
// socket's crumb.
func TestBootingRowJailedTSV(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newKillCLIJail(t)
	sidDir := filepath.Join(jail.root, "sid")

	epochSocket := "cc-" + strconv.FormatInt(time.Now().Unix(), 10) +
		"-" + strconv.Itoa(os.Getpid()) + "-1"
	newSocket := "cc-new-fixture-1"

	epochPanePID := startBootingPane(t, epochSocket)
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid:       92001,
		parentPID: epochPanePID,
		comm:      "claude",
		cmdline:   []string{"/opt/claude"},
	})
	newPanePID := startBootingPane(t, newSocket)
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid:       92002,
		parentPID: newPanePID,
		comm:      "claude",
		cmdline:   []string{"/opt/claude"},
	})

	// (a) + (d): neither socket has a crumb yet — both must row as booting,
	// keyed on the socket itself.
	rows := bootingRowsFromTSV(t)
	if got := rows[epochSocket]; got != "booting" {
		t.Fatalf("epoch-shaped socket kind = %q, want booting (rows=%v)", got, rows)
	}
	if got := rows[newSocket]; got != "booting" {
		t.Fatalf("cc-new-* socket kind = %q, want booting (rows=%v)", got, rows)
	}

	// (b): a crumb lands on the epoch socket's pane. Its booting row must
	// vanish and an ordinary live-claude row take over — never both, never
	// neither — while the untouched cc-new-* socket stays booting.
	paneID := strings.TrimSpace(runTmuxOutput(t, epochSocket, "list-panes", "-F", "#{pane_id}"))
	crumb := filepath.Join(sidDir, epochSocket+"."+paneID)
	if err := os.WriteFile(crumb, []byte("/nonexistent/booted.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rows = bootingRowsFromTSV(t)
	if got := rows[epochSocket]; got != "live-claude" {
		t.Fatalf("crumbed socket kind = %q, want live-claude (rows=%v)", got, rows)
	}
	if got := rows[newSocket]; got != "booting" {
		t.Fatalf("untouched cc-new-* socket kind = %q, want booting (rows=%v)", got, rows)
	}
}

// A named chat is addressable from the moment its process exists, before its
// first prompt creates a transcript or statusline crumb. This is the exact
// promptless `pfm chat new --name` window other chats must resolve.
func TestPromptlessNamedChatResolvesByItsLaunchName(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newKillCLIJail(t)
	socket := "cc-" + strconv.FormatInt(time.Now().Unix(), 10) +
		"-" + strconv.Itoa(os.Getpid()) + "-17"
	panePID := startNamedBootingPane(t, socket, "Promptless worker")
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid:       92017,
		parentPID: panePID,
		comm:      "claude",
		cmdline:   []string{"/opt/claude"},
	})

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "resolve", "Promptless worker"}, &stdout, &stderr); code != 0 {
		t.Fatalf("resolve promptless name rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), socket) {
		t.Fatalf("resolved promptless chat = %q, want socket %q", stdout.String(), socket)
	}
}

func startBootingPane(t *testing.T, socket string) int {
	return startNamedBootingPane(t, socket, socket)
}

func startNamedBootingPane(t *testing.T, socket, window string) int {
	t.Helper()
	session := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "-n", window, "sleep", "120",
	)
	if output, err := session.CombinedOutput(); err != nil {
		t.Fatalf("start jailed pane %q: %v: %s", socket, err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	})
	panePID, err := strconv.Atoi(strings.TrimSpace(
		runTmuxOutput(t, socket, "list-panes", "-F", "#{pane_pid}"),
	))
	if err != nil {
		t.Fatalf("parse pane pid for %q: %v", socket, err)
	}
	return panePID
}

func runTmuxOutput(t *testing.T, socket string, arguments ...string) string {
	t.Helper()
	args := append([]string{"-L", socket}, arguments...)
	output, err := exec.Command("tmux", args...).Output()
	if err != nil {
		t.Fatalf("tmux -L %s %s: %v", socket, strings.Join(arguments, " "), err)
	}
	return string(output)
}

func bootingRowsFromTSV(t *testing.T) map[string]string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"ls", "--tsv", "--all"}, &stdout, &stderr); code != 0 {
		t.Fatalf("pfm ls --tsv --all code=%d stderr=%s", code, stderr.String())
	}
	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(stdout.String()))
	header := true
	for scanner.Scan() {
		if header {
			header = false
			continue
		}
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 11 {
			continue
		}
		kind, socket := fields[0], fields[10]
		if socket != "" {
			result[socket] = kind
		}
	}
	return result
}

// TestBootingRowInteractivePickerJailed is fixture (c): a REAL interactive
// picker inside a scratch tmux pane. ⌃X on the booting row must be a no-op —
// no killed-store write, no non-zero exit on quit — and Enter must
// synthesize the same Live attach line an ordinary live row gets and really
// execute it, the same subprocess-reexec and raw/inside-tmux technique
// attach_jail_test.go's proveAttach uses (stdout is a real tty here, so
// dispatchAction's own terminal branch execs tmux directly — no zsh/eval
// wrapper is needed for a TMUX= line).
func TestBootingRowInteractivePickerJailed(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newBootingPickerJail(t)

	t.Run("ctrl-x is a no-op", func(t *testing.T) {
		targetSocket := "cc-" + strconv.FormatInt(time.Now().Unix(), 10) +
			"-" + strconv.Itoa(os.Getpid()) + "-3"
		jail.startTarget(t, targetSocket)
		jail.addBootingProcess(t, targetSocket, 92101)

		marker := filepath.Join(jail.root, "kill.done")
		scriptPath := filepath.Join(jail.root, "kill.sh")
		script := "#!/bin/sh\n" + shellQuote(jail.binary) + " ls\n" +
			"echo RC=$? > " + shellQuote(marker) + "\n"
		if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		driverSocket := "driver-boot-kill-" + strconv.Itoa(os.Getpid())
		driver := exec.Command(
			"tmux", "-L", driverSocket, "-f", "/dev/null",
			"new-session", "-d", "-s", "driver", scriptPath,
		)
		driver.Env = jail.env
		if output, err := driver.CombinedOutput(); err != nil {
			t.Fatalf("start kill-probe driver: %v: %s", err, output)
		}
		t.Cleanup(func() {
			_ = jail.tmux(driverSocket, "kill-server").Run()
		})

		// The picker paints its cached first frame, then promotes the row once
		// the async gather goroutine finds the live pane; pressing keys before
		// that settles risks landing on a frame that has not drawn it yet
		// (attach_jail_test.go:proveAttach uses the same wait for the same
		// reason). Down moves off row 0 ("New Claude chat") onto the booting
		// row — the only other row this minimal jail composes. Wait for "◐",
		// the marker rowMarker() (internal/ui/render.go) paints ONLY for
		// compose.Booting — the row itself never paints the word "booting".
		waitForTmuxPaneText(t, driverSocket, jail.env, "◐", 5*time.Second)
		for _, key := range []string{"Down", "C-x", "Escape"} {
			send := jail.tmux(driverSocket, "send-keys", "-t", "driver:0.0", key)
			if output, err := send.CombinedOutput(); err != nil {
				t.Fatalf("send-keys %s: %v: %s", key, err, output)
			}
			time.Sleep(300 * time.Millisecond)
		}
		if !waitForAttachFile(marker, 10*time.Second) {
			t.Fatal("kill-probe picker did not exit after Escape")
		}
		if content, err := os.ReadFile(marker); err != nil ||
			strings.TrimSpace(string(content)) != "RC=0" {
			t.Fatalf("kill-probe picker exit marker = %q, err=%v", content, err)
		}
		killed := exec.Command(jail.binary, "ls", "--killed")
		killed.Env = jail.env
		output, err := killed.CombinedOutput()
		if err != nil {
			t.Fatalf("pfm ls --killed: %v: %s", err, output)
		}
		if got := strings.TrimSpace(string(output)); got != "" {
			t.Fatalf("⌃X on the booting row wrote a kill: %q", got)
		}
	})

	t.Run("enter attaches", func(t *testing.T) {
		targetSocket := "cc-" + strconv.FormatInt(time.Now().Unix(), 10) +
			"-" + strconv.Itoa(os.Getpid()) + "-4"
		jail.startTarget(t, targetSocket)
		jail.addBootingProcess(t, targetSocket, 92102)

		scriptPath := filepath.Join(jail.root, "attach.sh")
		script := "#!/bin/sh\nexec " + shellQuote(jail.binary) + " ls\n"
		if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		driverSocket := "driver-boot-attach-" + strconv.Itoa(os.Getpid())
		driver := exec.Command(
			"tmux", "-L", driverSocket, "-f", "/dev/null",
			"new-session", "-d", "-s", "driver", scriptPath,
		)
		driver.Env = jail.env
		if output, err := driver.CombinedOutput(); err != nil {
			t.Fatalf("start attach driver: %v: %s", err, output)
		}
		t.Cleanup(func() {
			_ = jail.tmux(driverSocket, "kill-server").Run()
		})

		// Same wait as above: "◐" is the row's own painted marker, not the
		// word "booting".
		waitForTmuxPaneText(t, driverSocket, jail.env, "◐", 5*time.Second)
		for _, key := range []string{"Down", "Enter"} {
			send := jail.tmux(driverSocket, "send-keys", "-t", "driver:0.0", key)
			if output, err := send.CombinedOutput(); err != nil {
				t.Fatalf("send-keys %s: %v: %s", key, err, output)
			}
			time.Sleep(300 * time.Millisecond)
		}

		deadline := time.Now().Add(20 * time.Second)
		var clientSession string
		for time.Now().Before(deadline) {
			output, listErr := jail.tmux(
				targetSocket, "list-clients", "-F", "#{client_session}",
			).Output()
			if listErr == nil {
				clientSession = strings.TrimSpace(string(output))
				if clientSession != "" {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		if clientSession != targetSocket {
			t.Fatalf(
				"Enter on the booting row did not attach: client_session=%q, want %q",
				clientSession,
				targetSocket,
			)
		}
		if output, err := jail.tmux(targetSocket, "detach-client", "-a").CombinedOutput(); err != nil {
			t.Fatalf("detach attach proof client: %v: %s", err, output)
		}
	})
}

type bootingPickerJail struct {
	root     string
	procRoot string
	binary   string
	env      []string
}

// newBootingPickerJail wires the same subprocess-reexec technique
// attach_jail_test.go's newAttachJail uses, minus its pre-seeded transcript:
// this jail's Claude store starts empty, so the interactive picker composes
// exactly ["New Claude chat", the one booting row under test] — nothing else
// to navigate around.
func newBootingPickerJail(t *testing.T) *bootingPickerJail {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "ccfbp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove booting-picker jail: %v", err)
		}
	})
	home := filepath.Join(root, "h")
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	sidDir := filepath.Join(root, "sid")
	claudeRoot := filepath.Join(root, "claude")
	codexHome := filepath.Join(root, "codex")
	procRoot := filepath.Join(root, "proc")
	for _, directory := range []string{
		filepath.Join(home, ".local", "bin"),
		tmuxDir, sidDir, claudeRoot, codexHome, procRoot,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := "#!/bin/sh\nexec " + shellQuote(executable) +
		" -test.run '^TestPFMAttachHelper$' -- \"$@\"\n"
	binary := filepath.Join(home, ".local", "bin", "pfm")
	if err := os.WriteFile(binary, []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".local", "bin") + string(os.PathListSeparator) +
		os.Getenv("PATH")
	env := replaceAttachEnv(os.Environ(), map[string]string{
		attachHelperEnv:    "1",
		"HOME":             home,
		"PATH":             path,
		"TERM":             "xterm-256color",
		"TMUX":             "",
		"TMUX_TMPDIR":      root,
		"PFM_HOME":         home,
		"PFM_DB":           filepath.Join(root, "fleet.db"),
		"PFM_SID_DIR":      sidDir,
		"PFM_CLAUDE_ROOTS": claudeRoot,
		"PFM_CODEX_ROOT":   codexHome,
		"PFM_TMUX_DIR":     tmuxDir,
		"PFM_PROC_ROOT":    procRoot,
	})
	return &bootingPickerJail{root: root, procRoot: procRoot, binary: binary, env: env}
}

func (jail *bootingPickerJail) tmux(socket string, arguments ...string) *exec.Cmd {
	args := append([]string{"-L", socket}, arguments...)
	command := exec.Command("tmux", args...)
	command.Env = jail.env
	return command
}

func (jail *bootingPickerJail) startTarget(t *testing.T, socket string) {
	t.Helper()
	if output, err := jail.tmux(
		socket, "-f", "/dev/null", "new-session", "-d", "-s", socket, "sleep", "120",
	).CombinedOutput(); err != nil {
		t.Fatalf("start booting target %q: %v: %s", socket, err, output)
	}
	t.Cleanup(func() {
		_ = jail.tmux(socket, "kill-server").Run()
	})
}

func (jail *bootingPickerJail) addBootingProcess(t *testing.T, socket string, pid int) {
	t.Helper()
	paneOutput, err := jail.tmux(socket, "list-panes", "-F", "#{pane_pid}").Output()
	if err != nil {
		t.Fatal(err)
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(string(paneOutput)))
	if err != nil {
		t.Fatalf("parse pane pid for %q: %v", socket, err)
	}
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid:       pid,
		parentPID: panePID,
		comm:      "claude",
		cmdline:   []string{"/opt/claude"},
	})
}
