package spawn

import (
	"context"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

type claudeTestLauncher struct{}

func (claudeTestLauncher) ComposerReady(string) bool { return true }
func (claudeTestLauncher) Rename(
	context.Context, pfmcall.Screen, pfmcall.Target, string, Timings, Trace,
) (string, error) {
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
	door pfmcall.Screen,
	target pfmcall.Target,
	name string,
	timings Timings,
	trace Trace,
) (string, error) {
	return RenameCodex(ctx, door, target, name, timings, trace)
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
