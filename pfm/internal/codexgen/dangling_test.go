package codexgen

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests pin the typed Dangling classifier that replaced a
// prose-scanning strings.Contains(warning, "dangling") gate in compiler.go.
// The old classifier had three defects this file exercises directly:
//
//  1. Classifying by substring of rendered prose is a coincidence detector —
//     an unrelated warning that happens to carry the word "dangling" armed
//     the gate against nothing real (TestCyclicWarningMentioningDanglingDoesNotGateCheck).
//  2. Build must tolerate a dangling source (exit 0, still writes) while
//     Check gates on it (exit 1) — an intentional asymmetry that a careless
//     "fix" could collapse in either direction.
//  3. The Check problem must carry the actionable remedy so an operator can
//     clear it.

// dangleRemedy is the exact clause compiler.go appends to every promoted
// Dangling entry. Both ModeCheck promotion sites (Run and RunGlobalCommands)
// must emit it verbatim.
const dangleRemedy = "retire the stale link; `pfm install` prunes an orphaned global-command link automatically"

// TestDanglingRepoCommandGatesCheckNotBuildWithRemedy pins cases 1-3 against
// the repository-command source tree, which Run's own ModeCheck promotion
// block (compiler.go, first site) reads from result.Dangling.
func TestDanglingRepoCommandGatesCheckNotBuildWithRemedy(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
	commandDir := filepath.Join(root, ".claude", "commands")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-command.md", filepath.Join(commandDir, "gone.md")); err != nil {
		t.Fatal(err)
	}

	// Case 1: Check gates on the dangling source and names the path.
	check, err := Check(Options{Root: root, Home: home})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if check.OK {
		t.Fatalf("check.OK = true, want false for a dangling repo command source: %#v", check)
	}
	if !containsFinding(check.Problems, "gone.md") {
		t.Fatalf("check problems do not name the dangling path: %#v", check.Problems)
	}
	// Case 3: the promoted problem carries the operator remedy.
	if !containsFinding(check.Problems, dangleRemedy) {
		t.Fatalf("check problem missing the pfm install remedy clause: %#v", check.Problems)
	}

	// Case 2: the SAME fixture through Build must not gate — Build tolerates
	// a dangling source and still writes.
	build, err := Build(Options{Root: root, Home: home})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !build.OK {
		t.Fatalf("build.OK = false, want true: a dangling source must not gate build: %#v", build)
	}
	if !containsFinding(build.Warnings, "dangling") {
		t.Fatalf("build did not warn about the dangling source: %#v", build.Warnings)
	}
	if !containsFinding(build.Dangling, "gone.md") {
		t.Fatalf("Result.Dangling was not populated for the discoverMarkdown symlink-stat emitter: %#v", build.Dangling)
	}
	if containsFinding(build.Problems, "gone.md") {
		t.Fatalf("build gated on the dangling source, breaking the build/check asymmetry: %#v", build.Problems)
	}
}

// TestDanglingGlobalCommandViaRunGlobalCommandsGatesCheckNotBuildWithRemedy
// pins the SECOND ModeCheck promotion site: RunGlobalCommands, the installer
// entry point that compiles $HOME/.claude/commands alone (no repository
// root). It must show the identical asymmetry and remedy as the repository
// path above, since the fix touched both sites independently.
func TestDanglingGlobalCommandViaRunGlobalCommandsGatesCheckNotBuildWithRemedy(t *testing.T) {
	home := t.TempDir()
	commandDir := filepath.Join(home, ".claude", "commands")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-global-command.md", filepath.Join(commandDir, "gone-global.md")); err != nil {
		t.Fatal(err)
	}

	check, err := RunGlobalCommands(GlobalCommandsOptions{Home: home, Mode: ModeCheck})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if check.OK {
		t.Fatalf("check.OK = true, want false for a dangling global command source: %#v", check)
	}
	if !containsFinding(check.Problems, "gone-global.md") {
		t.Fatalf("check problems do not name the dangling path: %#v", check.Problems)
	}
	if !containsFinding(check.Problems, dangleRemedy) {
		t.Fatalf("check problem missing the pfm install remedy clause: %#v", check.Problems)
	}

	build, err := RunGlobalCommands(GlobalCommandsOptions{Home: home, Mode: ModeBuild})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !build.OK {
		t.Fatalf("build.OK = false, want true: a dangling source must not gate build: %#v", build)
	}
	if !containsFinding(build.Dangling, "gone-global.md") {
		t.Fatalf("Result.Dangling was not populated: %#v", build.Dangling)
	}
}

// TestDanglingRepoSkillLinkGatesCheckAndPopulatesDangling reaches the
// compileRepoSkills emitter (the fourth danglingSource call site): a broken
// symlink directly under .claude/skills, which os.ReadDir lists without
// following, so the dangling condition surfaces only on the subsequent
// os.Stat.
func TestDanglingRepoSkillLinkGatesCheckAndPopulatesDangling(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
	skillsDir := filepath.Join(root, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(
		filepath.Join(root, "does-not-exist-skill-target"),
		filepath.Join(skillsDir, "broken-skill"),
	); err != nil {
		t.Fatal(err)
	}

	check, err := Check(Options{Root: root, Home: home})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if check.OK {
		t.Fatalf("check.OK = true, want false for a dangling skill link: %#v", check)
	}
	if !containsFinding(check.Problems, "broken-skill") {
		t.Fatalf("check problems do not name the dangling skill link: %#v", check.Problems)
	}
	if !containsFinding(check.Problems, dangleRemedy) {
		t.Fatalf("check problem missing the pfm install remedy clause: %#v", check.Problems)
	}

	build, err := Build(Options{Root: root, Home: home})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !build.OK {
		t.Fatalf("build.OK = false, want true: a dangling skill link must not gate build: %#v", build)
	}
	if !containsFinding(build.Dangling, "broken-skill") {
		t.Fatalf("Result.Dangling was not populated for the compileRepoSkills emitter: %#v", build.Dangling)
	}
}

// TestCyclicWarningMentioningDanglingDoesNotGateCheck is the case that
// actually pins the fix. Under the old classifier, ANY warning whose
// rendered text happened to contain "dangling" armed the Check gate — a
// coincidence detector, not a real defect signal. Here a command directory
// is merely NAMED "dangling"; walking into a same-directory symlink loop
// beneath it produces an ordinary "skip cyclic command directory ...dangling/loop"
// warning that was never recorded through danglingSource. That warning must
// stay a warning: Check must read the typed Result.Dangling field, never
// scan Warnings text.
func TestCyclicWarningMentioningDanglingDoesNotGateCheck(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
	danglingNamedDir := filepath.Join(root, ".claude", "commands", "dangling")
	if err := os.MkdirAll(danglingNamedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink to "." inside "dangling" resolves back to danglingNamedDir
	// itself: walking into it hits the already-visited branch and emits a
	// cyclic-directory warning whose path contains "dangling" — with no
	// source ever missing.
	if err := os.Symlink(".", filepath.Join(danglingNamedDir, "loop")); err != nil {
		t.Fatal(err)
	}

	// Build first so the fixture is at rest: Check must fail only on the
	// cyclic-named-"dangling" warning under test, never on an unrelated
	// MISSING/STALE finding from a never-materialized AGENTS.md.
	if build, err := Build(Options{Root: root, Home: home}); err != nil || !build.OK {
		t.Fatalf("seed build: result=%#v err=%v", build, err)
	}

	check, err := Check(Options{Root: root, Home: home})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !containsFinding(check.Warnings, "dangling") {
		t.Fatalf(
			"fixture did not produce a warning mentioning \"dangling\" via the cyclic path — test no longer exercises the coincidence case: %#v",
			check.Warnings,
		)
	}
	if len(check.Dangling) != 0 {
		t.Fatalf(
			"Result.Dangling must stay empty for a merely-named cyclic path, not a real dangling source: %#v",
			check.Dangling,
		)
	}
	if !check.OK {
		t.Fatalf(
			"a warning that merely CONTAINS the word \"dangling\" gated Check; classification must be typed (Result.Dangling), never a textual scan of Warnings: %#v",
			check.Problems,
		)
	}
}
