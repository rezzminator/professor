package opencodegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSymlinkedGlobalCommandsAndCycle(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	commands := filepath.Join(home, ".claude", "commands")
	source := filepath.Join(root, "sources", "wave")
	writeTestFile(
		t,
		filepath.Join(source, "refine.md"),
		"---\ndescription: Refine.\n---\nRead then execute /wave:review.\n",
	)
	writeTestFile(t, filepath.Join(source, "review.md"), "---\ndescription: Review.\n---\nCheck.\n")
	if err := os.MkdirAll(commands, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(commands, "wave")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(commands, "context-meter", "SKILL.md"), "---\ndescription: Audit.\n---\nCount.\n")

	built, err := Compile(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil || !built.OK {
		t.Fatalf("symlinked command build result=%#v err=%v", built, err)
	}
	for _, path := range []string{
		filepath.Join(home, ".config", openCodeName(), "command", "wave-refine.md"),
		filepath.Join(home, ".config", openCodeName(), "command", "wave-review.md"),
		filepath.Join(home, ".config", openCodeName(), "command", "context-meter.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing global projection %s: %v", path, err)
		}
	}
	refine, _ := os.ReadFile(filepath.Join(home, ".config", openCodeName(), "command", "wave-refine.md"))
	if !strings.Contains(string(refine), "/wave-review") {
		t.Fatalf("global symlinked command was not flattened: %s", refine)
	}
	doctor, err := Compile(Options{Root: root, Home: home, Mode: ModeDoctor})
	if err != nil || !doctor.OK {
		t.Fatalf("doctor result=%#v err=%v", doctor, err)
	}

	if err := os.Symlink(
		filepath.Join("..", "..", "home", ".claude", "commands"),
		filepath.Join(source, "cycle"),
	); err != nil {
		t.Fatal(err)
	}
	cyclic, err := Compile(Options{Root: root, Home: home, Mode: ModeCheck})
	if err != nil || cyclic.OK || !strings.Contains(strings.ToLower(strings.Join(cyclic.Problems, "\n")), "cycle") {
		t.Fatalf("cyclic global source was not named as a failure: result=%#v err=%v", cyclic, err)
	}
}
