package claudelaunch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func renderMachine(t *testing.T) (string, pfmconfig.Config) {
	t.Helper()
	home := t.TempDir()
	machine := pfmconfig.Defaults(
		home,
		[]string{filepath.Join(home, ".claude", "projects", "one"), filepath.Join(home, ".cc", "2", "projects", "two")},
	)
	machine.MCPServers["chat"] = pfmconfig.MCPServer{Enabled: true}
	return home, machine
}

func renderParsed(t *testing.T, request Request, machine pfmconfig.Config) (Launch, Parsed) {
	t.Helper()
	launch, err := Render(request, machine)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(append([]string{launch.Binary}, launch.Argv...))
	if err != nil {
		t.Fatal(err)
	}
	return launch, parsed
}

func TestRenderFreshInteractive(t *testing.T) {
	home, machine := renderMachine(t)
	launch, parsed := renderParsed(
		t,
		Request{Purpose: PurposeInteractive, Home: home, Account: 2, SessionID: "S"},
		machine,
	)
	if !reflect.DeepEqual(launch.Unset, Hygiene()) || len(launch.Unset) != 22 {
		t.Errorf("unset=%q", launch.Unset)
	}
	if !reflect.DeepEqual(launch.Env, []string{"CLAUDE_CONFIG_DIR=" + machine.Accounts[1].ConfigDir}) {
		t.Errorf("env=%q", launch.Env)
	}
	if len(launch.Argv) < 2 || launch.Argv[0] != "--session-id" || launch.Argv[1] != "S" {
		t.Errorf("argv start=%q", launch.Argv)
	}
	if parsed.SessionID != "S" || !parsed.Autonomy {
		t.Errorf("verbs=%#v", parsed)
	}
	if parsed.Settings["outputStyle"] != "default" || parsed.Settings["cleanupPeriodDays"] != float64(36500) {
		t.Errorf("settings=%#v", parsed.Settings)
	}
	for name, want := range map[string]string{
		"CACHE_LIVE_CONTROL_MAIN_TTL": "1h", "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH": "8",
		"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION": "9007199254740991", "CLAUDE_CODE_TMUX_TRUECOLOR": "1",
		"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "0",
		"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":    "1", "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "100000",
	} {
		if got := parsed.SettingsEnv[name]; got != want {
			t.Errorf("env %s=%q, want %q", name, got, want)
		}
	}
	if len(parsed.Hooks) != 10 {
		t.Errorf("hooks=%d", len(parsed.Hooks))
	}
	if parsed.Settings["statusLine"] == nil || parsed.Settings["subagentStatusLine"] == nil {
		t.Errorf("status lines=%#v", parsed.Settings)
	}
	var mcp map[string]any
	if err := json.Unmarshal([]byte(parsed.MCPConfig), &mcp); err != nil {
		t.Fatal(err)
	}
	assertProfessorMCP(t, home, parsed.MCPConfig)
	if !strings.HasSuffix(
		strings.Join(launch.Argv, " "),
		"--allow-dangerously-skip-permissions --dangerously-skip-permissions",
	) {
		t.Errorf("autonomy order=%q", launch.Argv)
	}
	index := func(word string) int {
		for i, value := range launch.Argv {
			if value == word {
				return i
			}
		}
		return -1
	}
	if index("--settings") < 2 || index("--mcp-config") < index("--settings") ||
		index("--allow-dangerously-skip-permissions") < index("--mcp-config") {
		t.Errorf("flag order=%q", launch.Argv)
	}
	again, err := Render(Request{Purpose: PurposeInteractive, Home: home, Account: 2, SessionID: "S"}, machine)
	if err != nil || !reflect.DeepEqual(launch, again) {
		t.Errorf("render is not byte-stable: second=%#v err=%v", again, err)
	}
}

func TestRenderPluginEnvEveryPurposeAndAccount(t *testing.T) {
	home, machine := renderMachine(t)
	machine.Claude.AutoCompactWindow = 250000
	account := machine.Claude
	account.AutoCompactWindow = 50000
	machine.Accounts[1].Claude = &account
	for _, purpose := range []Purpose{PurposeResume, PurposeLauncher, PurposeQuery} {
		for _, tc := range []struct {
			account int
			window  string
		}{{1, "250000"}, {2, "50000"}} {
			_, parsed := renderParsed(t, Request{Purpose: purpose, Home: home, Account: tc.account}, machine)
			if got := parsed.SettingsEnv["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != tc.window {
				t.Errorf("purpose %d account %d window=%q, want %q", purpose, tc.account, got, tc.window)
			}
			if got := parsed.SettingsEnv["CLAUDE_CODE_ENABLE_FUNCTION_HOOKS"]; got != "1" {
				t.Errorf("purpose %d account %d function hooks=%q", purpose, tc.account, got)
			}
		}
	}
}

func TestRenderImplicitAccount(t *testing.T) {
	home, machine := renderMachine(t)
	launch, _ := renderParsed(t, Request{Purpose: PurposeInteractive, Home: home, Account: 1}, machine)
	if len(launch.Env) != 0 {
		t.Errorf("implicit env=%q", launch.Env)
	}
}

// cacheEnv keeps only the prompt-cache keys of a --settings env, so a test
// pins the whole cache surface by map equality: an extra or missing key fails.
func cacheEnv(env map[string]string) map[string]string {
	result := map[string]string{}
	for _, name := range []string{
		"CACHE_LIVE_CONTROL_MAIN_TTL", "CACHE_LIVE_CONTROL_AGENTS_TTL",
		"CLAUDE_CODE_PROMPT_CACHE_TTL", "CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL",
		"ENABLE_PROMPT_CACHING_1H", "FORCE_PROMPT_CACHING_5M",
	} {
		if value, ok := env[name]; ok {
			result[name] = value
		}
	}
	return result
}

// The cache knob hands only the main chat's starting lifetime to the
// cache-live-control plugin; pfm sets no Claude Code TTL, main or sub-agent.
func TestRenderCacheLifetimeMainChatOnly(t *testing.T) {
	home, machine := renderMachine(t)
	for _, entry := range []struct {
		cache1h bool
		want    map[string]string
	}{
		{true, map[string]string{"CACHE_LIVE_CONTROL_MAIN_TTL": "1h"}},
		{false, map[string]string{"CACHE_LIVE_CONTROL_MAIN_TTL": "5m"}},
	} {
		value := entry.cache1h
		launch, parsed := renderParsed(t, Request{Purpose: PurposeInteractive, Home: home, Cache1H: &value}, machine)
		if launch.Cache1H != entry.cache1h {
			t.Errorf("cache1h=%t: launch.Cache1H=%t", entry.cache1h, launch.Cache1H)
		}
		if got := cacheEnv(parsed.SettingsEnv); !reflect.DeepEqual(got, entry.want) {
			t.Errorf("cache1h=%t: settings cache env=%#v, want %#v", entry.cache1h, got, entry.want)
		}
	}
}

func TestRenderSystemPromptModes(t *testing.T) {
	home, machine := renderMachine(t)
	prompt := filepath.Join(t.TempDir(), "seat.md")
	if err := os.WriteFile(prompt, []byte("seat"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A role seat's file is the seat's constitution in every mode; the mode
	// decides only whether the composed prompt stands in when no seat is given.
	for _, entry := range []struct{ mode, seat, wantEnv, wantFlag string }{
		{"lean", prompt, "1", prompt},
		{"professor", prompt, "", prompt},
		{"production", prompt, "", prompt},
		{"lean", "", "1", ""},
		{"production", "", "", ""},
	} {
		t.Run(entry.mode+"/seat="+strconv.FormatBool(entry.seat != ""), func(t *testing.T) {
			machine.Claude.SystemPrompt = entry.mode
			_, parsed := renderParsed(
				t,
				Request{Purpose: PurposeInteractive, Home: home, PromptFile: entry.seat},
				machine,
			)
			if parsed.SettingsEnv["CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"] != entry.wantEnv ||
				parsed.PromptFile != entry.wantFlag {
				t.Errorf("prompt env=%#v file=%q", parsed.SettingsEnv, parsed.PromptFile)
			}
		})
	}
	machine.Claude.SystemPrompt = "professor"
	_, parsed := renderParsed(
		t,
		Request{Purpose: PurposeInteractive, Home: home, PromptFile: filepath.Join(home, "missing.md")},
		machine,
	)
	if parsed.PromptFile != "" {
		t.Errorf("missing prompt=%q", parsed.PromptFile)
	}
}

func TestRenderPrompted(t *testing.T) {
	home, machine := renderMachine(t)
	machine.Claude.PermissionMode = "prompted"
	_, parsed := renderParsed(t, Request{Purpose: PurposeInteractive, Home: home}, machine)
	if parsed.Autonomy {
		t.Error("prompted launch carries autonomy")
	}
}

func TestRenderLauncher(t *testing.T) {
	home, machine := renderMachine(t)
	launch, parsed := renderParsed(t, Request{Purpose: PurposeLauncher, Home: home, Binary: "/bin/claude"}, machine)
	if launch.Binary != "/bin/claude" || parsed.Autonomy {
		t.Errorf("launcher=%#v parsed=%#v", launch, parsed)
	}
}

func TestRenderQuery(t *testing.T) {
	home, machine := renderMachine(t)
	launch, parsed := renderParsed(
		t,
		Request{Purpose: PurposeQuery, Home: home, Args: []string{"agents", "--json"}},
		machine,
	)
	if len(launch.Unset) != 22 || parsed.Settings["outputStyle"] != "default" ||
		parsed.Settings["cleanupPeriodDays"] == nil ||
		parsed.Settings["env"] == nil {
		t.Errorf("query settings=%#v", parsed.Settings)
	}
	for _, key := range []string{"hooks", "statusLine", "subagentStatusLine"} {
		if _, ok := parsed.Settings[key]; ok {
			t.Errorf("query carries %s", key)
		}
	}
	if parsed.MCPConfig != "" || parsed.PromptFile != "" || parsed.Autonomy {
		t.Errorf("query extra=%#v", parsed)
	}
	if !reflect.DeepEqual(parsed.Rest, []string{"agents", "--json"}) {
		t.Errorf("query rest=%q", parsed.Rest)
	}
}

// assertProfessorMCP holds a --mcp-config payload to develop's one professor
// registration: exactly {type: stdio, command: <home>/.local/bin/pfm,
// args: [mcp serve --stdio]} under mcpServers.professor, and nothing else.
func assertProfessorMCP(t *testing.T, home, payload string) {
	t.Helper()
	var mcp map[string]map[string]any
	if err := json.Unmarshal([]byte(payload), &mcp); err != nil {
		t.Fatalf("mcp=%q: %v", payload, err)
	}
	want := map[string]any{
		"type": "stdio", "command": filepath.Join(home, ".local", "bin", "pfm"),
		"args": []any{"mcp", "serve", "--stdio"},
	}
	servers := mcp["mcpServers"]
	if len(mcp) != 1 || len(servers) != 1 || !reflect.DeepEqual(servers["professor"], want) {
		t.Errorf("mcp=%s, want only mcpServers.professor=%#v", payload, want)
	}
}

func TestRenderMCPIsOneProfessorServer(t *testing.T) {
	for _, entry := range []struct {
		name            string
		chat, harvester bool
	}{
		{"chat only", true, false},
		{"harvester only", false, true},
		{"both", true, true},
		{"neither", false, false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			home, machine := renderMachine(t)
			machine.MCPServers["chat"] = pfmconfig.MCPServer{Enabled: entry.chat}
			machine.MCPServers["harvester"] = pfmconfig.MCPServer{Enabled: entry.harvester}
			_, parsed := renderParsed(t, Request{Purpose: PurposeInteractive, Home: home, Account: 1}, machine)
			if !entry.chat && !entry.harvester {
				if parsed.MCPConfig != "" {
					t.Errorf("mcp=%q with every family off", parsed.MCPConfig)
				}
				return
			}
			assertProfessorMCP(t, home, parsed.MCPConfig)
		})
	}
}
