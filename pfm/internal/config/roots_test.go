package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportRootsWarnsOnUnreachableClaudeAndCodexHomes(t *testing.T) {
	home := t.TempDir()
	reachable := filepath.Join(home, "reachable")
	if err := os.MkdirAll(reachable, 0o700); err != nil {
		t.Fatal(err)
	}
	accounts := []Account{{ID: 1, ProjectDir: reachable}, {ID: 2, ProjectDir: filepath.Join(home, "gone")}}
	codexAccounts := []CodexAccount{{ID: 1, Home: filepath.Join(home, "codex-gone")}}

	var output bytes.Buffer
	warnings := ReportRoots(&output, accounts, codexAccounts, false)
	if warnings != 2 {
		t.Fatalf("warnings=%d, want 2\n%s", warnings, output.String())
	}
	if !strings.Contains(output.String(), "doctor: warning unreachable_root="+filepath.Join(home, "gone")) {
		t.Fatalf("missing unreachable Claude root:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "doctor: warning unreachable_root="+filepath.Join(home, "codex-gone")) {
		t.Fatalf("missing unreachable Codex root:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "doctor: roots reachable=1 total=3") {
		t.Fatalf("roots summary wrong:\n%s", output.String())
	}
}

// TestReportRootsSkipsClaudeRootsWhenClaudeAbsentButLeavesCodexUnchanged pins
// D2's third row: an absent Claude never gets its roots stat'd or counted —
// named skipped instead of a false unreachable_root warning — while a Codex
// root that genuinely does not exist still warns exactly as before.
func TestReportRootsSkipsClaudeRootsWhenClaudeAbsentButLeavesCodexUnchanged(t *testing.T) {
	home := t.TempDir()
	accounts := []Account{{ID: 1, ProjectDir: filepath.Join(home, "claude-gone")}}
	codexAccounts := []CodexAccount{{ID: 1, Home: filepath.Join(home, "codex-gone")}}

	var output bytes.Buffer
	warnings := ReportRoots(&output, accounts, codexAccounts, true)
	if warnings != 1 {
		t.Fatalf("warnings=%d, want 1 (only the Codex root)\n%s", warnings, output.String())
	}
	want := "doctor: roots claude root=" + filepath.Join(
		home,
		"claude-gone",
	) + " skipped (no Claude Code binary installed)"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("missing skip line %q:\n%s", want, output.String())
	}
	if strings.Contains(output.String(), "unreachable_root="+filepath.Join(home, "claude-gone")) {
		t.Fatalf("a skipped Claude root was still reported unreachable:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "doctor: warning unreachable_root="+filepath.Join(home, "codex-gone")) {
		t.Fatalf("Codex root reporting changed when Claude is absent:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "doctor: roots reachable=0 total=1") {
		t.Fatalf("a skipped Claude root must not count toward total:\n%s", output.String())
	}
}
