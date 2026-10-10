package config

import pfmengine "github.com/rezzminator/professor/pfm/internal/engine"

// The machine-config keys and default the key inventory shares with Load.
const (
	keyVersion          = "version"
	keyTheme            = "theme"
	keyStateDB          = "state.db"
	keyStateCacheDB     = "state.cacheDb"
	defaultAskLunaModel = "gpt-5.6-luna"
)

// DefaultHelperModel is the model Claude Code's helper calls run on when
// claude.helperModel is unset: a full model id, so it names Haiku 5.5 whatever
// the gateway's alias table says.
const DefaultHelperModel = "claude-haiku-5-5"

// KeyClaudePluginCheckoutRoot names the directory holding one checkout per
// Claude plugin pfm install ensures ({root}/{plugin}/plugins/{plugin}): an
// -alpha build installs each plugin from a copy of its checkout there.
const KeyClaudePluginCheckoutRoot = "claude.pluginCheckoutRoot"

// KeyClaudeCostLedger is the machine-wide switch of the per-call cost ledger:
// false launches Claude without the OTel knobs and starts no cost sink.
const KeyClaudeCostLedger = "claude.costLedger"

// KeyDefault describes a machine-config leaf and the value shown by the
// tracked example when the key is unset. Roster arrays are intentionally
// absent: accounts are discovered from the local machine.
type KeyDefault struct {
	Key     string
	Default any
}

// Keys is the ordered inventory of machine-config leaves outside rosters.
func Keys() []KeyDefault {
	return []KeyDefault{
		{keyVersion, Version},
		{keyTheme, "default"},
		{keyStateDB, "~/.local/state/pfm/pfm.db"},
		{keyStateCacheDB, "~/.local/state/pfm/pfm-cache.db"},
		{"claude.permissionMode", PermissionBypass},
		{"claude.binary", pfmengine.MustLookup(pfmengine.Claude).Binary},
		{"claude.theme", nil},
		{"claude.cache1h", true},
		{"claude.nativeCursor", false},
		{"claude.systemPrompt", nil},
		{"claude.webSearchesPerSession", int64(9007199254740991)},
		{"claude.autoCompactWindow", int64(100000)},
		{"claude.helperModel", DefaultHelperModel},
		{"claude.tmuxTruecolor", true},
		{"claude.cleanupPeriodDays", 36500},
		{"claude.requireManagedCleanup", true},
		{"claude.maxSubagentSpawnDepth", DefaultSubagentSpawnDepth},
		{"claude.maxConcurrentSubagents", nil},
		{KeyClaudePluginCheckoutRoot, nil},
		{KeyClaudeCostLedger, true},
		{"codex.yolo", true},
		{"codex.binary", pfmengine.MustLookup(pfmengine.Codex).Binary},
		{"opencode.binary", pfmengine.MustLookup(pfmengine.OpenCode).Binary},
		{"tmux.titles.enabled", true},
		{"nameSync.interval", DefaultNameSyncInterval.String()},
		{"mcp.servers.chat.enabled", false},
		{"mcp.http.port", DefaultMCPPort},
		{"mcp.thirdParty", map[string]any{}},
		{"mcp.authToken", nil},
		{"ask.engine", pfmengine.MustLookup(pfmengine.Codex).LongName},
		{"ask.claude.model", "claude-haiku-4-5"},
		{"ask.claude.effort", defaultAskEffort},
		{"ask.codex.model", defaultAskLunaModel},
		{"ask.codex.effort", defaultAskEffort},
		{"ask.opencode.model", defaultAskLunaModel},
		{"ask.opencode.effort", defaultAskEffort},
		{"log.level", nil},
		{"log.components", map[string]string{}},
		{"log.keepFiles", DefaultLog().KeepFiles},
		{"log.maxMB", DefaultLog().MaxMB},
		{"log.keepDays", DefaultLog().KeepDays},
		{keyDoctorIgnoreWarnings, []string{}},
		{keyDoctorAcceptedMCP, []AcceptedMCP{}},
	}
}
