package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// composeRolePrompt puts the role constitution FIRST — before any goal, per
// the user's literal requirement — then the separator, then the caller's own
// prompt. --role alone (no caller prompt) is legal and returns the
// constitution with no trailing separator.
func TestComposeRolePromptPutsConstitutionFirst(t *testing.T) {
	t.Run("constitution then separator then prompt", func(t *testing.T) {
		got := composeRolePrompt("CONSTITUTION BODY", "do the goal")
		want := "CONSTITUTION BODY" + rolePromptSeparator + "do the goal"
		if got != want {
			t.Fatalf("composeRolePrompt() = %q, want %q", got, want)
		}
		constitutionAt := strings.Index(got, "CONSTITUTION BODY")
		promptAt := strings.Index(got, "do the goal")
		if constitutionAt < 0 || promptAt < 0 || constitutionAt > promptAt {
			t.Fatalf("composeRolePrompt() = %q, constitution must precede the caller prompt", got)
		}
		if !strings.Contains(got, "\n\n---\n\n") {
			t.Fatalf("composeRolePrompt() = %q, want the literal \\n\\n---\\n\\n separator", got)
		}
	})

	t.Run("role alone has no trailing separator", func(t *testing.T) {
		got := composeRolePrompt("CONSTITUTION BODY", "")
		if got != "CONSTITUTION BODY" {
			t.Fatalf("composeRolePrompt() = %q, want the bare constitution", got)
		}
		if strings.HasSuffix(got, rolePromptSeparator) {
			t.Fatalf("composeRolePrompt() = %q, a role-only seat must carry no trailing separator", got)
		}
	})
}

// TestChatNewUnknownRoleExitsWithoutSpawning is the CLI-level seam for
// --role: pfm chat new --role <unknown> resolves the role BEFORE
// action.HeadlessRun / spawn.Run ever runs, so an unregistered role must
// exit 2, write the resolution error to stderr, print nothing to stdout, and
// leave the jailed tmux socket directory EMPTY — the closest observable
// proof this harness offers that nothing was spawned, since the CLI has no
// spawn-call counter to assert zero invocations against directly.
func TestChatNewUnknownRoleExitsWithoutSpawning(t *testing.T) {
	root := jailTest(t)
	workDir := filepath.Join(root, "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--engine", "cc",
		"--name", "worker",
		"--cwd", workDir,
		"--role", "ghost-role",
		"do the thing",
	}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("run() exit=%d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("run() stdout=%q, want nothing printed before a role resolution failure", stdout.String())
	}
	if !strings.Contains(stderr.String(), `no cc role "ghost-role" found`) {
		t.Fatalf("run() stderr=%q, want the agent role resolution error", stderr.String())
	}

	tmuxDir := os.Getenv("PFM_TMUX_DIR")
	entries, err := os.ReadDir(tmuxDir)
	if err != nil {
		t.Fatalf("read jailed tmux dir %s: %v", tmuxDir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("jailed tmux dir %s has %d entries, want 0 — a socket here means something spawned "+
			"despite the unresolved role", tmuxDir, len(entries))
	}
}
