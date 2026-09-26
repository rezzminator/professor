package installer

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestUninstallCodexCommandReconciliationNeverWritesANewMirrorEntry pins the
// distinction between "retire what we own" and "recompile whatever remains":
// reconcileCodexCommands must never WRITE a fresh Codex prompt mirror for a
// command that merely happens to still be sitting in ~/.claude/commands at
// uninstall time — a leftover of this installer's own teardown ordering, or
// simply a user's foreign command sharing the directory. Before this fix,
// the real (apply) reconciliation call scanned $HOME/.claude/commands
// directly regardless of installer mode, so any command still there when
// uninstall reached this step got a brand-new Codex mirror entry written for
// it, mid-teardown — the opposite of what uninstalling means.
func TestUninstallCodexCommandReconciliationNeverWritesANewMirrorEntry(t *testing.T) {
	home := t.TempDir()
	// A command this installer never wired and never mirrored — present only
	// because it happens to still be sitting in ~/.claude/commands when
	// uninstall's reconciliation step runs.
	writeFixture(t, filepath.Join(home, ".claude", "commands", "adhoc.md"), "# adhoc\n\nDo the thing.\n")

	installer := &engine{
		options:     Options{Mode: ModeUninstall, Home: home, Stdout: io.Discard},
		apply:       true,
		managedRoot: managedRootForHome(home),
	}
	if err := installer.reconcileCodexCommands(nil); err != nil {
		t.Fatal(err)
	}
	mirrored := filepath.Join(home, ".codex", "prompts", "adhoc.md")
	if _, err := os.Stat(mirrored); !os.IsNotExist(err) {
		t.Fatalf(
			"uninstall's Codex command reconciliation wrote a fresh mirror entry for an unwired command: %s (stat err=%v)",
			mirrored,
			err,
		)
	}
}

// TestInstallCodexCommandReconciliationStillMirrorsCommands is the control:
// the same reconciliation call during a normal apply (not uninstall) must
// still mirror whatever is present — the uninstall-only empty-source
// redirection must not leak into install.
func TestInstallCodexCommandReconciliationStillMirrorsCommands(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".claude", "commands", "adhoc.md"), "# adhoc\n\nDo the thing.\n")

	installer := &engine{
		options:     Options{Mode: ModeApply, Home: home, Stdout: io.Discard},
		apply:       true,
		managedRoot: managedRootForHome(home),
	}
	if err := installer.reconcileCodexCommands(nil); err != nil {
		t.Fatal(err)
	}
	mirrored := filepath.Join(home, ".codex", "prompts", "adhoc.md")
	if _, err := os.Stat(mirrored); err != nil {
		t.Fatalf("install's Codex command reconciliation did not mirror the command: %s (stat err=%v)", mirrored, err)
	}
}
