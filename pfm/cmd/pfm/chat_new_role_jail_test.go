//go:build jail

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestChatNewRoleWithoutSourceRepoMarkerNamesCause(t *testing.T) {
	jail := newRunJail(t)
	rolePath := filepath.Join(jail.root, "work", ".claude", "agents", "reader.md")
	if err := os.MkdirAll(filepath.Dir(rolePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolePath, []byte("---\nname: reader\n---\nROLE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(jail.root, "home", "pfm.config.json")
	if err := os.WriteFile(
		configPath,
		[]byte(`{"version":2,"claude":{"systemPrompt":"professor"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.SourceRepoPath(filepath.Join(jail.root, "home"))); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{
			"chat",
			"new",
			"--name",
			"reader",
			"--engine",
			"cc",
			"--cwd",
			filepath.Join(jail.root, "work"),
			"--agent-role",
			"reader",
		},
		&stdout,
		&stderr,
	)
	if got, want := rooted(
		stderr.String(),
		jail.root,
	), "pfm chat new: agent role: resolve Claude prompt: resolve generated harness prompt: read source repository marker {root}/home/.local/share/pfm/install/source-repo: no source repository recorded: open {root}/home/.local/share/pfm/install/source-repo: no such file or directory\n"; code != 2 ||
		got != want {
		t.Fatalf("chat new exit=%d stderr = %q, want %q", code, got, want)
	}
}

func TestChatNewAgentRoleKeysDuplicateNamesByFreshSocket(t *testing.T) {
	testjail.TmuxServer(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	roleDir := filepath.Join(jail.root, "work", ".claude", "agents")
	if err := os.MkdirAll(roleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(roleDir, "reviewer.md"),
		[]byte("---\nname: reviewer\n---\nROLE\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	fleetPrompt, err := action.ProfessorPromptPath(filepath.Join(jail.root, "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(fleetPrompt), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleetPrompt, []byte("FLEET"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(jail.root, "home", "pfm.config.json")
	if err := os.WriteFile(
		configPath,
		[]byte(`{"version":2,"ask":{"engine":"claude"},"claude":{"systemPrompt":"professor"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	for _, socket := range []string{"cc-role-one", "cc-role-two"} {
		t.Setenv(spawn.TestFreshSocketEnv, socket)
		var stdout, stderr bytes.Buffer
		code := run([]string{
			"chat", "new", "--engine", "cc", "--name", "duplicate",
			"--cwd", filepath.Join(jail.root, "work"), "--agent-role", "reviewer",
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("chat new socket=%s exit=%d stdout=%q stderr=%q", socket, code, stdout.String(), stderr.String())
		}
	}
	for _, socket := range []string{"cc-role-one", "cc-role-two"} {
		path, err := agentrole.SeatPromptPath(filepath.Join(jail.root, "sid"), socket, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("socket prompt %s: %v", path, err)
		}
	}
}
