package installer

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRetireOrphanGlobalCommandsPrunesOnlyItsOwnDanglingLinks pins
// retireOrphanGlobalCommands' doc comment promise: ownership is decided by a
// link's TARGET, never its name. A symlink retires only when it points at
// <recorded professor repo>/templates/global/commands/<its own name> AND
// that target no longer exists. Every other shape at that same registry path
// — a link whose target still ships, a dangling link pointing outside the
// blueprint entirely, and a plain regular file — survives untouched, the
// same preservation rule retireRenamedGlobalAgents holds to for agents.
func TestRetireOrphanGlobalCommandsPrunesOnlyItsOwnDanglingLinks(t *testing.T) {
	t.Run("orphan retired: a dangling link at its own recorded blueprint path is removed", func(t *testing.T) {
		home := t.TempDir()
		source := filepath.Join(home, ".professor", "templates", "global", "commands")
		// The source ships only tokens.md — wireGlobalCommands links that
		// fresh below. "p" is never in the source, so wireGlobalCommands never
		// touches it; the pre-planted link at commands/p models exactly what
		// an upstream retirement leaves behind: a link the installer made for
		// a command the source no longer holds.
		writeFixture(t, filepath.Join(source, "tokens.md"), "# tokens command\n")
		orphan := filepath.Join(home, ".claude", "commands", "p")
		if err := os.MkdirAll(filepath.Dir(orphan), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(source, "p"), orphan); err != nil {
			t.Fatal(err)
		}

		if _, err := Run(context.Background(), Options{
			Mode: ModeApply, Home: home, Runner: &fakeRunner{}, CodexHomes: []string{},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(orphan); !os.IsNotExist(err) {
			t.Fatalf("orphaned global command link survived: %v", err)
		}
	})

	t.Run("live link survives: a link whose target still ships is left alone", func(t *testing.T) {
		home := t.TempDir()
		source := filepath.Join(home, ".professor", "templates", "global", "commands")
		writeFixture(t, filepath.Join(source, "tokens.md"), "# tokens command\n")

		if _, err := Run(context.Background(), Options{
			Mode: ModeApply, Home: home, Runner: &fakeRunner{},
		}); err != nil {
			t.Fatal(err)
		}
		assertLink(t,
			filepath.Join(home, ".claude", "commands", "tokens.md"),
			filepath.Join(source, "tokens.md"))
	})

	t.Run("foreign link survives: a dangling link outside the blueprint is preserved", func(t *testing.T) {
		home := t.TempDir()
		foreign := filepath.Join(home, ".claude", "commands", "my-own-command.md")
		if err := os.MkdirAll(filepath.Dir(foreign), 0o700); err != nil {
			t.Fatal(err)
		}
		operatorSource := filepath.Join(home, "elsewhere", "my-own-command.md")
		if err := os.Symlink(operatorSource, foreign); err != nil {
			t.Fatal(err)
		}

		if _, err := Run(context.Background(), Options{
			Mode: ModeApply, Home: home, Runner: &fakeRunner{}, CodexHomes: []string{},
		}); err != nil {
			t.Fatal(err)
		}
		target, linked := resolvedLink(foreign)
		if !linked || target != filepath.Clean(operatorSource) {
			t.Fatalf(
				"preservation rule broke a foreign dangling link: target=%q linked=%v, want %q",
				target,
				linked,
				operatorSource,
			)
		}
	})

	t.Run(
		"regular file survives: a non-symlink entry in the registry is never a retirement candidate",
		func(t *testing.T) {
			home := t.TempDir()
			regular := filepath.Join(home, ".claude", "commands", "p.md")
			writeFixture(t, regular, "an operator's own plain command file\n")

			if _, err := Run(context.Background(), Options{
				Mode: ModeApply, Home: home, Runner: &fakeRunner{},
			}); err != nil {
				t.Fatal(err)
			}
			if got := readFixture(t, regular); got != "an operator's own plain command file\n" {
				t.Fatalf("installer touched a regular file in the command registry: %q", got)
			}
		},
	)
}

// TestRetireOrphanGlobalCommandsCannotLookReportsErrorNotSuccess pins the doc
// comment's honesty rule: a registry retireOrphanGlobalCommands cannot even
// READ must surface a wrapped error naming its path, never render as the
// silent no-op success of an empty registry that simply had no orphan.
//
// The method is called directly against a hand-built engine, the same
// isolation TestApplyRetiresDanglingBBLinksFromTheRecordedProfessorClone
// uses for retireBBInstall — going through the full Run() install sequence
// instead would hit retirePredecessors' own unwrapped Lstat on this same
// account's commands/chat/* first, which would make this test pass for a
// different function's error, not retireOrphanGlobalCommands' own.
func TestRetireOrphanGlobalCommandsCannotLookReportsErrorNotSuccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip(
			"running as root: chmod-denied directory reads are a no-op for root, so this failure cannot be forced genuinely here",
		)
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	unreadable := filepath.Join(config, "commands")
	if err := os.MkdirAll(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(unreadable, "placeholder.md"), "placeholder\n")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o700) })

	installer := &engine{
		options: Options{Mode: ModeApply, Home: home, ConfigDir: config, Stdout: io.Discard},
		apply:   true,
	}
	err := installer.retireOrphanGlobalCommands()
	if err == nil {
		t.Fatal(
			"expected an error surfacing the unreadable registry, got nil — a broken look must never render as a clean sweep",
		)
	}
	if !strings.Contains(err.Error(), unreadable) {
		t.Fatalf("error did not name the unreadable registry path %s: %v", unreadable, err)
	}
}
