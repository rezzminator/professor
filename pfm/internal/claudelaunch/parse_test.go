package claudelaunch

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestParseRoundTripEveryKnob(t *testing.T) {
	home, machine := renderMachine(t)
	machine.Claude.Theme = "dark"
	machine.Claude.NativeCursor = true
	machine.Claude.MaxConcurrentSubagents = 3
	machine.Claude.SystemPrompt = "professor"
	machine.MCPServers["harvester"] = pfmconfig.MCPServer{Enabled: true}
	prompt := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(prompt, []byte("prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := Request{
		Purpose:    PurposeInteractive,
		Home:       home,
		Account:    2,
		SessionID:  "S",
		Resume:     "R",
		Fork:       true,
		Name:       "named",
		Model:      "opus",
		Effort:     "high",
		PromptFile: prompt,
		Args:       []string{"hello"},
	}
	launch, parsed := renderParsed(t, request, machine)
	if !slices.Equal(parsed.Rest, request.Args) {
		t.Errorf("rest=%q", parsed.Rest)
	}
	for _, knob := range Knobs {
		t.Run(knob.Name, func(t *testing.T) {
			if knob.Wire == WireUnset {
				if !slices.Contains(launch.Unset, knob.Target) {
					t.Errorf("unset lacks %s", knob.Target)
				}
				return
			}
			switch knob.Name {
			case "configDir":
				if !slices.Contains(launch.Env, "CLAUDE_CONFIG_DIR="+machine.Accounts[1].ConfigDir) {
					t.Error("config dir missing")
				}
			case "binary":
				if launch.Binary != machine.Claude.Binary {
					t.Errorf("binary=%q", launch.Binary)
				}
			case "cache1h":
				if parsed.SettingsEnv["ENABLE_PROMPT_CACHING_1H"] != "1" {
					t.Error("cache missing")
				}
			case "systemPrompt":
				if parsed.PromptFile != prompt {
					t.Errorf("prompt=%q", parsed.PromptFile)
				}
			case "nativeCursor":
				if parsed.SettingsEnv["CLAUDE_CODE_NATIVE_CURSOR"] != "1" {
					t.Error("cursor missing")
				}
			case "maxSubagentSpawnDepth":
				if parsed.SettingsEnv["CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH"] != "8" {
					t.Error("depth missing")
				}
			case "maxConcurrentSubagents":
				if parsed.SettingsEnv["CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS"] != "3" {
					t.Error("concurrency missing")
				}
			case "webSearchesPerSession":
				if parsed.SettingsEnv["CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION"] != "9007199254740991" {
					t.Error("web cap missing")
				}
			case "tmuxTruecolor":
				if parsed.SettingsEnv["CLAUDE_CODE_TMUX_TRUECOLOR"] != "1" {
					t.Error("truecolor missing")
				}
			case "agentTeams":
				if parsed.SettingsEnv["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] != "0" {
					t.Error("agent teams missing")
				}
			case "outputStyle":
				if parsed.Settings["outputStyle"] != "default" {
					t.Error("output style missing")
				}
			case "theme":
				if parsed.Settings["theme"] != "dark" {
					t.Error("theme missing")
				}
			case "cleanupPeriodDays":
				if parsed.Settings["cleanupPeriodDays"] != float64(36500) {
					t.Error("cleanup missing")
				}
			case "hooks":
				if len(parsed.Hooks) != 18 {
					t.Errorf("hooks=%d", len(parsed.Hooks))
				}
				for _, hook := range parsed.Hooks {
					if hook.Name == "" {
						t.Errorf("unnamed hook: %#v", hook)
					}
				}
			case "statusLine", "subagentStatusLine":
				if parsed.Settings[knob.Name] == nil {
					t.Error("status line missing")
				}
			case "mcp":
				assertProfessorMCP(t, home, parsed.MCPConfig)
			case "permissionMode":
				if !parsed.Autonomy {
					t.Error("autonomy missing")
				}
			case "model":
				if parsed.Model != "opus" {
					t.Errorf("model=%q", parsed.Model)
				}
			case "effort":
				if parsed.Effort != "high" {
					t.Errorf("effort=%q", parsed.Effort)
				}
			case "sessionID":
				if parsed.SessionID != "S" {
					t.Errorf("session=%q", parsed.SessionID)
				}
			case "resume":
				if parsed.Resume != "R" {
					t.Errorf("resume=%q", parsed.Resume)
				}
			case "fork":
				if !parsed.Fork {
					t.Error("fork missing")
				}
			case "name":
				if parsed.Name != "named" {
					t.Errorf("name=%q", parsed.Name)
				}
			default:
				t.Errorf("uncovered knob %s", knob.Name)
			}
		})
	}
}

func TestParseMalformedJSONNamesFlag(t *testing.T) {
	for _, flag := range []string{"--settings", "--mcp-config"} {
		_, err := Parse([]string{"claude", flag, "{"})
		if err == nil || !strings.Contains(err.Error(), flag) {
			t.Errorf("%s error=%v", flag, err)
		}
	}
}

func TestParseUnknownFlagPairs(t *testing.T) {
	parsed, err := Parse([]string{"claude", "agents", "--format", "json", "--strange=yes", "tail"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(parsed.Rest, []string{"agents", "--format", "json", "--strange=yes", "tail"}) {
		t.Errorf("rest=%q", parsed.Rest)
	}
}

func TestNewSessionIDIsV4(t *testing.T) {
	id, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Errorf("id=%q", id)
	}
}
