package agentrole

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestHarnessPromptRecordLifecycleFallsBackToSocketAndIsRemovedWithTheSeat(t *testing.T) {
	sidDir := filepath.Join(t.TempDir(), "sid")
	prompt := filepath.Join(t.TempDir(), "alt.md")
	if err := os.WriteFile(prompt, []byte("ALT PROMPT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteHarnessPromptRecord(sidDir, "cc-10", "", prompt); err != nil {
		t.Fatalf("WriteHarnessPromptRecord() error = %v", err)
	}
	record := filepath.Join(sidDir, "harness-prompt-cc-10.path")
	if raw, err := os.ReadFile(record); err != nil || string(raw) != prompt+"\n" {
		t.Fatalf("record %s = %q, error %v", record, raw, err)
	}
	if !IsHarnessPromptRecordPath(filepath.Base(record)) || IsHarnessPromptRecordPath("harness-prompt-.path") {
		t.Fatal("IsHarnessPromptRecordPath misclassifies the canonical name or the empty identity")
	}
	got, found, err := ReadHarnessPromptRecord(sidDir, "cc-10", "%3")
	if err != nil || !found || got != prompt {
		t.Fatalf("pane read = %q found %v error %v, want the bare-socket record %q", got, found, err, prompt)
	}
	if err := RemoveSeatPrompt(sidDir, "cc-10", "%3"); err != nil {
		t.Fatalf("RemoveSeatPrompt() error = %v", err)
	}
	if _, found, err := ReadHarnessPromptRecord(sidDir, "cc-10", ""); err != nil || found {
		t.Fatalf("removed record read = found %v error %v", found, err)
	}
}

func TestHarnessPromptRecordRejectsARelativeOrEmptyPath(t *testing.T) {
	sidDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(sidDir, "harness-prompt-cc-bad.path"),
		[]byte("relative.md\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadHarnessPromptRecord(sidDir, "cc-bad", ""); err == nil ||
		!strings.Contains(err.Error(), "not an absolute path") {
		t.Fatalf("relative record error = %v", err)
	}
	if err := WriteHarnessPromptRecord(sidDir, "cc-x", "", "relative.md"); err == nil {
		t.Fatal("WriteHarnessPromptRecord accepted a relative path")
	}
}

func TestLoadHarnessPromptResolvesRelativeAndRefusesMissingDirectoryAndEmpty(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	if err := os.WriteFile("alt.md", []byte("ALT"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, body, err := LoadHarnessPrompt("alt.md")
	if err != nil || path != filepath.Join(directory, "alt.md") || body != "ALT" {
		t.Fatalf("LoadHarnessPrompt(relative) = %q %q %v", path, body, err)
	}
	if err := os.WriteFile("empty.md", []byte(" \n\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"missing.md": "no such file",
		".":          "is a directory",
		"empty.md":   "is empty",
	} {
		if _, _, err := LoadHarnessPrompt(name); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("LoadHarnessPrompt(%q) error = %v, want %q", name, err, want)
		}
	}
}

func TestLoadHarnessPromptForGatesTheEngineAndPassesAnEmptyPath(t *testing.T) {
	prompt := filepath.Join(t.TempDir(), "alt.md")
	if err := os.WriteFile(prompt, []byte("ALT"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, engineID := range []pfmengine.ID{pfmengine.Codex, pfmengine.OpenCode} {
		if _, _, err := LoadHarnessPromptFor(engineID, prompt); err == nil ||
			!strings.Contains(err.Error(), "--harness-prompt is supported for claude only") {
			t.Fatalf("LoadHarnessPromptFor(%s) error = %v", engineID, err)
		}
	}
	if path, body, err := LoadHarnessPromptFor(pfmengine.Codex, ""); path != "" || body != "" || err != nil {
		t.Fatalf("empty path = %q %q %v, want no harness prompt", path, body, err)
	}
	if path, body, err := LoadHarnessPromptFor(
		pfmengine.Claude,
		prompt,
	); path != prompt || body != "ALT" ||
		err != nil {
		t.Fatalf("claude = %q %q %v", path, body, err)
	}
}
