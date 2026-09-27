package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type LayoutVerdict string

const (
	VerdictOK      LayoutVerdict = "ok"
	VerdictCreate  LayoutVerdict = "create"
	VerdictRepoint LayoutVerdict = "repoint"
	VerdictMove    LayoutVerdict = "move"
	VerdictMerge   LayoutVerdict = "merge"
	VerdictStrip   LayoutVerdict = "strip"
	VerdictRemove  LayoutVerdict = "remove"
	VerdictRefuse  LayoutVerdict = "refuse"
)

const (
	layoutRowManagedCleanup  = "managed-cleanup"
	layoutRowConfig          = "config"
	layoutRowHarvesterConfig = "harvester-config"
	layoutRowStateDB         = "state-db"
	layoutRowCacheDB         = "cache-db"
	layoutRowSessionStore    = "session-store"
	layoutRowMemoryHelpers   = "memory-helpers"
	layoutRowAccountSettings = "account-settings"
	layoutRowAccountMCP      = "account-mcp"
	layoutRowHomeMCP         = "home-mcp"
	layoutRowZshrc           = "zshrc"
	layoutRowStagedPrompts   = "staged-prompts"
	layoutRowSharedDB        = "shared-db"
	layoutRowStrayDir        = "stray-dir"
	layoutDBWAL              = "-wal"
	layoutDBSHM              = "-shm"
	layoutNotRegular         = "not a regular file"
	layoutOwnershipLedger    = "ownership ledger"
)

type LayoutEnv struct {
	Home            string
	Clone           string
	Config          pfmconfig.Config
	ConfigPath      string
	StateDB         string
	CacheDB         string
	ManagedDir      string
	ProcRoot        string
	ManagedRoot     string
	LegacyConfigDir string
	moveProbe       func(source, destination string) (different bool, available uint64, err error)
	// spaceProbe reports the device and free bytes of the filesystem holding
	// dir; nil is the statfs default (layout_space.go).
	spaceProbe   func(dir string) (device uint64, free uint64, err error)
	runner       CommandRunner
	writeManaged func(path string, content []byte) error
}

func NewLayoutEnv(runtime pfmconfig.Runtime, env paths.Env) (LayoutEnv, error) {
	return NewInstallLayoutEnv(runtime, env, "")
}

// NewInstallLayoutEnv is NewLayoutEnv for `pfm install`, which knows the clone
// it runs from before it records the source-repo marker. On a first install no
// marker exists yet, so installClone stands in for it: the config row seeds
// pfm.config.json from {installClone}/example.pfm.config.json instead of
// refusing with "no source repo recorded". A recorded marker still wins.
func NewInstallLayoutEnv(runtime pfmconfig.Runtime, env paths.Env, installClone string) (LayoutEnv, error) {
	if env == nil {
		env = paths.OSEnv{}
	}
	home := runtime.Paths.Home
	clone, err := paths.ReadSourceRepoMarker(home)
	if err != nil && !errors.Is(err, paths.ErrNoSourceRepoMarker) {
		return LayoutEnv{}, fmt.Errorf("read source repo marker: %w", err)
	}
	if errors.Is(err, paths.ErrNoSourceRepoMarker) {
		clone = installClone
	}
	legacyDir := pfmconfig.LegacyConfigDir(env, home)
	configPath := runtime.Config.Path
	if !runtime.ConfigExplicit && filepath.Dir(configPath) == legacyDir {
		if clone != "" {
			configPath = filepath.Join(clone, pfmconfig.FileName)
		} else if filepath.Base(configPath) == pfmconfig.LegacyFileName {
			configPath = filepath.Join(legacyDir, pfmconfig.FileName)
		}
	}
	if configPath == "" {
		configPath, err = pfmconfig.ResolvePathFrom(env, home)
		if err != nil && !errors.Is(err, paths.ErrNoSourceRepoMarker) {
			return LayoutEnv{}, fmt.Errorf("resolve target config: %w", err)
		}
		if errors.Is(err, paths.ErrNoSourceRepoMarker) {
			configPath = filepath.Join(home, pfmconfig.FileName)
			if clone != "" {
				configPath = filepath.Join(clone, pfmconfig.FileName)
			}
		}
	}
	layout := LayoutEnv{
		Home: home, Clone: clone, Config: runtime.Config, ConfigPath: configPath,
		StateDB: runtime.Paths.StateDB, CacheDB: runtime.Paths.CacheDB,
		ManagedDir: runtime.Paths.ManagedSettingsDir, ProcRoot: runtime.Paths.ProcRoot,
		ManagedRoot:     filepath.Join(home, ".local", "share", "pfm", "install"),
		LegacyConfigDir: legacyDir,
	}
	if _, err := os.Lstat(configPath); errors.Is(err, fs.ErrNotExist) {
		for _, name := range []string{pfmconfig.FileName, "config.json"} {
			legacy := filepath.Join(legacyDir, name)
			if _, legacyErr := os.Lstat(legacy); legacyErr == nil {
				loaded, loadErr := pfmconfig.Load(
					legacy,
					home,
					runtime.Paths.Roots[pfmengine.Claude],
					runtime.Paths.FirstRoot(pfmengine.Codex),
				)
				if loadErr != nil {
					return LayoutEnv{}, fmt.Errorf("load legacy config %s: %w", legacy, loadErr)
				}
				layout.Config = loaded
				break
			} else if !errors.Is(legacyErr, fs.ErrNotExist) {
				return LayoutEnv{}, fmt.Errorf("inspect legacy config %s: %w", legacy, legacyErr)
			}
		}
	} else if err != nil {
		return LayoutEnv{}, fmt.Errorf("inspect target config %s: %w", configPath, err)
	}
	return layout, nil
}

// InstallConfig returns the config `pfm install` classifies against once its
// layout rows ran, so a preview and --yes build the same installer options:
// the file at ConfigPath when it exists; on a preview of a first install, the
// config the config row would seed there from {Clone}/example.pfm.config.json;
// otherwise runtime's config. An explicit --config is never seeded: the apply
// refuses a missing one.
func (env LayoutEnv) InstallConfig(runtime pfmconfig.Runtime, apply bool) (pfmconfig.Config, error) {
	values := runtime.Paths
	_, statErr := os.Stat(env.ConfigPath)
	if statErr == nil {
		config, err := pfmconfig.Load(
			env.ConfigPath,
			values.Home,
			values.Roots[pfmengine.Claude],
			values.FirstRoot(pfmengine.Codex),
		)
		if err != nil {
			return pfmconfig.Config{}, fmt.Errorf("reload layout config %s: %w", env.ConfigPath, err)
		}
		return config, nil
	}
	if !errors.Is(statErr, fs.ErrNotExist) {
		return pfmconfig.Config{}, fmt.Errorf("inspect layout config %s: %w", env.ConfigPath, statErr)
	}
	finding := classifyConfig(env)
	if apply || runtime.ConfigExplicit || env.Clone == "" || finding.Err != nil || finding.Verdict != VerdictCreate {
		return runtime.Config, nil
	}
	example := filepath.Join(env.Clone, "example.pfm.config.json")
	if _, err := os.Stat(example); err != nil {
		return runtime.Config, nil
	}
	config, err := pfmconfig.LoadSeed(
		example,
		env.ConfigPath,
		values.Home,
		values.Roots[pfmengine.Claude],
		values.FirstRoot(pfmengine.Codex),
	)
	if err != nil {
		return pfmconfig.Config{}, fmt.Errorf("preview seeded config %s: %w", env.ConfigPath, err)
	}
	return config, nil
}

type LayoutFinding struct {
	Row     string
	Verdict LayoutVerdict
	Path    string
	Source  string
	Detail  string
	Err     error
}

// LayoutRow records the desired form and the legacy form recognised by a row.
// A row can expand to one finding per account or session entry.
type LayoutRow struct {
	Row        string
	TargetForm string
	LegacyForm string
}

var HostLayout = []LayoutRow{
	{layoutRowManagedCleanup, "{ManagedDir}/pfm.json at configured cleanupPeriodDays", "absent or wrong value"},
	{layoutRowConfig, "{ConfigPath} regular file", "{LegacyConfigDir}/pfm.config.json or config.json"},
	{layoutRowHarvesterConfig, "beside ConfigPath, regular file or absent", "{LegacyConfigDir}/harvester.config.json"},
	{layoutRowStateDB, "{StateDB}, no legacy database", "{home}/.cc/legacy database and siblings"},
	{layoutRowCacheDB, "{CacheDB}, no legacy database", "{home}/.local/state/pfm/legacy database and siblings"},
	{
		layoutRowSessionStore,
		"{configDir}/{entry} symlink to {home}/.claude/{entry}",
		"real dir, missing link or account link",
	},
	{layoutRowMemoryHelpers, "account helper without cc prefix", "fingerprint-matched cc-memory helper"},
	{layoutRowAccountSettings, "account settings without pfm hooks or status lines", "pfm-owned settings entries"},
	{layoutRowAccountMCP, "account registry without pfm MCP servers", "ledger-owned or pfm-shaped registry entries"},
	{layoutRowHomeMCP, "{home}/.mcp.json without pfm MCP servers, no ledger clients", "pfm entries or ledger clients"},
	{layoutRowZshrc, "source {Clone}/pfm/internal/installer/assets/shim/pfm.zsh", "source managed staged shim"},
	{layoutRowStagedPrompts, "no staged prompt directory", "{ManagedRoot}/harness-prompts"},
	{layoutRowSharedDB, "no shared database", "empty shared.db"},
	{layoutRowStrayDir, "no stray directories", "empty .cc/.git, .cc/.codex or .cc/.agents"},
}

// ClassifyLayout reads host state without changing any file or process.
func ClassifyLayout(env LayoutEnv) []LayoutFinding {
	findings := make([]LayoutFinding, 0, len(HostLayout)+len(env.Config.Accounts)*8)
	for _, row := range HostLayout {
		first := len(findings)
		switch row.Row {
		case layoutRowManagedCleanup:
			findings = append(findings, classifyManagedCleanup(env))
		case layoutRowConfig:
			findings = append(findings, classifyConfig(env))
		case layoutRowHarvesterConfig:
			findings = append(findings, classifyHarvesterConfig(env))
		case layoutRowStateDB:
			findings = append(
				findings,
				classifyDB(env, row.Row, env.StateDB, paths.LegacyStateDB(env.Home)),
			)
		case layoutRowCacheDB:
			findings = append(
				findings,
				classifyDB(env, row.Row, env.CacheDB, paths.LegacyCacheDB(env.Home)),
			)
		case layoutRowSessionStore:
			findings = append(findings, classifySessionStore(env)...)
		case layoutRowMemoryHelpers:
			findings = append(findings, classifyMemoryHelpers(env)...)
		case layoutRowAccountSettings:
			findings = append(findings, classifyAccountSettings(env)...)
		case layoutRowAccountMCP:
			findings = append(findings, classifyAccountMCP(env)...)
		case layoutRowHomeMCP:
			findings = append(findings, classifyHomeMCP(env))
		case layoutRowZshrc:
			findings = append(findings, classifyZshrc(env))
		case layoutRowStagedPrompts:
			findings = append(findings, classifyStagedPrompts(env))
		case layoutRowSharedDB:
			findings = append(findings, classifySharedDB(env))
		case layoutRowStrayDir:
			for _, name := range []string{".git", ".codex", ".agents"} {
				findings = append(findings, classifyStrayDir(filepath.Join(env.Home, ".cc", name)))
			}
		}
		for index := first; index < len(findings); index++ {
			if findings[index].Verdict != VerdictMove || findings[index].Err != nil {
				continue
			}
			detail, err := moveSpaceGuard(env, findings[index])
			if err != nil {
				findings[index].Err = err
			} else if detail != "" {
				findings[index].Verdict, findings[index].Detail = VerdictRefuse, detail
			}
		}
	}
	return findings
}

func layoutLstat(row, path string) (LayoutFinding, fs.FileInfo, bool) {
	finding := LayoutFinding{Row: row, Verdict: VerdictOK, Path: path}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return finding, nil, false
	}
	if err != nil {
		finding.Err = err
		return finding, nil, false
	}
	return finding, info, true
}

func classifyManagedCleanup(env LayoutEnv) LayoutFinding {
	path := filepath.Join(env.ManagedDir, "pfm.json")
	finding, info, exists := layoutLstat(layoutRowManagedCleanup, path)
	if !env.Config.Claude.RequireManagedCleanup {
		finding.Detail = "check off by config"
		return finding
	}
	if finding.Err != nil {
		return finding
	}
	if !exists {
		finding.Verdict = VerdictCreate
		return finding
	}
	if !info.Mode().IsRegular() {
		finding.Verdict, finding.Detail = VerdictRefuse, layoutNotRegular
		return finding
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		finding.Err = err
		return finding
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		finding.Err = err
		return finding
	}
	var value int
	if err := json.Unmarshal(document["cleanupPeriodDays"], &value); err != nil {
		finding.Err = fmt.Errorf("cleanupPeriodDays: %w", err)
		return finding
	}
	if value != env.Config.Claude.CleanupPeriodDays {
		finding.Verdict, finding.Detail = VerdictRepoint, fmt.Sprintf(
			"cleanupPeriodDays=%d, want %d",
			value,
			env.Config.Claude.CleanupPeriodDays,
		)
	}
	return finding
}

func classifyConfig(env LayoutEnv) LayoutFinding {
	return classifyMovedFile(layoutRowConfig, env.ConfigPath, []string{
		filepath.Join(env.LegacyConfigDir, pfmconfig.FileName),
		filepath.Join(env.LegacyConfigDir, "config.json"),
	}, true)
}

func classifyHarvesterConfig(env LayoutEnv) LayoutFinding {
	return classifyMovedFile(
		layoutRowHarvesterConfig,
		filepath.Join(filepath.Dir(env.ConfigPath), "harvester.config.json"),
		[]string{filepath.Join(env.LegacyConfigDir, "harvester.config.json")},
		false,
	)
}

func classifyMovedFile(row, target string, legacy []string, createWhenAbsent bool) LayoutFinding {
	finding, targetInfo, targetExists := layoutLstat(row, target)
	if finding.Err != nil {
		return finding
	}
	if targetExists && !targetInfo.Mode().IsRegular() {
		finding.Verdict, finding.Detail = VerdictRefuse, "target is not a regular file"
		return finding
	}
	for _, old := range legacy {
		if filepath.Clean(old) == filepath.Clean(target) {
			continue
		}
		legacyFinding, info, exists := layoutLstat(row, old)
		if legacyFinding.Err != nil {
			finding.Err = legacyFinding.Err
			return finding
		}
		if !exists {
			continue
		}
		if !info.Mode().IsRegular() {
			finding.Verdict, finding.Source, finding.Detail = VerdictRefuse, old, "legacy path is not a regular file"
			return finding
		}
		if finding.Source != "" {
			finding.Verdict, finding.Detail = VerdictRefuse, "multiple legacy config files exist"
			return finding
		}
		if targetExists {
			targetRaw, targetErr := os.ReadFile(target)
			legacyRaw, legacyErr := os.ReadFile(old)
			if targetErr != nil || legacyErr != nil {
				finding.Err = errors.Join(targetErr, legacyErr)
				return finding
			}
			if !bytes.Equal(targetRaw, legacyRaw) {
				finding.Verdict, finding.Detail = VerdictRefuse, "target and legacy config both exist and differ"
				return finding
			}
			finding.Detail = "identical legacy copy"
		}
		finding.Source = old
		finding.Verdict = VerdictMove
	}
	if finding.Source == "" && !targetExists && createWhenAbsent {
		finding.Verdict = VerdictCreate
	}
	return finding
}

func classifyDB(env LayoutEnv, row, target, legacy string) LayoutFinding {
	finding, targetInfo, targetExists := layoutLstat(row, target)
	if finding.Err != nil {
		return finding
	}
	if targetExists && !targetInfo.Mode().IsRegular() {
		finding.Verdict, finding.Detail = VerdictRefuse, "target is not a regular file"
		return finding
	}
	found := false
	for _, suffix := range []string{"", layoutDBWAL, layoutDBSHM} {
		legacyFinding, info, exists := layoutLstat(row, legacy+suffix)
		if legacyFinding.Err != nil {
			finding.Err = legacyFinding.Err
			return finding
		}
		if !exists {
			continue
		}
		if !info.Mode().IsRegular() {
			finding.Verdict, finding.Detail = VerdictRefuse, "legacy database sibling is not a regular file"
			return finding
		}
		found = true
	}
	if !found {
		return finding
	}
	finding.Source = legacy
	pids, err := dbHolderPIDs(env.ProcRoot, legacy)
	switch {
	case err != nil:
		finding.Err = err
	case len(pids) > 0:
		finding.Verdict, finding.Detail = VerdictRefuse, "held by pid "+strings.Join(pids, ",")
	case targetExists:
		finding.Verdict, finding.Detail = VerdictRefuse, "target and legacy database both exist"
	default:
		finding.Verdict = VerdictMove
	}
	return finding
}

func accountDirs(env LayoutEnv) []string {
	seen := map[string]bool{}
	dirs := make([]string, 0, len(env.Config.Accounts))
	for _, account := range env.Config.Accounts {
		dir := account.ConfigDir
		if account.Implicit || dir == "" {
			dir = filepath.Join(env.Home, ".claude")
		}
		physical := physicalSettingsPath(dir)
		if !seen[physical] {
			seen[physical] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func classifySessionStore(env LayoutEnv) []LayoutFinding {
	store := filepath.Join(env.Home, ".claude")
	findings := []LayoutFinding{}
	for _, dir := range accountDirs(env) {
		if physicalSettingsPath(dir) == physicalSettingsPath(store) {
			continue
		}
		live, liveErr := liveChatPIDs(env.ProcRoot, dir)
		for _, entry := range SessionPaths {
			path := filepath.Join(dir, entry)
			want := filepath.Join(store, entry)
			finding, info, exists := layoutLstat(layoutRowSessionStore, path)
			if finding.Err == nil {
				switch {
				case !exists:
					finding.Verdict = VerdictCreate
				case info.Mode()&os.ModeSymlink != 0:
					target, err := os.Readlink(path)
					if err != nil {
						finding.Err = err
						break
					}
					if !filepath.IsAbs(target) {
						target = filepath.Join(dir, target)
					}
					target = filepath.Clean(target)
					finding.Source = target
					if target == want {
						break
					}
					if physicalSettingsPath(target) == physicalSettingsPath(want) &&
						accountLinkTarget(target, entry, accountDirs(env)) {
						finding.Verdict = VerdictRepoint
					} else {
						finding.Verdict = VerdictRefuse
					}
					finding.Detail = target
				case info.IsDir():
					children, err := os.ReadDir(path)
					if err != nil {
						finding.Err = err
						break
					}
					finding.Verdict = VerdictMerge
					finding.Detail = fmt.Sprintf("%d entries", len(children))
				default:
					finding.Verdict, finding.Detail = VerdictRefuse, "not a directory or link"
				}
			}
			if liveErr != nil {
				finding.Err = liveErr
			} else if len(live) > 0 {
				finding.Verdict, finding.Detail = VerdictRefuse, "live chats: "+strings.Join(live, ",")
			}
			findings = append(findings, finding)
		}
	}
	return findings
}

func accountLinkTarget(target, entry string, dirs []string) bool {
	for _, dir := range dirs {
		if target == filepath.Join(dir, entry) {
			return true
		}
	}
	return false
}

func classifyMemoryHelpers(env LayoutEnv) []LayoutFinding {
	dirs := accountDirs(env)
	installer := engine{options: Options{Home: env.Home, ConfigDirs: dirs, Stdout: io.Discard}}
	migrations, err := installer.planMemoryHelperMigrations()
	if err != nil {
		finding := LayoutFinding{
			Row:     layoutRowMemoryHelpers,
			Verdict: VerdictRefuse,
			Path:    filepath.Join(env.Home, ".claude"),
		}
		if !strings.Contains(err.Error(), "refuse to") {
			finding.Err = err
		} else {
			finding.Detail = err.Error()
		}
		return []LayoutFinding{finding}
	}
	byDir := map[string][]memoryHelperMigration{}
	for _, migration := range migrations {
		dir := filepath.Dir(filepath.Dir(migration.oldPath))
		byDir[physicalSettingsPath(dir)] = append(byDir[physicalSettingsPath(dir)], migration)
	}
	findings := make([]LayoutFinding, 0, len(dirs)+len(migrations))
	for _, dir := range dirs {
		old := byDir[physicalSettingsPath(dir)]
		if len(old) == 0 {
			findings = append(findings, LayoutFinding{Row: layoutRowMemoryHelpers, Verdict: VerdictOK, Path: dir})
			continue
		}
		for _, migration := range old {
			findings = append(findings, LayoutFinding{
				Row: layoutRowMemoryHelpers, Verdict: VerdictMove,
				Path: dir, Source: migration.oldPath, Detail: migration.oldPath,
			})
		}
	}
	return findings
}

func classifyAccountSettings(env LayoutEnv) []LayoutFinding {
	dirs := accountDirs(env)
	ledgerPath := settingsHookOwnershipPath(env.ManagedRoot)
	ownership, _, ledgerErr := readSettingsHookOwnership(ledgerPath)
	findings := make([]LayoutFinding, 0, len(dirs))
	for _, dir := range dirs {
		path := filepath.Join(dir, "settings.json")
		finding, info, exists := layoutLstat(layoutRowAccountSettings, path)
		if ledgerErr != nil {
			finding.Err, finding.Source, finding.Detail = ledgerErr, ledgerPath, layoutOwnershipLedger
		} else if finding.Err == nil && exists {
			if !info.Mode().IsRegular() {
				finding.Verdict, finding.Detail = VerdictRefuse, layoutNotRegular
			} else if raw, err := os.ReadFile(path); err != nil {
				finding.Err = err
			} else if leftovers, err := accountSettingsLeftovers(raw, env.Home, ownership[physicalSettingsPath(path)], true); err != nil {
				finding.Err = err
			} else if len(leftovers) > 0 {
				finding.Verdict, finding.Detail = VerdictStrip, strings.Join(leftovers, ",")
			}
		}
		live, err := liveChatPIDs(env.ProcRoot, dir)
		if err != nil {
			finding.Err = err
		} else if len(live) > 0 {
			finding.Verdict, finding.Detail = VerdictRefuse, "live chats: "+strings.Join(live, ",")
		}
		findings = append(findings, finding)
	}
	return findings
}

func classifyAccountMCP(env LayoutEnv) []LayoutFinding {
	ownership, ledgerErr := readMCPOwnership(filepath.Join(env.ManagedRoot, mcpOwnershipName))
	shaped := layoutMCPShaped(env)
	findings := []LayoutFinding{}
	seen := map[string]bool{}
	for _, registry := range ClaudeUserRegistries(env.Home, env.Config.Accounts, "") {
		path := registry.Path
		physical := physicalSettingsPath(path)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		finding, info, exists := layoutLstat(layoutRowAccountMCP, path)
		if ledgerErr != nil {
			finding.Err, finding.Source, finding.Detail = ledgerErr, filepath.Join(
				env.ManagedRoot,
				mcpOwnershipName,
			), layoutOwnershipLedger
		} else if finding.Err == nil && exists {
			owned := make([]string, 0, len(ownership.Registrations[physical]))
			for name := range ownership.Registrations[physical] {
				owned = append(owned, name)
			}
			sort.Strings(owned)
			judgeMCPFile(&finding, info, owned, shaped)
		}
		if registry.Account != 0 {
			dir := filepath.Dir(path)
			for _, account := range env.Config.Accounts {
				if account.ID == registry.Account && account.Implicit {
					dir = filepath.Join(env.Home, ".claude")
					break
				}
			}
			live, err := liveChatPIDs(env.ProcRoot, dir)
			if err != nil {
				finding.Err = err
			} else if len(live) > 0 {
				finding.Verdict, finding.Detail = VerdictRefuse, "live chats: "+strings.Join(live, ",")
			}
		}
		findings = append(findings, finding)
	}
	return findings
}

// classifyHomeMCP judges {home}/.mcp.json by the ledger's old clients list and
// by shape, and reports a non-empty clients list as its own leftover.
func classifyHomeMCP(env LayoutEnv) LayoutFinding {
	ledger := filepath.Join(env.ManagedRoot, mcpOwnershipName)
	finding, info, exists := layoutLstat(layoutRowHomeMCP, filepath.Join(env.Home, ".mcp.json"))
	ownership, err := readMCPOwnership(ledger)
	if err != nil {
		finding.Err, finding.Source, finding.Detail = err, ledger, layoutOwnershipLedger
		return finding
	}
	if finding.Err != nil {
		return finding
	}
	if exists {
		judgeMCPFile(&finding, info, ownership.Clients, layoutMCPShaped(env))
		if finding.Err != nil || finding.Verdict == VerdictRefuse {
			return finding
		}
	}
	if len(ownership.Clients) > 0 {
		finding.Verdict, finding.Detail = VerdictStrip, strings.TrimPrefix(finding.Detail+",clients", ",")
	}
	return finding
}

// judgeMCPFile marks an existing Claude MCP file strip when it carries
// ledger-owned or pfm-shaped mcpServers entries.
func judgeMCPFile(finding *LayoutFinding, info fs.FileInfo, owned []string, shaped mcpShaped) {
	if !info.Mode().IsRegular() {
		finding.Verdict, finding.Detail = VerdictRefuse, layoutNotRegular
		return
	}
	raw, err := os.ReadFile(finding.Path)
	if err != nil {
		finding.Err = err
		return
	}
	leftovers, err := accountMCPLeftovers(raw, owned, shaped)
	if err != nil {
		finding.Err = err
	} else if len(leftovers) > 0 {
		finding.Verdict, finding.Detail = VerdictStrip, strings.Join(leftovers, ",")
	}
}

// layoutMCPShaped is the by-shape judge both MCP rows share: the host's pfm
// binary and the configured loopback port.
func layoutMCPShaped(env LayoutEnv) mcpShaped {
	return func(name string, registration map[string]any) bool {
		return pfmClaudeMCPShape(name, registration, env.Home, env.Config.MCP.HTTP.Port)
	}
}

func classifyZshrc(env LayoutEnv) LayoutFinding {
	path := filepath.Join(env.Home, ".zshrc")
	finding, info, exists := layoutLstat(layoutRowZshrc, path)
	if finding.Err != nil {
		return finding
	}
	if !exists {
		finding.Verdict = VerdictCreate
		return finding
	}
	if !info.Mode().IsRegular() {
		finding.Verdict, finding.Detail = VerdictRefuse, layoutNotRegular
		return finding
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		finding.Err = err
		return finding
	}
	want := sourceLine(filepath.Join(env.Clone, "pfm", "internal", "installer", "assets", "shim", "pfm.zsh"))
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == want {
			return finding
		}
		if isFleetSourceLine(line) {
			finding.Verdict, finding.Detail = VerdictRepoint, strings.TrimSpace(line)
			return finding
		}
	}
	finding.Verdict = VerdictCreate
	return finding
}

func classifyStagedPrompts(env LayoutEnv) LayoutFinding {
	path := filepath.Join(env.ManagedRoot, "harness-prompts")
	finding, info, exists := layoutLstat(layoutRowStagedPrompts, path)
	if finding.Err != nil || !exists {
		return finding
	}
	if !info.IsDir() {
		finding.Verdict, finding.Detail = VerdictRefuse, "not a directory"
		return finding
	}
	finding.Verdict = VerdictRemove
	count, err := stagedPromptUsers(env.ProcRoot, path)
	switch {
	case err != nil:
		finding.Err = err
	case count > 0:
		finding.Verdict, finding.Detail = VerdictRefuse, fmt.Sprintf("in use by %d live chats", count)
	}
	return finding
}

func classifySharedDB(env LayoutEnv) LayoutFinding {
	path := filepath.Join(env.Home, ".local", "state", "pfm", "shared.db")
	finding, info, exists := layoutLstat(layoutRowSharedDB, path)
	if finding.Err != nil || !exists {
		return finding
	}
	if !info.Mode().IsRegular() || info.Size() > 0 {
		finding.Verdict, finding.Detail = VerdictRefuse, fmt.Sprintf("not empty or regular (%d bytes)", info.Size())
	} else {
		finding.Verdict = VerdictRemove
	}
	return finding
}

func classifyStrayDir(path string) LayoutFinding {
	finding, info, exists := layoutLstat(layoutRowStrayDir, path)
	if finding.Err != nil || !exists {
		return finding
	}
	if !info.IsDir() {
		finding.Verdict, finding.Detail = VerdictRefuse, "not a directory"
		return finding
	}
	entries, err := os.ReadDir(path)
	switch {
	case err != nil:
		finding.Err = err
	case len(entries) > 0:
		finding.Verdict, finding.Detail = VerdictRefuse, fmt.Sprintf("contains %d entries", len(entries))
	default:
		finding.Verdict = VerdictRemove
	}
	return finding
}
