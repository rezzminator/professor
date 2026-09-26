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
		{"claude.tmuxTruecolor", true},
		{"claude.cleanupPeriodDays", 36500},
		{"claude.requireManagedCleanup", true},
		{"claude.compactNudge.enabled", true},
		{"claude.compactNudge.start", 35},
		{"claude.compactNudge.step", 10},
		{"claude.maxSubagentSpawnDepth", DefaultSubagentSpawnDepth},
		{"claude.maxConcurrentSubagents", nil},
		{"codex.yolo", true},
		{"codex.binary", pfmengine.MustLookup(pfmengine.Codex).Binary},
		{"opencode.binary", pfmengine.MustLookup(pfmengine.OpenCode).Binary},
		{"tmux.titles.enabled", true},
		{"nameSync.interval", DefaultNameSyncInterval.String()},
		{"mcp.servers.chat.enabled", false},
		{"mcp.servers.harvester.enabled", false},
		{"mcp.http.port", DefaultMCPPort},
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
	}
}
