package spawn

import (
	"context"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

type claudeTestLauncher struct{}

func (claudeTestLauncher) ComposerReady(string) bool { return true }
func (claudeTestLauncher) Rename(context.Context, Tmux, string, string, string, Timings, Trace) (string, error) {
	return "", nil
}

type codexTestLauncher struct{}

func (codexTestLauncher) ComposerReady(capture string) bool { return CodexComposerReady(capture) }

func TestCodexDefaultFooterComposerIsReady(t *testing.T) {
	capture := `╭───────────────────────────────────────────────────╮
│ >_ OpenAI Codex (v0.149.1)                        │
│ model:       gpt-5.6-sol xhigh   /model to change │
│ directory:   /worktree                            │
╰───────────────────────────────────────────────────╯

› Ask Codex to do anything

  gpt-5.6-sol xhigh · /worktree`
	if !CodexComposerReady(capture) {
		t.Fatal("Codex 0.149 default footer composer was not recognized")
	}
}

func (codexTestLauncher) Rename(
	ctx context.Context,
	tmux Tmux,
	socket, target, name string,
	timings Timings,
	trace Trace,
) (string, error) {
	return RenameCodex(ctx, tmux, socket, target, name, timings, trace)
}

func init() {
	RegisterLauncher(pfmengine.Claude, claudeTestLauncher{})
	RegisterLauncher(pfmengine.Codex, codexTestLauncher{})
}

func TestUnknownEngineIsANamedError(t *testing.T) {
	_, err := LauncherFor(pfmengine.ID("zz"))
	if err == nil || err.Error() != "engine zz: no launcher registered" {
		t.Fatalf("LauncherFor(zz) error = %v", err)
	}
}
