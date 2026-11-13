package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/hostcheck"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestWiredIndexListOpenAndDoctor(t *testing.T) {
	root := jailTest(t)
	t.Setenv(spawn.TestFreshSocketEnv, "cc-1700000000-1-1")
	project := filepath.Join(root, "work", "project")
	transcriptDir := filepath.Join(root, "claude", "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "11111111-1111-4111-8111-111111111111"
	content := `{"type":"user","cwd":` +
		strconv.Quote(project) +
		`,"message":{"content":"Wired prompt"}}` + "\n"
	if err := os.WriteFile(
		filepath.Join(transcriptDir, id+".jsonl"),
		[]byte(content),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"index", "--full"}, &stdout, &stderr); code != 0 {
		t.Fatalf("index code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "files=1") ||
		!strings.Contains(stdout.String(), "full=1") ||
		!strings.Contains(stdout.String(), "touched=2") {
		t.Fatalf("index stdout=%q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ls", "--tsv"}, &stdout, &stderr); code != 0 {
		t.Fatalf("ls code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "\t"+id+"\t") ||
		!strings.Contains(stdout.String(), "resume-claude") {
		t.Fatalf("ls stdout=%q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	readServer := holdClaudeOpen(t, root, "cc-1700000000-1-1")
	if code := run([]string{"chat", "open", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("open code=%d stderr=%q", code, stderr.String())
	}
	if lines := strings.Count(stdout.String(), "\n"); lines != 1 {
		t.Fatalf("open emitted %d lines: %q", lines, stdout.String())
	}
	if !strings.Contains(stdout.String(), "attach -t 'cc-1700000000-1-1'") {
		t.Fatalf("open stdout=%q", stdout.String())
	}
	// The resume is born through the one chat-server creator: its window
	// carries the engine's name, never one its pane command chose.
	if run := readServer("#{pane_start_command}"); !strings.Contains(run, "--resume") || !strings.Contains(run, id) {
		t.Fatalf("opened server runs %q, want the resume of %s", run, id)
	}
	if window := readServer("#{window_name}"); window != "Claude" {
		t.Fatalf("opened window = %q, want Claude", window)
	}
	stdout.Reset()
	stderr.Reset()
	testjail.PinClaudeAsk(t, filepath.Join(root, "home"))
	if code := run([]string{"doctor"}, &stdout, &stderr); code != 0 {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	links := fmt.Sprintf("account-links: ok (1 accounts × %d entries)", len(installer.StoreEntries))
	if !strings.Contains(stdout.String(), "doctor: clean") ||
		!strings.Contains(stdout.String(), "transcripts=1") ||
		!strings.Contains(stdout.String(), fmt.Sprintf("host-check: ok (%d checks)", len(hostcheck.Detectors()))) ||
		!strings.Contains(stdout.String(), links) {
		t.Fatalf("doctor stdout=%q", stdout.String())
	}
}

func writeJailedCodexAuth(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(
		filepath.Join(root, "codex", "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture","account_id":"fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}

// Keep a fixture Claude pane (a screen under Claude's name) alive until cleanup.
func holdClaudeOpen(t *testing.T, root, socket string) func(format string) string {
	t.Helper()
	managed := filepath.Join(root, "home", ".local", "share", "pfm", "install", "bin", "claude")
	fixture := []byte("#!/bin/bash\nprintf 'fixture claude\\n'\nexec -a claude sleep 120\n")
	if err := testjail.WriteExecutable(managed, fixture, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(root, "tmux", socket)
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", socketPath, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
	return func(format string) string {
		read := exec.Command("tmux", "-S", socketPath, "display-message", "-p", "-t", socket, format)
		read.Env = append(os.Environ(), "TMUX=")
		output, err := read.Output()
		if err != nil {
			t.Fatalf("read the opened server %s: %v", socket, err)
		}
		return strings.TrimSpace(string(output))
	}
}
