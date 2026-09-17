package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestJailedPickerEscDoesNotWritePendingKillOrPrimarySwitch reproduces the
// picker-cancel bug: Esc/⌃C must cancel the interactive picker without
// applying a pending ⌃X kill or a pending ⌃S primary-account switch. It
// drives the REAL Bubble Tea picker inside a scratch tmux pane — the same
// subprocess-reexec technique TestJailedEvalAttachFromPlainAndNestedTmux uses
// (attach_e2e_test.go) — because the bug lives in the command loop wired
// around the picker (runLS), not in the pure Model in isolation.
func TestJailedPickerEscDoesNotWritePendingKillOrPrimarySwitch(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	jail := newPickerCancelJail(t)
	index := exec.Command(jail.binary, "index", "--full")
	index.Env = jail.env
	if output, err := index.CombinedOutput(); err != nil {
		t.Fatalf("index picker-cancel fixture: %v: %s", err, output)
	}

	socket := "probe-picker-cancel-" + strconv.Itoa(os.Getpid())
	marker := filepath.Join(jail.root, "done")
	scriptPath := filepath.Join(jail.root, "run.sh")
	script := "#!/bin/sh\n" +
		shellQuote(jail.binary) + " ls\n" +
		"echo RC=$? > " + shellQuote(marker) + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	session := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", "picker", scriptPath,
	)
	session.Env = jail.env
	if output, err := session.CombinedOutput(); err != nil {
		t.Fatalf("start picker session: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	})

	// The picker paints its cached first frame, then promotes the row once
	// the async gather goroutine finishes. Pressing keys before that settles
	// risks landing on a frame that has not drawn the row yet
	// (attach_e2e_test.go:proveAttach uses the same wait for the same reason).
	waitForTmuxPaneText(t, socket, jail.env, "PICKERCANCEL", 5*time.Second)
	for _, key := range []string{"C-x", "C-s", "Escape"} {
		send := exec.Command("tmux", "-L", socket, "send-keys", "-t", "picker", key)
		send.Env = jail.env
		if output, err := send.CombinedOutput(); err != nil {
			t.Fatalf("send-keys %s: %v: %s", key, err, output)
		}
		time.Sleep(300 * time.Millisecond)
	}

	if !waitForAttachFile(marker, 10*time.Second) {
		t.Fatal("picker did not exit after Esc")
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(content)); got != "RC=0" {
		t.Fatalf("picker exit = %q, want RC=0", got)
	}

	if _, err := os.Stat(filepath.Join(jail.home, ".claude-primary")); !os.IsNotExist(err) {
		t.Fatalf(
			"Esc wrote ~/.claude-primary after a pending ⌃S switch: stat err = %v",
			err,
		)
	}

	killed := exec.Command(jail.binary, "ls", "--killed")
	killed.Env = jail.env
	output, err := killed.CombinedOutput()
	if err != nil {
		t.Fatalf("pfm ls --killed: %v: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "" {
		t.Fatalf("Esc left a kill behind: %q", got)
	}
}

func waitForTmuxPaneText(t *testing.T, socket string, environment []string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		capture := exec.Command("tmux", "-L", socket, "capture-pane", "-p")
		capture.Env = environment
		output, err := capture.CombinedOutput()
		last = string(output)
		if err == nil && strings.Contains(last, want) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("tmux pane did not paint %q within %s; last capture=%q", want, timeout, last)
}

type pickerCancelJail struct {
	root   string
	home   string
	binary string
	env    []string
}

func newPickerCancelJail(t *testing.T) *pickerCancelJail {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "ccfpc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove picker-cancel jail: %v", err)
		}
	})
	home := filepath.Join(root, "h")
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	sidDir := filepath.Join(root, "sid")
	claudeRoot := filepath.Join(root, "claude")
	project := filepath.Join(root, "work", "picker-cancel-project")
	procRoot := filepath.Join(root, "proc")
	codexRoot := filepath.Join(root, "codex")
	for _, directory := range []string{
		filepath.Join(home, ".local", "bin"),
		tmuxDir,
		sidDir,
		claudeRoot,
		project,
		procRoot,
		codexRoot,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const id = "c2222222-2222-4222-8222-222222222222"
	transcriptDir := filepath.Join(claudeRoot, "picker-cancel-project")
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(transcriptDir, id+".jsonl")
	content := `{"type":"user","cwd":` + strconv.Quote(project) +
		`,"message":{"content":"PICKERCANCEL proof"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(home, ".local", "bin", "pfm")
	if err := os.Symlink(testPFMBinary, binary); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".local", "bin") + string(os.PathListSeparator) +
		os.Getenv("PATH")
	env := replaceAttachEnv(os.Environ(), map[string]string{
		"HOME":             home,
		"PATH":             path,
		"TERM":             "xterm-256color",
		"TMUX":             "",
		"TMUX_TMPDIR":      root,
		"PFM_HOME":         home,
		"PFM_DB":           filepath.Join(root, "fleet.db"),
		"PFM_SID_DIR":      sidDir,
		"PFM_CLAUDE_ROOTS": claudeRoot,
		"PFM_CODEX_ROOT":   codexRoot,
		"PFM_TMUX_DIR":     tmuxDir,
		"PFM_PROC_ROOT":    procRoot,
	})
	return &pickerCancelJail{
		root:   root,
		home:   home,
		binary: binary,
		env:    env,
	}
}
