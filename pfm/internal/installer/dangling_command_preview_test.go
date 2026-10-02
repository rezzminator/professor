package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestCommandPreviewSurvivesDanglingGlobalCommandLink pins the preview
// staging's obligation to the very links the install exists to retire. An
// apply run opens with a dry-run preflight, and only a dry run stages the
// current command tree to preview the Codex plan; a retirement that deletes
// templates/global/commands/<name> leaves the host link at
// .claude/commands/<name> dangling until retireDeadRegistryLinks prunes it,
// which runs AFTER that preflight. So preview must step over a dangling link
// rather than abort on it — otherwise the installer can never reach the prune
// that would have fixed the tree, and every upgrade across the retirement
// fails with the link's own resolution error.
//
// TestInspectDeadRegistryLinks proves the prune
// itself, but runs ModeApply, where future=!apply is false and the preview path
// never executes — the gap this test closes.
func TestCommandPreviewSurvivesDanglingGlobalCommandLink(t *testing.T) {
	t.Parallel()
	for _, mode := range []struct {
		name string
		mode Mode
	}{
		{"dry run stages the preview directly", ModeDryRun},
		{"apply reaches the preview through its preflight", ModeApply},
	} {
		t.Run(mode.name, func(t *testing.T) {
			home := t.TempDir()
			source := filepath.Join(home, ".professor", "templates", "global", "commands")
			writeFixture(t, filepath.Join(source, "tokens.md"), "# tokens command\n")

			// The retired namespace: a host link whose blueprint target the
			// release deleted. os.Lstat sees it, EvalSymlinks cannot resolve it.
			dangling := filepath.Join(home, ".claude", "commands", "p")
			if err := os.MkdirAll(filepath.Dir(dangling), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(source, "p"), dangling); err != nil {
				t.Fatal(err)
			}

			if _, err := Run(context.Background(), Options{
				MCPConfigPath: testConfigPath(t),
				Mode:          mode.mode,
				Home:          home,
				ConfigDir:     filepath.Join(home, ".claude"),
				Runner:        &fakeRunner{},
			}); err != nil {
				t.Fatalf("install aborted on a dangling global-command link it was about to retire: %v", err)
			}
		})
	}
}
