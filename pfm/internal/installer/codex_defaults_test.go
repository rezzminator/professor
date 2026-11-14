package installer

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestCodexDefaultsInstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "codex", "config.toml")
	writeFixture(t, source, "[features.multi_agent_v2]\nwait_agent_enabled = true\ndefault_wait_timeout_ms = 750000\n")
	config := filepath.Join(home, ".codex", "config.toml")
	original := "# local preference\nmodel = 'custom'\ndeveloper_instructions = '''Keep my rules.\n[not.a.table]\n\n<!-- BEGIN Professor subagent coordination -->\nUse the agent mailbox.\n<!-- END Professor subagent coordination -->\n'''\n[features.multi_agent_v2]\ndefault_wait_timeout_ms = 900000\n# BEGIN pfm mcp\n[mcp_servers.chat]\nurl = 'http://localhost:1234'\n# END pfm mcp\n"
	writeFixture(t, config, original)
	var output strings.Builder
	options := Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeDryRun,
		Home:          home,
		Runner:        &fakeRunner{},
		Stdout:        &output,
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, config); got != original {
		t.Fatal("dry run modified config")
	}
	if !strings.Contains(output.String(), "  change  merge Professor defaults into "+config+"\n") {
		t.Fatalf("defaults report = %q, want unchanged label", output.String())
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

func TestCodexDefaultsNamesYieldedMCPTables(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".codex", "config.toml")
	writeFixture(
		t,
		config,
		codexChatFence(home)+"[mcp_servers.chat-professor]\nurl = \"http://127.0.0.1:1/lane-m-foreign\"\n",
	)
	var output strings.Builder
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), Mode: ModeDryRun, Home: home,
		Runner: &fakeRunner{}, Stdout: &output, MCPEnabled: map[string]bool{chatName: true},
	}); err != nil {
		t.Fatal(err)
	}
	want := "  change  merge Professor defaults into " + config + "; remove pfm's MCP tables: mcp_servers.chat-professor\n"
	wantOutputLines(t, output.String(), []string{strings.TrimSuffix(want, "\n")}, "merge Professor defaults")
}

func TestYieldedCodexMCPTables(t *testing.T) {
	for _, tc := range []struct {
		name, raw, yielded string
		want               []string
	}{
		{name: "unchanged", raw: "[mcp_servers.professor]\n", yielded: "[mcp_servers.professor]\n"},
		{name: "duplicate retained", raw: "[mcp_servers.professor]\n[mcp_servers.professor]\n", yielded: "[mcp_servers.professor]\n", want: []string{"professor"}},
		{name: "sorted multiset", raw: "[mcp_servers.zeta]\n[mcp_servers.alpha]\n[mcp_servers.alpha]\n[mcp_servers.professor]\n[mcp_servers.professor]\n", yielded: "[mcp_servers.professor]\n", want: []string{"alpha", "alpha", "professor", "zeta"}},
		{name: "literal headers", raw: " [mcp_servers.indented]\n[mcp_servers.trailing] # comment\n[features]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := yieldedCodexMCPTables(tc.raw, tc.yielded); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("yielded tables = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCodexDefaultsFreshHomes(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "codex", "config.toml")
	writeFixture(t, source, "[features.multi_agent_v2]\nwait_agent_enabled = true\n")
	homes := []string{filepath.Join(home, "account-one"), filepath.Join(home, "account-two")}
	if _, err := Run(
		context.Background(),
		Options{
			MCPConfigPath: testConfigPath(t),
			Mode:          ModeApply,
			Home:          home,
			CodexHomes:    homes,
			Runner:        &fakeRunner{},
		},
	); err != nil {
		t.Fatal(err)
	}
	prompt, err := assembledHarnessPrompt(t.TempDir(), pfmengine.Codex)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range homes {
		raw, err := os.ReadFile(filepath.Join(dir, "config.toml"))
		if err != nil || !strings.Contains(string(raw), "wait_agent_enabled = true") {
			t.Fatalf("home %s: %s %v", dir, raw, err)
		}
		if got, _ := decodeCodexConfig(t, string(raw))[codexInstructionsKey].(string); got != string(prompt) {
			t.Fatalf("home %s carries %d bytes of %s, want the %d-byte fleet prompt",
				dir, len(got), codexInstructionsKey, len(prompt))
		}
	}
}

func TestCodexDefaultsMergeLayouts(t *testing.T) {
	t.Parallel()
	defaults := "[features.multi_agent_v2]\nwait_agent_enabled = true\n"
	for _, input := range []string{
		"", "# keep this comment", "developer_instructions = 'Use mailbox.'",
		"developer_instructions = 'Use mailbox.' # retain policy note\n",
		"features.multi_agent_v2.wait_agent_enabled = false\n",
		"[features]\nother = true\n",
		"developer_instructions = \"\"\"\n[features.multi_agent_v2]\nkeep this text\n\"\"\"\n",
	} {
		t.Run(input, func(t *testing.T) {
			merged, _, err := mergeCodexDefaults(input, defaults)
			if err != nil {
				t.Fatal(err)
			}
			again, _, err := mergeCodexDefaults(merged, defaults)
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
		if _, _, err := mergeCodexDefaults(input, defaults); err == nil {
			t.Fatalf("accepted unsafe layout: %s", input)
		}
	}
}

func TestCodexDefaultsPreservesConfigSymlink(t *testing.T) {
	t.Parallel()
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
	if err := installer.generateHarnessPrompts(); err != nil {
		t.Fatal(err)
	}
	if err := installer.wireCodexDefaults(); err != nil {
		t.Fatal(err)
	}
	assertLink(t, config, target)
	if !strings.Contains(readFixture(t, target), "wait_agent_enabled = true") {
		t.Fatal("symlink target did not receive policy")
	}
}

// A user's own out-of-range or unordered wait timeout leaves
// features.multi_agent_v2 as set and names why, while the rest of the merge
// goes ahead; a bad template value is pfm's own error.
func TestCodexDefaultsKeepsUserTimeoutsTheyCannotOrder(t *testing.T) {
	t.Parallel()
	defaults := "[features.multi_agent_v2]\nmin_wait_timeout_ms = 150000\ndefault_wait_timeout_ms = 750000\nmax_wait_timeout_ms = 1500000\n"
	const outOfRange = "max_wait_timeout_ms must be an integer from 0 to 3600000"
	for _, testCase := range []struct{ value, why string }{
		{"30000", "default_wait_timeout_ms exceeds max_wait_timeout_ms"},
		{"-1", outOfRange},
		{"3600001", outOfRange},
		{"'wrong type'", outOfRange},
	} {
		input := "[features.multi_agent_v2]\nmax_wait_timeout_ms = " + testCase.value + "\n"
		merged, note, err := mergeCodexDefaults(input, defaults)
		if err != nil {
			t.Fatalf("maximum %s: %v", testCase.value, err)
		}
		want := "features.multi_agent_v2 left as set, no wait defaults inserted: " + testCase.why +
			"; set a consistent minimum/default/maximum"
		if note != want {
			t.Fatalf("maximum %s: note %q, want %q", testCase.value, note, want)
		}
		if !strings.Contains(merged, input) || strings.Contains(merged, codexMinWaitTimeout) {
			t.Fatalf("maximum %s: multi_agent_v2 not left as set:\n%s", testCase.value, merged)
		}
		if !strings.Contains(merged, codexDefaultExecYieldTime) {
			t.Fatalf("maximum %s: the rest of the merge did not land:\n%s", testCase.value, merged)
		}
	}
	if _, _, err := mergeCodexDefaults(
		"",
		"[features.multi_agent_v2]\nmin_wait_timeout_ms = 9\nmax_wait_timeout_ms = 1\n",
	); err == nil ||
		err.Error() != "wait defaults for Codex conflict: min_wait_timeout_ms exceeds max_wait_timeout_ms" {
		t.Fatalf("unordered template defaults: err = %v", err)
	}
}

// Codex clamps every command wait at ~31s unless both long-yield keys are set,
// which turns one wait into a poll loop; a user's own value still wins.
func TestCodexDefaultsLongCommandWaits(t *testing.T) {
	t.Parallel()
	defaults := "[features.multi_agent_v2]\nwait_agent_enabled = true\n"
	for _, testCase := range []struct {
		name       string
		input      string
		yield      int64
		background int64
	}{
		{"fresh config", "", codexLongYieldMs, codexLongYieldMs},
		{"features table of booleans", "[features]\ngoals = true\nhooks = true\n", codexLongYieldMs, codexLongYieldMs},
		{"code_mode table with siblings", "[features.code_mode]\nenabled = true\n", codexLongYieldMs, codexLongYieldMs},
		{"user yield wins", "[features.code_mode]\ndefault_exec_yield_time_ms = 60000\n", 60000, codexLongYieldMs},
		{"dotted user yield wins", "features.code_mode.default_exec_yield_time_ms = 45000\n", 45000, codexLongYieldMs},
		{"user background wait wins", "background_terminal_max_timeout = 90000\n", codexLongYieldMs, 90000},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			merged, _, err := mergeCodexDefaults(testCase.input, defaults)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if _, err := toml.Decode(merged, &document); err != nil {
				t.Fatalf("invalid TOML: %v\n%s", err, merged)
			}
			codeMode, ok := document["features"].(map[string]any)["code_mode"].(map[string]any)
			if !ok {
				t.Fatalf("missing features.code_mode: %s", merged)
			}
			if codeMode[codexDefaultExecYieldTime] != testCase.yield {
				t.Fatalf("yield %#v, want %d in %s", codeMode[codexDefaultExecYieldTime], testCase.yield, merged)
			}
			if document[codexBackgroundTerminalMaxTimeout] != testCase.background {
				t.Fatalf(
					"background wait %#v, want %d in %s",
					document[codexBackgroundTerminalMaxTimeout],
					testCase.background,
					merged,
				)
			}
			if strings.Contains(testCase.input, "enabled = true") && codeMode["enabled"] != true {
				t.Fatalf("sibling key lost: %s", merged)
			}
			again, _, err := mergeCodexDefaults(merged, defaults)
			if err != nil || again != merged {
				t.Fatalf("not idempotent: %v\n%s\n%s", err, merged, again)
			}
		})
	}
	if _, _, err := mergeCodexDefaults("[features]\ncode_mode = true\n", defaults); err == nil {
		t.Fatal("accepted a non-table features.code_mode")
	}
}

// A dangling config link and a config the defaults cannot merge into cost
// only their own homes' defaults: the link stays, the unmergeable home still
// gets the fleet prompt, and every other home is still wired.
func TestCodexDefaultsFailingHomeCostsOnlyItself(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "codex", "config.toml")
	writeFixture(t, source, "[features.multi_agent_v2]\nwait_agent_enabled = true\n")
	unmergeable := filepath.Join(home, "a-unmergeable")
	writeFixture(t, filepath.Join(unmergeable, "config.toml"), "features = true\n")
	configHome := filepath.Join(home, "b-dangling")
	if err := os.MkdirAll(configHome, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(configHome, "config.toml")
	target := filepath.Join(home, "missing.toml")
	if err := os.Symlink(target, config); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(home, "c-fresh")
	var output bytes.Buffer
	installer := &engine{
		options: Options{Home: home, Stdout: &output, CodexHomes: []string{unmergeable, configHome, fresh}},
		apply:   true, stamp: "test",
	}
	if err := installer.generateHarnessPrompts(); err != nil {
		t.Fatal(err)
	}
	prompt, err := installer.generatedHarnessPrompt(pfmengine.Codex)
	if err != nil {
		t.Fatal(err)
	}
	err = installer.wireCodexDefaults()
	merge := "merge Codex defaults into " + filepath.Join(unmergeable, "config.toml") + ": "
	if err == nil || !strings.Contains(err.Error(), "config for Codex is a dangling symlink: "+config) ||
		!strings.Contains(err.Error(), merge) {
		t.Fatalf("wireCodexDefaults = %v, want the dangling link and the merge failure", err)
	}
	if !strings.Contains(output.String(), "  FAIL    "+merge) {
		t.Fatalf("output lacks the FAIL row %q:\n%s", merge, output.String())
	}
	if got, err := os.Readlink(config); err != nil || got != target {
		t.Fatalf("link changed: %s %v", got, err)
	}
	unmerged := decodeCodexConfig(t, readFixture(t, filepath.Join(unmergeable, "config.toml")))
	if unmerged["features"] != true || unmerged[codexInstructionsKey] != string(prompt) {
		t.Fatalf("unmergeable home = %v, want features kept and the fleet prompt", unmerged)
	}
	wired := decodeCodexConfig(t, readFixture(t, filepath.Join(fresh, "config.toml")))
	if wired[codexInstructionsKey] != string(prompt) {
		t.Fatalf("fresh home after the failing ones lacks the fleet prompt: %v", wired)
	}
}

func TestCodexDefaultsRemovesOnlyManagedInstructions(t *testing.T) {
	t.Parallel()
	input := "developer_instructions = '<!-- BEGIN Professor subagent coordination -->old<!-- END Professor subagent coordination -->' # keep note\nmodel = 'personal'\n"
	got, _, err := mergeCodexDefaults(input, "[features.multi_agent_v2]\nwait_agent_enabled = true\n")
	if err != nil {
		t.Fatal(err)
	}
	want := "background_terminal_max_timeout = 3600000\n # keep note\nmodel = 'personal'\n\n[features.multi_agent_v2]\n" +
		"wait_agent_enabled = true\n\n[features.code_mode]\ndefault_exec_yield_time_ms = 3600000\n"
	if got != want {
		t.Fatalf("merged config = %q, want only the managed key gone: %q", got, want)
	}
}
