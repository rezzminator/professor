package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/paths"
)

// TestCodexAgentsCommandCompilesAndInstalls exercises the "pfm codex agents"
// CLI adapter end to end inside the path jail: it reads
// {PFM_HOME}/.professor/templates/global/agents/*.md, writes a compiled
// .toml into the pfm-owned generated directory (never beside the source
// .md), and symlinks both shapes into the jailed home's .claude/agents and
// .codex/agents — the same registries pfm install wires.
func TestCodexAgentsCommandCompilesAndInstalls(t *testing.T) {
	jailTest(t)
	home := os.Getenv("PFM_HOME")
	writeCodexCLIFile(
		t,
		filepath.Join(home, ".professor", "templates", "global", "agents", "quirky.md"),
		"---\nname: quirky\ndescription: Uses \"walker fast\" and \"map it now\" verbatim.\ntools: Read\nmodel: sonnet\n---\n\n"+
			"Body has a literal triple quote \"\"\" and a backslash \\ standalone.\n",
	)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"codex", "agents"}, &stdout, &stderr); code != 0 {
		t.Fatalf("codex agents code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "CODEX AGENTS PASS") {
		t.Fatalf("codex agents did not report PASS: stdout=%q", stdout.String())
	}

	compiled := filepath.Join(paths.GeneratedCodexAgentsDir(home), "quirky.toml")
	want := "name = \"quirky\"\n" +
		"description = \"Uses \\\"walker fast\\\" and \\\"map it now\\\" verbatim.\"\n" +
		"developer_instructions = \"\"\"\n" +
		"Body has a literal triple quote \\\"\\\"\\\" and a backslash \\\\ standalone.\n" +
		"\"\"\"\n"
	got, err := os.ReadFile(compiled)
	if err != nil {
		t.Fatalf("read compiled toml: %v", err)
	}
	if string(got) != want {
		t.Fatalf("compiled toml =\n%q\nwant\n%q", string(got), want)
	}

	inClone := filepath.Join(home, ".professor", "templates", "global", "agents", "quirky.toml")
	if _, err := os.Lstat(inClone); !os.IsNotExist(err) {
		t.Fatalf("expected no .toml written inside the source clone at %s, lstat err=%v", inClone, err)
	}

	for _, expect := range []struct{ target, source string }{
		{filepath.Join(home, ".claude", "agents", "quirky.md"), filepath.Join(home, ".professor", "templates", "global", "agents", "quirky.md")},
		{filepath.Join(home, ".codex", "agents", "quirky.toml"), compiled},
	} {
		info, err := os.Lstat(expect.target)
		if err != nil {
			t.Fatalf("expected install at %s: %v", expect.target, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is not a symlink (mode=%v)", expect.target, info.Mode())
		}
		resolved, err := os.Readlink(expect.target)
		if err != nil {
			t.Fatalf("readlink %s: %v", expect.target, err)
		}
		if filepath.Clean(resolved) != filepath.Clean(expect.source) {
			t.Fatalf("%s -> %s, want -> %s", expect.target, resolved, expect.source)
		}
	}
}

// TestCodexAgentsCommandRejectsAPositionalArgument matches build/check,
// which both reject anything past a lone optional repo-root; agents takes no
// positional at all since it has no repo root.
func TestCodexAgentsCommandRejectsAPositionalArgument(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"codex", "agents", "unexpected"}, &stdout, &stderr); code != 2 {
		t.Fatalf("codex agents with a positional: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// TestCodexAgentsCommandListedInHelp keeps the action discoverable alongside
// build/check.
func TestCodexAgentsCommandListedInHelp(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"codex", "help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("codex help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "pfm codex agents") {
		t.Fatalf("codex help does not mention agents: %s", stdout.String())
	}
}
