package installer

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// OpenCode takes no prompt file on its command line and runs no appendix
// hook, so the composed prompt reaches it only by being named in the config's
// instructions array — and every other key, including an operator's own
// instruction entries, has to survive the write.
func TestInstallWiresOpenCodeInstructions(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clone := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "pfm.config.json")
	writeFixture(t, configPath, "{\"version\":2}\n")
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
		Mode:               ModeApply,
		Home:               home,
		SourceRepo:         clone,
		MCPConfigPath:      configPath,
		Runner:             &fakeRunner{},
		Stdout:             io.Discard,
		OpenCodeConfigPath: config,
	}); err != nil {
		t.Fatal(err)
	}
	composed := filepath.Join(clone, "pfm", "harness-prompts", "composed", "opencode.md")
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
	if len(entries) != 2 || entries[0] != composed || entries[1] != "./house-rules.md" {
		t.Fatalf("instructions=%v, want the composed prompt ahead of the operator's entry", entries)
	}

	// A second apply is idempotent, and uninstall gives the operator's array back.
	if _, err := Run(context.Background(), Options{
		Mode:               ModeApply,
		Home:               home,
		SourceRepo:         clone,
		MCPConfigPath:      configPath,
		Runner:             &fakeRunner{},
		Stdout:             io.Discard,
		OpenCodeConfigPath: config,
	}); err != nil {
		t.Fatal(err)
	}
	again, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, readFixture(t, config)), config)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 2 || again[0] != composed {
		t.Fatalf("second apply changed instructions: %v", again)
	}
	if _, err := Run(context.Background(), Options{
		Mode:               ModeUninstall,
		Home:               home,
		SourceRepo:         clone,
		MCPConfigPath:      configPath,
		Runner:             &fakeRunner{},
		Stdout:             io.Discard,
		OpenCodeConfigPath: config,
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
}

func TestOpenCodeInstructionsWithoutMarkerSkips(t *testing.T) {
	home := t.TempDir()
	config := OpenCodeConfigPath(home)
	writeFixture(t, config, "{\"instructions\":[\"operator.md\"]}\n")
	var output strings.Builder
	installer := &engine{options: Options{Home: home, OpenCodeConfigPath: config, Stdout: &output}}
	if err := installer.wireOpenCodeInstructions(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "skip opencode instructions: no source repo recorded") {
		t.Fatalf("skip report = %q", output.String())
	}
	if raw := readFixture(t, config); raw != "{\"instructions\":[\"operator.md\"]}\n" {
		t.Fatalf("config changed: %q", raw)
	}
}

// A malformed instructions value is an error, never a silent overwrite of
// whatever the operator put there.
func TestOpenCodeInstructionsRejectsForeignShape(t *testing.T) {
	t.Parallel()
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
