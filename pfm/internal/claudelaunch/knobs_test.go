package claudelaunch

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

func TestKnobsExampleParity(t *testing.T) {
	keys := map[string]bool{}
	for _, key := range pfmconfig.Keys() {
		keys[key.Key] = true
	}
	for _, knob := range Knobs {
		if knob.Source == SourceConfig || knob.Source == SourceLaunchThenConfig {
			key := "claude." + knob.Name
			if !keys[key] {
				t.Errorf("knob %s has no config key %s", knob.Name, key)
			}
		}
	}
}

func TestKnobsInventory(t *testing.T) {
	wantHygiene := []string{
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDECODE",
		"CLAUDE_CODE_CHILD_SESSION",
		"CLAUDE_CONFIG_DIR",
		"PFM_CLAUDE_CONFIG_DIR_DEFAULT",
		"CLAUDE_PROJECT_DIR",
		"ENABLE_PROMPT_CACHING_1H",
		"FORCE_PROMPT_CACHING_5M",
		"CLAUDE_CODE_PROMPT_CACHE_TTL",
		"CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL",
		"CACHE_LIVE_CONTROL_MAIN_TTL",
		"CACHE_LIVE_CONTROL_AGENTS_TTL",
		"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT",
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_MODEL",
		"ANTHROPIC_SMALL_FAST_MODEL",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
		"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY",
		"CODEX_THREAD_ID",
	}
	if !reflect.DeepEqual(Hygiene(), wantHygiene) {
		t.Errorf("hygiene=%q, want %q", Hygiene(), wantHygiene)
	}
	seen := map[string]bool{}
	for _, knob := range Knobs {
		if knob.Name == "" || knob.Target == "" || knob.Reason == "" {
			t.Errorf("incomplete knob: %#v", knob)
		}
		if seen[knob.Name] {
			t.Errorf("duplicate knob %s", knob.Name)
		}
		seen[knob.Name] = true
		if knob.Name == "cache1h" && (knob.Wire != WireEnv || knob.Target != "CACHE_LIVE_CONTROL_MAIN_TTL") {
			t.Errorf("cache1h wire=%s target=%q, want the process environment", knob.Wire, knob.Target)
		}
	}
	for _, name := range Hygiene() {
		if !seen[name] {
			t.Errorf("hygiene row %s missing", name)
		}
	}
	for _, name := range []string{"configDir", "binary", "cache1h", "shell", "systemPrompt", "nativeCursor", "maxSubagentSpawnDepth", "maxConcurrentSubagents", "webSearchesPerSession", "autoCompactWindow", "helperModel", "tmuxTruecolor", "noFlicker", "agentTeams", "functionHooks", "outputStyle", "theme", "cleanupPeriodDays", "hooks", "statusLine", "subagentStatusLine", "mcp", "permissionMode", "model", "effort", "sessionID", "resume", "fork", "name"} {
		if !seen[name] {
			t.Errorf("knob %s missing", name)
		}
	}
}

// TestKnobHooksCountMatchesRegistry pins the hooks knob's "N registrations"
// default to the registry it describes, so the two cannot drift apart.
func TestKnobHooksCountMatchesRegistry(t *testing.T) {
	for _, knob := range Knobs {
		if knob.Name != knobHooks {
			continue
		}
		text, ok := knob.Default.(string)
		if !ok {
			t.Fatalf("hooks knob default %v (%T), want a string leading with a count", knob.Default, knob.Default)
		}
		count, err := strconv.Atoi(strings.Fields(text)[0])
		if err != nil {
			t.Fatalf("hooks knob default %q does not lead with a count: %v", text, err)
		}
		if want := len(HookTemplates(t.TempDir())); count != want {
			t.Fatalf("hooks knob default %q, but HookTemplates returns %d", text, want)
		}
		return
	}
	t.Fatal("no hooks knob in Knobs")
}

// TestClaudeEnvRefusesLaunchNames guards against settings env overriding a
// launch-owned plugin variable without even an unknown-plugin warning.
func TestClaudeEnvRefusesLaunchNames(t *testing.T) {
	var prefixes []string
	for _, plugin := range pfmconfig.DefaultClaudePlugins() {
		prefixes = append(prefixes, strings.ToUpper(strings.ReplaceAll(plugin.Name(), "-", "_"))+"_")
	}
	checked := map[string]bool{}
	for _, knob := range Knobs {
		if knob.Wire != WireEnv && knob.Wire != WireUnset && knob.Wire != WireWrapperEnv {
			continue
		}
		for _, prefix := range prefixes {
			if !strings.HasPrefix(knob.Target, prefix) || checked[knob.Target] {
				continue
			}
			checked[knob.Target] = true
			t.Run(knob.Target, func(t *testing.T) {
				home := t.TempDir()
				path := filepath.Join(home, pfmconfig.FileName)
				content := `{"version":2,"claude":{"env":{"` + knob.Target + `":"x"}}}`
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := pfmconfig.Load(path, home, nil); err == nil {
					t.Errorf("claude.env accepts %s, which the launch registry's %s knob owns", knob.Target, knob.Name)
				} else if !strings.Contains(err.Error(), "claude.env."+knob.Target) {
					t.Fatalf("Load failed for another reason: %v", err)
				}
			})
		}
	}
	if len(checked) == 0 {
		t.Fatal("no launch variable carries a default plugin's prefix: the check ran on nothing")
	}
}
