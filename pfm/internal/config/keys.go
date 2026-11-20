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

// KeyClaudePluginCheckoutRoot names the directory holding one checkout per
// Claude plugin pfm install ensures ({root}/{plugin}/plugins/{plugin}): an
// -alpha build installs each plugin from a copy of its checkout there. Unset,
// that directory is the source clone's plugins/.
const KeyClaudePluginCheckoutRoot = "claude.pluginCheckoutRoot"

// KeyClaudePlugins names the list of Claude Code plugins pfm install ensures
// once, through the primary account, in the store every account links: each
// entry a bare plugin name of ours, a full name@marketplace id, or an
// {"id","source"} object whose source `claude plugin marketplace add` takes.
// Unset, it is DefaultClaudePlugins; machine-wide, an account may not set it.
const KeyClaudePlugins = "claude.plugins"

// KeyClaudeEnv names the machine-wide map of environment variables pfm
// install writes into the primary seat's settings.json env: each name upper
// snake, each value a string. pfm owns only the names it wrote.
const KeyClaudeEnv = "claude.env"

// KeyClaudeCostLedger is the machine-wide switch of the per-call cost ledger:
// false launches Claude without the OTel knobs and starts no cost sink.
const KeyClaudeCostLedger = "claude.costLedger"

// KeyClaudeGptAgents is the machine-wide switch declaring the engine-sync capability gpt-agents.
const KeyClaudeGptAgents = "claude.gptAgents"

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
		{"chat.backend", DefaultChatBackend},
		{"chat.herdrTabStatus", DefaultHerdrTabStatus},
		{keyStateDB, "~/.local/state/pfm/pfm.db"},
		{keyStateCacheDB, "~/.local/state/pfm/pfm-cache.db"},
		{"claude.permissionMode", PermissionBypass},
		{"claude.binary", pfmengine.MustLookup(pfmengine.Claude).Binary},
		{"claude.theme", nil},
		{"claude.cache1h", true},
		{"claude.remoteControl", true},
		{"claude.systemPrompt", SystemPromptProfessor},
		{"claude.requireManagedCleanup", true},
		{KeyClaudePluginCheckoutRoot, nil},
		{KeyClaudePlugins, defaultClaudePluginIDs()},
		{KeyClaudeEnv, map[string]any{}},
		{KeyClaudeCostLedger, true},
		{KeyClaudeGptAgents, false},
		// claude.settings defaults to the shipped launch defaults (claudelaunch's defaults.json), whose
		// test holds the example's value to them.
		{KeyClaudeSettings, nil},
		{KeyEnvs, map[string]any{}},
		{KeyHarnessPrompt, nil},
		{"codex.yolo", true},
		{"codex.binary", pfmengine.MustLookup(pfmengine.Codex).Binary},
		{"opencode.binary", pfmengine.MustLookup(pfmengine.OpenCode).Binary},
		{"tmux.titles.enabled", true},
		{"nameSync.interval", DefaultNameSyncInterval.String()},
		{"mcp.servers.chat.enabled", false},
		{"mcp.http.port", DefaultMCPPort},
		{"mcp.thirdParty", map[string]any{}},
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
		{keyDoctorIgnoreWarnings, []string{}},
		{keyDoctorAcceptedMCP, []AcceptedMCP{}},
	}
}
