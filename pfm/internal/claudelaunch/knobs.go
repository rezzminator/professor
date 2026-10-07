package claudelaunch

import pfmengine "github.com/rezzminator/professor/pfm/internal/engine"

const (
	knobBinary                     = "binary"
	knobCache1H                    = "cache1h"
	knobSystemPrompt               = "systemPrompt"
	knobNativeCursor               = "nativeCursor"
	knobMaxSubagentSpawnDepth      = "maxSubagentSpawnDepth"
	knobMaxConcurrentSubagents     = "maxConcurrentSubagents"
	knobWebSearchesPerSession      = "webSearchesPerSession"
	knobAutoCompactWindow          = "autoCompactWindow"
	knobTmuxTruecolor              = "tmuxTruecolor"
	knobTheme                      = "theme"
	knobCleanupPeriodDays          = "cleanupPeriodDays"
	knobPermissionMode             = "permissionMode"
	knobMCP                        = "mcp"
	knobHooks                      = "hooks"
	knobStatusLine                 = "statusLine"
	knobSubagentStatusLine         = "subagentStatusLine"
	knobOutputStyle                = "outputStyle"
	knobConfigDir                  = "configDir"
	knobModel                      = "model"
	knobEffort                     = "effort"
	knobSessionID                  = "sessionID"
	knobResume                     = "resume"
	knobFork                       = "fork"
	knobName                       = "name"
	defaultWord                    = "default"
	accountWord                    = "account"
	productionMode                 = "production"
	unsetWord                      = "unset"
	commandWord                    = "command"
	typeWord                       = "type"
	configDirEnv                   = "CLAUDE_CONFIG_DIR"
	envSessionID                   = "CLAUDE_CODE_SESSION_ID"
	envClaudeCode                  = "CLAUDECODE"
	envChildSession                = "CLAUDE_CODE_CHILD_SESSION"
	envProjectDir                  = "CLAUDE_PROJECT_DIR"
	envCache1H                     = "ENABLE_PROMPT_CACHING_1H"
	envCache5M                     = "FORCE_PROMPT_CACHING_5M"
	envPromptCacheTTL              = "CLAUDE_CODE_PROMPT_CACHE_TTL"
	envSubagentPromptCacheTTL      = "CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL"
	envCacheLiveControlMainTTL     = "CACHE_LIVE_CONTROL_MAIN_TTL"
	envCacheLiveControlAgentsTTL   = "CACHE_LIVE_CONTROL_AGENTS_TTL"
	envSimplePrompt                = "CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"
	envAnthropicBaseURL            = "ANTHROPIC_BASE_URL"
	envAnthropicAuthToken          = "ANTHROPIC_AUTH_TOKEN"
	envAnthropicAPIKey             = "ANTHROPIC_API_KEY"
	envAnthropicModel              = "ANTHROPIC_MODEL"
	envAnthropicSmallFastModel     = "ANTHROPIC_SMALL_FAST_MODEL"
	envAutoCompactWindow           = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"
	envDisableNonessentialTraffic  = "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"
	envDisableNonstreamingFallback = "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK"
	envGatewayDiscovery            = "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"
	envCodexThreadID               = "CODEX_THREAD_ID"
	envNativeCursor                = "CLAUDE_CODE_NATIVE_CURSOR"
	envSpawnDepth                  = "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH"
	envConcurrentSubagents         = "CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS"
	envWebSearches                 = "CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION"
	envTmuxTruecolor               = "CLAUDE_CODE_TMUX_TRUECOLOR"
	envAgentTeams                  = "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"
	envFunctionHooks               = "CLAUDE_CODE_ENABLE_FUNCTION_HOOKS"
	flagSettings                   = "--settings"
	flagMCPConfig                  = "--mcp-config"
	flagModel                      = "--model"
	flagEffort                     = "--effort"
	flagSessionID                  = "--session-id"
	flagResume                     = "--resume"
	flagName                       = "--name"
	flagNameShort                  = "-n"
	flagPromptFile                 = "--system-prompt-file"
	flagForkSession                = "--fork-session"
	flagAllowBypass                = "--allow-dangerously-skip-permissions"
	flagBypass                     = "--dangerously-skip-permissions"
)

const (
	SettingsFlag  = flagSettings
	MCPConfigFlag = flagMCPConfig
)

type Wire int

const (
	WireSettings Wire = iota
	WireEnv
	WireUnset
	WireFlag
)

func (wire Wire) String() string {
	switch wire {
	case WireSettings:
		return "settings"
	case WireEnv:
		return "env"
	case WireUnset:
		return unsetWord
	case WireFlag:
		return "flag"
	default:
		return "unknown"
	}
}

type Source int

const (
	SourceConfig Source = iota
	SourceLaunchThenConfig
	SourceLaunch
	SourceConstant
	SourceAccount
	SourceDoor
	SourceMachineConfig
	SourceHost
)

type Knob struct {
	Name    string
	Wire    Wire
	Target  string
	Source  Source
	Default any
	Reason  string
}

var hygiene = []string{
	envSessionID, envClaudeCode, envChildSession, configDirEnv, ConfigDirDefaultEnv,
	envProjectDir, envCache1H, envCache5M, envPromptCacheTTL, envSubagentPromptCacheTTL,
	envCacheLiveControlMainTTL, envCacheLiveControlAgentsTTL,
	envSimplePrompt, envAnthropicBaseURL, envAnthropicAuthToken,
	envAnthropicAPIKey, envAnthropicModel, envAnthropicSmallFastModel,
	envAutoCompactWindow, envDisableNonessentialTraffic,
	envDisableNonstreamingFallback, envGatewayDiscovery,
	envCodexThreadID,
}

// Knobs is the ordered inventory of values carried into a Claude launch.
var Knobs = func() []Knob {
	rows := []Knob{
		{
			knobConfigDir,
			WireEnv,
			configDirEnv,
			SourceAccount,
			nil,
			"Choose the account store before Claude reads settings.",
		},
	}
	for _, name := range hygiene {
		rows = append(rows, Knob{name, WireUnset, name, SourceConstant, nil, "Discard inherited launch state."})
	}
	return append(
		rows,
		Knob{
			knobBinary, WireFlag, knobBinary, SourceConfig,
			pfmengine.MustLookup(pfmengine.Claude).Binary,
			"Run the selected Claude executable.",
		},
		Knob{
			knobCache1H,
			WireEnv,
			envCacheLiveControlMainTTL,
			SourceLaunchThenConfig,
			true,
			"Hand the main chat's starting prompt-cache TTL to the cache-live-control plugin in the process environment; the plugin owns every TTL from then on.",
		},
		Knob{
			knobShell,
			WireEnv,
			envShell,
			SourceHost,
			shellBash,
			"Run the Bash tool under bash, not the login shell; a usable inherited value is kept.",
		},
		Knob{
			knobSystemPrompt,
			WireFlag,
			flagPromptFile + "|env." + envSimplePrompt,
			SourceConfig,
			productionMode,
			"Choose the managed system prompt.",
		},
		Knob{
			knobNativeCursor,
			WireSettings,
			"env." + envNativeCursor,
			SourceConfig,
			false,
			"Enable Claude's native cursor when configured.",
		},
		Knob{
			knobMaxSubagentSpawnDepth,
			WireSettings,
			"env." + envSpawnDepth,
			SourceConfig,
			8,
			"Lift the agent nesting limit.",
		},
		Knob{
			knobMaxConcurrentSubagents,
			WireSettings,
			"env." + envConcurrentSubagents,
			SourceConfig,
			nil,
			"Set the agent concurrency limit when configured.",
		},
		Knob{
			knobWebSearchesPerSession,
			WireSettings,
			"env." + envWebSearches,
			SourceConfig,
			int64(9007199254740991),
			"Set the web search ceiling.",
		},
		Knob{
			knobAutoCompactWindow,
			WireSettings,
			"env." + envAutoCompactWindow,
			SourceConfig,
			int64(100000),
			"Set the auto-compact window the compaction plugin reads.",
		},
		Knob{
			knobTmuxTruecolor,
			WireSettings,
			"env." + envTmuxTruecolor,
			SourceConfig,
			true,
			"Preserve color under tmux.",
		},
		Knob{
			knobNoFlicker,
			WireSettings,
			"env." + envNoFlicker,
			SourceAccount,
			nil,
			"Keep a fullscreen seat's renderer past Claude's boot canary.",
		},
		Knob{
			"agentTeams",
			WireSettings,
			"env." + envAgentTeams,
			SourceConstant,
			"0",
			"Keep experimental agent teams off.",
		},
		Knob{
			"functionHooks",
			WireSettings,
			"env." + envFunctionHooks,
			SourceConstant,
			"1",
			"Enable function hooks for pfm's Claude plugins.",
		},
		Knob{
			knobOutputStyle,
			WireSettings,
			knobOutputStyle,
			SourceConstant,
			defaultWord,
			"Use the managed output style.",
		},
		Knob{knobTheme, WireSettings, knobTheme, SourceConfig, nil, "Carry the selected Claude theme."},
		Knob{
			knobCleanupPeriodDays,
			WireSettings,
			knobCleanupPeriodDays,
			SourceConfig,
			36500,
			"Keep managed transcripts.",
		},
		Knob{knobHooks, WireSettings, knobHooks, SourceConstant, "11 registrations", "Attach the fleet hook set."},
		Knob{knobStatusLine, WireSettings, knobStatusLine, SourceConstant, "pfm-statusline", "Show fleet status."},
		Knob{
			knobSubagentStatusLine,
			WireSettings,
			knobSubagentStatusLine,
			SourceConstant,
			"pfm-statusline --subagents",
			"Show agent status.",
		},
		Knob{knobMCP, WireFlag, flagMCPConfig, SourceMachineConfig, nil, "Attach enabled fleet MCP servers."},
		Knob{
			knobPermissionMode,
			WireFlag,
			flagAllowBypass + "|" + flagBypass,
			SourceConfig,
			"bypass",
			"Apply the configured autonomy mode.",
		},
		Knob{knobModel, WireFlag, flagModel, SourceLaunch, nil, "Choose a model for this launch."},
		Knob{knobEffort, WireFlag, flagEffort, SourceLaunch, nil, "Choose effort for this launch."},
		Knob{knobSessionID, WireFlag, flagSessionID, SourceDoor, nil, "Identify a new session before exec."},
		Knob{knobResume, WireFlag, flagResume, SourceDoor, nil, "Resume a named session."},
		Knob{knobFork, WireFlag, flagForkSession, SourceDoor, false, "Fork the resumed session."},
		Knob{knobName, WireFlag, flagName, SourceDoor, nil, "Name the session."},
	)
}()

func Hygiene() []string { return append([]string(nil), hygiene...) }

func IdentityHygiene() []string {
	return []string{
		envSessionID,
		envClaudeCode,
		envChildSession,
		configDirEnv,
		ConfigDirDefaultEnv,
		envCodexThreadID,
	}
}
