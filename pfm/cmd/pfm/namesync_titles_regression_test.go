package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestNameSyncTitlesReportsAnUnreadableServerAsUnverified(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := t.TempDir()
	client := gather.TmuxProbe{Binary: "tmux", TmuxTmpDir: root}
	titles := pfmconfig.DefaultTmuxTitles()
	var stdout, stderr bytes.Buffer
	unverified := convergeChatServerOptions(
		context.Background(), client,
		[]string{filepath.Join(root, "missing-socket")},
		titles,
		"/pfm/bin/pfm",
		&stdout,
		&stderr,
	)
	if !strings.Contains(stdout.String(), "tmux options converged: 0") {
		t.Fatalf("stdout=%q, want nothing counted converged for an unreadable server", stdout.String())
	}
	if unverified == 0 {
		t.Fatalf(
			"unverified=%d, want title probe failure carried into command result; stderr=%q",
			unverified,
			stderr.String(),
		)
	}
	if !strings.Contains(stderr.String(), "could not read set-titles") {
		t.Fatalf("stderr=%q, want the failed title read named", stderr.String())
	}
}

// name-sync converges a live server onto the same option list a fresh one is
// born with — automatic-rename off included, under either title policy. A
// server born before its door went through the one creator (a picker chat
// whose window the claude binary kept renaming "2.1.257") stops renaming on
// the next pass, and the transition is named.
func TestNameSyncFreezesAWindowNameTheServerStillAutoRenames(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, enabled := range []bool{true, false} {
		root, err := os.MkdirTemp("/tmp", "pfmns")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
		socket := "cc-1800000031-1-1"
		environment := append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
		start := exec.Command(
			"tmux",
			"-L",
			socket,
			"-f",
			"/dev/null",
			"new-session",
			"-d",
			"-s",
			socket,
			"sleep",
			"120",
		)
		start.Env = environment
		if output, err := start.CombinedOutput(); err != nil {
			t.Fatalf("start tmux fixture: %v: %s", err, output)
		}
		t.Cleanup(func() {
			kill := exec.Command("tmux", "-L", socket, "kill-server")
			kill.Env = environment
			_ = kill.Run()
		})
		client := gather.TmuxProbe{Binary: "tmux", TmuxTmpDir: root}
		var stdout, stderr bytes.Buffer
		unverified := convergeChatServerOptions(
			context.Background(), client, []string{socket},
			pfmconfig.TmuxTitles{Enabled: enabled}, "/pfm/bin/pfm", &stdout, &stderr,
		)
		if unverified != 0 {
			t.Fatalf("titles=%t: unverified=%d stderr=%q", enabled, unverified, stderr.String())
		}
		got, err := client.ShowGlobalOption(context.Background(), socket, "automatic-rename")
		if err != nil || got != "off" {
			t.Fatalf("titles=%t: automatic-rename = %q (%v) after convergence, want off", enabled, got, err)
		}
		if !strings.Contains(stdout.String(), socket) || !strings.Contains(stdout.String(), "automatic-rename") {
			t.Fatalf("titles=%t: stdout = %q, want the transition named", enabled, stdout.String())
		}
	}
}

// startNameSyncFixture runs a detached one-window tmux server named socket in a
// short private TMUX_TMPDIR and returns that directory and the environment a
// tmux client needs to reach it.
func startNameSyncFixture(t *testing.T, socket string) (root string, environment []string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root = testjail.ShortRoot(t)
	environment = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	start := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "sleep", "120",
	)
	start.Env = environment
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start tmux fixture: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", socket, "kill-server")
		kill.Env = environment
		_ = kill.Run()
	})
	return root, environment
}

func nameSyncTmux(t *testing.T, environment []string, socket string, arguments ...string) string {
	t.Helper()
	command := exec.Command("tmux", append([]string{"-L", socket}, arguments...)...)
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimRight(string(output), "\n")
}

// A pfm binary path with a space and a quote reaches every Herdr hook as one
// shell word, and name-sync's read-back compares equal to the form tmux prints
// a stored hook back in: the second pass over a converged server reports
// nothing converged and rewrites nothing.
func TestNameSyncConvergesTheHerdrHooksInTheFormTmuxPrintsBack(t *testing.T) {
	const socket = "cc-1800000041-1-1"
	root, environment := startNameSyncFixture(t, socket)
	client := gather.TmuxProbe{Binary: "tmux", TmuxTmpDir: root}
	titles := pfmconfig.TmuxTitles{Enabled: false}
	binary := "/tmp/pfm dir/it's/pfm"

	var firstOut, firstErr bytes.Buffer
	if unverified := convergeChatServerOptions(
		context.Background(), client, []string{socket}, titles, binary, &firstOut, &firstErr,
	); unverified != 0 {
		t.Fatalf("first pass unverified=%d stderr=%q", unverified, firstErr.String())
	}
	var secondOut, secondErr bytes.Buffer
	if unverified := convergeChatServerOptions(
		context.Background(), client, []string{socket}, titles, binary, &secondOut, &secondErr,
	); unverified != 0 {
		t.Fatalf("second pass unverified=%d stderr=%q", unverified, secondErr.String())
	}
	if secondOut.String() != "tmux options converged: 0\n" {
		t.Fatalf("second pass stdout = %q, want every option already converged", secondOut.String())
	}

	prefix := `run-shell -b "'/tmp/pfm dir/it'\\''s/pfm' internal herdr-state `
	for hook, want := range map[string]string{
		"client-attached[50]": prefix + `attach --socket #{q:socket_path} --session #{q:session_name} --client #{q:client_pid}"`,
		"client-detached[50]": prefix + `detach --socket #{q:socket_path} --session #{q:session_name} --client #{q:client_pid}"`,
		"client-resized[50]":  prefix + `sync --socket #{q:socket_path} --session #{q:session_name} --client #{q:client_pid}"`,
		"window-renamed[50]":  prefix + `name --socket #{q:socket_path} --session #{q:session_name}"`,
	} {
		got := nameSyncTmux(t, environment, socket, "show-hooks", "-g", hook)
		if got != hook+" "+want {
			t.Fatalf("show-hooks -g %s =\n%s\nwant\n%s", hook, got, hook+" "+want)
		}
	}
}

// A rename from a tmux command client — the shape pfm's own writers use — runs
// the window-renamed hook once, and the hook command reaches the pfm binary
// with the server's socket path and the session name.
func TestWindowRenamedHookRunsTheReporterOnceForACommandClientRename(t *testing.T) {
	const socket = "cc-1800000042-1-1"
	root, environment := startNameSyncFixture(t, socket)
	logPath := filepath.Join(root, "reporter.log")
	stub := filepath.Join(root, "pfm-stub")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + logPath + "'\n"
	if err := testjail.WriteExecutable(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	client := gather.TmuxProbe{Binary: "tmux", TmuxTmpDir: root}
	var stdout, stderr bytes.Buffer
	if unverified := convergeChatServerOptions(
		context.Background(), client, []string{socket},
		pfmconfig.TmuxTitles{Enabled: false}, stub, &stdout, &stderr,
	); unverified != 0 {
		t.Fatalf("unverified=%d stderr=%q", unverified, stderr.String())
	}

	nameSyncTmux(t, environment, socket, "rename-window", "-t", socket, "X")

	want := "internal herdr-state name --socket " +
		filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()), socket) + " --session " + socket
	deadline := time.Now().Add(5 * time.Second)
	for {
		logged, _ := os.ReadFile(logPath)
		if lines := strings.Fields(string(logged)); len(lines) > 0 {
			// Let a second, wrongly fired run land before counting.
			time.Sleep(300 * time.Millisecond)
			logged, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(logged)); got != want {
				t.Fatalf("reporter log = %q, want exactly one run %q", got, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the window-renamed hook never ran the reporter for rename-window from a command client")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestNameSyncApplyReannouncesLiveChats pins the re-announce pass of
// `pfm name-sync --apply`: each live chat server is re-announced through the
// seam by its socket path under the tmux directory; a server that reached
// panes is named with them, one that reached none prints nothing, and a
// failure is named on stderr and counted unverified without stopping the pass.
func TestNameSyncApplyReannouncesLiveChats(t *testing.T) {
	tmuxDir := "/tmuxdir"
	asked := []string{}
	reannounce := func(_ context.Context, socket string) ([]string, error) {
		asked = append(asked, socket)
		switch filepath.Base(socket) {
		case "cc-1-2-3":
			return []string{"w6:p2", "wA:p1"}, nil
		case "cc-4-5-6":
			return nil, nil
		case "cc-7-8-9":
			return nil, errors.New("herdr re-announce " + socket + ": list the sessions: no server running")
		default:
			return []string{"w2:p1"}, errors.New("herdr re-announce " + socket + " session s: the pane is gone")
		}
	}
	var stdout, stderr bytes.Buffer
	unverified := reannounceChatServers(
		context.Background(), reannounce, tmuxDir,
		[]string{"cc-1-2-3", "cc-4-5-6", "cc-7-8-9", "cc-0-0-1"}, &stdout, &stderr,
	)
	wantAsked := []string{"/tmuxdir/cc-1-2-3", "/tmuxdir/cc-4-5-6", "/tmuxdir/cc-7-8-9", "/tmuxdir/cc-0-0-1"}
	if strings.Join(asked, "|") != strings.Join(wantAsked, "|") {
		t.Fatalf("re-announced sockets = %q, want %q", asked, wantAsked)
	}
	wantOut := "herdr re-announce cc-1-2-3: 2 client(s) w6:p2, wA:p1\n" +
		"herdr re-announce cc-0-0-1: 1 client(s) w2:p1\n"
	if stdout.String() != wantOut {
		t.Errorf("stdout = %q, want %q (nothing for a server that reached no pane)", stdout.String(), wantOut)
	}
	wantErr := "pfm name-sync: herdr re-announce /tmuxdir/cc-7-8-9: list the sessions: no server running\n" +
		"pfm name-sync: herdr re-announce /tmuxdir/cc-0-0-1 session s: the pane is gone\n"
	if stderr.String() != wantErr {
		t.Errorf("stderr = %q, want %q", stderr.String(), wantErr)
	}
	if unverified != 2 {
		t.Errorf("unverified = %d, want 2 (one per failed server)", unverified)
	}
}
