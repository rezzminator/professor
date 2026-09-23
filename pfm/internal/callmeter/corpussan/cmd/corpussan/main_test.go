package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

func TestRunSanitizeWritesTheSession(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "acme")
	session := filepath.Join(root, "sess.jsonl")
	line := `{"type":"user","cwd":"` + project + `","message":{"role":"user","content":"hello bob"}}` + "\n"
	if err := os.MkdirAll(filepath.Join(project, "pfm"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(session, []byte(line), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := filepath.Join(root, "out")
	var stdout, stderr strings.Builder
	args := []string{"-session", session, "-out", out, "-project", project, "-home", root, "-deny", "bob"}
	if code := sanitizeExit(args, &stdout, &stderr); code != 0 {
		t.Fatalf("sanitizeExit = %d, stderr %q", code, stderr.String())
	}
	if want := filepath.Join(out, "sess.jsonl") + "\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	got, err := os.ReadFile(filepath.Join(out, "sess.jsonl"))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	want := `{"type":"user","cwd":"/tmp/demo-proj","message":{"role":"user","content":"xxxxxxxxx"}}` + "\n"
	if string(got) != want {
		t.Errorf("output\n got %s\nwant %s", got, want)
	}
}

func TestRunSanitizeRejectsMissingFlags(t *testing.T) {
	var stdout, stderr strings.Builder
	code := sanitizeExit([]string{"-out", t.TempDir()}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "-session is required") {
		t.Errorf("sanitizeExit with no -session = %d, stderr %q", code, stderr.String())
	}
}
