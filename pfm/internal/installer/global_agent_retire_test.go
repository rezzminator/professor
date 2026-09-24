package installer

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestRetireOrphanGlobalAgentsPrunesUndeclaredVariants pins the variant half
// of retireOrphanGlobalAgents' doc comment promise: a variant link resolving
// into paths.GeneratedClaudeAgentsDir retires — along with its rendered
// generated file — the moment its own installed.Source is no longer among
// this run's plan, while a still-declared variant's link and file survive
// untouched.
func TestRetireOrphanGlobalAgentsPrunesUndeclaredVariants(t *testing.T) {
	home := t.TempDir()
	generatedDir := paths.GeneratedClaudeAgentsDir(home)
	keepFile := filepath.Join(generatedDir, "keep.md")
	dropFile := filepath.Join(generatedDir, "drop.md")
	writeFixture(t, keepFile, "---\nname: keep\n---\n\nbody\n")
	writeFixture(t, dropFile, "---\nname: drop\n---\n\nbody\n")

	config := filepath.Join(home, ".claude")
	registry := filepath.Join(config, "agents")
	keepLink := filepath.Join(registry, "keep.md")
	dropLink := filepath.Join(registry, "drop.md")
	if err := os.MkdirAll(registry, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(keepFile, keepLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dropFile, dropLink); err != nil {
		t.Fatal(err)
	}

	installer := &engine{
		options: Options{Mode: ModeApply, Home: home, ConfigDir: config, Stdout: io.Discard},
		apply:   true,
	}
	// The plan this run just produced declares only "keep" — "drop" was
	// removed from variants.json before this install ran.
	installed := []codexgen.GlobalAgentInstalled{{Path: keepLink, Source: keepFile}}

	if err := installer.retireOrphanGlobalAgents(installed); err != nil {
		t.Fatalf("retireOrphanGlobalAgents: %v", err)
	}

	if _, err := os.Lstat(dropLink); !os.IsNotExist(err) {
		t.Fatalf("undeclared variant link survived: %v", err)
	}
	if _, err := os.Lstat(dropFile); !os.IsNotExist(err) {
		t.Fatalf("undeclared variant's generated file survived: %v", err)
	}
	if _, err := os.Lstat(keepLink); err != nil {
		t.Fatalf("declared variant link was removed: %v", err)
	}
	if _, err := os.Lstat(keepFile); err != nil {
		t.Fatalf("declared variant's generated file was removed: %v", err)
	}
}

// TestRetireOrphanGlobalAgentsPrunesDanglingOriginals pins the original-agent
// half of the same promise: a link resolving at <recorded professor repo>/
// templates/global/agents/<its own name> retires only once that source no
// longer exists, while a live original link, a plain regular file, and a
// dangling link pointing outside the blueprint entirely all survive — the
// same preservation rule retireOrphanGlobalCommands holds to for commands.
func TestRetireOrphanGlobalAgentsPrunesDanglingOriginals(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, ".professor")
	liveSource := filepath.Join(repo, "templates", "global", "agents", "alpha.md")
	writeFixture(t, liveSource, "---\nname: alpha\n---\n\nbody\n")
	// "gone.md" is never written to the source: this link models exactly what
	// an upstream agent deletion leaves behind.
	goneSource := filepath.Join(repo, "templates", "global", "agents", "gone.md")

	config := filepath.Join(home, ".claude")
	registry := filepath.Join(config, "agents")
	if err := os.MkdirAll(registry, 0o700); err != nil {
		t.Fatal(err)
	}
	liveLink := filepath.Join(registry, "alpha.md")
	if err := os.Symlink(liveSource, liveLink); err != nil {
		t.Fatal(err)
	}
	goneLink := filepath.Join(registry, "gone.md")
	if err := os.Symlink(goneSource, goneLink); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(registry, "operator.md")
	writeFixture(t, regular, "an operator's own plain agent file\n")
	foreignLink := filepath.Join(registry, "foreign.md")
	foreignTarget := filepath.Join(home, "elsewhere", "foreign.md")
	if err := os.Symlink(foreignTarget, foreignLink); err != nil {
		t.Fatal(err)
	}

	installer := &engine{
		options: Options{Mode: ModeApply, Home: home, ConfigDir: config, Stdout: io.Discard},
		apply:   true,
	}

	if err := installer.retireOrphanGlobalAgents(nil); err != nil {
		t.Fatalf("retireOrphanGlobalAgents: %v", err)
	}

	if _, err := os.Lstat(goneLink); !os.IsNotExist(err) {
		t.Fatalf("dangling original link survived: %v", err)
	}
	if _, err := os.Lstat(liveLink); err != nil {
		t.Fatalf("live original link was removed: %v", err)
	}
	if got := readFixture(t, regular); got != "an operator's own plain agent file\n" {
		t.Fatalf("touched a regular file in the agent registry: %q", got)
	}
	target, linked := resolvedLink(foreignLink)
	if !linked || target != filepath.Clean(foreignTarget) {
		t.Fatalf(
			"preservation rule broke a foreign dangling link: target=%q linked=%v, want %q",
			target, linked, foreignTarget,
		)
	}
}

// TestRetireOrphanGlobalAgentsCannotLookReportsErrorNotSuccess mirrors
// TestRetireOrphanGlobalCommandsCannotLookReportsErrorNotSuccess: a registry
// retireOrphanGlobalAgents cannot even READ must surface a wrapped error
// naming its path, never render as the silent no-op success of a registry
// that simply had no orphan.
func TestRetireOrphanGlobalAgentsCannotLookReportsErrorNotSuccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip(
			"running as root: chmod-denied directory reads are a no-op for root, so this failure cannot be forced genuinely here",
		)
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	unreadable := filepath.Join(config, "agents")
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
	err := installer.retireOrphanGlobalAgents(nil)
	if err == nil {
		t.Fatal(
			"expected an error surfacing the unreadable registry, got nil — a broken look must never render as a clean sweep",
		)
	}
	if !strings.Contains(err.Error(), unreadable) {
		t.Fatalf("error did not name the unreadable registry path %s: %v", unreadable, err)
	}
}
