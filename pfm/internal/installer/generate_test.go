package installer

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// generateFixture is a home and a clone whose head part differs from the
// embedded one, with the jail's config pointed at the home.
func generateFixture(t *testing.T) (home, clone, configPath string) {
	t.Helper()
	home, clone = t.TempDir(), t.TempDir()
	head, err := harnessprompts.ReadPart("share/head.md")
	if err != nil {
		t.Fatal(err)
	}
	seedHarnessPromptTree(t, clone, map[string]string{"share/head.md": string(head) + "\n" + cloneHeadLine + "\n"})
	configPath = filepath.Join(home, "pfm.config.json")
	writeFixture(t, configPath, "{\"version\":2}\n")
	t.Setenv(paths.EnvConfig, configPath)
	return home, clone, configPath
}

// Generate composes from the clone's edited head and writes it into the
// generated Claude prompt and the Codex developer_instructions fence.
func TestGenerateWritesTheCloneTreeIntoPromptsAndCodexFence(t *testing.T) {
	home, clone, configPath := generateFixture(t)
	if _, err := Generate(context.Background(), Options{
		Mode: ModeApply, Home: home, SourceRepo: clone, MCPConfigPath: configPath,
		Runner: &fakeRunner{}, Stdout: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	claude := readFixture(t, filepath.Join(clone, "pfm", "harness-prompts", "generated", "claude.md"))
	if !strings.Contains(claude, cloneHeadLine) {
		t.Fatalf("generated/claude.md lacks the clone's edited head:\n%.300s", claude)
	}
	config := readFixture(t, filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(config, codexInstructionsFenceBegin) || !strings.Contains(config, cloneHeadLine) {
		t.Fatalf("the Codex developer_instructions fence lacks the clone's edited head:\n%.500s", config)
	}
}

// A plan previews every step and writes nothing under the home or the clone.
func TestGeneratePlanWritesNothing(t *testing.T) {
	home, clone, configPath := generateFixture(t)
	before := map[string]map[string]string{home: snapshotTree(t, home), clone: snapshotTree(t, clone)}
	var out strings.Builder
	if _, err := Generate(context.Background(), Options{
		Mode: ModeDryRun, Home: home, SourceRepo: clone, MCPConfigPath: configPath,
		Runner: &fakeRunner{}, Stdout: &out,
	}); err != nil {
		t.Fatal(err)
	}
	for root, files := range before {
		after := snapshotTree(t, root)
		for path, content := range after {
			if old, ok := files[path]; !ok || old != content {
				t.Errorf("generate --plan wrote %s", path)
			}
		}
		for path := range files {
			if _, ok := after[path]; !ok {
				t.Errorf("generate --plan removed %s", path)
			}
		}
	}
	if !strings.Contains(out.String(), "generate harness prompt") {
		t.Fatalf("the plan does not name the prompt it would generate:\n%s", out.String())
	}
}

// Generate records the clone it compiled, so doctor, role seats and launches
// read the prompts it wrote, and links the clone's global commands, so the
// Codex commands engine-sync compiles are the ones its plan previewed.
func TestGenerateRecordsTheCloneAndLinksItsCommands(t *testing.T) {
	home, clone, configPath := generateFixture(t)
	command := filepath.Join(clone, "templates", "global", "commands", "generate-probe.md")
	writeFixture(t, command, "---\ndescription: probe\n---\nprobe\n")
	if _, err := Generate(context.Background(), Options{
		Mode: ModeApply, Home: home, SourceRepo: clone, MCPConfigPath: configPath,
		Runner: &fakeRunner{}, Stdout: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	recorded, err := paths.ReadSourceRepoMarker(home)
	if err != nil {
		t.Fatalf("generate recorded no source clone: %v", err)
	}
	want, err := filepath.EvalSymlinks(clone)
	if err != nil {
		t.Fatal(err)
	}
	if recorded != want {
		t.Fatalf("recorded source clone = %q, want %q", recorded, want)
	}
	link := filepath.Join(home, ".claude", "commands", "generate-probe.md")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("generate did not link the clone's command %s: %v", link, err)
	}
	if target != command {
		t.Fatalf("%s -> %q, want %q", link, target, command)
	}
}

// A mode that is neither apply nor plan is refused, never run as a plan.
func TestGenerateRefusesAnyModeButApplyAndPlan(t *testing.T) {
	home, clone, configPath := generateFixture(t)
	for _, mode := range []Mode{ModeUninstall, ModeUninstall + 1} {
		if _, err := Generate(context.Background(), Options{
			Mode: mode, Home: home, SourceRepo: clone, MCPConfigPath: configPath,
			Runner: &fakeRunner{}, Stdout: io.Discard,
		}); err == nil {
			t.Errorf("Generate in mode %d succeeded, want a refusal", mode)
		}
	}
}
