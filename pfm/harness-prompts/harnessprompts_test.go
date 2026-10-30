package harnessprompts

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
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

// The prompt-byte contract is exactly the source parts in order, with one
// blank line at each seam and one final newline. No tracked output is needed.
func TestComposeMatchesSourceParts(t *testing.T) {
	for _, engine := range []string{"claude", "codex", "opencode"} {
		t.Run(engine, func(t *testing.T) {
			got, err := Composed(t.TempDir(), engine)
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
		t.TempDir(), "no-such-engine",
	); err == nil ||
		!strings.Contains(err.Error(), "no-such-engine/professor.md") {
		t.Fatalf("assembly did not name missing engine part: %v", err)
	}
}

// writeCloneTree lays out a clone whose harness-prompt tree holds the given
// parts, and returns the clone root.
func writeCloneTree(t *testing.T, parts map[string]string) string {
	t.Helper()
	clone := t.TempDir()
	for name, content := range parts {
		path := filepath.Join(clone, "pfm", DirName, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return clone
}

// A clone's tree is the source: its edited head reaches the composed prompt
// with no rebuild, where the embed would still carry the old one.
func TestComposedReadsTheCloneTree(t *testing.T) {
	clone := writeCloneTree(t, map[string]string{
		"share/head.md":       "clone head\n",
		"claude/professor.md": "clone middle\n",
		"share/tail.md":       "clone tail\n",
	})
	got, err := Composed(clone, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if want := "clone head\n\nclone middle\n\nclone tail\n"; string(got) != want {
		t.Fatalf("Composed = %q, want %q", got, want)
	}
}

// A clone tree that exists but lacks a part is an error naming the part and
// the tree, never a quiet fallback to the embedded copy.
func TestComposedNamesAPartMissingFromTheCloneTree(t *testing.T) {
	clone := writeCloneTree(t, map[string]string{
		"share/head.md":       "clone head\n",
		"claude/professor.md": "clone middle\n",
	})
	_, err := Composed(clone, "claude")
	dir := filepath.Join(clone, "pfm", DirName)
	if err == nil || !strings.Contains(err.Error(), "share/tail.md") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("Composed with a missing tail = %v, want an error naming share/tail.md in %s", err, dir)
	}
}

// No clone tree at all is a binary-only install: the embedded tree composes.
func TestComposedFallsBackToTheEmbedWithoutACloneTree(t *testing.T) {
	got, err := Composed(t.TempDir(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	head, err := ReadPart("share/head.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, bytes.TrimRight(head, "\n")) {
		t.Fatal("Composed without a clone tree does not start with the embedded head")
	}
	generatedOnly := writeCloneTree(t, map[string]string{"generated/claude.md": "an earlier install's output\n"})
	fromGenerated, err := Composed(generatedOnly, "claude")
	if err != nil || !bytes.Equal(fromGenerated, got) {
		t.Fatalf("a tree holding only generated/ did not compose from the embed: %v", err)
	}
	if _, err := Composed("", "claude"); err == nil {
		t.Fatal("Composed with no clone path composed instead of refusing")
	}
}

// A binary-only host's tree holding generated/ beside a stray file (an editor
// swap file, .DS_Store) is no source tree: the embed composes, never an error
// for a part the host was never meant to hold.
func TestComposedIgnoresStrayFilesBesideGenerated(t *testing.T) {
	clone := writeCloneTree(t, map[string]string{
		"generated/claude.md": "an earlier install's output\n",
		".DS_Store":           "finder metadata\n",
	})
	got, err := Composed(clone, "claude")
	if err != nil {
		t.Fatalf("Composed with only generated/ and a stray file: %v", err)
	}
	want, err := Composed(t.TempDir(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("Composed with only generated/ and a stray file does not equal the embedded composition")
	}
}
