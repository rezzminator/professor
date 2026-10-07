package claudelaunch

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/obs"
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

// CheckConfigDir refuses an account dir Claude would create itself or one
// InspectConfigDir does not accept; a link resolving outside the store passes.
func CheckConfigDir(account int, dir string) error {
	prefix := ""
	if account > 0 {
		prefix = fmt.Sprintf("account %d: ", account)
	}
	home, err := paths.Home()
	if err != nil {
		return fmt.Errorf("%sinspect %s: %w", prefix, dir, err)
	}
	inspected := InspectConfigDir(ClaudeStore(home), dir)
	switch inspected.State {
	case ConfigDirMissing:
		return fmt.Errorf("%s%s does not exist — run pfm install", prefix, dir)
	case ConfigDirUnreadable:
		return fmt.Errorf("%sinspect %s: %w", prefix, dir, inspected.Err)
	case ConfigDirNotDir:
		return fmt.Errorf("%s%s is not a real directory — run pfm doctor", prefix, dir)
	case ConfigDirStore:
		return fmt.Errorf("%s%s resolves to the Claude store — run pfm doctor", prefix, dir)
	}
	return nil
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
	// settingsDir is the store whose settings.json the launched Claude reads.
	settingsDir := ""
	if account, found := machine.AccountByID(request.Account); found {
		dir := account.ConfigDir
		if request.ConfigDir != "" {
			dir = request.ConfigDir
		}
		if err := CheckConfigDir(request.Account, dir); err != nil {
			return Launch{}, err
		}
		result.Env = []string{configDirEnv + "=" + dir}
		settingsDir = dir
	} else if request.Account == 0 && request.ConfigDir != "" {
		if err := CheckConfigDir(request.Account, request.ConfigDir); err != nil {
			return Launch{}, err
		}
		result.Env = []string{configDirEnv + "=" + request.ConfigDir}
		settingsDir = request.ConfigDir
	} else if request.Account != 0 && len(machine.Accounts) > 0 {
		return Launch{}, fmt.Errorf("account %d is not in the configured roster", request.Account)
	}
	result.Env = append(result.Env, envCacheLiveControlMainTTL+"="+promptCacheTTL(result.Cache1H))
	result.Env = append(result.Env, ShellEnv(paths.OSEnv{}.Lookup)...)
	if request.SessionID != "" {
		result.Argv = append(result.Argv, flagSessionID, request.SessionID)
	}
	if request.Resume != "" {
		result.Argv = append(result.Argv, flagResume, request.Resume)
	}
	if request.Fork {
		result.Argv = append(result.Argv, flagForkSession)
	}
	if name := naming.LaunchName(request.Name); name != "" && !argsName(request.Args) {
		result.Argv = append(result.Argv, flagName, name)
	}
	result.Argv = append(result.Argv, request.Args...)
	settings := settingsFor(request, prefs, noFlicker(request, settingsDir))
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
			// The payload rides a private file, never argv: a third-party
			// entry's env and headers would show in ps and /proc/{pid}/cmdline.
			path, err := writeMCPFile(request.Home, encoded)
			if err != nil {
				return Launch{}, fmt.Errorf("render --mcp-config: %w", err)
			}
			result.Argv = append(result.Argv, flagMCPConfig, path)
		}
		if request.Model != "" && !argsPersonaFlag(request.Args, flagModel) {
			result.Argv = append(result.Argv, flagModel, request.Model)
		}
		if request.Effort != "" && !argsPersonaFlag(request.Args, flagEffort) {
			result.Argv = append(result.Argv, flagEffort, request.Effort)
		}
		// A role seat's file is its constitution whatever the mode; the
		// composed prompt stands in for it only under professor.
		prompt := request.PromptFile
		if prompt == "" && !argsPersonaFlag(request.Args, flagPromptFile) &&
			prefs.SystemPrompt == pfmconfig.SystemPromptProfessor {
			// A missing marker or composed file omits the flag by contract.
			prompt, _ = PromptFile(request.Home)
		}
		if prompt != "" && !argsPersonaFlag(request.Args, flagPromptFile) {
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

// noFlicker reports whether a seat's launch carries envNoFlicker: an
// interactive seat whose account asks for fullscreen (WantsFullscreen). A
// settings file that cannot be judged never blocks the launch: the failure is
// logged with its path and the seat launches without the knob.
func noFlicker(request Request, settingsDir string) bool {
	if request.Purpose == PurposeQuery || settingsDir == "" {
		return false
	}
	wants, err := WantsFullscreen(settingsDir)
	if err != nil {
		obs.Logger(context.Background()).Warn(
			"claudelaunch: fullscreen settings unreadable; launching without "+envNoFlicker,
			obs.FieldErr, err.Error(),
		)
		return false
	}
	return wants
}

const (
	cacheTTL1H = "1h"
	cacheTTL5M = "5m"
)

// promptCacheTTL is the main chat's starting prompt-cache TTL word, handed to
// the cache-live-control plugin as CACHE_LIVE_CONTROL_MAIN_TTL. The plugin sets
// Claude Code's own TTL variables and owns every TTL, main chat and sub-agents,
// from then on; pfm writes no Claude Code TTL variable itself. Render carries
// the handoff in the process environment, never the settings env block: Claude
// Code re-applies that block on every settings-file reload, which would re-hand
// the plugin a handoff it already consumed. RenderHeadless keeps it in its
// settings, since a -p run takes no /cache.
func promptCacheTTL(cache1h bool) string {
	if cache1h {
		return cacheTTL1H
	}
	return cacheTTL5M
}

func settingsFor(request Request, prefs pfmconfig.ClaudePrefs, fullscreen bool) map[string]any {
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
	if fullscreen {
		env[envNoFlicker] = "1"
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

// mcpConfig combines third-party entries with the professor registration
// when the chat or harvester family is enabled.
func mcpConfig(home string, machine pfmconfig.Config) map[string]any {
	servers := make(map[string]any, len(machine.MCP.ThirdParty)+1)
	for name, entry := range machine.MCP.ThirdParty {
		servers[name] = entry
	}
	if !machine.MCPServers[pfmconfig.MCPServerChat].Enabled &&
		!machine.MCPServers[pfmconfig.MCPServerHarvester].Enabled {
		if len(servers) != 0 {
			return servers
		}
		return nil
	}
	servers[pfmconfig.MCPServerProfessor] = map[string]any{
		typeWord: "stdio", commandWord: filepath.Join(home, ".local", "bin", "pfm"),
		"args": []string{knobMCP, "serve", "--stdio"},
	}
	return servers
}

func PromptFile(home string) (string, error) {
	return paths.ComposedHarnessPrompt(home, pfmengine.Claude)
}

func argsPersonaFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
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
