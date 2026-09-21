package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/rearm"
)

// TestChatEndRemovesRoleCrumbOnKill pins T1 re-arm's cleanup half (behaviour
// 6, cmd/pfm/chat_command.go's runChatEnd): a seat's remembered role crumb
// (cmd/pfm/chat_new_command.go's WriteCrumb, exercised directly by
// internal/rearm's own package tests) is litter once its socket is dead;
// the canonical kill path `pfm chat end` removes it on the same tmux
// kill-server call that ends the chat. The crumb here is seeded directly
// with rearm.WriteCrumb — exactly the shape chat_new_command.go's own --role
// path produces — onto a plain (roleless) real spawn, so this test proves
// runChatEnd's OWN wiring to rearm.RemoveCrumb without also depending on
// the --role launch path spawn.Run already needs its own coverage for.
func TestChatEndRemovesRoleCrumbOnKill(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--engine", "cc",
		"--name", "EndCrumbWorker",
		"--cwd", filepath.Join(jail.root, "work"),
		"audit the firewall",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat new exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	entries, err := os.ReadDir(jail.tmuxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("jailed tmux sockets=%v err=%v", entries, err)
	}
	socket := entries[0].Name()
	sidDir := filepath.Join(jail.root, "sid")
	artifact := filepath.Join(jail.root, "role-artifact.md")
	if err := os.WriteFile(artifact, []byte("constitution\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rearm.WriteCrumb(sidDir, socket, rearm.Crumb{
		Role:         "tester",
		ArtifactPath: artifact,
		TOMLKey:      false,
	}); err != nil {
		t.Fatalf("WriteCrumb() error = %v", err)
	}
	crumbPath := filepath.Join(sidDir, "role-"+socket)
	if _, err := os.Stat(crumbPath); err != nil {
		t.Fatalf("setup: role crumb not present at %s: %v", crumbPath, err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"chat", "end", socket}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat end exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(crumbPath); !os.IsNotExist(err) {
		t.Fatalf("role crumb %s outlived chat end: err=%v", crumbPath, err)
	}
}

// TestChatEndSucceedsDespiteCrumbRemovalFailure pins the WARNING-only
// contract on rearm.RemoveCrumb's failure path (the third clause of
// behaviour 6): the chat is dead either way once tmux kill-server has run,
// so a crumb this process cannot remove (sabotaged here into a non-empty
// directory, so os.Remove fails with ENOTEMPTY rather than the ordinary
// fs.ErrNotExist RemoveCrumb already tolerates) must produce a stderr
// WARNING, never a non-zero `pfm chat end`.
func TestChatEndSucceedsDespiteCrumbRemovalFailure(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--engine", "cc",
		"--name", "SabotagedCrumbWorker",
		"--cwd", filepath.Join(jail.root, "work"),
		"audit the firewall",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat new exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	entries, err := os.ReadDir(jail.tmuxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("jailed tmux sockets=%v err=%v", entries, err)
	}
	socket := entries[0].Name()
	sidDir := filepath.Join(jail.root, "sid")
	crumbPath := filepath.Join(sidDir, "role-"+socket)
	// A non-empty directory in the crumb's place: os.Remove refuses it with
	// ENOTEMPTY, never fs.ErrNotExist — the one failure shape RemoveCrumb
	// does NOT tolerate silently.
	if err := os.MkdirAll(filepath.Join(crumbPath, "litter"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(crumbPath, "litter", "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"chat", "end", socket}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat end exit=%d, want 0 despite a crumb removal failure; stdout=%q stderr=%q",
			code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "WARNING") || !strings.Contains(stderr.String(), "crumb") {
		t.Fatalf("chat end stderr=%q, want a WARNING naming the crumb removal failure", stderr.String())
	}
	if _, err := os.Stat(crumbPath); err != nil {
		t.Fatalf("sabotaged crumb directory unexpectedly vanished: %v", err)
	}
}
