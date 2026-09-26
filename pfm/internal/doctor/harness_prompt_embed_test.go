package doctor

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
)

// materializeHarnessPromptTree writes the binary's embedded tree to disk —
// the state of a clone whose templates the running binary was built from.
// Taken from the embed FS rather than a fixture, so no byte of the tree is
// pinned twice.
func materializeHarnessPromptTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := fs.WalkDir(harnessprompts.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := fs.ReadFile(harnessprompts.FS(), name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	}); err != nil {
		t.Fatalf("materialize embedded harness prompt tree: %v", err)
	}
	return root
}

func reportHarnessPromptEmbed(t *testing.T, tree string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	warnings := printHarnessPromptEmbedReport(&out, inspectHarnessPromptEmbed(tree))
	return strings.TrimSpace(out.String()), warnings
}

// Equal: the clone's tree and the binary's are the same bytes.
func TestHarnessPromptEmbedReportsOKWhenTreesMatch(t *testing.T) {
	line, warnings := reportHarnessPromptEmbed(t, materializeHarnessPromptTree(t))
	if warnings != 0 {
		t.Fatalf("matching trees warned: %s", line)
	}
	if !strings.Contains(line, "harness-prompts embed=ok") {
		t.Fatalf("no ok row: %s", line)
	}
	if strings.Contains(line, "files=0") {
		t.Fatalf("ok row counted no files — the comparison did not run: %s", line)
	}
}

// Differ: every file the two trees disagree about is named, and the row
// prescribes no direction — a difference says which files, never which side
// is the newer one.
func TestHarnessPromptEmbedNamesTheDifferingFiles(t *testing.T) {
	tree := materializeHarnessPromptTree(t)
	edited := filepath.Join(tree, "share", "tail.md")
	content, err := os.ReadFile(edited)
	if err != nil {
		t.Fatal(err)
	}
	edit := string(content) + "\nan edit one of the two sides does not have\n"
	if err := os.WriteFile(edited, []byte(edit), 0o644); err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(tree, "share", "middle.md")
	if err := os.WriteFile(added, []byte("a part only one of the two sides has\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	line, warnings := reportHarnessPromptEmbed(t, tree)
	if warnings != 1 {
		t.Fatalf("differing trees warned %d times: %s", warnings, line)
	}
	for _, want := range []string{
		"harness-prompts embed=MISMATCH",
		"share/tail.md",
		"share/middle.md(only-in-clone)",
		// Both directions, so a clone checked out older than the binary is
		// never told to overwrite the newer prompts it carries.
		"if the clone holds the newer prompts, rebuild pfm",
		"if the clone is checked out to an older revision than this binary",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("MISMATCH row does not carry %q: %s", want, line)
		}
	}
	if strings.Contains(line, "embed=ok") {
		t.Fatalf("MISMATCH row also reads as ok: %s", line)
	}
}

// Could not look: an absent clone tree is an error, never the ok row and
// never silence.
func TestHarnessPromptEmbedReportsAnUnreadableClone(t *testing.T) {
	line, warnings := reportHarnessPromptEmbed(t, filepath.Join(t.TempDir(), "no-clone-here", "harness-prompts"))
	if warnings != 1 {
		t.Fatalf("an unreadable clone warned %d times: %s", warnings, line)
	}
	if line == "" {
		t.Fatal("an unreadable clone reported nothing at all")
	}
	if !strings.Contains(line, "harness-prompts embed=CHECK FAILED") {
		t.Fatalf("no CHECK FAILED row: %s", line)
	}
	if strings.Contains(line, "embed=ok") || strings.Contains(line, "embed=MISMATCH") {
		t.Fatalf("a failed look reads as a verdict: %s", line)
	}
	if !strings.Contains(line, "no-clone-here") {
		t.Fatalf("CHECK FAILED row does not name the tree it could not read: %s", line)
	}
}

// A file in the clone's tree that `go:embed` would never carry — this
// package's own sources, a dot-file the OS drops in — is not a difference
// between the trees.
func TestHarnessPromptEmbedIgnoresWhatEmbedNeverCarries(t *testing.T) {
	tree := materializeHarnessPromptTree(t)
	for name, content := range map[string]string{
		"harnessprompts.go":      "package harnessprompts\n",
		"harnessprompts_test.go": "package harnessprompts\n",
		".DS_Store":              "\x00",
	} {
		if err := os.WriteFile(filepath.Join(tree, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	line, warnings := reportHarnessPromptEmbed(t, tree)
	if warnings != 0 {
		t.Fatalf("non-template files read as a difference: %s", line)
	}
}

// A top-level entry the clone gains that this binary's `go:embed` never
// names — a new engine directory, a stray note — is outside what the binary
// carries, so it is not a difference between the trees.
func TestHarnessPromptEmbedIgnoresATopLevelEntryTheBinaryNeverEmbeds(t *testing.T) {
	tree := materializeHarnessPromptTree(t)
	if err := os.MkdirAll(filepath.Join(tree, "newengine"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"newengine/professor.md": "a middle the binary does not embed\n",
		"NOTES.md":               "a root file the binary does not embed\n",
	} {
		if err := os.WriteFile(filepath.Join(tree, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	line, warnings := reportHarnessPromptEmbed(t, tree)
	if warnings != 0 || !strings.Contains(line, "harness-prompts embed=ok") {
		t.Fatalf("a top-level entry outside the embed read as a difference (warnings=%d): %s", warnings, line)
	}
}

// The resolver door, both ways round: a host with no blueprint clone has
// nothing to be behind and is named rather than warned, while a clone that IS
// there but carries no readable tree is a failed look.
func TestHarnessPromptEmbedDoctorNamesAHomeWithNoClone(t *testing.T) {
	var out bytes.Buffer
	warnings := printHarnessPromptEmbedDoctor(&out, t.TempDir())
	line := strings.TrimSpace(out.String())
	if warnings != 0 {
		t.Fatalf("a home with no clone warned %d times: %s", warnings, line)
	}
	if !strings.Contains(line, "harness-prompts embed=no-clone") {
		t.Fatalf("no no-clone row: %s", line)
	}
	if strings.Contains(line, "embed=ok") {
		t.Fatalf("an unchecked host reads as ok: %s", line)
	}
}

func TestHarnessPromptEmbedDoctorReportsACloneWithNoTree(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".professor", "pfm"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	warnings := printHarnessPromptEmbedDoctor(&out, home)
	line := strings.TrimSpace(out.String())
	if warnings != 1 {
		t.Fatalf("a clone with no harness-prompt tree warned %d times: %s", warnings, line)
	}
	if !strings.Contains(line, "harness-prompts embed=CHECK FAILED") {
		t.Fatalf("no CHECK FAILED row: %s", line)
	}
}
