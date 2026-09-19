// Package config owns pfm's versioned machine configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"hostops/pfm/internal/atomicfile"
	pfmengine "hostops/pfm/internal/engine"
)

const Version = 2

const (
	jsonKeyEnabled  = "enabled"
	engineKeyBinary = "binary"
	engineKeyYolo   = "yolo"
	jsonKeyPort     = "port"
	// defaultAskEffort is the reasoning effort every engine's ask defaults
	// carry until an operator sets one.
	defaultAskEffort = "low"
)

type Source string

const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
)

const (
	PermissionBypass = "bypass"
	PermissionPrompt = "prompted"

	// claude.systemPrompt values: which system prompt every managed Claude
	// launch carries. Production leaves the CLI's own prompt untouched; lean
	// selects its built-in minimal prompt (CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1);
	// professor injects the staged professor prompt via --system-prompt-file.
	SystemPromptProduction = "production"
	SystemPromptLean       = "lean"
	SystemPromptProfessor  = "professor"
)

// Account is one Claude account. ProjectDir is the transcript root used by
// fleet discovery; ConfigDir is the directory handed to Claude at launch.
// Implicit is true only for the default account whose current launch shape
// deliberately leaves CLAUDE_CONFIG_DIR unset.
type Account struct {
	ID         int
	ConfigDir  string
	ProjectDir string
	Implicit   bool
	Emoji      string
	Claude     *ClaudePrefs
	Codex      *CodexPrefs
}

// AccountSkip is a numeric ~/.cc directory discovery inspected and rejected
// as an account. Keeping skips in the materialized runtime config lets visible
// consumers explain why a directory was omitted without fabricating an
// account or retrying a known absence forever.
type AccountSkip struct {
	ID        int
	ConfigDir string
	Reason    string
}

type ClaudePrefs struct {
	PermissionMode string
	Binary, Theme  string
	// SystemPrompt is one of the SystemPrompt* values; empty means
	// SystemPromptProduction.
	SystemPrompt string
	// Cache1H is Claude Code's prompt-cache TTL choice: true selects the
	// ~32%-cheaper 1-hour TTL (ENABLE_PROMPT_CACHING_1H), false the 5-minute
	// TTL. Defaults true — see decodeClaudePrefs and defaultsWithMCPServers.
	Cache1H      bool
	NativeCursor bool
	// CompactNudge governs the UserPromptSubmit reminder that a self-compact
	// is due at a context milestone — see decodeClaudePrefs for the defaults.
	CompactNudge CompactNudge
}

// CompactNudge is the milestone reminder's policy: whether the hook speaks at
// all, the context percentage it first speaks at, and how many points of
// context pass between reminders. A reminder, never an order — the hook only
// says the milestone is here.
type CompactNudge struct {
	Enabled bool
	Start   int
	Step    int
}

// DefaultCompactNudge is the fleet's milestone policy when the file says
// nothing: on, first at 35% of context, then every 10 points.
func DefaultCompactNudge() CompactNudge {
	return CompactNudge{Enabled: true, Start: 35, Step: 10}
}

// applyCompactNudge overlays the fields a file actually set onto base — the
// resolved top-level policy for an account, the default for the top level —
// so an account that touched only step keeps the file's enabled and start.
func applyCompactNudge(base CompactNudge, raw *rawCompactNudge, path, scope string, index int) (CompactNudge, error) {
	if raw == nil {
		return base, nil
	}
	result := base
	if raw.Enabled != nil {
		result.Enabled = *raw.Enabled
	}
	if raw.Start != nil {
		if *raw.Start < 1 || *raw.Start > 100 {
			return CompactNudge{}, fmt.Errorf(
				"config %s: %s.compactNudge.start must be 1..100 (a context percentage), got %d",
				path,
				configScope(scope, index),
				*raw.Start,
			)
		}
		result.Start = *raw.Start
	}
	if raw.Step != nil {
		if *raw.Step < 1 || *raw.Step > 100 {
			return CompactNudge{}, fmt.Errorf(
				"config %s: %s.compactNudge.step must be 1..100 (context points between reminders), got %d",
				path,
				configScope(scope, index),
				*raw.Step,
			)
		}
		result.Step = *raw.Step
	}
	return result, nil
}

func recordCompactNudgeSources(sources map[string]Source, prefix string, raw *rawCompactNudge) {
	if raw == nil {
		return
	}
	if raw.Enabled != nil {
		sources[prefix+".compactNudge.enabled"] = SourceFile
	}
	if raw.Start != nil {
		sources[prefix+".compactNudge.start"] = SourceFile
	}
	if raw.Step != nil {
		sources[prefix+".compactNudge.step"] = SourceFile
	}
}

// NameSync is the window-name convergence schedule. Interval is rendered into
// BOTH schedulers at install time — the launchd job's StartInterval and the
// systemd timer's OnUnitInactiveSec — from this ONE value, so the two can
// never drift apart on a host that runs either.
type NameSync struct {
	Interval time.Duration
}

// DefaultNameSyncInterval is the poll the fleet shipped with: a claude
// /rename's backstop between picker runs.
const DefaultNameSyncInterval = 15 * time.Minute

// MinNameSyncInterval floors the poll. name-sync gathers the whole fleet and
// captures every claude pane; below a minute the poll costs more than the
// drift it converges.
const MinNameSyncInterval = time.Minute

// DefaultNameSync is the schedule when the file says nothing.
func DefaultNameSync() NameSync {
	return NameSync{Interval: DefaultNameSyncInterval}
}

// Claude is retained as an alias for callers that used the v1 public shape.
type Claude = ClaudePrefs

type CodexPrefs struct {
	Yolo   bool
	Binary string
}

// Codex is retained as an alias for callers that used the v1 public shape.
type Codex = CodexPrefs

// CodexAccount is one independently authenticated Codex home. Prefs overrides
// the top-level Codex posture for this account only.
type CodexAccount struct {
	ID    int
	Home  string
	Emoji string
	Prefs *CodexPrefs
}

// EngineCounts is the materialized engine roster summary every surface uses.
// OpenCodePrefs is OpenCode's launch posture. There is one fleet-wide
// OpenCode home (its data lives under ~/.local/share/opencode), so unlike
// Codex there are no per-account overrides to resolve.
type OpenCodePrefs struct {
	Binary string
}

// OpenCode is retained as an alias for callers that used the public shape.
type OpenCode = OpenCodePrefs

// OpenCodeAccount is the implicit single OpenCode seat. It exists only when
// OpenCode's session store is present at load time.
type OpenCodeAccount struct {
	ID   int
	Home string
}

type EngineCounts map[pfmengine.ID]int

type MCPServer struct {
	Enabled bool
}

type MCPHTTP struct {
	Port int
}

type MCPConfig struct {
	Servers map[string]MCPServer
	HTTP    MCPHTTP
}

type EnginePrefs struct {
	Model  string
	Effort string
}

type AskConfig struct {
	Engine pfmengine.ID
	Prefs  map[pfmengine.ID]EnginePrefs
}

func (config AskConfig) PrefsFor(id pfmengine.ID) EnginePrefs {
	return config.Prefs[id]
}

// Config is the fully materialized configuration. Sources records whether
// each effective value came from the machine file or from a default.
type Config struct {
	Version          int
	InputVersion     int
	Theme            string
	Accounts         []Account
	AccountSkips     []AccountSkip
	CodexAccounts    []CodexAccount
	OpenCodeAccounts []OpenCodeAccount
	Claude           Claude
	Codex            Codex
	OpenCode         OpenCode
	Tmux             Tmux
	NameSync         NameSync
	Log              Log
	MCPServers       map[string]MCPServer
	MCP              MCPConfig
	Ask              AskConfig
	// Harvester is harvester.config.json, loaded beside Path. Its Enabled is
	// mirrored into MCPServers["harvester"] for server-generic consumers.
	Harvester HarvesterConfig

	Path    string
	Exists  bool
	Sources map[string]Source
}

func productionMCPServers() map[string]MCPServer {
	return map[string]MCPServer{
		"chat":             {Enabled: false},
		MCPServerHarvester: {Enabled: false},
	}
}

type rawConfig struct {
	Version  *int          `json:"version"`
	Theme    *string       `json:"theme,omitempty"`
	Accounts *[]rawAccount `json:"accounts,omitempty"`
	Claude   *rawClaude    `json:"claude,omitempty"`
	Codex    *rawCodex     `json:"codex,omitempty"`
	OpenCode *rawOpenCode  `json:"opencode,omitempty"`
	Tmux     *rawTmux      `json:"tmux,omitempty"`
	NameSync *rawNameSync  `json:"nameSync,omitempty"`
	MCP      *rawMCP       `json:"mcp,omitempty"`
	Log      *rawLog       `json:"log,omitempty"`
	Ask      *rawAsk       `json:"ask,omitempty"`
}

type rawTmux struct {
	Titles *rawTmuxTitles `json:"titles,omitempty"`
}

type rawTmuxTitles struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type rawNameSync struct {
	Interval *string `json:"interval,omitempty"`
}

type rawAccount struct {
	ID        int        `json:"id"`
	ConfigDir string     `json:"configDir"`
	Emoji     string     `json:"emoji,omitempty"`
	Claude    *rawClaude `json:"claude,omitempty"`
	Codex     *rawCodex  `json:"codex,omitempty"`
}

type rawClaude struct {
	PermissionMode *string          `json:"permissionMode,omitempty"`
	Binary         *string          `json:"binary,omitempty"`
	Theme          *string          `json:"theme,omitempty"`
	Cache1H        *bool            `json:"cache1h,omitempty"`
	NativeCursor   *bool            `json:"nativeCursor,omitempty"`
	SystemPrompt   *string          `json:"systemPrompt,omitempty"`
	CompactNudge   *rawCompactNudge `json:"compactNudge,omitempty"`
}

type rawCompactNudge struct {
	Enabled *bool `json:"enabled,omitempty"`
	Start   *int  `json:"start,omitempty"`
	Step    *int  `json:"step,omitempty"`
}

type rawOpenCode struct {
	Binary *string `json:"binary,omitempty"`
}

type rawCodex struct {
	Yolo   *bool           `json:"yolo,omitempty"`
	Binary *string         `json:"binary,omitempty"`
	Homes  *[]rawCodexHome `json:"homes,omitempty"`
}

type rawCodexHome struct {
	ID    int            `json:"id"`
	Home  string         `json:"home"`
	Emoji string         `json:"emoji,omitempty"`
	Prefs *rawCodexPrefs `json:"prefs,omitempty"`
}

type rawCodexPrefs struct {
	Yolo   *bool   `json:"yolo,omitempty"`
	Binary *string `json:"binary,omitempty"`
}

type rawMCP struct {
	Servers   map[string]rawMCPServer `json:"servers,omitempty"`
	HTTP      *rawMCPHTTP             `json:"http,omitempty"`
	AuthToken *string                 `json:"authToken,omitempty"`
}

type rawMCPServer struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type rawMCPHTTP struct {
	Port int `json:"port"`
}

type rawAsk struct {
	Engine *string
	Prefs  map[pfmengine.ID]rawEngine
}

type rawEngine struct {
	Model  *string `json:"model,omitempty"`
	Effort *string `json:"effort,omitempty"`
}

func (raw *rawAsk) UnmarshalJSON(content []byte) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(content, &values); err != nil {
		return err
	}
	raw.Prefs = make(map[pfmengine.ID]rawEngine)
	for key, value := range values {
		if key == "engine" {
			var selected string
			if err := json.Unmarshal(value, &selected); err != nil {
				return fmt.Errorf("ask.engine: %w", err)
			}
			raw.Engine = &selected
			continue
		}
		id, err := pfmengine.Parse(key)
		if err != nil {
			return fmt.Errorf("ask.%s: %w", key, err)
		}
		var prefs rawEngine
		if err := decodeStrict(value, &prefs); err != nil {
			return fmt.Errorf("ask.%s: %w", key, err)
		}
		raw.Prefs[id] = prefs
	}
	return nil
}

// resolveExistingPath is ResolvePath, except that a machine which still has
// only the pre-split config.json reads that file until `pfm install`
// migrates it — every command keeps working across the binary upgrade.
func resolveExistingPath(home string) string {
	current := ResolvePath(home)
	if _, err := os.Stat(current); err == nil {
		return current
	}
	legacy := filepath.Join(filepath.Dir(current), LegacyFileName)
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return current
}

// Defaults returns today's effective behavior over the supplied discovery
// roots. The roots are preserved byte-for-byte as discovery inputs; only the
// corresponding launch directory is derived.
func Defaults(home string, projectRoots []string, codexHomes ...string) Config {
	return defaultsWithMCPServers(home, projectRoots, productionMCPServers(), codexHomes...)
}

func engineConfigKey(id pfmengine.ID, field string) string {
	return pfmengine.MustLookup(id).LongName + "." + field
}

func defaultsWithMCPServers(
	home string,
	projectRoots []string,
	registered map[string]MCPServer,
	codexHomes ...string,
) Config {
	accounts := make([]Account, 0, len(projectRoots))
	var accountSkips []AccountSkip
	for index, projectRoot := range projectRoots {
		configDir := filepath.Dir(projectRoot)
		accounts = append(accounts, Account{
			ID:         index + 1,
			ConfigDir:  filepath.Clean(configDir),
			ProjectDir: filepath.Clean(projectRoot),
			Implicit:   index == 0,
			Emoji:      DefaultEmoji(index + 1),
		})
	}
	if len(accounts) == 0 {
		accounts, accountSkips = discoverAccounts(home)
	}
	codexHome := pfmengine.MustLookup(pfmengine.Codex).DefaultRoots(home)[0]
	if len(codexHomes) != 0 && strings.TrimSpace(codexHomes[0]) != "" {
		codexHome = filepath.Clean(codexHomes[0])
	}
	var codexAccounts []CodexAccount
	codexValid, codexErr := hasValidCodexCredentials(codexHome)
	if codexErr != nil {
		accountSkips = append(accountSkips, AccountSkip{
			ConfigDir: codexHome, Reason: fmt.Sprintf("codex discovery failed: %v", codexErr),
		})
	} else if codexValid {
		codexAccounts = []CodexAccount{{ID: 1, Home: codexHome, Emoji: DefaultEmoji(1)}}
	}
	var openCodeAccounts []OpenCodeAccount
	openCodeHome := openCodeAccountHome(home)
	if exists, openCodeErr := openCodeStoreExists(openCodeHome); openCodeErr != nil {
		accountSkips = append(accountSkips, AccountSkip{
			ConfigDir: openCodeHome,
			Reason:    fmt.Sprintf("OpenCode discovery failed: %v", openCodeErr),
		})
	} else if exists {
		openCodeAccounts = []OpenCodeAccount{{ID: 1, Home: openCodeHome}}
	}
	sources := map[string]Source{
		"version":  SourceDefault,
		"theme":    SourceDefault,
		"accounts": SourceDefault,
		engineConfigKey(pfmengine.Claude, "permissionMode"):  SourceDefault,
		engineConfigKey(pfmengine.Claude, engineKeyBinary):   SourceDefault,
		engineConfigKey(pfmengine.Claude, "theme"):           SourceDefault,
		engineConfigKey(pfmengine.Claude, "cache1h"):         SourceDefault,
		engineConfigKey(pfmengine.Claude, "nativeCursor"):    SourceDefault,
		engineConfigKey(pfmengine.Codex, engineKeyYolo):      SourceDefault,
		engineConfigKey(pfmengine.Codex, engineKeyBinary):    SourceDefault,
		engineConfigKey(pfmengine.Codex, "homes"):            SourceDefault,
		engineConfigKey(pfmengine.OpenCode, engineKeyBinary): SourceDefault,
		"mcp.http.port":       SourceDefault,
		"ask.engine":          SourceDefault,
		"tmux.titles.enabled": SourceDefault,
		"nameSync.interval":   SourceDefault,
	}
	for _, id := range pfmengine.All() {
		name := pfmengine.MustLookup(id).LongName
		sources["ask."+name+".model"] = SourceDefault
		sources["ask."+name+".effort"] = SourceDefault
	}
	servers := make(map[string]MCPServer, len(registered))
	for name, server := range registered {
		servers[name] = server
		if name != MCPServerHarvester {
			sources["mcp.servers."+name+".enabled"] = SourceDefault
		}
	}
	harvester := DefaultHarvester()
	if server, found := registered[MCPServerHarvester]; found {
		harvester.Enabled = server.Enabled
	}
	for _, key := range harvesterSourceKeys {
		sources[key] = SourceDefault
	}
	return Config{
		Version:          Version,
		InputVersion:     Version,
		Theme:            "default",
		Accounts:         accounts,
		AccountSkips:     accountSkips,
		CodexAccounts:    codexAccounts,
		OpenCodeAccounts: openCodeAccounts,
		Claude: Claude{
			PermissionMode: PermissionBypass,
			Binary:         pfmengine.MustLookup(pfmengine.Claude).Binary,
			Cache1H:        true,
			CompactNudge:   DefaultCompactNudge(),
		},
		Codex:      Codex{Yolo: true, Binary: pfmengine.MustLookup(pfmengine.Codex).Binary},
		OpenCode:   OpenCode{Binary: pfmengine.MustLookup(pfmengine.OpenCode).Binary},
		Tmux:       Tmux{Titles: DefaultTmuxTitles()},
		NameSync:   DefaultNameSync(),
		Log:        DefaultLog(),
		MCPServers: servers,
		MCP:        MCPConfig{Servers: cloneMCPServers(servers), HTTP: MCPHTTP{Port: DefaultMCPPort}},
		Harvester:  harvester,
		Ask: AskConfig{
			Engine: pfmengine.Codex,
			Prefs: map[pfmengine.ID]EnginePrefs{
				pfmengine.Codex:    {Model: "gpt-5.6-luna", Effort: defaultAskEffort},
				pfmengine.OpenCode: {Model: "gpt-5.6-luna", Effort: defaultAskEffort},
				pfmengine.Claude:   {Model: "claude-haiku-4-5", Effort: defaultAskEffort},
			},
		},
		Sources: sources,
	}
}

func cloneMCPServers(values map[string]MCPServer) map[string]MCPServer {
	cloned := make(map[string]MCPServer, len(values))
	for name, server := range values {
		cloned[name] = server
	}
	return cloned
}

// DefaultEmoji is the config-owned conventional badge for an account id.
// Consumers with an explicit roster must still fail closed when an id is
// absent from that roster instead of treating this convention as discovery.
func DefaultEmoji(id int) string {
	switch id {
	case 1:
		return "🥇"
	case 2:
		return "🥈"
	case 3:
		return "🥉"
	case 4:
		return "🍀"
	default:
		return "·"
	}
}

// DefaultAccountDir is the config-owned conventional directory for a numeric
// account. Consumers must not reconstruct this filesystem policy.
func DefaultAccountDir(home string, id int) string {
	return filepath.Join(home, ".cc", strconv.Itoa(id))
}

// DefaultAccountProjectDir is the discovery root beneath DefaultAccountDir.
func DefaultAccountProjectDir(home string, id int) string {
	return filepath.Join(DefaultAccountDir(home, id), "projects")
}

// DisplayAccountDir names a conventional account without exposing a full home
// path, while preserving custom configured directories verbatim.
func DisplayAccountDir(home string, id int, configDir string) string {
	clean := filepath.Clean(configDir)
	if clean == filepath.Clean(DefaultAccountDir(home, id)) {
		return filepath.Join(".cc", strconv.Itoa(id))
	}
	if clean == filepath.Join(filepath.Clean(home), ".cc") {
		return ".cc"
	}
	return fmt.Sprintf("account %d", id)
}

func discoverAccounts(home string) ([]Account, []AccountSkip) {
	root := filepath.Join(home, ".cc")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []AccountSkip{{ConfigDir: root, Reason: fmt.Sprintf("discovery failed: %v", err)}}
	}
	type candidate struct {
		id   int
		path string
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		id, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || id < 1 {
			continue
		}
		configDir := filepath.Join(root, entry.Name())
		candidates = append(candidates, candidate{id: id, path: configDir})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })
	accounts := make([]Account, 0, len(candidates))
	skips := make([]AccountSkip, 0)
	for _, candidate := range candidates {
		if !hasValidAccountCredentials(candidate.path) {
			skips = append(skips, AccountSkip{
				ID: candidate.id, ConfigDir: candidate.path, Reason: "no valid credentials",
			})
			continue
		}
		accounts = append(accounts, Account{
			ID: candidate.id, ConfigDir: candidate.path,
			ProjectDir: filepath.Join(candidate.path, "projects"),
			Implicit:   candidate.id == 1, Emoji: DefaultEmoji(candidate.id),
		})
	}
	return accounts, skips
}

func hasValidAccountCredentials(configDir string) bool {
	body, err := os.ReadFile(filepath.Join(configDir, ".credentials.json"))
	if err != nil {
		return false
	}
	var marker struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	return json.Unmarshal(body, &marker) == nil && strings.TrimSpace(marker.OAuth.AccessToken) != ""
}

func skipsOutsideDir(skips []AccountSkip, root string) []AccountSkip {
	filtered := make([]AccountSkip, 0, len(skips))
	for _, skip := range skips {
		relative, err := filepath.Rel(root, skip.ConfigDir)
		inside := err == nil && relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(os.PathSeparator))
		if !inside {
			filtered = append(filtered, skip)
		}
	}
	return filtered
}

// Load reads a machine config over defaults. An absent file returns defaults;
// every present-file error is returned with the file path attached.
func Load(path, home string, projectRoots []string, codexHomes ...string) (Config, error) {
	return loadWithMCPServers(path, home, projectRoots, productionMCPServers(), codexHomes...)
}

func loadWithMCPServers(
	path, home string,
	projectRoots []string,
	registered map[string]MCPServer,
	codexHomes ...string,
) (Config, error) {
	openCodeHome := openCodeAccountHome(home)
	if _, err := openCodeStoreExists(openCodeHome); err != nil {
		return Config{}, fmt.Errorf("discover OpenCode account data %s: %w", openCodeHome, err)
	}
	result := defaultsWithMCPServers(home, projectRoots, registered, codexHomes...)
	if path == "" {
		path = resolveExistingPath(home)
	} else if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return Config{}, fmt.Errorf("resolve config path %q: %w", path, err)
		}
		path = absolute
	}
	result.Path = filepath.Clean(path)

	content, err := os.ReadFile(result.Path)
	if errors.Is(err, os.ErrNotExist) {
		if err := finishHarvester(&result, home, registered, nil); err != nil {
			return Config{}, err
		}
		return result, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", result.Path, err)
	}
	result.Exists = true

	var raw rawConfig
	if err := decodeStrict(content, &raw); err != nil {
		return Config{}, configJSONError(result.Path, err, int64(len(content)))
	}
	if raw.Version == nil {
		return Config{}, fmt.Errorf("config %s: required key %q is missing", result.Path, "version")
	}
	if *raw.Version != 1 && *raw.Version != Version {
		return Config{}, fmt.Errorf("config %s: version must be 1 or %d, got %d", result.Path, Version, *raw.Version)
	}
	result.Sources["version"] = SourceFile
	result.InputVersion = *raw.Version
	// Version 1 is accepted as a compatibility input, but callers always see
	// the current materialized schema version.
	result.Version = Version
	if raw.Theme != nil {
		if strings.TrimSpace(*raw.Theme) == "" {
			return Config{}, fmt.Errorf("config %s: theme must be non-empty", result.Path)
		}
		result.Theme = *raw.Theme
		result.Sources["theme"] = SourceFile
	}

	// The top-level claude posture resolves before accounts so an unset
	// per-account cache1h can inherit the FILE-resolved default (not the
	// pre-file default) below — cache1h has no empty-value sentinel the way
	// PermissionMode/Binary do, so "unset at this account" is decided here,
	// once, instead of guessed from a materialized zero value later.
	if raw.Claude != nil {
		name := pfmengine.MustLookup(pfmengine.Claude).LongName
		prefs, err := decodeClaudePrefs(*raw.Claude, result.Path, name, -1)
		if err != nil {
			return Config{}, err
		}
		if raw.Claude.PermissionMode != nil {
			result.Sources[engineConfigKey(pfmengine.Claude, "permissionMode")] = SourceFile
			result.Claude.PermissionMode = prefs.PermissionMode
		}
		if raw.Claude.Binary != nil {
			result.Sources[engineConfigKey(pfmengine.Claude, engineKeyBinary)] = SourceFile
			result.Claude.Binary = prefs.Binary
		}
		if raw.Claude.Cache1H != nil {
			result.Sources[engineConfigKey(pfmengine.Claude, "cache1h")] = SourceFile
			result.Claude.Cache1H = prefs.Cache1H
		}
		if err := applyTheme(&result.Claude, raw.Claude.Theme, name, -1, result.Sources); err != nil {
			return Config{}, fmt.Errorf("config %s: %w", result.Path, err)
		}
		applyNativeCursor(&result.Claude, raw.Claude.NativeCursor, false, result.Sources, -1)
		if raw.Claude.SystemPrompt != nil {
			result.Claude.SystemPrompt = prefs.SystemPrompt
			result.Sources[engineConfigKey(pfmengine.Claude, "systemPrompt")] = SourceFile
		}
		applied, err := applyCompactNudge(result.Claude.CompactNudge, raw.Claude.CompactNudge, result.Path, name, -1)
		if err != nil {
			return Config{}, err
		}
		result.Claude.CompactNudge = applied
		recordCompactNudgeSources(result.Sources, name, raw.Claude.CompactNudge)
	}
	if raw.Accounts != nil {
		accounts, err := validateAccounts(*raw.Accounts, home)
		if err != nil {
			return Config{}, fmt.Errorf("config %s: accounts: %w", result.Path, err)
		}
		result.Accounts = accounts
		// An explicit Claude roster is authoritative. Diagnostics from default
		// ~/.cc discovery must not re-enter observers as phantom skipped
		// accounts; unrelated discovery failures remain visible.
		result.AccountSkips = skipsOutsideDir(result.AccountSkips, filepath.Join(home, ".cc"))
		result.Sources["accounts"] = SourceFile
		for index, value := range *raw.Accounts {
			if value.Emoji != "" {
				result.Accounts[index].Emoji = value.Emoji
				result.Sources[fmt.Sprintf("accounts[%d].emoji", index)] = SourceFile
			}
			if value.Claude != nil {
				prefs, err := decodeClaudePrefs(*value.Claude, result.Path, "accounts", index)
				if err != nil {
					return Config{}, err
				}
				if value.Claude.Cache1H == nil {
					// No account-level override: inherit the already-resolved
					// top-level value rather than the type's false zero value,
					// so EffectiveClaude can apply Cache1H unconditionally
					// (like EffectiveCodex does for Yolo) without silently
					// forcing 1h caching off for an account that only touched
					// permissionMode or binary.
					prefs.Cache1H = result.Claude.Cache1H
				} else {
					result.Sources[fmt.Sprintf("accounts[%d].claude.cache1h", index)] = SourceFile
				}
				if err := applyTheme(&prefs, value.Claude.Theme, "accounts", index, result.Sources); err != nil {
					return Config{}, fmt.Errorf("config %s: %w", result.Path, err)
				}
				applyNativeCursor(&prefs, value.Claude.NativeCursor, result.Claude.NativeCursor, result.Sources, index)
				// Same inheritance for the nudge policy: seeded from the
				// resolved top level, then only the fields this account set.
				applied, err := applyCompactNudge(
					result.Claude.CompactNudge,
					value.Claude.CompactNudge,
					result.Path,
					"accounts",
					index,
				)
				if err != nil {
					return Config{}, err
				}
				prefs.CompactNudge = applied
				key := fmt.Sprintf("accounts[%d].claude", index)
				recordCompactNudgeSources(result.Sources, key, value.Claude.CompactNudge)
				result.Accounts[index].Claude = &prefs
			}
			if value.Codex != nil {
				prefs, err := decodeCodexPrefs(*value.Codex, result.Path, "accounts", index)
				if err != nil {
					return Config{}, err
				}
				result.Accounts[index].Codex = &prefs
			}
		}
	}
	if raw.Codex != nil {
		prefs, err := decodeCodexPrefs(*raw.Codex, result.Path, pfmengine.MustLookup(pfmengine.Codex).LongName, -1)
		if err != nil {
			return Config{}, err
		}
		if raw.Codex.Yolo != nil {
			result.Codex.Yolo = prefs.Yolo
			result.Sources[engineConfigKey(pfmengine.Codex, engineKeyYolo)] = SourceFile
		}
		if raw.Codex.Binary != nil {
			result.Codex.Binary = prefs.Binary
			result.Sources[engineConfigKey(pfmengine.Codex, engineKeyBinary)] = SourceFile
		}
		if raw.Codex.Homes != nil {
			accounts, err := validateCodexHomes(*raw.Codex.Homes, home, result.CodexAccounts, result.Path)
			if err != nil {
				return Config{}, err
			}
			result.CodexAccounts = accounts
			result.Sources[engineConfigKey(pfmengine.Codex, "homes")] = SourceFile
		}
	}
	if raw.OpenCode != nil {
		if raw.OpenCode.Binary != nil {
			binary := strings.TrimSpace(*raw.OpenCode.Binary)
			if binary == "" || strings.ContainsRune(binary, '\x00') {
				return Config{}, fmt.Errorf("config %s: opencode.binary must be a non-empty command", result.Path)
			}
			result.OpenCode.Binary = binary
			result.Sources[engineConfigKey(pfmengine.OpenCode, engineKeyBinary)] = SourceFile
		}
	}

	if raw.Tmux != nil && raw.Tmux.Titles != nil && raw.Tmux.Titles.Enabled != nil {
		result.Tmux.Titles.Enabled = *raw.Tmux.Titles.Enabled
		result.Sources["tmux.titles.enabled"] = SourceFile
	}
	if raw.NameSync != nil && raw.NameSync.Interval != nil {
		interval, err := parseNameSyncInterval(*raw.NameSync.Interval, result.Path)
		if err != nil {
			return Config{}, err
		}
		result.NameSync.Interval = interval
		result.Sources["nameSync.interval"] = SourceFile
	}
	if err := applyLog(&result, raw.Log); err != nil {
		return Config{}, err
	}

	var legacyHarvesterEnabled *bool
	if raw.MCP != nil {
		for name, server := range raw.MCP.Servers {
			if _, known := registered[name]; !known {
				return Config{}, fmt.Errorf("config %s: unknown key %q", result.Path, "mcp.servers."+name)
			}
			if server.Enabled == nil {
				return Config{}, fmt.Errorf(
					"config %s: required key %q is missing",
					result.Path,
					"mcp.servers."+name+".enabled",
				)
			}
			if name == MCPServerHarvester {
				// Pre-split layout: the flag now lives in harvester.config.json.
				// Honored until `pfm install` migrates it (PlanMigration).
				enabled := *server.Enabled
				legacyHarvesterEnabled = &enabled
				continue
			}
			result.MCPServers[name] = MCPServer{Enabled: *server.Enabled}
			result.MCP.Servers[name] = MCPServer{Enabled: *server.Enabled}
			result.Sources["mcp.servers."+name+".enabled"] = SourceFile
		}
		if raw.MCP.HTTP != nil {
			if raw.MCP.HTTP.Port < 1 || raw.MCP.HTTP.Port > 65535 {
				return Config{}, fmt.Errorf("config %s: mcp.http.port must be between 1 and 65535", result.Path)
			}
			result.MCP.HTTP.Port = raw.MCP.HTTP.Port
			result.Sources["mcp.http.port"] = SourceFile
		}
		// authToken was briefly installer-owned. Keep accepting the legacy key
		// so install can remove it, but it has no effective runtime value: the
		// local MCP daemon is loopback-only and deliberately unauthenticated.
	}
	if raw.Ask != nil {
		if raw.Ask.Engine != nil {
			id, err := pfmengine.Parse(*raw.Ask.Engine)
			if err != nil {
				return Config{}, fmt.Errorf("config %s: %w", result.Path, err)
			}
			result.Ask.Engine = id
			result.Sources["ask.engine"] = SourceFile
		}
		for id, rawPrefs := range raw.Ask.Prefs {
			prefs := result.Ask.Prefs[id]
			applyEnginePrefs(&prefs, rawPrefs)
			result.Ask.Prefs[id] = prefs
			name := pfmengine.MustLookup(id).LongName
			if rawPrefs.Model != nil {
				result.Sources["ask."+name+".model"] = SourceFile
			}
			if rawPrefs.Effort != nil {
				result.Sources["ask."+name+".effort"] = SourceFile
			}
		}
	}
	if raw.Ask != nil && raw.Ask.Engine != nil {
		counts := result.Engines()
		switch result.Ask.Engine {
		case pfmengine.Claude:
			if counts[pfmengine.Claude] == 0 {
				return Config{}, fmt.Errorf(
					"config %s: ask.engine %q has zero Claude accounts; add an accounts entry or choose codex",
					result.Path,
					result.Ask.Engine,
				)
			}
		case pfmengine.Codex:
			if counts[pfmengine.Codex] == 0 {
				return Config{}, fmt.Errorf(
					"config %s: ask.engine %q has zero Codex accounts; authenticate the default Codex home, add codex.homes, or choose claude",
					result.Path,
					result.Ask.Engine,
				)
			}
		case pfmengine.OpenCode:
			if counts[pfmengine.OpenCode] == 0 {
				descriptor := pfmengine.MustLookup(pfmengine.OpenCode)
				return Config{}, fmt.Errorf(
					"config %s: ask.engine %q has zero %s accounts; create %s or choose another engine",
					result.Path,
					result.Ask.Engine,
					descriptor.Short,
					filepath.Join(descriptor.DefaultRoots(home)[0], "opencode.db"),
				)
			}
		}
	}
	if err := finishHarvester(&result, home, registered, legacyHarvesterEnabled); err != nil {
		return Config{}, err
	}
	return result, nil
}

// finishHarvester loads harvester.config.json, mirrors its enabled flag into
// the server-generic MCP maps, and checks the one cross-file invariant.
func finishHarvester(result *Config, home string, registered map[string]MCPServer, legacyEnabled *bool) error {
	if err := loadHarvester(result, home, legacyEnabled); err != nil {
		return err
	}
	if _, found := registered[MCPServerHarvester]; found {
		result.MCP.Servers[MCPServerHarvester] = MCPServer{Enabled: result.Harvester.Enabled}
	}
	result.MCPServers = cloneMCPServers(result.MCP.Servers)
	if result.Harvester.External.Enabled && result.Harvester.External.Port == result.MCP.HTTP.Port {
		return fmt.Errorf(
			"harvester config %s: external.port %d collides with mcp.http.port in %s; the two gateways need distinct ports",
			result.Harvester.Path,
			result.Harvester.External.Port,
			result.Path,
		)
	}
	return nil
}

// parseNameSyncInterval validates nameSync.interval the way compactNudge's
// percentages are validated: a bad value is a refused config, never a silently
// substituted default, because the value it renders into is a scheduler nobody
// reads again after install.
func parseNameSyncInterval(value, path string) (time.Duration, error) {
	interval, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf(
			"config %s: nameSync.interval must be a Go duration such as %q, got %q",
			path,
			DefaultNameSyncInterval.String(),
			value,
		)
	}
	if interval < MinNameSyncInterval {
		return 0, fmt.Errorf(
			"config %s: nameSync.interval must be at least %s, got %s",
			path,
			MinNameSyncInterval,
			interval,
		)
	}
	return interval, nil
}

func decodeStrict(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func configJSONError(path string, err error, contentSize ...int64) error {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Errorf("parse config %s at byte %d: %w", path, syntax.Offset, err)
	}
	var kind *json.UnmarshalTypeError
	if errors.As(err, &kind) {
		return fmt.Errorf("parse config %s at byte %d: %w", path, kind.Offset, err)
	}
	if errors.Is(err, io.ErrUnexpectedEOF) && len(contentSize) > 0 {
		return fmt.Errorf("parse config %s at byte %d: %w", path, contentSize[0], err)
	}
	return fmt.Errorf("parse config %s: %w", path, err)
}

func decodeClaudePrefs(raw rawClaude, path, scope string, index int) (ClaudePrefs, error) {
	prefs := ClaudePrefs{}
	if raw.PermissionMode != nil {
		mode := *raw.PermissionMode
		// v1 used "prompt". Keep accepting it as an input alias while
		// materializing the v2 canonical value.
		if mode == "prompt" {
			mode = PermissionPrompt
		}
		if mode != PermissionBypass && mode != PermissionPrompt {
			return ClaudePrefs{}, fmt.Errorf(
				"config %s: %s.permissionMode must be %q or %q, got %q",
				path,
				configScope(scope, index),
				PermissionBypass,
				PermissionPrompt,
				*raw.PermissionMode,
			)
		}
		prefs.PermissionMode = mode
	}
	if raw.Binary != nil {
		if strings.TrimSpace(*raw.Binary) == "" || strings.ContainsRune(*raw.Binary, '\x00') {
			return ClaudePrefs{}, fmt.Errorf(
				"config %s: %s.binary must be a non-empty command",
				path,
				configScope(scope, index),
			)
		}
		prefs.Binary = *raw.Binary
	}
	if raw.Cache1H != nil {
		prefs.Cache1H = *raw.Cache1H
	}
	if raw.SystemPrompt != nil {
		value := *raw.SystemPrompt
		if value != SystemPromptProduction && value != SystemPromptLean && value != SystemPromptProfessor {
			return ClaudePrefs{}, fmt.Errorf(
				"config %s: %s.systemPrompt must be %q, %q or %q, got %q",
				path,
				configScope(scope, index),
				SystemPromptProduction,
				SystemPromptLean,
				SystemPromptProfessor,
				value,
			)
		}
		prefs.SystemPrompt = value
	}
	return prefs, nil
}

func decodeCodexPrefs(raw rawCodex, path, scope string, index int) (CodexPrefs, error) {
	return decodeCodexPrefValues(raw.Yolo, raw.Binary, path, scope, index)
}

func decodeCodexHomePrefs(raw rawCodexPrefs, path, scope string, index int) (CodexPrefs, error) {
	return decodeCodexPrefValues(raw.Yolo, raw.Binary, path, scope, index)
}

func decodeCodexPrefValues(yolo *bool, binary *string, path, scope string, index int) (CodexPrefs, error) {
	prefs := CodexPrefs{}
	if yolo != nil {
		prefs.Yolo = *yolo
	}
	if binary != nil {
		if strings.TrimSpace(*binary) == "" || strings.ContainsRune(*binary, '\x00') {
			return CodexPrefs{}, fmt.Errorf(
				"config %s: %s.binary must be a non-empty command",
				path,
				configScope(scope, index),
			)
		}
		prefs.Binary = *binary
	}
	return prefs, nil
}

func configScope(scope string, index int) string {
	if index >= 0 {
		return fmt.Sprintf("%s[%d]", scope, index)
	}
	return scope
}

func applyEnginePrefs(target *EnginePrefs, raw rawEngine) {
	if raw.Model != nil {
		target.Model = *raw.Model
	}
	if raw.Effort != nil {
		target.Effort = *raw.Effort
	}
}

func validateAccounts(values []rawAccount, home string) ([]Account, error) {
	seen := make(map[int]bool, len(values))
	accounts := make([]Account, 0, len(values))
	for index, value := range values {
		if value.ID < 1 {
			return nil, fmt.Errorf("entry %d id must be positive", index+1)
		}
		if seen[value.ID] {
			return nil, fmt.Errorf("duplicate id %d", value.ID)
		}
		seen[value.ID] = true
		configDir, err := expandHomePath(value.ConfigDir, home)
		if err != nil {
			return nil, fmt.Errorf("entry %d configDir: %w", index+1, err)
		}
		accounts = append(accounts, Account{
			ID:         value.ID,
			ConfigDir:  configDir,
			ProjectDir: filepath.Join(configDir, "projects"),
			Emoji:      DefaultEmoji(value.ID),
		})
	}
	return accounts, nil
}

func validateCodexHomes(
	values []rawCodexHome,
	home string,
	existing []CodexAccount,
	path string,
) ([]CodexAccount, error) {
	if len(values) == 0 {
		return nil, nil
	}
	accounts := append([]CodexAccount(nil), existing...)
	ids := make(map[int]int, len(accounts)+len(values))
	homes := make(map[string]int, len(accounts)+len(values))
	configuredIDs := make(map[int]bool, len(values))
	for index, account := range accounts {
		ids[account.ID] = index
		homes[filepath.Clean(account.Home)] = index
	}
	homesScope := engineConfigKey(pfmengine.Codex, "homes")
	for index, value := range values {
		scope := fmt.Sprintf("%s[%d]", homesScope, index)
		if value.ID < 1 {
			return nil, fmt.Errorf("config %s: %s.id must be positive", path, scope)
		}
		if configuredIDs[value.ID] {
			return nil, fmt.Errorf("config %s: %s duplicates id %d", path, scope, value.ID)
		}
		configuredIDs[value.ID] = true
		codexHome, err := expandHomePath(value.Home, home)
		if err != nil {
			return nil, fmt.Errorf("config %s: %s.home: %w", path, scope, err)
		}
		valid, credErr := hasValidCodexCredentials(codexHome)
		if credErr != nil {
			return nil, fmt.Errorf("config %s: %s auth.json: %w", path, scope, credErr)
		}
		if !valid {
			return nil, fmt.Errorf(
				"config %s: %s must contain a valid auth.json with tokens.access_token and account_id",
				path,
				scope,
			)
		}
		emoji := value.Emoji
		if emoji == "" {
			emoji = DefaultEmoji(value.ID)
		}
		var prefs *CodexPrefs
		if value.Prefs != nil {
			decoded, err := decodeCodexHomePrefs(*value.Prefs, path, homesScope, index)
			if err != nil {
				return nil, err
			}
			prefs = &decoded
		}
		cleanHome := filepath.Clean(codexHome)
		if existingIndex, found := homes[cleanHome]; found {
			if accounts[existingIndex].ID != value.ID {
				return nil, fmt.Errorf(
					"config %s: %s home duplicates Codex account %d",
					path,
					scope,
					accounts[existingIndex].ID,
				)
			}
			accounts[existingIndex].Emoji = emoji
			accounts[existingIndex].Prefs = prefs
			continue
		}
		if existingIndex, found := ids[value.ID]; found {
			delete(homes, filepath.Clean(accounts[existingIndex].Home))
			accounts[existingIndex] = CodexAccount{ID: value.ID, Home: cleanHome, Emoji: emoji, Prefs: prefs}
			homes[cleanHome] = existingIndex
			continue
		}
		ids[value.ID] = len(accounts)
		homes[cleanHome] = len(accounts)
		accounts = append(accounts, CodexAccount{ID: value.ID, Home: cleanHome, Emoji: emoji, Prefs: prefs})
	}
	sort.SliceStable(accounts, func(left, right int) bool { return accounts[left].ID < accounts[right].ID })
	return accounts, nil
}

func expandHomePath(value, home string) (string, error) {
	if strings.ContainsRune(value, '\x00') {
		return "", errors.New("must not contain NUL")
	}
	switch {
	case value == "~":
		value = home
	case strings.HasPrefix(value, "~/"):
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	case value == "$HOME":
		value = home
	case strings.HasPrefix(value, "$HOME/"):
		value = filepath.Join(home, strings.TrimPrefix(value, "$HOME/"))
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("must be absolute or start with ~/ or $HOME/, got %q", value)
	}
	return filepath.Clean(value), nil
}

func (config Config) Source(key string) Source {
	if source, found := config.Sources[key]; found {
		return source
	}
	return SourceDefault
}

func (config Config) Account(id int) (Account, bool) {
	return config.AccountByID(id)
}

// AccountByID returns the configured account with the requested id.
func (config Config) AccountByID(id int) (Account, bool) {
	for _, account := range config.Accounts {
		if account.ID == id {
			return account, true
		}
	}
	return Account{}, false
}

// EffectiveClaude resolves an account override over the top-level Claude posture, binary, and theme; unknown accounts receive the top-level posture.
func (config Config) EffectiveClaude(id int) ClaudePrefs {
	result := config.Claude
	if account, ok := config.AccountByID(id); ok && account.Claude != nil {
		if account.Claude.PermissionMode != "" {
			result.PermissionMode = account.Claude.PermissionMode
		}
		if account.Claude.Binary != "" {
			result.Binary = account.Claude.Binary
		}
		if account.Claude.Theme != "" {
			result.Theme = account.Claude.Theme
		}
		// Unconditional, like EffectiveCodex's Yolo: Load already seeded an
		// unset account-level Cache1H with the resolved top-level value, so
		// there is no false-zero ambiguity left to guard against here.
		result.Cache1H, result.NativeCursor = account.Claude.Cache1H, account.Claude.NativeCursor
		result.CompactNudge = account.Claude.CompactNudge
		if account.Claude.SystemPrompt != "" {
			result.SystemPrompt = account.Claude.SystemPrompt
		}
	}
	return result
}

// CodexAccountByID returns one independently authenticated Codex home.
func (config Config) CodexAccountByID(id int) (CodexAccount, bool) {
	for _, account := range config.CodexAccounts {
		if account.ID == id {
			return account, true
		}
	}
	return CodexAccount{}, false
}

// OpenCodeAccountByID returns the implicit OpenCode seat when its store exists.
func (config Config) OpenCodeAccountByID(id int) (OpenCodeAccount, bool) {
	for _, account := range config.OpenCodeAccounts {
		if account.ID == id {
			return account, true
		}
	}
	return OpenCodeAccount{}, false
}

// EffectiveCodex resolves a Codex-account override over the top-level posture
// and binary. The Claude-account lookup is retained only as v2 input
// compatibility for configs that predate independent Codex homes.
func (config Config) EffectiveCodex(id int) CodexPrefs {
	result := config.Codex
	if account, ok := config.CodexAccountByID(id); ok && account.Prefs != nil {
		result.Yolo = account.Prefs.Yolo
		if account.Prefs.Binary != "" {
			result.Binary = account.Prefs.Binary
		}
		return result
	}
	if legacy, ok := config.AccountByID(id); ok && legacy.Codex != nil {
		result.Yolo = legacy.Codex.Yolo
		if legacy.Codex.Binary != "" {
			result.Binary = legacy.Codex.Binary
		}
	}
	return result
}

// EmojiFor returns the configured account badge or the honest unknown marker.
func (config Config) EmojiFor(id int) string {
	if account, ok := config.AccountByID(id); ok && account.Emoji != "" {
		return account.Emoji
	}
	return "·"
}

func (config Config) AccountIDs() []int {
	ids := make([]int, 0, len(config.Accounts))
	for _, account := range config.Accounts {
		ids = append(ids, account.ID)
	}
	return ids
}

func (config Config) CodexAccountIDs() []int {
	ids := make([]int, 0, len(config.CodexAccounts))
	for _, account := range config.CodexAccounts {
		ids = append(ids, account.ID)
	}
	return ids
}

func (config Config) CodexEmojiFor(id int) string {
	if account, ok := config.CodexAccountByID(id); ok && account.Emoji != "" {
		return account.Emoji
	}
	return "·"
}

func (config Config) Engines() EngineCounts {
	counts := EngineCounts{}
	if count := len(config.Accounts); count != 0 {
		counts[pfmengine.Claude] = count
	}
	if count := len(config.CodexAccounts); count != 0 {
		counts[pfmengine.Codex] = count
	}
	if count := len(config.OpenCodeAccounts); count != 0 {
		counts[pfmengine.OpenCode] = count
	}
	return counts
}

func (config Config) DefaultEngine() (pfmengine.ID, error) {
	counts := config.Engines()
	preferred := config.Ask.Engine
	if preferred == "" {
		preferred = pfmengine.Codex
	}
	id := preferred
	switch id {
	case pfmengine.Claude:
		if counts[pfmengine.Claude] > 0 {
			return pfmengine.Claude, nil
		}
	case pfmengine.Codex:
		if counts[pfmengine.Codex] > 0 {
			return pfmengine.Codex, nil
		}
	case pfmengine.OpenCode:
		if counts[pfmengine.OpenCode] > 0 {
			return pfmengine.OpenCode, nil
		}
	}
	if counts[pfmengine.Claude] > 0 {
		return pfmengine.Claude, nil
	}
	if counts[pfmengine.Codex] > 0 {
		return pfmengine.Codex, nil
	}
	if counts[pfmengine.OpenCode] > 0 {
		return pfmengine.OpenCode, nil
	}
	return "", errors.New("no engines configured: Claude roster empty; Codex roster empty; OpenCode store absent")
}

func (config Config) ProjectRoots() []string {
	roots := make([]string, 0, len(config.Accounts))
	for _, account := range config.Accounts {
		roots = append(roots, account.ProjectDir)
	}
	return roots
}

func RegisteredMCPServers() []string {
	registered := productionMCPServers()
	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SetMCPServer atomically changes one registered server in the machine file.
// Repeating an already-effective setting is a no-op.
func SetMCPServer(config Config, name string, enabled bool) (bool, error) {
	server, registered := config.MCPServers[name]
	if !registered {
		return false, fmt.Errorf("unknown MCP server %q", name)
	}
	if name == MCPServerHarvester {
		return SetHarvesterEnabled(config, enabled)
	}
	if server.Enabled == enabled {
		return false, nil
	}

	top := make(map[string]json.RawMessage)
	if config.Exists {
		content, err := os.ReadFile(config.Path)
		if err != nil {
			return false, fmt.Errorf("read config %s for update: %w", config.Path, err)
		}
		if err := json.Unmarshal(content, &top); err != nil {
			return false, configJSONError(config.Path, err)
		}
	}
	version, _ := json.Marshal(Version)
	top["version"] = version

	mcpObject := make(map[string]json.RawMessage)
	if content := top["mcp"]; len(content) != 0 {
		if err := json.Unmarshal(content, &mcpObject); err != nil {
			return false, fmt.Errorf("decode config %s mcp for update: %w", config.Path, err)
		}
	}
	servers := make(map[string]json.RawMessage)
	if content := mcpObject["servers"]; len(content) != 0 {
		if err := json.Unmarshal(content, &servers); err != nil {
			return false, fmt.Errorf("decode config %s mcp.servers for update: %w", config.Path, err)
		}
	}
	serverObject := map[string]bool{jsonKeyEnabled: enabled}
	serverContent, _ := json.Marshal(serverObject)
	servers[name] = serverContent
	serversContent, _ := json.Marshal(servers)
	mcpObject["servers"] = serversContent
	mcpContent, _ := json.Marshal(mcpObject)
	top["mcp"] = mcpContent

	content, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode config %s: %w", config.Path, err)
	}
	content = append(content, '\n')
	if err := atomicfile.Write(config.Path, content, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveMCPAuthToken removes the retired installer-owned daemon credential
// while preserving every unrelated field. The strict loader still accepts the
// legacy key so an existing host can reach this cleanup path.
func RemoveMCPAuthToken(config Config) (bool, error) {
	content, changed, err := configWithoutMCPAuthToken(config)
	if err != nil || !changed {
		return changed, err
	}
	if err := atomicfile.Write(config.Path, content, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// MCPAuthTokenPresent reports whether the retired installer credential is
// present without modifying the config. Install preview uses the same parser
// as apply so the cleanup appears in the plan before any host mutation.
func MCPAuthTokenPresent(config Config) (bool, error) {
	_, changed, err := configWithoutMCPAuthToken(config)
	return changed, err
}

func configWithoutMCPAuthToken(config Config) ([]byte, bool, error) {
	if config.Path == "" {
		return nil, false, errors.New("MCP auth token cleanup has no config path")
	}
	top := make(map[string]json.RawMessage)
	if !config.Exists {
		return nil, false, nil
	}
	content, err := os.ReadFile(config.Path)
	if err != nil {
		return nil, false, fmt.Errorf("read config %s for MCP auth cleanup: %w", config.Path, err)
	}
	if err := json.Unmarshal(content, &top); err != nil {
		return nil, false, configJSONError(config.Path, err)
	}
	mcpObject := make(map[string]json.RawMessage)
	if content := top["mcp"]; len(content) != 0 {
		if err := json.Unmarshal(content, &mcpObject); err != nil {
			return nil, false, fmt.Errorf("decode config %s mcp for auth cleanup: %w", config.Path, err)
		}
	}
	if _, present := mcpObject["authToken"]; !present {
		return nil, false, nil
	}
	delete(mcpObject, "authToken")
	mcpContent, _ := json.Marshal(mcpObject)
	top["mcp"] = mcpContent
	content, err = json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("encode config %s: %w", config.Path, err)
	}
	return append(content, '\n'), true, nil
}

// MarshalDefault returns the strict, comment-free JSON used by `pfm config
// init`. It deliberately emits resolved defaults so the file is useful as a
// documented starting point while the loader remains backward compatible.
func MarshalDefault(home string, projectRoots []string) ([]byte, error) {
	defaults := Defaults(home, projectRoots)
	defaults.Log.Level = InstallLogLevel
	return Marshal(defaults, false)
}

// WriteDefault installs the default machine file atomically. Existing files
// are protected unless force is explicitly requested.
func WriteDefault(path, home string, projectRoots []string, force bool) error {
	if path == "" {
		path = ResolvePath(home)
	}
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("config %s already exists; use --force to overwrite", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect config %s: %w", path, err)
	}
	content, err := MarshalDefault(home, projectRoots)
	if err != nil {
		return fmt.Errorf("encode defaults: %w", err)
	}
	return atomicfile.Write(path, content, 0o600)
}

// Marshal encodes a resolved config as strict JSON. The redaction option is
// intended for human-facing output only.
func Marshal(config Config, redact bool) ([]byte, error) {
	claudeName := pfmengine.MustLookup(pfmengine.Claude).LongName
	codexName := pfmengine.MustLookup(pfmengine.Codex).LongName
	accounts := make([]map[string]any, 0, len(config.Accounts))
	for _, account := range config.Accounts {
		value := map[string]any{
			"id":        account.ID,
			"configDir": account.ConfigDir,
			"emoji":     account.Emoji,
		}
		if account.Claude != nil {
			value[claudeName] = map[string]any{
				"permissionMode": account.Claude.PermissionMode,
				engineKeyBinary:  account.Claude.Binary,
				"theme":          themeMarshalValue(account.Claude.Theme),
				"cache1h":        account.Claude.Cache1H,
				"nativeCursor":   account.Claude.NativeCursor,
			}
		}
		if account.Codex != nil {
			value[codexName] = map[string]any{
				engineKeyYolo:   account.Codex.Yolo,
				engineKeyBinary: account.Codex.Binary,
			}
		}
		accounts = append(accounts, value)
	}
	codexHomes := make([]map[string]any, 0, len(config.CodexAccounts))
	for _, account := range config.CodexAccounts {
		value := map[string]any{
			"id": account.ID, "home": account.Home, "emoji": account.Emoji,
		}
		if account.Prefs != nil {
			value["prefs"] = map[string]any{engineKeyYolo: account.Prefs.Yolo, engineKeyBinary: account.Prefs.Binary}
		}
		codexHomes = append(codexHomes, value)
	}
	servers := make(map[string]any, len(config.MCP.Servers))
	for name, server := range config.MCP.Servers {
		if name == MCPServerHarvester {
			continue // lives in harvester.config.json (MarshalHarvester)
		}
		servers[name] = map[string]any{jsonKeyEnabled: server.Enabled}
	}
	codexValue := map[string]any{
		engineKeyYolo:   config.Codex.Yolo,
		engineKeyBinary: config.Codex.Binary,
	}
	if len(codexHomes) != 0 || config.Source(engineConfigKey(pfmengine.Codex, "homes")) == SourceFile {
		codexValue["homes"] = codexHomes
	}
	askValue := make(map[string]any, len(config.Ask.Prefs)+1)
	for _, id := range pfmengine.All() {
		prefs, found := config.Ask.Prefs[id]
		if !found {
			continue
		}
		askValue[pfmengine.MustLookup(id).LongName] = map[string]any{
			"model": prefs.Model, "effort": prefs.Effort,
		}
	}
	if engine, err := config.DefaultEngine(); err == nil {
		askValue["engine"] = pfmengine.MustLookup(engine).LongName
	}
	value := map[string]any{
		"version":  config.Version,
		"theme":    config.Theme,
		"accounts": accounts,
		claudeName: map[string]any{
			"permissionMode": config.Claude.PermissionMode,
			engineKeyBinary:  config.Claude.Binary,
			"theme":          themeMarshalValue(config.Claude.Theme),
			"cache1h":        config.Claude.Cache1H,
			"nativeCursor":   config.Claude.NativeCursor,
			"compactNudge": map[string]any{
				jsonKeyEnabled: config.Claude.CompactNudge.Enabled,
				"start":        config.Claude.CompactNudge.Start,
				"step":         config.Claude.CompactNudge.Step,
			},
		},
		codexName: codexValue,
		"tmux": map[string]any{
			"titles": map[string]any{jsonKeyEnabled: config.Tmux.Titles.Enabled},
		},
		"nameSync": map[string]any{"interval": config.NameSync.Interval.String()},
		"mcp": map[string]any{
			"servers": servers,
			"http":    map[string]any{jsonKeyPort: config.MCP.HTTP.Port},
		},
		"ask": askValue,
		"log": MarshalLog(config.Log),
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	if redact {
		return RedactSecrets(append(content, '\n')), nil
	}
	return append(content, '\n'), nil
}
