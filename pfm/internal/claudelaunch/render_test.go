package claudelaunch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func renderMachine(t *testing.T) (string, pfmconfig.Config) {
	t.Helper()
	home := t.TempDir()
	installFakePFM(t, home)
	machine := pfmconfig.Defaults(
		home,
		[]string{filepath.Join(home, ".claude", "projects", "one"), filepath.Join(home, ".cc", "2", "projects", "two")},
	)
	machine.MCPServers["chat"] = pfmconfig.MCPServer{Enabled: true}
	for _, account := range machine.Accounts {
		if err := os.MkdirAll(account.ConfigDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return home, machine
}

func TestRenderExplicitPersonaFlags(t *testing.T) {
	home, machine := renderMachine(t)
	machine.Claude.SystemPrompt = pfmconfig.SystemPromptProfessor
	prompt := filepath.Join(home, "seat.md")
	if err := os.WriteFile(prompt, []byte("seat"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{flagModel, flagEffort, flagPromptFile} {
		for _, equals := range []bool{false, true} {
			t.Run(flag+"/equals="+strconv.FormatBool(equals), func(t *testing.T) {
				args := []string{flag, "opus"}
				if equals {
					args = []string{flag + "=opus"}
				}
				launch, err := Render(
					Request{
						Purpose:    PurposeLauncher,
						Home:       home,
						Model:      "sonnet",
						Effort:     "xhigh",
						PromptFile: prompt,
						Args:       args,
					},
					machine,
				)
				if err != nil {
					t.Fatal(err)
				}
				var flags []string
				for _, arg := range launch.Argv {
					if arg == flag || strings.HasPrefix(arg, flag+"=") {
						flags = append(flags, arg)
					}
				}
				if !reflect.DeepEqual(flags, args[:1]) {
					t.Fatalf("explicit %s: flags %q, want %q; argv %q", flag, flags, args[:1], launch.Argv)
				}
			})
		}
	}
}

func TestRenderExplicitDefaultPrompt(t *testing.T) {
	home, machine := renderMachine(t)
	machine.Claude.SystemPrompt = pfmconfig.SystemPromptProfessor
	clone := filepath.Join(home, "blueprint")
	for path, body := range map[string]string{
		filepath.Join(clone, "pfm", "harness-prompts", "generated", "claude.md"): "Professor.",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	staged, err := PromptFile(home)
	if err != nil || staged == "" {
		t.Fatalf("professor prompt fixture = %q, %v", staged, err)
	}
	launch, err := Render(
		Request{Purpose: PurposeLauncher, Home: home, Args: []string{flagPromptFile, "/work/alt.md"}},
		machine,
	)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, arg := range launch.Argv {
		if arg == flagPromptFile {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("prompt flag count = %d; argv %q", count, launch.Argv)
	}
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
	if !reflect.DeepEqual(launch.Unset, Hygiene()) || len(launch.Unset) != 23 {
		t.Errorf("unset=%q", launch.Unset)
	}
	if !reflect.DeepEqual(launch.Env, []string{
		"CLAUDE_CONFIG_DIR=" + machine.Accounts[1].ConfigDir, "CACHE_LIVE_CONTROL_MAIN_TTL=1h",
	}) {
		t.Errorf("env=%q", launch.Env)
	}
	if parsed.SessionID != "S" || !parsed.Autonomy {
		t.Errorf("verbs=%#v", parsed)
	}
	if parsed.Settings["outputStyle"] != "default" || parsed.Settings["cleanupPeriodDays"] != float64(36500) {
		t.Errorf("settings=%#v", parsed.Settings)
	}
	for name, want := range map[string]string{
		"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH":     "8",
		"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION": "9007199254740991", "CLAUDE_CODE_TMUX_TRUECOLOR": "1",
		"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "0",
		"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":    "1", "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "100000",
		"ANTHROPIC_SMALL_FAST_MODEL": "claude-haiku-5-5",
	} {
		if got := parsed.SettingsEnv[name]; got != want {
			t.Errorf("env %s=%q, want %q", name, got, want)
		}
	}
	if len(parsed.Hooks) != 21 {
		t.Errorf("hooks=%d", len(parsed.Hooks))
	}
	if parsed.Settings["statusLine"] == nil || parsed.Settings["subagentStatusLine"] == nil {
		t.Errorf("status lines=%#v", parsed.Settings)
	}
	var mcp map[string]any
	if err := json.Unmarshal([]byte(mcpPayload(t, parsed.MCPConfig)), &mcp); err != nil {
		t.Fatal(err)
	}
	assertFamilyMCP(t, home, parsed.MCPConfig)
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
	// Each launch gets its own MCP file, so only that path may differ; the
	// two files carry the same bytes.
	again, err := Render(Request{Purpose: PurposeInteractive, Home: home, Account: 2, SessionID: "S"}, machine)
	if err != nil {
		t.Fatal(err)
	}
	at := index("--mcp-config") + 1
	if at == 0 || at >= len(again.Argv) || again.Argv[at] == launch.Argv[at] ||
		mcpPayload(t, again.Argv[at]) != mcpPayload(t, launch.Argv[at]) {
		t.Fatalf("second render's mcp file = %q, want a fresh file with the first's bytes", again.Argv)
	}
	again.Argv[at] = launch.Argv[at]
	if !reflect.DeepEqual(launch, again) {
		t.Errorf("render is not byte-stable beyond the mcp file: second=%#v", again)
	}
}

func TestRenderPluginEnvEveryPurposeAndAccount(t *testing.T) {
	home, machine := renderMachine(t)
	machine.Claude.AutoCompactWindow = 250000
	machine.Claude.HelperModel = "claude-haiku-4-5"
	account := machine.Claude
	account.AutoCompactWindow = 50000
	account.HelperModel = "claude-sonnet-5-5"
	machine.Accounts[1].Claude = &account
	for _, purpose := range []Purpose{PurposeResume, PurposeLauncher, PurposeQuery} {
		for _, tc := range []struct {
			account int
			window  string
			helper  string
		}{{1, "250000", "claude-haiku-4-5"}, {2, "50000", "claude-sonnet-5-5"}} {
			_, parsed := renderParsed(t, Request{Purpose: purpose, Home: home, Account: tc.account}, machine)
			if got := parsed.SettingsEnv["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != tc.window {
				t.Errorf("purpose %d account %d window=%q, want %q", purpose, tc.account, got, tc.window)
			}
			if got := parsed.SettingsEnv["ANTHROPIC_SMALL_FAST_MODEL"]; got != tc.helper {
				t.Errorf("purpose %d account %d helper model=%q, want %q", purpose, tc.account, got, tc.helper)
			}
			if got := parsed.SettingsEnv["CLAUDE_CODE_ENABLE_FUNCTION_HOOKS"]; got != "1" {
				t.Errorf("purpose %d account %d function hooks=%q", purpose, tc.account, got)
			}
		}
	}
}

// Every launch purpose, with and without the cost ledger, defaults Claude's
// helper calls to Haiku 5.5 through the --settings env block, never the process
// environment: a stray export must not reach the helper model. A hand-built
// machine config (the busy-agent and agent-registry queries state ClaudePrefs
// with only a binary) never sends an empty helper model either.
func TestRenderHelperModelDefaultsToHaikuEveryPurpose(t *testing.T) {
	home, machine := renderMachine(t)
	for _, ledger := range []bool{true, false} {
		machine.Claude.CostLedger = ledger
		for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
			for _, account := range []int{1, 2} {
				launch, parsed := renderParsed(t, Request{Purpose: purpose, Home: home, Account: account}, machine)
				if got := parsed.SettingsEnv["ANTHROPIC_SMALL_FAST_MODEL"]; got != "claude-haiku-5-5" {
					t.Errorf("ledger=%t purpose %d account %d helper model=%q", ledger, purpose, account, got)
				}
				handBuilt := machine
				handBuilt.Claude.HelperModel = ""
				_, bare := renderParsed(t, Request{Purpose: purpose, Home: home, Account: account}, handBuilt)
				if got := bare.SettingsEnv["ANTHROPIC_SMALL_FAST_MODEL"]; got != "claude-haiku-5-5" {
					t.Errorf("ledger=%t purpose %d account %d hand-built config helper model=%q",
						ledger, purpose, account, got)
				}
				if !slices.Contains(launch.Unset, "ANTHROPIC_SMALL_FAST_MODEL") {
					t.Errorf(
						"ledger=%t purpose %d account %d: a stray ANTHROPIC_SMALL_FAST_MODEL export is not unset: %q",
						ledger,
						purpose,
						account,
						launch.Unset,
					)
				}
				for _, entry := range launch.Env {
					if strings.HasPrefix(entry, "ANTHROPIC_SMALL_FAST_MODEL=") {
						t.Errorf("ledger=%t purpose %d account %d: helper model in the process env: %q",
							ledger, purpose, account, launch.Env)
					}
				}
			}
		}
	}
}

// cacheEnv keeps only the prompt-cache keys of a --settings env, so a test
// pins the whole cache surface by map equality: an extra or missing key fails.
func cacheEnv(env map[string]string) map[string]string {
	result := map[string]string{}
	for _, name := range cacheEnvNames {
		if value, ok := env[name]; ok {
			result[name] = value
		}
	}
	return result
}

// cacheEntries keeps only the prompt-cache assignments of a launch's process
// environment, in order.
func cacheEntries(env []string) []string {
	var result []string
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if slices.Contains(cacheEnvNames, name) {
			result = append(result, entry)
		}
	}
	return result
}

var cacheEnvNames = []string{
	"CACHE_LIVE_CONTROL_MAIN_TTL", "CACHE_LIVE_CONTROL_AGENTS_TTL",
	"CLAUDE_CODE_PROMPT_CACHE_TTL", "CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL",
	"ENABLE_PROMPT_CACHING_1H", "FORCE_PROMPT_CACHING_5M",
}

// The cache knob hands only the main chat's starting lifetime to the
// cache-live-control plugin, in the launch's process environment and never the
// --settings env: Claude Code re-applies that block on every settings-file
// reload, which would re-hand the plugin a handoff it already consumed. pfm sets
// no Claude Code TTL, main or sub-agent.
func TestRenderCacheLifetimeMainChatOnly(t *testing.T) {
	home, machine := renderMachine(t)
	for _, entry := range []struct {
		cache1h bool
		want    []string
	}{
		{true, []string{"CACHE_LIVE_CONTROL_MAIN_TTL=1h"}},
		{false, []string{"CACHE_LIVE_CONTROL_MAIN_TTL=5m"}},
	} {
		value := entry.cache1h
		launch, parsed := renderParsed(t, Request{Purpose: PurposeInteractive, Home: home, Cache1H: &value}, machine)
		if launch.Cache1H != entry.cache1h {
			t.Errorf("cache1h=%t: launch.Cache1H=%t", entry.cache1h, launch.Cache1H)
		}
		if got := cacheEntries(launch.Env); !reflect.DeepEqual(got, entry.want) {
			t.Errorf("cache1h=%t: process cache env=%q, want %q", entry.cache1h, got, entry.want)
		}
		if got := cacheEnv(parsed.SettingsEnv); len(got) != 0 {
			t.Errorf(
				"cache1h=%t: settings env carries %#v; a settings reload would re-hand a consumed handoff",
				entry.cache1h, got,
			)
		}
	}
}

func TestRenderSubagentStatuslineTraceFollowsTheConfigSwitch(t *testing.T) {
	home, machine := renderMachine(t)
	for _, purpose := range []Purpose{PurposeInteractive, PurposeQuery} {
		for _, on := range []bool{false, true} {
			machine.Claude.SubagentStatuslineTrace = on
			_, parsed := renderParsed(t, Request{Purpose: purpose, Home: home, Args: []string{"hello"}}, machine)
			got, set := parsed.SettingsEnv[EnvSubagentStatuslineTrace]
			if set != on || (on && got != "1") {
				t.Errorf("purpose %v trace=%t: settings env %s = %q (set %t), want %t", purpose, on,
					EnvSubagentStatuslineTrace, got, set, on)
			}
		}
	}
	if EnvSubagentStatuslineTrace != "PFM_SUBAGENT_STATUSLINE_TRACE" {
		t.Errorf("EnvSubagentStatuslineTrace = %q", EnvSubagentStatuslineTrace)
	}
	machine.Claude.SubagentStatuslineTrace = true
	for _, row := range Resolve(machine, 0) {
		if row.Knob.Name == knobSubagentStatuslineTrace && row.Value != "true" {
			t.Errorf("Resolve value = %q, want true", row.Value)
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

// A query child is never the chat bound to the parent's Herdr pane; every
// interactive purpose keeps the variables its pane reports through.
func TestRenderUnsetsTheHerdrPaneOnlyForAQuery(t *testing.T) {
	home, machine := renderMachine(t)
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
		launch, err := Render(Request{Purpose: purpose, Home: home}, machine)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range herdr.Env() {
			if got := slices.Contains(launch.Unset, name); got != (purpose == PurposeQuery) {
				t.Errorf("purpose %d: Unset holds %s = %t", purpose, name, got)
			}
		}
	}
}

func TestRenderQuery(t *testing.T) {
	home, machine := renderMachine(t)
	launch, parsed := renderParsed(
		t,
		Request{Purpose: PurposeQuery, Home: home, Args: []string{"agents", "--json"}},
		machine,
	)
	if len(launch.Unset) != 23+len(herdr.Env()) || parsed.Settings["outputStyle"] != "default" ||
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

// assertProfessorMCP holds the --mcp-config file's payload to develop's one professor
// registration: exactly {type: stdio, command: <home>/.local/bin/pfm,
// args: [mcp serve --stdio]} under mcpServers.chat-professor, and nothing else.
func assertFamilyMCP(t *testing.T, home, word string, enabled ...bool) {
	t.Helper()
	payload := mcpPayload(t, word)
	var mcp map[string]map[string]any
	if err := json.Unmarshal([]byte(payload), &mcp); err != nil {
		t.Fatalf("mcp=%q: %v", payload, err)
	}
	chat, harvester := true, false
	if len(enabled) == 2 {
		chat, harvester = enabled[0], enabled[1]
	}
	want := map[string]any{}
	for family, on := range map[string]bool{"chat": chat, "harvester": harvester} {
		if on {
			want[family+"-professor"] = map[string]any{
				"type":    "stdio",
				"command": filepath.Join(home, ".local", "bin", "pfm"),
				"args":    []any{"mcp", "serve", "--stdio", "--family", family},
			}
		}
	}
	if len(mcp) != 1 || !reflect.DeepEqual(mcp["mcpServers"], want) {
		t.Errorf("mcp=%s, want family registrations %#v", payload, want)
	}
}

func TestRenderMCPFamilyRegistrations(t *testing.T) {
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
			assertFamilyMCP(t, home, parsed.MCPConfig, entry.chat, entry.harvester)
		})
	}
}

func TestRenderAccountOneConfigDir(t *testing.T) {
	home, machine := renderMachine(t)
	if err := os.MkdirAll(filepath.Join(home, "override"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
		for _, override := range []string{"", filepath.Join(home, "override")} {
			t.Run(fmt.Sprintf("purpose=%d/override=%t", purpose, override != ""), func(t *testing.T) {
				launch, _ := renderParsed(
					t,
					Request{Purpose: purpose, Home: home, Account: 1, ConfigDir: override},
					machine,
				)
				dir := machine.Accounts[0].ConfigDir
				if override != "" {
					dir = override
				}
				want := []string{"CLAUDE_CONFIG_DIR=" + dir, "CACHE_LIVE_CONTROL_MAIN_TTL=1h"}
				if !reflect.DeepEqual(launch.Env, want) {
					t.Fatalf("Env=%q, want %q", launch.Env, want)
				}
			})
		}
	}
}

func TestRenderThirdPartyMCP(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		purpose                     Purpose
		chat, harvester, thirdParty bool
	}{
		{"third party only", PurposeInteractive, false, false, true},
		{"beside professor", PurposeInteractive, true, false, true},
		{"beside harvester", PurposeInteractive, false, true, true},
		{"resume", PurposeResume, false, false, true},
		{"launcher", PurposeLauncher, false, false, true},
		{"none", PurposeInteractive, false, false, false},
		{"query", PurposeQuery, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, machine := renderMachine(t)
			machine.MCPServers[pfmconfig.MCPServerChat] = pfmconfig.MCPServer{Enabled: tc.chat}
			machine.MCPServers[pfmconfig.MCPServerHarvester] = pfmconfig.MCPServer{Enabled: tc.harvester}
			browser := json.RawMessage(
				`{"type":"stdio","command":"x","args":["--flag"],"env":{"EXAMPLE":"value"},"custom":{"future":true}}`,
			)
			remote := json.RawMessage(
				`{"type":"http","url":"https://example.invalid/mcp","headers":{"X-Example":"value"}}`,
			)
			if tc.thirdParty {
				machine.MCP.ThirdParty = map[string]json.RawMessage{"browser": browser, "remote": remote}
			}
			_, parsed := renderParsed(t, Request{Purpose: tc.purpose, Home: home, Account: 1}, machine)
			if tc.purpose == PurposeQuery || (!tc.chat && !tc.harvester && !tc.thirdParty) {
				if parsed.MCPConfig != "" {
					t.Fatalf("mcp-config = %q, want none", parsed.MCPConfig)
				}
				return
			}
			want := map[string]json.RawMessage{"browser": browser, "remote": remote}
			for family, enabled := range map[string]bool{pfmconfig.MCPServerChat: tc.chat, pfmconfig.MCPServerHarvester: tc.harvester} {
				if enabled {
					want[pfmconfig.MCPRegistrationName(family)] = json.RawMessage(
						fmt.Sprintf(
							`{"args":["mcp","serve","--stdio","--family",%q],"command":%q,"type":"stdio"}`,
							family,
							filepath.Join(home, ".local", "bin", "pfm"),
						),
					)
				}
			}
			encoded, err := json.Marshal(map[string]any{"mcpServers": want})
			if err != nil {
				t.Fatal(err)
			}
			if payload := mcpPayload(t, parsed.MCPConfig); payload != string(encoded) {
				t.Fatalf("mcp-config = %s, want %s", payload, encoded)
			}
		})
	}
}

func TestRenderConfigDirValidation(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		account               int
		emptyRoster, explicit bool
		state, suffix         string
	}{
		{name: "absent account", account: 2, state: "absent", suffix: " does not exist — run pfm install"},
		{name: "symlink account", account: 1, state: "symlink", suffix: " resolves to the Claude store — run pfm doctor"},
		{name: "file account", account: 1, state: "file", suffix: " is not a real directory — run pfm doctor"},
		{name: "uninspectable account", account: 2, state: "uninspectable"},
		{name: "absent explicit dir", explicit: true, state: "absent", suffix: " does not exist — run pfm install"},
		{name: "symlink explicit dir", explicit: true, state: "symlink", suffix: " resolves to the Claude store — run pfm doctor"},
		{name: "uninspectable explicit dir", explicit: true, state: "uninspectable"},
		{name: "no dir"},
		{name: "unknown account", account: 9},
		{name: "no roster", account: 1, emptyRoster: true},
		{name: "existing account", account: 2, state: "directory"},
		{name: "existing explicit dir", explicit: true, state: "directory"},
		{name: "absent override", account: 1, explicit: true, state: "absent", suffix: " does not exist — run pfm install"},
	} {
		for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
			t.Run(fmt.Sprintf("%s/purpose=%d", tc.name, purpose), func(t *testing.T) {
				home, machine := renderMachine(t)
				request := Request{Account: tc.account, Purpose: purpose, Home: home}
				dir := ""
				if account, found := machine.AccountByID(tc.account); found && !tc.emptyRoster {
					dir = account.ConfigDir
				}
				if tc.emptyRoster {
					machine.Accounts = nil
				}
				if tc.explicit {
					dir = filepath.Join(home, "override")
					request.ConfigDir = dir
				}
				if tc.state != "" {
					if err := os.RemoveAll(dir); err != nil {
						t.Fatal(err)
					}
				}
				switch tc.state {
				case "directory":
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					target := jailStore(t)
					if err := os.MkdirAll(target, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, dir); err != nil {
						t.Fatal(err)
					}
				case "file", "uninspectable":
					if err := os.WriteFile(dir, nil, 0o600); err != nil {
						t.Fatal(err)
					}
					if tc.state == "uninspectable" {
						dir = filepath.Join(dir, "child")
						if tc.explicit {
							request.ConfigDir = dir
						} else {
							machine.Accounts[tc.account-1].ConfigDir = dir
						}
					}
				}
				prefix := ""
				if tc.account > 0 {
					prefix = fmt.Sprintf("account %d: ", tc.account)
				}
				want := ""
				if tc.suffix != "" {
					want = prefix + dir + tc.suffix
				}
				if tc.state == "uninspectable" {
					_, cause := os.Lstat(dir)
					if !errors.Is(cause, syscall.ENOTDIR) {
						t.Fatalf("fixture Lstat: %v", cause)
					}
					want = fmt.Sprintf("%sinspect %s: %v", prefix, dir, cause)
				}
				if tc.account == 9 {
					want = "account 9 is not in the configured roster"
				}
				launch, err := Render(request, machine)
				if want != "" {
					if err == nil || err.Error() != want {
						t.Fatalf("Render error = %v, want %q", err, want)
					}
					if tc.state == "uninspectable" && !errors.Is(err, syscall.ENOTDIR) {
						t.Fatalf("inspection cause not wrapped: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := slices.Contains(launch.Env, configDirEnv+"="+dir); got != (dir != "") {
					t.Fatalf("Env=%q, want config dir %q", launch.Env, dir)
				}
				if dir == "" {
					for _, entry := range launch.Env {
						if strings.HasPrefix(entry, configDirEnv+"=") {
							t.Fatalf("Env=%q, want no config dir", launch.Env)
						}
					}
				}
			})
		}
	}
}

// nameWords is every value that follows a name flag in a rendered argv.
func nameWords(argv []string) []string {
	var words []string
	for index, word := range argv {
		if (word == flagName || word == flagNameShort) && index+1 < len(argv) {
			words = append(words, argv[index+1])
		}
	}
	return words
}

func TestRenderNameFlag(t *testing.T) {
	home, machine := renderMachine(t)
	for _, scenario := range []struct {
		name    string
		request Request
		want    []string
	}{
		{"empty name carries no flag", Request{Name: ""}, nil},
		{"blank name carries no flag", Request{Name: "   "}, nil},
		{"control-only name carries no flag", Request{Name: "\n\t\x00"}, nil},
		{"unnamed sentinel carries no flag", Request{Name: "(unnamed)"}, nil},
		{"whitespace runs collapse", Request{Name: "fix\n  login"}, []string{"fix login"}},
		{"control runes drop", Request{Name: "fix\x00 lo\x1bgin"}, []string{"fix login"}},
		{"long name is clipped", Request{Name: strings.Repeat("é", 300)}, []string{strings.Repeat("é", 120)}},
		{"kill marker passes verbatim", Request{Name: "_KILL worker 3"}, []string{"_KILL worker 3"}},
		{
			"an Args name wins, long form",
			Request{Name: "other", Args: []string{"--name", "keep"}},
			[]string{"keep"},
		},
		{
			"an Args name wins, short form",
			Request{Name: "other", Args: []string{flagNameShort, "keep"}},
			[]string{"keep"},
		},
		{
			"an Args name wins, equals form",
			Request{Name: "other", Args: []string{"--name=keep"}},
			nil,
		},
		// A resumed session carries its own name in its transcript: a launch name
		// lands after a /rename typed while Claude registers, and reverts it.
		{"a resumed session carries no flag", Request{Name: "keep", Resume: "s1"}, nil},
		{"an Args --resume carries no flag", Request{Name: "keep", Args: []string{"--resume", "s1"}}, nil},
		{"an Args --resume= carries no flag", Request{Name: "keep", Args: []string{"--resume=s1"}}, nil},
		{"an Args -r carries no flag", Request{Name: "keep", Args: []string{"-r", "s1"}}, nil},
		{"an Args --continue carries no flag", Request{Name: "keep", Args: []string{"--continue"}}, nil},
		{"an Args -c carries no flag", Request{Name: "keep", Args: []string{"-c"}}, nil},
		{"a forked resume is a new session, named", Request{Name: "keep", Resume: "s1", Fork: true}, []string{"keep"}},
		{
			"an Args fork of a resume is named",
			Request{Name: "keep", Args: []string{"--resume", "s1", "--fork-session"}},
			[]string{"keep"},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := scenario.request
			request.Purpose, request.Home, request.Account = PurposeResume, home, 2
			launch, err := Render(request, machine)
			if err != nil {
				t.Fatal(err)
			}
			if got := nameWords(launch.Argv); !slices.Equal(got, scenario.want) {
				t.Fatalf("name words = %q, want %q", got, scenario.want)
			}
			if request.Args != nil && slices.Contains(request.Args, "--name=keep") &&
				slices.Contains(launch.Argv, flagName) {
				t.Fatal("a second --name rode beside --name=keep")
			}
		})
	}
}

// stageComposedPrompt records a clone for home and, when write is set, puts a
// composed Claude prompt where the door looks for it.
func stageComposedPrompt(t *testing.T, home string, write bool) string {
	t.Helper()
	clone := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	path := paths.GeneratedHarnessPromptIn(clone, pfmengine.Claude)
	if !write {
		return path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fleet prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A launch that should carry a system prompt file and does not says so, with
// the cause: one verdict, shared by every surface that tells the person who
// started the chat, and true to the argv Render builds — the flag is dropped
// exactly when the verdict says so.
func TestPromptWarning(t *testing.T) {
	seat := filepath.Join(t.TempDir(), "seat.md")
	if err := os.WriteFile(seat, []byte("seat"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		name    string
		mode    string
		stage   string // "", "marker", "file"
		request Request
		want    string // substring of the warning; "" is no warning
		carries bool   // Render adds --system-prompt-file itself
	}{
		{"professor, no clone recorded", "professor", "", Request{}, "no source repository recorded", false},
		{"professor, composed file missing", "professor", "marker", Request{}, "claude.md", false},
		{"professor, composed file present", "professor", "file", Request{}, "", true},
		{"production wants no fleet prompt", "production", "", Request{}, "", false},
		{"lean wants no fleet prompt", "lean", "", Request{}, "", false},
		{"seat file present", "professor", "", Request{PromptFile: seat}, "", true},
		{
			"seat file missing", "production", "",
			Request{PromptFile: "/nonexistent/seat.md"},
			"/nonexistent/seat.md", false,
		},
		{"seat file is a directory", "production", "", Request{PromptFile: filepath.Dir(seat)}, "is a directory", false},
		{
			"caller states its own prompt file", "professor", "",
			Request{Args: []string{"--system-prompt-file", "/elsewhere.md"}},
			"", false,
		},
		{
			"caller states its own prompt file with =", "professor", "",
			Request{Args: []string{"--system-prompt-file=/elsewhere.md"}},
			"", false,
		},
		{
			"caller states its own inline prompt", "professor", "marker",
			Request{Args: []string{"--system-prompt", "be brief"}},
			"", false,
		},
		{
			"caller states its own inline prompt with =", "professor", "marker",
			Request{Args: []string{"--system-prompt=be brief"}},
			"", false,
		},
		{
			"inline prompt leaves the fleet file in the argv", "professor", "file",
			Request{Args: []string{"--system-prompt", "be brief"}},
			"", true,
		},
		{"query carries no prompt", "professor", "", Request{Purpose: PurposeQuery}, "", false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			home, machine := renderMachine(t)
			machine.Claude.SystemPrompt = entry.mode
			switch entry.stage {
			case "marker":
				stageComposedPrompt(t, home, false)
			case "file":
				stageComposedPrompt(t, home, true)
			}
			entry.request.Home = home
			got := PromptWarning(entry.request, machine)
			if entry.want == "" {
				if got != "" {
					t.Fatalf("PromptWarning = %q, want none", got)
				}
			} else if !strings.Contains(got, entry.want) {
				t.Fatalf("PromptWarning = %q, want it to name %q", got, entry.want)
			}
			launch, err := Render(entry.request, machine)
			if err != nil {
				t.Fatal(err)
			}
			added := 0
			for _, arg := range launch.Argv {
				if arg == "--system-prompt-file" {
					added++
				}
			}
			for _, arg := range entry.request.Args {
				if arg == "--system-prompt-file" {
					added--
				}
			}
			if carried := added == 1; carried != entry.carries || added > 1 {
				t.Fatalf(
					"Render added --system-prompt-file %d times, want carried=%t: %q",
					added,
					entry.carries,
					launch.Argv,
				)
			}
		})
	}
}
