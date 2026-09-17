package installer

import (
	"path/filepath"
	"testing"
)

func TestReloadCommandTargetMigratesLegacySwap(t *testing.T) {
	installer := &engine{options: Options{ConfigDir: filepath.Join(t.TempDir(), ".claude")}}
	target, ok := installer.commandTarget("reload.command.md")
	if !ok || filepath.Base(target) != "reload.md" {
		t.Fatalf("reload target=%q found=%v", target, ok)
	}
	if _, ok := installer.commandTarget("swap.command.md"); ok {
		t.Fatal("legacy swap asset still maps as an installed command")
	}
}

func TestSkillTargetMapsHandoffUnderItsOwnDir(t *testing.T) {
	installer := &engine{options: Options{ConfigDir: filepath.Join(t.TempDir(), ".claude")}}
	target, ok := installer.skillTarget(installer.options.ConfigDir, "handoff.skill.md")
	if !ok {
		t.Fatal("handoff.skill.md does not map to a skill target")
	}
	if filepath.Base(target) != "SKILL.md" || filepath.Base(filepath.Dir(target)) != "handoff" {
		t.Fatalf("handoff skill target=%q, want .../skills/handoff/SKILL.md", target)
	}
	if _, ok := installer.skillTarget(installer.options.ConfigDir, "reload.command.md"); ok {
		t.Fatal("a command asset must not also resolve as a skill target")
	}
}
