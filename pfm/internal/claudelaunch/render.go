package claudelaunch

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type Purpose int

const (
	PurposeInteractive Purpose = iota
	PurposeResume
	PurposeLauncher
	PurposeQuery
)

type Request struct {
	Purpose                              Purpose
	Home                                 string
	Account                              int
	ConfigDir, Binary, SessionID, Resume string
	Fork                                 bool
	Name                                 string
	Cache1H                              *bool
	Model, Effort, PromptFile            string
	Args                                 []string
}

type Launch struct {
	Unset, Env []string
	Binary     string
	Argv       []string
	Cache1H    bool
	SessionID  string
}

func Render(request Request, machine pfmconfig.Config) (Launch, error) {
	prefs := machine.EffectiveClaude(request.Account)
	result := Launch{Unset: Hygiene(), Binary: prefs.Binary, Cache1H: prefs.Cache1H, SessionID: request.SessionID}
	if request.Binary != "" {
		result.Binary = request.Binary
	}
	if result.Binary == "" {
		result.Binary = pfmengine.MustLookup(pfmengine.Claude).Binary
	}
	if request.Cache1H != nil {
		result.Cache1H = *request.Cache1H
	}
	if result.SessionID == "" {
		result.SessionID = request.Resume
	}
	if account, found := machine.AccountByID(request.Account); found && !account.Implicit {
		dir := account.ConfigDir
		if request.ConfigDir != "" {
			dir = request.ConfigDir
		}
		if dir != "" {
			result.Env = []string{configDirEnv + "=" + dir}
		}
	} else if request.Account == 0 && request.ConfigDir != "" {
		result.Env = []string{configDirEnv + "=" + request.ConfigDir}
	}
	result.Env = append(result.Env, envPromptCacheTTL+"="+cacheTTL(result.Cache1H))
	if request.SessionID != "" {
		result.Argv = append(result.Argv, flagSessionID, request.SessionID)
	}
	if request.Resume != "" {
		result.Argv = append(result.Argv, flagResume, request.Resume)
	}
	if request.Fork {
		result.Argv = append(result.Argv, flagForkSession)
	}
	if request.Name != "" {
		result.Argv = append(result.Argv, flagName, request.Name)
	}
	result.Argv = append(result.Argv, request.Args...)
	settings := settingsFor(request, prefs)
	payload, err := json.Marshal(settings)
	if err != nil {
		return Launch{}, fmt.Errorf("render --settings: %w", err)
	}
	result.Argv = append(result.Argv, flagSettings, string(payload))
	if request.Purpose != PurposeQuery {
		mcp := mcpConfig(request.Home, machine)
		if len(mcp) != 0 {
			encoded, err := json.Marshal(map[string]any{"mcpServers": mcp})
			if err != nil {
				return Launch{}, fmt.Errorf("render --mcp-config: %w", err)
			}
			result.Argv = append(result.Argv, flagMCPConfig, string(encoded))
		}
		if request.Model != "" {
			result.Argv = append(result.Argv, flagModel, request.Model)
		}
		if request.Effort != "" {
			result.Argv = append(result.Argv, flagEffort, request.Effort)
		}
		// A role seat's file is its constitution whatever the mode; the
		// composed prompt stands in for it only under professor.
		prompt := request.PromptFile
		if prompt == "" && prefs.SystemPrompt == pfmconfig.SystemPromptProfessor {
			// A missing marker or composed file omits the flag by contract.
			prompt, _ = PromptFile(request.Home)
		}
		if prompt != "" {
			if info, err := os.Stat(prompt); err == nil && !info.IsDir() {
				result.Argv = append(result.Argv, flagPromptFile, prompt)
			}
		}
		if prefs.PermissionMode == pfmconfig.PermissionBypass && request.Purpose != PurposeLauncher {
			result.Argv = append(result.Argv, flagAllowBypass, flagBypass)
		}
	}
	return result, nil
}

func settingsFor(request Request, prefs pfmconfig.ClaudePrefs) map[string]any {
	settings := map[string]any{knobOutputStyle: defaultWord, knobCleanupPeriodDays: prefs.CleanupPeriodDays}
	env := map[string]string{
		envWebSearches:       strconv.FormatInt(prefs.WebSearchesPerSession, 10),
		envAutoCompactWindow: strconv.FormatInt(prefs.AutoCompactWindow, 10),
		envAgentTeams:        "0",
		envFunctionHooks:     "1",
	}
	depth := prefs.MaxSubagentSpawnDepth
	if depth < 1 {
		depth = pfmconfig.DefaultSubagentSpawnDepth
	}
	env[envSpawnDepth] = strconv.Itoa(depth)
	if prefs.MaxConcurrentSubagents > 0 {
		env[envConcurrentSubagents] = strconv.Itoa(prefs.MaxConcurrentSubagents)
	}
	if prefs.TmuxTruecolor {
		env[envTmuxTruecolor] = "1"
	}
	if prefs.NativeCursor {
		env[envNativeCursor] = "1"
	}
	if request.Purpose != PurposeQuery && prefs.SystemPrompt == pfmconfig.SystemPromptLean {
		env[envSimplePrompt] = "1"
	}
	settings["env"] = env
	if prefs.Theme != "" {
		settings[knobTheme] = prefs.Theme
	}
	if request.Purpose != PurposeQuery {
		settings[knobHooks] = hookSettings(HookTemplates(request.Home))
		settings[knobStatusLine] = map[string]any{
			typeWord: commandWord, commandWord: StatusLineCommand(request.Home), "padding": 0,
			"refreshInterval": 3, "hideVimModeIndicator": true,
		}
		settings[knobSubagentStatusLine] = map[string]any{
			typeWord:    commandWord,
			commandWord: SubagentStatusLineCommand(request.Home),
		}
	}
	return settings
}

func hookSettings(templates []Hook) map[string]any {
	events := map[string]any{}
	groups := map[string]map[string]map[string]any{}
	for _, hook := range templates {
		if groups[hook.Event] == nil {
			groups[hook.Event] = map[string]map[string]any{}
		}
		group := groups[hook.Event][hook.Matcher]
		if group == nil {
			group = map[string]any{knobHooks: []any{}}
			if hook.Matcher != "" {
				group["matcher"] = hook.Matcher
			}
			groups[hook.Event][hook.Matcher] = group
		}
		command := map[string]any{typeWord: commandWord, commandWord: hook.Command}
		if hook.Async {
			command["async"] = true
		}
		group[knobHooks] = append(group[knobHooks].([]any), command)
	}
	for event, byMatcher := range groups {
		// Keep registration order within each event, including distinct matchers.
		seen := map[string]bool{}
		var ordered []any
		for _, hook := range templates {
			if hook.Event == event && !seen[hook.Matcher] {
				ordered = append(ordered, byMatcher[hook.Matcher])
				seen[hook.Matcher] = true
			}
		}
		events[event] = ordered
	}
	return events
}

// mcpConfig is develop's one professor registration, the shape every engine
// installs: {type: stdio, command: <home>/.local/bin/pfm, args: [mcp serve
// --stdio]}, present when the chat or the harvester family is enabled. The
// stdio server serves every enabled family itself.
func mcpConfig(home string, machine pfmconfig.Config) map[string]any {
	if !machine.MCPServers[pfmconfig.MCPServerChat].Enabled &&
		!machine.MCPServers[pfmconfig.MCPServerHarvester].Enabled {
		return nil
	}
	return map[string]any{pfmconfig.MCPServerProfessor: map[string]any{
		typeWord: "stdio", commandWord: filepath.Join(home, ".local", "bin", "pfm"),
		"args": []string{knobMCP, "serve", "--stdio"},
	}}
}

func PromptFile(home string) (string, error) {
	return paths.ComposedHarnessPrompt(home, pfmengine.Claude)
}

func NewSessionID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:]), nil
}

// cacheTTL names the main chat's prompt-cache lifetime. It sets only
// CLAUDE_CODE_PROMPT_CACHE_TTL: FORCE_PROMPT_CACHING_5M would outrank the
// cache-live-control plugin's /cache, and ENABLE_PROMPT_CACHING_1H would
// raise every sub-agent to 1h. Render carries it in the process environment,
// never the settings env block: Claude Code re-applies that block on every
// settings-file reload, which would overwrite a live /cache. RenderHeadless
// keeps it in the settings, since a -p run takes no /cache.
func cacheTTL(cache1h bool) string {
	if cache1h {
		return "1h"
	}
	return "5m"
}
