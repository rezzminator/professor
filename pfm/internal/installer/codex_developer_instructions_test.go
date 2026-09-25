package installer

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// decodeCodexConfig parses one config.toml the way codex-cli does, so a test
// asserts what the engine will read rather than what the file looks like.
func decodeCodexConfig(t *testing.T, raw string) map[string]any {
	t.Helper()
	var document map[string]any
	if _, err := toml.Decode(raw, &document); err != nil {
		t.Fatalf("config is not valid TOML: %v\n%s", err, raw)
	}
	return document
}

// The composed prompt lands verbatim, whatever the config around it looks
// like, and a second pass over the result changes nothing.
func TestCodexDeveloperInstructionsLandVerbatimAndIdempotently(t *testing.T) {
	prompt := "# Fleet\n\nLine with a \\ backslash, a \" quote and `ticks`.\n"
	cases := []struct {
		name string
		raw  string
	}{
		{name: "empty config", raw: ""},
		{name: "bare keys only", raw: "model = 'personal'\n"},
		// A bare key appended after a table would be parsed as a member of
		// that table — the block has to lead the document.
		{name: "opens with a table", raw: "[features.multi_agent_v2]\ndefault_wait_timeout_ms = 900000\n"},
		{name: "table and fenced MCP block", raw: "model = 'personal'\n\n" + mcpFenceBegin +
			"\n[mcp_servers.professor]\ncommand = '/fixture/.local/bin/pfm'\nargs = ['mcp', 'serve', '--stdio']\n" +
			mcpFenceEnd + "\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			updated, foreign, err := mergeCodexDeveloperInstructions(testCase.raw, prompt)
			if err != nil {
				t.Fatal(err)
			}
			if foreign != "" {
				t.Fatalf("a config pfm owns was reported foreign: %s", foreign)
			}
			if got, _ := decodeCodexConfig(t, updated)[codexInstructionsKey].(string); got != prompt {
				t.Fatalf("developer_instructions = %q, want %q", got, prompt)
			}
			if testCase.raw != "" && !strings.Contains(updated, strings.TrimRight(testCase.raw, "\n")) {
				t.Fatalf("existing config text was not preserved:\n%s", updated)
			}
			again, _, err := mergeCodexDeveloperInstructions(updated, prompt)
			if err != nil {
				t.Fatal(err)
			}
			if again != updated {
				t.Fatalf("second merge rewrote the config:\n%s\n---\n%s", updated, again)
			}
		})
	}
}

// A prompt the fence already holds is REPLACED, never stacked beside a second
// declaration (which TOML rejects as a duplicate key).
func TestCodexDeveloperInstructionsReplaceAnEarlierPrompt(t *testing.T) {
	first, _, err := mergeCodexDeveloperInstructions("model = 'personal'\n", "old prompt\n")
	if err != nil {
		t.Fatal(err)
	}
	updated, foreign, err := mergeCodexDeveloperInstructions(first, "new prompt\n")
	if err != nil {
		t.Fatal(err)
	}
	if foreign != "" {
		t.Fatalf("pfm's own block was reported foreign: %s", foreign)
	}
	if got, _ := decodeCodexConfig(t, updated)[codexInstructionsKey].(string); got != "new prompt\n" {
		t.Fatalf("developer_instructions = %q", got)
	}
	if strings.Contains(updated, "old prompt") {
		t.Fatalf("the earlier prompt survived:\n%s", updated)
	}
	if count := strings.Count(updated, codexInstructionsFenceBegin); count != 1 {
		t.Fatalf("fence count=%d:\n%s", count, updated)
	}
}

// A developer_instructions pfm did not write is kept, and the caller is told
// why nothing was installed — silence here would destroy an operator's own
// instructions, or hide that the fleet prompt never landed.
func TestCodexDeveloperInstructionsPreserveAForeignValue(t *testing.T) {
	raw := "developer_instructions = 'Keep my rules.'\nmodel = 'personal'\n"
	updated, foreign, err := mergeCodexDeveloperInstructions(raw, "fleet prompt\n")
	if err != nil {
		t.Fatal(err)
	}
	if updated != raw {
		t.Fatalf("a foreign config was rewritten:\n%s", updated)
	}
	if !strings.Contains(foreign, codexInstructionsKey) || !strings.Contains(foreign, codexInstructionsFenceBegin) {
		t.Fatalf("the reason does not name the key and the fence: %q", foreign)
	}
}

// Any prompt at all has to produce valid TOML: a literal string cannot hold
// one containing a triple single quote, so the encoder's basic string does, and the value
// still reads back byte for byte.
func TestCodexDeveloperInstructionsSurviveHostileText(t *testing.T) {
	for _, prompt := range []string{
		"has ''' three quotes\n",
		"ends without a newline",
		"carries a \x07 control byte\n",
		"\\\\ backslashes \"\"\" and 'quotes'\n",
	} {
		updated, foreign, err := mergeCodexDeveloperInstructions("model = 'personal'\n", prompt)
		if err != nil {
			t.Fatalf("prompt %q: %v", prompt, err)
		}
		if foreign != "" {
			t.Fatalf("prompt %q reported foreign: %s", prompt, foreign)
		}
		if got, _ := decodeCodexConfig(t, updated)[codexInstructionsKey].(string); got != prompt {
			t.Fatalf("prompt %q read back as %q", prompt, got)
		}
	}
}

// An apply writes the composed Codex prompt into every configured account,
// and an uninstall takes pfm's block back out without touching what was
// there before.
func TestInstallWritesTheComposedPromptIntoEveryCodexHome(t *testing.T) {
	home := t.TempDir()
	accounts := []string{filepath.Join(home, ".codex"), filepath.Join(home, ".codex-2")}
	for _, account := range accounts {
		if err := os.MkdirAll(account, 0o700); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(account, "config.toml"), "model = 'personal'\n")
	}
	options := Options{
		Mode: ModeApply, Home: home, CodexHomes: accounts, Runner: &fakeRunner{}, Stdout: io.Discard,
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	prompt, err := codexHarnessPrompt()
	if err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(filepath.Join(
		home, ".local", "share", "pfm", "install", harnessPromptsDirName, "codex.md",
	))
	if err != nil {
		t.Fatalf("staged Codex prompt: %v", err)
	}
	if !bytes.Equal(staged, prompt) {
		t.Fatal("the staged Codex prompt and the config value are not the same bytes")
	}
	for _, account := range accounts {
		path := filepath.Join(account, "config.toml")
		raw := readFixture(t, path)
		if got, _ := decodeCodexConfig(t, raw)[codexInstructionsKey].(string); got != string(prompt) {
			t.Fatalf("%s carries %d bytes of developer_instructions, want %d", path, len(got), len(prompt))
		}
		if !strings.Contains(raw, "model = 'personal'") {
			t.Fatalf("%s lost its own settings:\n%s", path, raw)
		}
	}
	options.Mode = ModeUninstall
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		path := filepath.Join(account, "config.toml")
		raw := readFixture(t, path)
		if strings.Contains(raw, codexInstructionsKey) || strings.Contains(raw, codexInstructionsFenceBegin) {
			t.Fatalf("uninstall left the fleet prompt in %s:\n%s", path, raw)
		}
		if !strings.Contains(raw, "model = 'personal'") {
			t.Fatalf("uninstall removed more than pfm's block from %s:\n%s", path, raw)
		}
	}
}
