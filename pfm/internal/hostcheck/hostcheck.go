// Package hostcheck owns every host detector: each recognises one old or wrong shape on the host and returns a row with its fix line. pfm install runs them before any write and refuses on a BLOCK row; pfm doctor prints the same rows.
package hostcheck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type Severity string

const (
	Block             Severity = "BLOCK"
	Warn              Severity = "WARN"
	checkPFMMCP                = "pfm-mcp"
	classUnclassified          = "unclassified"
)

type Row struct {
	Severity Severity
	Check    string
	Path     string
	Problem  string
	Fix      string
}

// Line renders "{severity} {check} {path} — {problem}".
func (row Row) Line() string {
	return fmt.Sprintf("%s %s %s — %s", row.Severity, row.Check, row.Path, row.Problem)
}

// Render is the one printed form of a row, shared by pfm doctor and the
// install refusal: "{prefix}{Line}\n{prefix}  fix: {Fix}\n". The refusal
// carries the fix because a rolled-back binary's doctor may have no host checks.
func (row Row) Render(prefix string) string {
	return prefix + row.Line() + "\n" + prefix + "  fix: " + row.Fix + "\n"
}

type Env struct {
	Home, Store, ConfigPath, LegacyConfigDir, StateDB, CacheDB, ManagedRoot string
	ConfigExplicit                                                          bool
	HarvesterCacheDir                                                       string
	MCPPort                                                                 int
	Accounts                                                                []config.Account
	Now                                                                     time.Time
}

// EnvFor builds the read-only detector environment from the loaded runtime.
func EnvFor(runtime config.Runtime, now time.Time) Env {
	home := runtime.Paths.Home
	return Env{
		Home: home, Store: installer.ClaudeStore(home), ConfigPath: runtime.Config.Path,
		LegacyConfigDir: config.LegacyConfigDir(paths.OSEnv{}, home),
		StateDB:         runtime.Paths.StateDB, CacheDB: runtime.Paths.CacheDB,
		ManagedRoot: installer.ManagedRoot(home), ConfigExplicit: runtime.ConfigExplicit,
		HarvesterCacheDir: runtime.Config.Harvester.Cache.Dir,
		MCPPort:           runtime.Config.MCP.HTTP.Port, Accounts: runtime.Config.Accounts, Now: now,
	}
}

type Detector struct {
	Check  string
	Detect func(Env) ([]Row, error)
}

func Detectors() []Detector {
	return []Detector{
		{"legacy-config", legacyConfig},
		{"legacy-harvester-config", legacyHarvesterConfig},
		{"pre-split-config", preSplitConfig},
		{"legacy-state-db", detectLegacyStateDB},
		{"legacy-cache-db", detectLegacyCacheDB},
		{"legacy-harvester-cache", legacyHarvesterCache},
		{"pfm-settings", pfmSettings},
		{checkPFMMCP, pfmMCP},
		{"memory-helpers", memoryHelpers},
		{"staged-shim", stagedShim},
		{"staged-prompts", stagedPrompts},
		{"shared-db", sharedDB},
		{"stray-dir", strayDir},
		{"account-is-store", accountIsStore},
		{"store-identity", storeIdentity},
		{"home-state-file", homeStateFile},
		{"account-entry-real", accountEntryReal},
		{classUnclassified, unclassified},
		{"third-party-mcp", thirdPartyMCP},
		{"stale-state-tmp", staleStateTmp},
		{"beside-backup", besideBackup},
	}
}

func RunAll(env Env) []Row { return runDetectors(env, Detectors()) }

func runDetectors(env Env, detectors []Detector) []Row {
	var rows []Row
	for _, detector := range detectors {
		found, err := detector.Detect(env)
		rows = append(rows, found...)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			rows = append(rows, unreadable(detector.Check, detector.Check, err))
		}
	}
	return rows
}

func Count(rows []Row, severity Severity) int {
	count := 0
	for _, row := range rows {
		if row.Severity == severity {
			count++
		}
	}
	return count
}

func unreadable(check, path string, err error) Row {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		path, err = pathErr.Path, pathErr.Err
	}
	return Row{
		Block,
		check,
		path,
		fmt.Sprintf("UNREADABLE %s: %v", path, err),
		"make " + path + " readable to you, then rerun",
	}
}

func inspectPath(rows *[]Row, check, path string) fs.FileInfo {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		*rows = append(*rows, unreadable(check, path, err))
		return nil
	}
	return info
}

func readFile(rows *[]Row, check, path string) ([]byte, bool) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		*rows = append(*rows, unreadable(check, path, err))
		return nil, false
	}
	return raw, true
}

func readDir(rows *[]Row, check, path string) []fs.DirEntry {
	entries, err := os.ReadDir(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		*rows = append(*rows, unreadable(check, path, err))
	}
	return entries
}

func sortedAccounts(env Env) []config.Account {
	accounts := append([]config.Account(nil), env.Accounts...)
	sort.SliceStable(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	return accounts
}

func firstAccountDir(env Env) string {
	accounts := sortedAccounts(env)
	if len(accounts) == 0 {
		return config.DefaultAccountDir(env.Home, 1)
	}
	return accounts[0].ConfigDir
}

func claudeDirs(env Env) []string {
	dirs := []string{env.Store}
	for _, account := range sortedAccounts(env) {
		dirs = append(dirs, account.ConfigDir)
	}
	return uniquePaths(dirs)
}

func uniquePaths(candidates []string) []string {
	var result []string
	seen := map[string]bool{}
	for _, path := range candidates {
		physical := paths.PhysicalPath(path)
		if !seen[physical] {
			seen[physical] = true
			result = append(result, path)
		}
	}
	return result
}

func moveOrRemove(rows *[]Row, check, source, target string) (string, bool) {
	before := len(*rows)
	info := inspectPath(rows, check, target)
	if len(*rows) != before {
		return "", false
	}
	if info == nil {
		return "mv " + source + " " + target, true
	}
	return "diff " + source + " " + target + " && rm " + source, true
}
