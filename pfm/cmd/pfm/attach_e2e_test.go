package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/testjail"
)

const attachHelperEnv = "PFM_ATTACH_HELPER"

// TestPFMAttachHelper turns the package test binary into the real CLI for
// subprocess integration tests. Keeping the CLI inside the test process is
// load-bearing for process-table fixtures: TestMain installs the jail's
// package-level seams before run dispatches.
func TestPFMAttachHelper(_ *testing.T) {
	if os.Getenv(attachHelperEnv) != "1" {
		return
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		os.Exit(97)
	}
	os.Exit(run(os.Args[separator+1:], os.Stdout, os.Stderr))
}

type attachJail struct {
	root       string
	home       string
	tmuxDir    string
	sidDir     string
	transcript string
	id         string
	env        []string
	shim       string
}

func TestJailedEvalAttachFromPlainAndNestedTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh is not installed")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) is not installed")
	}

	jail := newAttachJail(t)
	index := exec.Command(filepath.Join(jail.home, ".local", "bin", "pfm"), "index", "--full")
	index.Env = jail.env
	if output, err := index.CombinedOutput(); err != nil {
		t.Fatalf("index attach fixture: %v: %s", err, output)
	}

	for _, mode := range []string{"plain", "inside-tmux", "bunker"} {
		for _, flow := range []string{"picker", "open"} {
			t.Run("eval/"+mode+"/"+flow, func(t *testing.T) {
				jail.proveAttach(t, "eval", mode, flow)
			})
		}
	}
	for _, mode := range []string{"plain", "inside-tmux"} {
		for _, flow := range []string{"picker", "open"} {
			t.Run("raw/"+mode+"/"+flow, func(t *testing.T) {
				jail.proveAttach(t, "raw", mode, flow)
			})
		}
	}
	t.Run("raw/plain/bare", func(t *testing.T) {
		jail.proveAttach(t, "raw", "plain", "bare")
	})
}

func newAttachJail(t *testing.T) *attachJail {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "ccf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove attach jail: %v", err)
		}
	})
	home := filepath.Join(root, "h")
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	sidDir := filepath.Join(root, "sid")
	claudeRoot := filepath.Join(root, "claude")
	project := filepath.Join(root, "work", "attach-project")
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
	const id = "b1111111-1111-4111-8111-111111111111"
	transcriptDir := filepath.Join(claudeRoot, "attach-project")
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(transcriptDir, id+".jsonl")
	content := `{"type":"user","cwd":` + strconv.Quote(project) +
		`,"message":{"content":"JAILATTACH proof"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(content), 0o600); err != nil {
		t.Fatal(err)
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
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".local", "bin") + string(os.PathListSeparator) +
		os.Getenv("PATH")
	env := replaceAttachEnv(os.Environ(), map[string]string{
		attachHelperEnv: "1",
		// The fixture's target pane runs sleep rather than Claude, so gather
		// correctly has no 1h birth marker to observe. Match that explicit 5m
		// posture now that the product default is 1h; otherwise the cache gate
		// deliberately forks instead of proving the attach path.
		"CC_ARM_1H":        "0",
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
	return &attachJail{
		root:       root,
		home:       home,
		tmuxDir:    tmuxDir,
		sidDir:     sidDir,
		transcript: transcript,
		id:         id,
		env:        env,
		shim:       filepath.Clean(filepath.Join(cwd, "..", "..", "shim", "pfm.zsh")),
	}
}

func (jail *attachJail) proveAttach(
	t *testing.T,
	protocol, mode, flow string,
) {
	t.Helper()
	suffix := strings.NewReplacer("/", "-", "_", "-").Replace(t.Name())
	targetSocket := "cc-" + strconv.FormatInt(time.Now().Unix(), 10) +
		"-" + strconv.Itoa(os.Getpid()) + "-" + strconv.Itoa(len(suffix)+len(mode)+len(flow))
	targetSession := "live-session"
	if output, err := jail.tmux(
		targetSocket,
		"-f", "/dev/null",
		"new-session", "-d",
		"-s", targetSession,
		"-n", "chat",
		"sleep 120",
	).CombinedOutput(); err != nil {
		t.Fatalf("start target server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = jail.tmux(targetSocket, "kill-server").Run()
	})
	paneOutput, err := jail.tmux(
		targetSocket,
		"list-panes",
		"-F", "#{pane_id}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	paneID := strings.TrimSpace(string(paneOutput))
	crumb := filepath.Join(jail.sidDir, targetSocket+"."+paneID)
	if err := os.WriteFile(crumb, []byte(jail.transcript+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(crumb)
	})

	marker := filepath.Join(jail.root, suffix+".done")
	commandText := `pfm chat open ` + shellQuote(jail.id)
	switch flow {
	case "picker":
		commandText = `pfm ls`
	case "bare":
		commandText = `pfm`
	}
	if protocol == "eval" {
		commandText = `eval "$(` + commandText + `)"`
	}
	scriptPath := filepath.Join(jail.root, suffix+".zsh")
	script := "#!/usr/bin/env zsh\nsource " + shellQuote(jail.shim) + "\n" +
		commandText + "\nprint -r -- attached > " + shellQuote(marker) + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	var command *exec.Cmd
	var input io.WriteCloser
	var output synchronizedBuffer
	driverSocket := ""
	if mode == "plain" {
		command = testjail.PTYCommand(scriptPath)
		command.Env = jail.env
		input, err = command.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		command.Stdout = &output
		command.Stderr = &output
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if command.Process != nil {
				_ = command.Process.Kill()
			}
		})
	} else {
		if mode == "bunker" {
			driverSocket = "vsct"
		} else {
			driverSocket = "driver-" + strconv.Itoa(os.Getpid()) + "-" +
				strconv.Itoa(len(suffix)+len(flow))
		}
		driver := exec.Command(
			"tmux",
			"-L", driverSocket,
			"-f", "/dev/null",
			"new-session", "-d",
			"-s", "driver",
			scriptPath,
		)
		driver.Env = jail.env
		if output, err := driver.CombinedOutput(); err != nil {
			t.Fatalf("start nested driver: %v: %s", err, output)
		}
		t.Cleanup(func() {
			_ = jail.tmux(driverSocket, "kill-server").Run()
		})
	}

	if flow == "picker" || flow == "bare" {
		// The picker paints cached resume state first, then promotes the row to
		// live after async gather. Under a parallel full-suite stress run the
		// gather goroutine can be CPU-starved; this functional proof must not
		// press Enter while the deliberately stale first frame is still shown.
		if mode == "plain" {
			// The picker renders through its own /dev/tty handle
			// (internal/ui/picker.go:Pick opens /dev/tty directly and hands it
			// to bubbletea as both input and output), never through the
			// process's stdout/stderr that command.Stdout captures here, and
			// testjail.PTYCommand wraps script(1) with no separate PTY-master
			// handle to poll instead. Confirmed live: even with the deadline
			// widened to 20s, output never advances past the initial
			// alt-screen setup escape sequence — the painted row is genuinely
			// unobservable in plain mode, so this stays a bounded sleep.
			time.Sleep(5 * time.Second)
		} else {
			deadline := time.Now().Add(5 * time.Second)
			ready := false
			for time.Now().Before(deadline) {
				capture := jail.tmux(driverSocket, "capture-pane", "-p", "-t", "driver:0.0")
				captured, captureErr := capture.Output()
				if captureErr == nil && strings.Contains(string(captured), "JAILATTACH") {
					ready = true
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if !ready {
				t.Fatalf("%s picker did not paint the target row within 5s", mode)
			}
		}
		if mode == "plain" {
			if _, err := io.WriteString(input, "JAILATTACH\r"); err != nil {
				t.Fatalf("drive plain picker: %v", err)
			}
		} else {
			send := jail.tmux(
				driverSocket,
				"send-keys", "-t", "driver:0.0",
				"JAILATTACH", "Enter",
			)
			if output, err := send.CombinedOutput(); err != nil {
				t.Fatalf("drive nested picker: %v: %s", err, output)
			}
		}
	}

	deadline := time.Now().Add(20 * time.Second)
	var sessions string
	for time.Now().Before(deadline) {
		output, listErr := jail.tmux(
			targetSocket,
			"list-clients",
			"-F", "#{client_session}",
		).Output()
		if listErr == nil {
			sessions = strings.TrimSpace(string(output))
			if sessions != "" {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if sessions != targetSession {
		t.Fatalf(
			"%s/%s did not switch client to target session: got %q",
			mode,
			flow,
			sessions,
		)
	}
	if output, err := jail.tmux(targetSocket, "detach-client", "-a").CombinedOutput(); err != nil {
		t.Fatalf("detach proof client: %v: %s", err, output)
	}
	if output, err := jail.tmux(targetSocket, "detach-client").CombinedOutput(); err != nil {
		t.Fatalf("detach final proof client: %v: %s", err, output)
	}
	if command != nil {
		wait := make(chan error, 1)
		go func() {
			wait <- command.Wait()
		}()
		select {
		case err := <-wait:
			if err != nil {
				t.Fatalf("plain shell ended with error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("plain shell did not return after detach")
		}
	}
	if mode == "bunker" {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) &&
			jail.tmux(driverSocket, "has-session", "-t", "driver").Run() == nil {
			time.Sleep(25 * time.Millisecond)
		}
		if jail.tmux(driverSocket, "has-session", "-t", "driver").Run() == nil {
			t.Fatal("bunker attach left its outer tmux viewport husk")
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("bunker shell continued after exec attach")
		}
		t.Logf(
			"ATTACH protocol=%s mode=%s flow=%s client_session=%s exec_replaced=true husk=false",
			protocol,
			mode,
			flow,
			sessions,
		)
		return
	}
	if !waitForAttachFile(marker, 5*time.Second) {
		t.Fatalf(
			"%s/%s/%s command did not return to its shell",
			protocol,
			mode,
			flow,
		)
	}
	t.Logf(
		"ATTACH protocol=%s mode=%s flow=%s client_session=%s command_returned=true",
		protocol,
		mode,
		flow,
		sessions,
	)
}

func (jail *attachJail) tmux(socket string, arguments ...string) *exec.Cmd {
	args := []string{"-L", socket}
	args = append(args, arguments...)
	command := exec.Command("tmux", args...)
	command.Env = jail.env
	return command
}

func waitForAttachFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// A shell redirection creates the marker before echo/print writes its
		// payload. Waiting only for existence races the following ReadFile and
		// can observe an empty marker under full-suite load.
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func replaceAttachEnv(environment []string, replacements map[string]string) []string {
	result := make([]string, 0, len(environment)+len(replacements))
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if _, replace := replacements[key]; found && replace {
			continue
		}
		result = append(result, entry)
	}
	for key, value := range replacements {
		result = append(result, key+"="+value)
	}
	return result
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (jail *attachJail) String() string {
	return fmt.Sprintf("attachJail(%s)", jail.root)
}
