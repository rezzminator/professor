package harnessprompts

import (
	"bytes"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
)

// The tree the binary carries is a closed world: these are the parts the
// installer composes and stages, plus the README, and nothing else. Naming
// them here rather than walking and counting is the point — `go:embed` over a
// directory silently drops any name beginning with "." or "_", and a dropped
// baseline would otherwise read as a smaller tree that still passes.
var expectedParts = []string{
	"README.md",
	"claude/baselines/harness-opus-v2.1.300.md",
	"claude/baselines/harness-opus.model",
	"claude/baselines/harness-opus.sha256",
	"claude/baselines/harness-original-v2.1.300.md",
	"claude/baselines/harness-original.model",
	"claude/baselines/harness-original.sha256",
	"claude/professor.md",
	"codex/professor.md",
	"opencode/professor.md",
	"share/head.md",
	"share/tail.md",
}

func TestEveryExpectedPartIsReadable(t *testing.T) {
	for _, name := range expectedParts {
		content, err := ReadPart(name)
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		if len(content) == 0 {
			t.Errorf("embedded %s is empty", name)
		}
	}
}

// FS walks exactly the expected parts — an extra file in the tree is as much
// a defect as a missing one, because the installer stages whatever it finds.
func TestFSHoldsExactlyTheExpectedParts(t *testing.T) {
	var found []string
	if err := fs.WalkDir(FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			found = append(found, name)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk embedded tree: %v", err)
	}
	sort.Strings(found)
	want := append([]string(nil), expectedParts...)
	sort.Strings(want)
	if len(found) != len(want) {
		t.Fatalf("embedded tree holds %d files, want %d: %v", len(found), len(want), found)
	}
	for index := range want {
		if found[index] != want[index] {
			t.Fatalf("embedded tree holds %q where %q was expected", found[index], want[index])
		}
	}
}

// A part that could not be read reports the failure by name, never an empty
// result a caller could mistake for an empty file.
func TestReadPartNamesTheMissingPart(t *testing.T) {
	content, err := ReadPart("share/nothing-here.md")
	if err == nil {
		t.Fatalf("read of an absent part returned %d bytes and no error", len(content))
	}
	if !strings.Contains(err.Error(), "share/nothing-here.md") {
		t.Fatalf("error does not name the part: %v", err)
	}
}

// TopLevel names the root of the embedded tree — the first element of every
// expected part, each once — so doctor can hold a clone entry the binary
// never embeds outside its comparison.
func TestTopLevelNamesTheEmbeddedRoot(t *testing.T) {
	seen := map[string]bool{}
	var want []string
	for _, name := range expectedParts {
		first, _, _ := strings.Cut(name, "/")
		if !seen[first] {
			seen[first] = true
			want = append(want, first)
		}
	}
	sort.Strings(want)
	got := TopLevel()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("TopLevel() = %v, want %v", got, want)
	}
}

// The prompt-byte contract is exactly the source parts in order, with one
// blank line at each seam and one final newline. No tracked output is needed.
func TestComposeMatchesSourceParts(t *testing.T) {
	for _, engine := range []string{"claude", "codex", "opencode"} {
		t.Run(engine, func(t *testing.T) {
			got, err := Composed(engine)
			if err != nil {
				t.Fatal(err)
			}
			remaining := got
			for index, part := range []string{"share/head.md", engine + "/professor.md", "share/tail.md"} {
				raw, err := os.ReadFile(part)
				if err != nil {
					t.Fatal(err)
				}
				suffix := "\n\n"
				if index == 2 {
					suffix = "\n"
				}
				want := append(bytes.TrimRight(raw, "\n"), suffix...)
				if !bytes.HasPrefix(remaining, want) {
					t.Fatalf("assembled %s prompt differs at %s or its seam", engine, part)
				}
				remaining = remaining[len(want):]
			}
			if len(remaining) != 0 {
				t.Fatalf("assembled %s prompt has %d unexpected trailing bytes", engine, len(remaining))
			}
		})
	}
	if _, err := Composed(
		"no-such-engine",
	); err == nil ||
		!strings.Contains(err.Error(), "no-such-engine/professor.md") {
		t.Fatalf("assembly did not name missing engine part: %v", err)
	}
}
