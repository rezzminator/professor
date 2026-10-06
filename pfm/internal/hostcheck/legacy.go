package hostcheck

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	preSplitMCPPort           = 8377
	checkLegacyStateDB        = "legacy-state-db"
	checkLegacyCacheDB        = "legacy-cache-db"
	checkLegacyHarvesterCache = "legacy-harvester-cache"
	checkStagedPrompts        = "staged-prompts"
	checkSharedDB             = "shared-db"
)

const legacyConfigCheck = "legacy-config"

func legacyConfig(env Env) ([]Row, error) {
	var rows []Row
	if env.ConfigExplicit {
		if !inStore(env.LegacyConfigDir, filepath.Dir(env.ConfigPath)) {
			return rows, nil
		}
		if _, ok := readFile(&rows, legacyConfigCheck, env.ConfigPath); !ok {
			return rows, nil
		}
		fix := "move " + env.ConfigPath + " into the clone as pfm.config.json, then run pfm install from the clone"
		if env.CloneConfigPath != "" {
			var ok bool
			fix, ok = moveOrRemove(&rows, legacyConfigCheck, env.ConfigPath, env.CloneConfigPath)
			if !ok {
				return rows, nil
			}
		}
		rows = append(
			rows,
			Row{Block, legacyConfigCheck, env.ConfigPath, "explicit --config inside the legacy config dir", fix},
		)
		return rows, nil
	}
	for _, name := range []string{config.FileName, "config.json"} {
		path := filepath.Join(env.LegacyConfigDir, name)
		if paths.PhysicalPath(path) == paths.PhysicalPath(env.ConfigPath) {
			continue
		}
		if _, ok := readFile(&rows, legacyConfigCheck, path); !ok {
			continue
		}
		if fix, ok := moveOrRemove(&rows, legacyConfigCheck, path, env.ConfigPath); ok {
			rows = append(rows, Row{Block, legacyConfigCheck, path, "legacy pfm config outside the clone", fix})
		}
	}
	return rows, nil
}

func legacyHarvesterConfig(env Env) ([]Row, error) {
	var rows []Row
	path := filepath.Join(env.LegacyConfigDir, "harvester.config.json")
	target := filepath.Join(filepath.Dir(env.ConfigPath), "harvester.config.json")
	if paths.PhysicalPath(path) == paths.PhysicalPath(target) {
		return rows, nil
	}
	if _, ok := readFile(&rows, "legacy-harvester-config", path); ok {
		if fix, ok := moveOrRemove(&rows, "legacy-harvester-config", path, target); ok {
			rows = append(
				rows,
				Row{Block, "legacy-harvester-config", path, "legacy harvester config outside the clone", fix},
			)
		}
	}
	return rows, nil
}

func preSplitConfig(env Env) ([]Row, error) {
	var rows []Row
	const check = "pre-split-config"
	dir := filepath.Dir(env.ConfigPath)
	raw, exists := readFile(&rows, check, env.ConfigPath)
	if exists {
		var document struct {
			MCP struct {
				Servers map[string]json.RawMessage `json:"servers"`
				HTTP    struct {
					Port int `json:"port"`
				} `json:"http"`
			} `json:"mcp"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			rows = append(rows, unreadable(check, env.ConfigPath, err))
			rows = append(rows, besideConfig(env)...)
		} else {
			if filepath.Base(env.ConfigPath) == "config.json" {
				rows = append(
					rows,
					Row{
						Block,
						check,
						env.ConfigPath,
						"the config still has its pre-split name",
						"mv " + env.ConfigPath + " " + filepath.Join(dir, config.FileName),
					},
				)
			}
			// The beside-file finding precedes key findings in the contract.
			rows = append(rows, besideConfig(env)...)
			if harvester, present := document.MCP.Servers["harvester"]; present {
				var server struct {
					Enabled json.RawMessage `json:"enabled"`
				}
				if err := json.Unmarshal(harvester, &server); err != nil {
					rows = append(rows, unreadable(check, env.ConfigPath, err))
				} else {
					value := string(server.Enabled)
					if value == "" {
						value = "null"
					}
					fix := fmt.Sprintf(
						"move mcp.servers.harvester.enabled (%s) to \"enabled\" in %s, then delete mcp.servers.harvester from %s",
						value,
						filepath.Join(dir, "harvester.config.json"),
						env.ConfigPath,
					)
					rows = append(
						rows,
						Row{
							Block,
							check,
							env.ConfigPath,
							"mcp.servers.harvester belongs in harvester.config.json",
							fix,
						},
					)
				}
			}
			if document.MCP.HTTP.Port == preSplitMCPPort {
				rows = append(
					rows,
					Row{
						Block,
						check,
						env.ConfigPath,
						"mcp.http.port is the pre-split default 8377",
						fmt.Sprintf(
							"set mcp.http.port to %d in %s; pfm install re-wires every client",
							config.DefaultMCPPort,
							env.ConfigPath,
						),
					},
				)
			}
		}
	} else {
		rows = append(rows, besideConfig(env)...)
	}
	return rows, nil
}

func besideConfig(env Env) []Row {
	var rows []Row
	if filepath.Base(env.ConfigPath) != config.FileName {
		return rows
	}
	path := filepath.Join(filepath.Dir(env.ConfigPath), "config.json")
	if raw, ok := readFile(&rows, "pre-split-config", path); ok {
		var document map[string]json.RawMessage
		if err := json.Unmarshal(raw, &document); err != nil {
			rows = append(rows, unreadable("pre-split-config", path, err))
		} else if fix, ok := removeKeeping(
			&rows,
			"pre-split-config",
			env.ConfigPath,
			"rm "+path+" once its content is in "+env.ConfigPath,
			path,
		); ok {
			rows = append(
				rows,
				Row{
					Block,
					"pre-split-config",
					path,
					"a pre-split config.json beside the config",
					fix,
				},
			)
		}
	}
	return rows
}

func detectLegacyStateDB(env Env) ([]Row, error) {
	return legacyDB(checkLegacyStateDB, "state", paths.LegacyStateDB(env.Home), env.StateDB)
}

func detectLegacyCacheDB(env Env) ([]Row, error) {
	return legacyDB(checkLegacyCacheDB, "cache", paths.LegacyCacheDB(env.Home), env.CacheDB)
}

func legacyDB(check, kind, legacy, target string) ([]Row, error) {
	var rows []Row
	if paths.PhysicalPath(legacy) == paths.PhysicalPath(target) {
		return rows, nil
	}
	present := []string{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if inspectPath(&rows, check, legacy+suffix) != nil {
			present = append(present, suffix)
		}
	}
	if len(present) == 0 {
		return rows, nil
	}
	before := len(rows)
	targetInfo := inspectPath(&rows, check, target)
	if len(rows) != before {
		return rows, nil
	}
	var fix string
	if targetInfo != nil {
		removes := make([]string, 0, len(present))
		for _, suffix := range present {
			removes = append(removes, legacy+suffix)
		}
		var ok bool
		fix, ok = removeKeeping(
			&rows,
			check,
			target,
			"both exist — keep "+target+": rm "+legacy+" "+legacy+"-wal "+legacy+"-shm",
			removes...,
		)
		if !ok {
			return rows, nil
		}
	} else {
		fix = "close every chat, stop pfm's services, then: "
		for i, suffix := range present {
			if i > 0 {
				fix += " && "
			}
			fix += "mv " + legacy + suffix + " " + target + suffix
		}
	}
	rows = append(rows, Row{Block, check, legacy, "legacy " + kind + " database at the pre-layout path", fix})
	return rows, nil
}

func legacyHarvesterCache(env Env) ([]Row, error) {
	var rows []Row
	if env.HarvesterCacheDir != "" {
		return rows, nil
	}
	const check = checkLegacyHarvesterCache
	path, target := paths.LegacyHarvesterCacheDir(env.Home), paths.HarvesterCacheDir(env.Home)
	if inspectPath(&rows, check, path) == nil {
		return rows, nil
	}
	before := len(rows)
	readDir(&rows, check, path)
	if len(rows) != before {
		return rows, nil
	}
	info := inspectPath(&rows, check, target)
	if len(rows) != before {
		return rows, nil
	}
	fix := "mv " + path + " " + target
	if info != nil {
		var ok bool
		if fix, ok = removeKeeping(&rows, check, target, "rm -r "+path, path); !ok {
			return rows, nil
		}
	}
	rows = append(rows, Row{Warn, check, path, "pre-rename harvester cache dir", fix})
	return rows, nil
}

func stagedShim(env Env) ([]Row, error) {
	var rows []Row
	path := filepath.Join(env.Home, ".zshrc")
	if raw, ok := readFile(&rows, "staged-shim", path); ok {
		for _, line := range strings.Split(string(raw), "\n") {
			if installer.IsStagedShimLine(line) {
				rows = append(
					rows,
					Row{
						Block,
						"staged-shim",
						path,
						"sources pfm's retired staged shim",
						"delete the line \"" + line + "\" from " + path + "; pfm install writes the clone's source line",
					},
				)
			}
		}
	}
	return rows, nil
}

func stagedPrompts(env Env) ([]Row, error) {
	var rows []Row
	path := filepath.Join(env.ManagedRoot, "harness-prompts")
	if inspectPath(&rows, checkStagedPrompts, path) != nil {
		before := len(rows)
		readDir(&rows, checkStagedPrompts, path)
		if len(rows) == before {
			rows = append(
				rows,
				Row{
					Warn,
					checkStagedPrompts,
					path,
					"retired staged prompt dir",
					"rm -r " + path + " once no chat started before the move is open",
				},
			)
		}
	}
	return rows, nil
}

func sharedDB(env Env) ([]Row, error) {
	var rows []Row
	path := filepath.Join(env.Home, ".local", "state", "pfm", "shared.db")
	if raw, ok := readFile(&rows, checkSharedDB, path); ok {
		problem := "retired empty shared.db"
		if len(raw) != 0 {
			problem = fmt.Sprintf("retired shared.db holds %d bytes", len(raw))
		}
		rows = append(rows, Row{Warn, checkSharedDB, path, problem, "rm " + path})
	}
	return rows, nil
}

func strayDir(env Env) ([]Row, error) {
	var rows []Row
	for _, name := range []string{".git", ".codex", ".agents"} {
		path := filepath.Join(filepath.Dir(config.DefaultAccountDir(env.Home, 1)), name)
		info := inspectPath(&rows, "stray-dir", path)
		if info == nil {
			continue
		}
		problem, fix := "not a directory", "inspect, then rm -r "+path
		if info.IsDir() {
			before := len(rows)
			entries := readDir(&rows, "stray-dir", path)
			if len(rows) != before {
				continue
			}
			problem = fmt.Sprintf("stray dir holds %d entries", len(entries))
			if len(entries) == 0 {
				problem, fix = "empty stray dir", "rmdir "+path
			}
		}
		rows = append(rows, Row{Warn, "stray-dir", path, problem, fix})
	}
	return rows, nil
}
