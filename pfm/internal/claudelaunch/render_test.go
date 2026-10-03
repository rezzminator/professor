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
)

func renderMachine(t *testing.T) (string, pfmconfig.Config) {
	t.Helper()
	home := t.TempDir()
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
	if !reflect.DeepEqual(launch.Env, []string{
		"CLAUDE_CONFIG_DIR=" + machine.Accounts[1].ConfigDir, "CACHE_LIVE_CONTROL_MAIN_TTL=1h",
	}) {
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
		"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH":     "8",
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
	if err := json.Unmarshal([]byte(mcpPayload(t, parsed.MCPConfig)), &mcp); err != nil {
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

// assertProfessorMCP holds the --mcp-config file's payload to develop's one professor
// registration: exactly {type: stdio, command: <home>/.local/bin/pfm,
// args: [mcp serve --stdio]} under mcpServers.professor, and nothing else.
func assertProfessorMCP(t *testing.T, home, word string) {
	t.Helper()
	payload := mcpPayload(t, word)
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
			if tc.chat || tc.harvester {
				want[pfmconfig.MCPServerProfessor] = json.RawMessage(
					fmt.Sprintf(
						`{"args":["mcp","serve","--stdio"],"command":%q,"type":"stdio"}`,
						filepath.Join(home, ".local", "bin", "pfm"),
					),
				)
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
