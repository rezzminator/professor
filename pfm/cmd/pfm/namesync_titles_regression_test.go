package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/gather"
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
			pfmconfig.TmuxTitles{Enabled: enabled}, &stdout, &stderr,
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
