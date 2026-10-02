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

func TestFirstInstallWiresOpenCodeInstructions(t *testing.T) {
	for _, alias := range []bool{false, true} {
		name := "clone"
		if alias {
			name = "alias"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			clone := t.TempDir()
			source := clone
			if alias {
				source = filepath.Join(t.TempDir(), "clone")
				if err := os.Symlink(clone, source); err != nil {
					t.Fatal(err)
				}
			}
			configPath := filepath.Join(home, "pfm.config.json")
			writeFixture(t, configPath, "{\"version\":2}\n")
			config := OpenCodeConfigPath(home)
			writeFixture(t, config, "{\n // operator comment\n \"instructions\": [\"operator.md\"]\n}\n")
			var output strings.Builder
			options := Options{
				Mode: ModeApply, Home: home, SourceRepo: source,
				MCPConfigPath: configPath, Runner: &fakeRunner{},
				Stdout: &output, OpenCodeConfigPath: config,
			}
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			physical, err := filepath.EvalSymlinks(clone)
			if err != nil {
				t.Fatal(err)
			}
			composed := filepath.Join(physical, "pfm", "harness-prompts", "composed", "opencode.md")
			raw := readFixture(t, config)
			entries, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, raw), config)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 || entries[0] != composed || entries[1] != "operator.md" {
				t.Fatalf("first install instructions=%v, want [%s operator.md]", entries, composed)
			}
			if !strings.Contains(raw, "// operator comment") {
				t.Fatalf("operator comment lost: %s", raw)
			}
			if marker := readFixture(t, paths.SourceRepoPath(home)); marker != physical+"\n" {
				t.Fatalf("marker=%q, want %q", marker, physical+"\n")
			}
			for _, fromMarker := range []bool{false, true} {
				if fromMarker {
					options.SourceRepo = ""
				}
				output.Reset()
				if _, err := Run(context.Background(), options); err != nil {
					t.Fatal(err)
				}
				if got := readFixture(t, config); got != raw {
					t.Fatalf("repeat install (from marker=%v) changed config: %s", fromMarker, got)
				}
				if !strings.Contains(output.String(), "ok      "+config+" OpenCode prompt wiring") {
					t.Fatalf("repeat install prompt report: %s", output.String())
				}
			}
		})
	}
}

func TestOpenCodeInstructionsRejectsUnusableClone(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "missing")
	config := OpenCodeConfigPath(home)
	original := "{\"instructions\":[\"operator.md\"]}\n"
	writeFixture(t, config, original)
	installer := &engine{options: Options{
		Home: home, SourceRepo: source, OpenCodeConfigPath: config, Stdout: io.Discard,
	}}
	err := installer.wireOpenCodeInstructions()
	if err == nil || !strings.HasPrefix(err.Error(), "resolve OpenCode prompt:") ||
		!strings.Contains(err.Error(), source) {
		t.Fatalf("unusable clone error=%v", err)
	}
	if raw := readFixture(t, config); raw != original {
		t.Fatalf("config changed: %q", raw)
	}
}
