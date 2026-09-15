package installer

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestCodexDefaultsInstall(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "codex", "config.toml")
	writeFixture(t, source, "[features.multi_agent_v2]\nwait_agent_enabled = true\ndefault_wait_timeout_ms = 750000\n")
	config := filepath.Join(home, ".codex", "config.toml")
	original := "# local preference\nmodel = 'custom'\ndeveloper_instructions = '''Keep my rules.\n[not.a.table]\n\n<!-- BEGIN Professor subagent coordination -->\nUse the agent mailbox.\n<!-- END Professor subagent coordination -->\n'''\n[features.multi_agent_v2]\ndefault_wait_timeout_ms = 900000\n# BEGIN pfm mcp\n[mcp_servers.chat]\nurl = 'http://localhost:1234'\n# END pfm mcp\n"
	writeFixture(t, config, original)
	options := Options{Mode: ModeDryRun, Home: home, Runner: &fakeRunner{}}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, config); got != original {
		t.Fatal("dry run modified config")
	}
	options.Mode = ModeApply
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	first := readFixture(t, config)
	var document map[string]any
	if _, err := toml.Decode(first, &document); err != nil {
		t.Fatal(err)
	}
	instructions := document["developer_instructions"].(string)
	if instructions != "Keep my rules.\n[not.a.table]\n\n\n" {
		t.Fatalf("missing developer instructions: %q", instructions)
	}
	feature := document["features"].(map[string]any)["multi_agent_v2"].(map[string]any)
	if feature["default_wait_timeout_ms"] != int64(900000) || feature["wait_agent_enabled"] != true {
		t.Fatalf("incorrect defaults: %#v", feature)
	}
	if !strings.Contains(first, "# local preference\nmodel = 'custom'") ||
		!strings.Contains(first, "# BEGIN pfm mcp\n[mcp_servers.chat]\nurl = 'http://localhost:1234'\n# END pfm mcp") {
		t.Fatal("unrelated config or ownership fence changed")
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, config); got != first {
		t.Fatal("reinstall was not idempotent")
	}
	backups, err := filepath.Glob(config + ".pre-professor-*")
	if err != nil || len(backups) == 0 {
		t.Fatalf("missing backup: %v %v", backups, err)
	}
}

func TestCodexDefaultsFreshHomes(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "codex", "config.toml")
	writeFixture(t, source, "[features.multi_agent_v2]\nwait_agent_enabled = true\n")
	homes := []string{filepath.Join(home, "account-one"), filepath.Join(home, "account-two")}
	if _, err := Run(
		context.Background(),
		Options{Mode: ModeApply, Home: home, CodexHomes: homes, Runner: &fakeRunner{}},
	); err != nil {
		t.Fatal(err)
	}
	for _, dir := range homes {
		raw, err := os.ReadFile(filepath.Join(dir, "config.toml"))
		if err != nil || !strings.Contains(string(raw), "wait_agent_enabled = true") ||
			strings.Contains(string(raw), "developer_instructions") {
			t.Fatalf("home %s: %s %v", dir, raw, err)
		}
	}
}

func TestCodexDefaultsMergeLayouts(t *testing.T) {
	defaults := "[features.multi_agent_v2]\nwait_agent_enabled = true\n"
	for _, input := range []string{
		"", "# keep this comment", "developer_instructions = 'Use mailbox.'",
		"developer_instructions = 'Use mailbox.' # retain policy note\n",
		"features.multi_agent_v2.wait_agent_enabled = false\n",
		"[features]\nother = true\n",
		"developer_instructions = \"\"\"\n[features.multi_agent_v2]\nkeep this text\n\"\"\"\n",
	} {
		t.Run(input, func(t *testing.T) {
			merged, err := mergeCodexDefaults(input, defaults)
			if err != nil {
				t.Fatal(err)
			}
			again, err := mergeCodexDefaults(merged, defaults)
			if err != nil || again != merged {
				t.Fatalf("not idempotent: %v\n%s\n%s", err, merged, again)
			}
			if strings.Contains(input, "# retain policy note") && !strings.Contains(merged, "# retain policy note") {
				t.Fatal("inline comment lost")
			}
			var doc map[string]any
			if _, err := toml.Decode(merged, &doc); err != nil {
				t.Fatal(err)
			}
			var before map[string]any
			if _, err := toml.Decode(input, &before); err != nil {
				t.Fatal(err)
			}
			if doc["developer_instructions"] != before["developer_instructions"] {
				t.Fatal("personal instructions changed")
			}
		})
	}
	for _, input := range []string{
		"broken = [", "developer_instructions = 7\n", "features = true\n",
		"developer_instructions = '<!-- END Professor subagent coordination -->'\n",
		"features = { multi_agent_v2 = { custom = true } }\n",
	} {
		if _, err := mergeCodexDefaults(input, defaults); err == nil {
			t.Fatalf("accepted unsafe layout: %s", input)
		}
	}
}

func TestCodexDefaultsPreservesConfigSymlink(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "codex", "config.toml")
	writeFixture(t, source, "[features.multi_agent_v2]\nwait_agent_enabled = true\n")
	target := filepath.Join(home, "personal.toml")
	writeFixture(t, target, "model = 'personal'\n")
	configHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configHome, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(configHome, "config.toml")
	if err := os.Symlink(target, config); err != nil {
		t.Fatal(err)
	}
	installer := &engine{options: Options{Home: home, Stdout: io.Discard}, apply: true, stamp: "test"}
	if err := installer.wireCodexDefaults(); err != nil {
		t.Fatal(err)
	}
	assertLink(t, config, target)
	if !strings.Contains(readFixture(t, target), "wait_agent_enabled = true") {
		t.Fatal("symlink target did not receive policy")
	}
}

func TestCodexDefaultsRejectsInconsistentTimeouts(t *testing.T) {
	defaults := "[features.multi_agent_v2]\nmin_wait_timeout_ms = 150000\ndefault_wait_timeout_ms = 750000\nmax_wait_timeout_ms = 1500000\n"
	for _, value := range []string{"30000", "-1", "3600001", "'wrong type'"} {
		if _, err := mergeCodexDefaults(
			"[features.multi_agent_v2]\nmax_wait_timeout_ms = "+value+"\n",
			defaults,
		); err == nil {
			t.Fatalf("accepted incompatible maximum %s", value)
		}
	}
}

func TestCodexDefaultsRefusesDanglingConfigSymlink(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "codex", "config.toml")
	writeFixture(t, source, "[features.multi_agent_v2]\nwait_agent_enabled = true\n")
	configHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(configHome, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(configHome, "config.toml")
	target := filepath.Join(home, "missing.toml")
	if err := os.Symlink(target, config); err != nil {
		t.Fatal(err)
	}
	installer := &engine{options: Options{Home: home, Stdout: io.Discard}, apply: true, stamp: "test"}
	if err := installer.wireCodexDefaults(); err == nil || !strings.Contains(err.Error(), "dangling symlink") {
		t.Fatalf("expected dangling-link refusal, got %v", err)
	}
	if got, err := os.Readlink(config); err != nil || got != target {
		t.Fatalf("link changed: %s %v", got, err)
	}
}

func TestCodexDefaultsRemovesOnlyManagedInstructions(t *testing.T) {
	input := "developer_instructions = '<!-- BEGIN Professor subagent coordination -->old<!-- END Professor subagent coordination -->' # keep note\nmodel = 'personal'\n"
	got, err := mergeCodexDefaults(input, "[features.multi_agent_v2]\nwait_agent_enabled = true\n")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if _, err := toml.Decode(got, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["developer_instructions"]; ok {
		t.Fatal("managed-only key retained")
	}
	if doc["model"] != "personal" || !strings.Contains(got, "# keep note") {
		t.Fatal("unrelated config changed")
	}
}
