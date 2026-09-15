package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/testjail"
)

// jailSelfCompactEnv points every path pfm's engine construction reads at a
// scratch root — never the real $HOME or a live tmux dir. It exists so a
// usage-error test that is SUPPOSED to fail before ever building the engine
// still cannot reach live state if that early-return regresses (or is
// deliberately reverted to watch a regression test fail): the "self" target
// then resolves to nothing rather than to whatever chat happens to own this
// terminal. This is exactly the isolation TestChatInjectRefusesCompactBeforeAnyResolveOrEngine
// already carries; here it is factored out because it must ALSO wrap the
// two usage-error tests, whose reverted-hunk probe proved this the hard
// way once (an unjailed run reached and armed a real self-compact waiter
// against this host's own live orchestrator pane).
func jailSelfCompactEnv(t *testing.T) {
	t.Helper()
	root := testjail.ShortRoot(t)
	home := filepath.Join(root, "home")
	for _, directory := range []string{
		home,
		filepath.Join(root, "claude"),
		filepath.Join(root, "codex"),
		filepath.Join(root, "tmux"),
		filepath.Join(root, "proc"),
		filepath.Join(root, "sid"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("TMUX", "")
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
	t.Setenv("PFM_SHARED_DB", filepath.Join(root, "shared.db"))
	t.Setenv("PFM_SID_DIR", filepath.Join(root, "sid"))
	t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(root, "claude"))
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("PFM_TMUX_DIR", filepath.Join(root, "tmux"))
	t.Setenv("PFM_PROC_ROOT", filepath.Join(root, "proc"))
	statedTestSender(t)
}

// TestChatSelfCompactUsageErrors pins Task D's CLI twin usage contract: no
// focus, or a second --then, is a usage error (rc 2) — never a call into
// the engine. Revert runHeadlessSelfCompact's NArg() check or singleSteer's
// duplicate guard and one of these cases stops failing fast (and, absent
// jailSelfCompactEnv, reaches a real "self" resolve — see that helper's
// comment).
func TestChatSelfCompactUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no focus", args: nil},
		{name: "two then flags", args: []string{"--then", "one", "--then", "two", "hold the state"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jailSelfCompactEnv(t)
			var stdout, stderr bytes.Buffer
			code := runHeadlessSelfCompact(test.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("usage error wrote to stdout: %q", stdout.String())
			}
			if stderr.Len() == 0 {
				t.Fatalf("usage error produced no diagnostic at all")
			}
		})
	}
}

// TestChatSelfCompactNoFocusNamesItsOwnUsage pins the exact usage string
// for the no-focus case specifically (the one case that reaches
// flags.Usage() through this command's own code, not through stdlib flag
// parsing's error path).
func TestChatSelfCompactNoFocusNamesItsOwnUsage(t *testing.T) {
	jailSelfCompactEnv(t)
	var stdout, stderr bytes.Buffer
	code := runHeadlessSelfCompact(nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: pfm chat self-compact") {
		t.Fatalf("usage error did not print its own usage line: %q", stderr.String())
	}
}

// TestChatUsageListsSelfCompact pins that `pfm chat` help lists the new
// subcommand — a dangling command nobody's help mentions is the "no CLI
// twin" gap Task D closes.
func TestChatUsageListsSelfCompact(t *testing.T) {
	var stdout bytes.Buffer
	printChatUsage(&stdout)
	if !strings.Contains(stdout.String(), "self-compact") {
		t.Fatalf("chat usage does not list self-compact: %q", stdout.String())
	}
}

// TestChatInjectRefusesCompactBeforeAnyResolveOrEngine pins Task C at the
// CLI surface: `pfm chat inject <target> '/compact ...'` is refused with
// the chat_self_compact / `pfm chat self-compact` remedy — code 6 — even
// against a target that does not exist, proving the ban fires before any
// resolve step, exactly like the engine-level test. A `--then` steer is
// included deliberately: without one, checkSteerChain's OWN independent
// "a /compact inject requires exactly one then steer" guard produces the identical
// remedy text and masks a reverted Inject()-level ban — a `--then` steer
// clears that guard, so only the ban under test can produce this refusal.
// Revert engine.go's Inject() ban and this exits 0 (scheduled) instead.
func TestChatInjectRefusesCompactBeforeAnyResolveOrEngine(t *testing.T) {
	jailSelfCompactEnv(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "inject", "--then", "resume the wave",
		"no-such-target", "/compact", "hold:", "state",
	}, &stdout, &stderr)
	if code != codeUndelivered {
		t.Fatalf(
			"exit=%d stdout=%q stderr=%q, want codeUndelivered (%d)",
			code,
			stdout.String(),
			stderr.String(),
			codeUndelivered,
		)
	}
	if stdout.Len() != 0 {
		t.Fatalf("a banned /compact primary printed to stdout as if delivered: %q", stdout.String())
	}
	for _, want := range []string{"chat_self_compact", "pfm chat self-compact", "/compact is never injected"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("refusal %q lacks %q", stderr.String(), want)
		}
	}
}
