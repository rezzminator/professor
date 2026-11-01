package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const vscodeLawFixture = "---\nname: how-to-build-prompt\ndescription: routing\n---\n\n# Prompt Quality\n\nCut every line.\n"

// writeVSCodeLawFixture puts a /how-to-build-prompt source into the clone repo and
// returns repo.
func writeVSCodeLawFixture(t *testing.T, repo string) string {
	t.Helper()
	writeFixture(t, vscodeLawSource(repo), vscodeLawFixture)
	return repo
}

// vscodeLawHome is a home whose install resolves the clone ~/.professor (no
// source-repo marker), holding the law.
func vscodeLawHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	writeVSCodeLawFixture(t, filepath.Join(home, ".professor"))
	return home
}

// TestVSCodeSystemPromptIsPromptLintThenTheLaw: wireVSCode builds
// prompt-lint.system.md in the staged extension from the embedded
// prompt-lint.md and the clone's law without its frontmatter; a rerun changes
// nothing, and an edited law is rebuilt.
func TestVSCodeSystemPromptIsPromptLintThenTheLaw(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	first := newVSCodeExtensionEngine(t, home, []string{}, true)
	if err := first.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	lint, err := readAsset(vscodeLintPromptAsset)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(first.managedRoot, "vscode", "professor", "prompt-lint.system.md")
	want := strings.TrimRight(string(lint), "\n") + "\n\n# Prompt Quality\n\nCut every line.\n"
	if got := readFixture(t, target); got != want {
		t.Fatalf("prompt-lint.system.md =\n%s\nwant\n%s", got, want)
	}

	second := newVSCodeExtensionEngine(t, home, []string{}, true)
	if err := second.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	if second.report.Changed != 0 {
		t.Fatalf("rerun reported %d changes, want 0:\n%s", second.report.Changed, second.options.Stdout.(*bytes.Buffer))
	}

	third := newVSCodeExtensionEngine(t, home, []string{}, true)
	writeFixture(t, vscodeLawSource(third.options.SourceRepo), "# Prompt Quality\n\nCut harder.\n")
	if err := third.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	if got, want := readFixture(
		t,
		target,
	), strings.TrimRight(
		string(lint),
		"\n",
	)+"\n\n# Prompt Quality\n\nCut harder.\n"; got != want {
		t.Fatalf("rebuilt prompt-lint.system.md =\n%s\nwant\n%s", got, want)
	}
}

// TestVSCodeSystemPromptMissingLawFailsByPath: a clone without the law fails
// the VS Code step naming the law's path and writes no system prompt, while
// the extension link still lands.
func TestVSCodeSystemPromptMissingLawFailsByPath(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	root := filepath.Join(home, "product-a")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	installer := newVSCodeExtensionEngine(t, home, []string{root}, true)
	installer.options.SourceRepo = filepath.Join(home, "empty-clone")
	err := installer.wireVSCode()
	law := vscodeLawSource(installer.options.SourceRepo)
	if err == nil || !strings.Contains(err.Error(), law) {
		t.Fatalf("wireVSCode error = %v, want one naming %s", err, law)
	}
	requireNoPath(t, installer.vscodeSystemPromptPath(), "system prompt built without the law")
	if _, linked := resolvedLink(filepath.Join(root, "extensions", vscodeExtensionLinkName)); !linked {
		t.Fatal("a missing law also dropped the extension link")
	}
}

// TestVSCodeSystemPromptUninstallRemovesIt: uninstall deletes the built
// system prompt with the rest of the extension's managed files.
func TestVSCodeSystemPromptUninstallRemovesIt(t *testing.T) {
	t.Setenv("VSCODE_PORTABLE", "")
	home := t.TempDir()
	installer := newVSCodeExtensionEngine(t, home, []string{}, true)
	if err := installer.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	target := installer.vscodeSystemPromptPath()
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
	installer.options.Mode = ModeUninstall
	if err := installer.wireVSCode(); err != nil {
		t.Fatal(err)
	}
	requireNoPath(t, target, "uninstall left the system prompt")
}

func TestVSCodeSystemPromptFrontmatter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, law, want string }{
		{"stripped", vscodeLawFixture, "L\n\n# Prompt Quality\n\nCut every line.\n"},
		{"none", "# Law\n", "L\n\n# Law\n"},
		{"unterminated kept whole", "---\nname: x\n# Law\n", "L\n\n---\nname: x\n# Law\n"},
		{"crlf stripped", "---\r\nname: x\r\n---\r\n\r\n# Law\r\n", "L\n\n# Law\n"},
	} {
		if got := string(vscodeSystemPrompt([]byte("L\n"), []byte(tc.law))); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestVSCodeSystemPromptMissingLawFailsOnlyItsStep: a full install with
// --vscode and a clone without the law writes no system prompt and names the
// law's path in a FAIL row, while the VS Code settings and every other step
// still land.
func TestVSCodeSystemPromptMissingLawFailsOnlyItsStep(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".config", "Code", "User", "settings.json")
	writeFixture(t, settings, `{}`)
	clone := t.TempDir()
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &output, MCPConfigPath: testConfigPath(t),
		SourceRepo: clone, VSCode: true, vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	})
	law := vscodeLawSource(clone)
	if err == nil || strings.Contains(err.Error(), "preflight") || !strings.Contains(err.Error(), law) {
		t.Fatalf("install error=%v output=%s, want the step failure naming %s", err, output.String(), law)
	}
	want := "  FAIL    build VS Code " + vscodeSystemPromptName + ": read the prompt law " + law + ": "
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output lacks the FAIL row %q:\n%s", want, output.String())
	}
	if got := readFixture(t, settings); !strings.Contains(got, `"PFM"`) {
		t.Fatalf("VS Code settings did not land past the missing law:\n%s", got)
	}
	requireNoPath(
		t,
		filepath.Join(managedRootForHome(home), filepath.FromSlash(vscodeExtensionSource), vscodeSystemPromptName),
		"a missing law wrote a system prompt",
	)
}
