package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

// templateHarnessPromptsDir is the shipped tree the embedded assets are cut
// from, relative to this package.
func templateHarnessPromptsDir() string {
	return filepath.Join("..", "..", "..", "templates", harnessPromptsDirName)
}

// The shipped template tree and the embedded asset tree are the same source
// by contract — file for file, byte for byte, in BOTH directions. A template
// part with no embedded twin ships nothing; an embedded part with no template
// twin is a file no adopter can read or review.
func TestHarnessPromptTreeMatchesShippedTemplate(t *testing.T) {
	root := templateHarnessPromptsDir()
	template := map[string]bool{}
	if err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(relative)
		if slashed == "README.md" {
			// Human-facing, never staged: the assets carry no README.
			return nil
		}
		template[slashed] = true
		assertHarnessPromptPair(t, slashed)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(template) == 0 {
		t.Fatalf("no shipped harness-prompt templates found under %s", root)
	}
	embedded := 0
	if err := fs.WalkDir(embeddedAssets, path.Join("assets", harnessPromptsDirName), func(
		name string,
		entry fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative := strings.TrimPrefix(name, path.Join("assets", harnessPromptsDirName)+"/")
		embedded++
		if !template[relative] {
			t.Errorf("embedded asset %s has no shipped template twin", relative)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if embedded != len(template) {
		t.Fatalf("embedded harness-prompt files=%d, shipped templates=%d", embedded, len(template))
	}
}

func assertHarnessPromptPair(t *testing.T, relative string) []byte {
	t.Helper()
	embedded, err := readAsset(path.Join(harnessPromptsDirName, relative))
	if err != nil {
		t.Fatalf("embedded %s: %v", relative, err)
	}
	template, err := os.ReadFile(filepath.Join(templateHarnessPromptsDir(), filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(embedded, template) {
		t.Fatalf("embedded harness-prompts/%s differs from its shipped template", relative)
	}
	return embedded
}

func TestHarnessBaselineAssetPairIsCoherent(t *testing.T) {
	for _, stem := range []string{"harness-original", "harness-opus"} {
		t.Run(stem, func(t *testing.T) {
			baselines := path.Join("claude", "baselines")
			pin := assertHarnessPromptPair(t, path.Join(baselines, stem+".sha256"))
			fields := bytes.Fields(pin)
			if len(fields) != 2 {
				t.Fatalf("malformed baseline pin: %q", pin)
			}
			name := string(fields[1])
			prompt := assertHarnessPromptPair(t, path.Join(baselines, name))
			sum := sha256.Sum256(prompt)
			if hex.EncodeToString(sum[:]) != string(fields[0]) {
				t.Fatal("baseline body does not match pinned hash")
			}
			model := assertHarnessPromptPair(t, path.Join(baselines, stem+".model"))
			if len(bytes.TrimSpace(model)) == 0 {
				t.Fatal("baseline model provenance missing")
			}
		})
	}
}

// A fresh apply stages one prompt per engine, and each one is its three parts
// with exactly one blank line at each seam. The expectation is spelled out
// here from the SHIPPED templates rather than taken from composeHarnessPrompt,
// so a change to the joining rule has to be made twice to pass.
func TestInstallStagesComposedHarnessPrompts(t *testing.T) {
	home := t.TempDir()
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	root := templateHarnessPromptsDir()
	read := func(parts ...string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	head, tail := read("share", "head.md"), read("share", "tail.md")
	for _, id := range harnessPromptEngines {
		long := pfmengine.MustLookup(id).LongName
		t.Run(long, func(t *testing.T) {
			middle := read(long, "professor.md")
			staged := filepath.Join(home, ".local", "share", "pfm", "install", harnessPromptsDirName, long+".md")
			actual, err := os.ReadFile(staged)
			if err != nil {
				t.Fatalf("staged %s prompt: %v", long, err)
			}
			want := strings.TrimRight(head, "\n") + "\n\n" +
				strings.TrimRight(middle, "\n") + "\n\n" +
				strings.TrimRight(tail, "\n") + "\n"
			if string(actual) != want {
				t.Fatalf("staged %s.md is not head + middle + tail", long)
			}
			// The parts each end in a single newline today, so the seams are
			// also plain concatenation — a second, independent reading of
			// "exactly one blank line, no other bytes added".
			if string(actual) != head+"\n"+middle+"\n"+tail {
				t.Fatalf("staged %s.md seams are not one blank line over the parts as shipped", long)
			}
		})
	}
	// The parts and the Claude drift baselines stage beside the composed
	// prompts, in the one place doctor is pointed at.
	for _, relative := range []string{
		filepath.Join("share", "head.md"),
		filepath.Join("claude", "baselines", "harness-original.sha256"),
		filepath.Join("claude", "baselines", "harness-opus.sha256"),
	} {
		staged := filepath.Join(home, ".local", "share", "pfm", "install", harnessPromptsDirName, relative)
		if _, err := os.Stat(staged); err != nil {
			t.Fatalf("staged %s: %v", relative, err)
		}
	}
}

// OpenCode takes no prompt file on its command line and runs no appendix
// hook, so the staged prompt reaches it only by being named in the config's
// instructions array — and every other key, including an operator's own
// instruction entries, has to survive the write.
func TestInstallWiresOpenCodeInstructions(t *testing.T) {
	home := t.TempDir()
	config := OpenCodeConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, config, `{
  // operator comment
  "theme": "opencode",
  "instructions": ["./house-rules.md"]
}
`)
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: io.Discard, OpenCodeConfigPath: config,
	}); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(
		home, ".local", "share", "pfm", "install", harnessPromptsDirName, "opencode.md",
	)
	raw := readFixture(t, config)
	if !strings.Contains(raw, "// operator comment") {
		t.Fatalf("operator comment lost:\n%s", raw)
	}
	document := decodeOpenCodeFixture(t, raw)
	if document["theme"] != "opencode" {
		t.Fatalf("unrelated key lost:\n%s", raw)
	}
	entries, err := openCodeInstructionEntries(document, config)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0] != staged || entries[1] != "./house-rules.md" {
		t.Fatalf("instructions=%v, want the staged prompt ahead of the operator's entry", entries)
	}

	// A second apply is idempotent, and uninstall gives the operator's array back.
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: io.Discard, OpenCodeConfigPath: config,
	}); err != nil {
		t.Fatal(err)
	}
	again, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, readFixture(t, config)), config)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 2 || again[0] != staged {
		t.Fatalf("second apply changed instructions: %v", again)
	}
	if _, err := Run(context.Background(), Options{
		Mode: ModeUninstall, Home: home, Runner: &fakeRunner{}, Stdout: io.Discard, OpenCodeConfigPath: config,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, readFixture(t, config)), config)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0] != "./house-rules.md" {
		t.Fatalf("uninstall did not restore the operator's instructions: %v", after)
	}
	if _, err := os.Stat(staged); err == nil {
		t.Fatal("uninstall left the composed OpenCode prompt staged")
	}
}

// A malformed instructions value is an error, never a silent overwrite of
// whatever the operator put there.
func TestOpenCodeInstructionsRejectsForeignShape(t *testing.T) {
	for _, raw := range []string{`{"instructions": "house-rules.md"}`, `{"instructions": [1]}`} {
		document, err := decodeJSONCObject([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := openCodeInstructionEntries(document, "fixture.jsonc"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func decodeOpenCodeFixture(t *testing.T, raw string) map[string]any {
	t.Helper()
	sanitized, err := sanitizeJSONC([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(sanitized, &document); err != nil {
		t.Fatalf("decode OpenCode fixture: %v\n%s", err, raw)
	}
	return document
}
