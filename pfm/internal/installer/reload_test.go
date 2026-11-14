package installer

import (
	"path/filepath"
	"testing"
)

func TestReloadCommandTargetMigratesLegacySwap(t *testing.T) {
	t.Parallel()
	installer := &engine{options: Options{ConfigDir: filepath.Join(t.TempDir(), ".claude")}}
	target, ok := installer.commandTarget("reload.command.md")
	if !ok || filepath.Base(target) != "reload.md" {
		t.Fatalf("reload target=%q found=%v", target, ok)
	}
	if _, ok := installer.commandTarget("swap.command.md"); ok {
		t.Fatal("legacy swap asset still maps as an installed command")
	}
}
