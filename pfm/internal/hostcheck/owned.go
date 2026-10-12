package hostcheck

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func appendProbeError(rows *[]Row, check, path string, err error) {
	row := unreadable(check, path, err)
	for _, prior := range *rows {
		if prior.Path == row.Path && prior.Problem == row.Problem {
			return
		}
	}
	*rows = append(*rows, row)
}

func pfmSettings(env Env) ([]Row, error) {
	var rows []Row
	var candidates []string
	for _, dir := range claudeDirs(env) {
		candidates = append(candidates, filepath.Join(dir, "settings.json"))
	}
	for _, path := range uniquePaths(candidates) {
		keys, err := installer.PFMSettingsLeftovers(env.Home, path)
		if err != nil {
			appendProbeError(&rows, "pfm-settings", path, err)
			continue
		}
		if len(keys) > 0 {
			items := strings.Join(keys, ", ")
			rows = append(
				rows,
				Row{
					Block,
					"pfm-settings",
					path,
					"carries pfm's " + items + " — pfm supplies its own hooks and status lines through " + claudelaunch.SettingsFlag + " at launch, so these entries are leftovers of an earlier install",
					"remove " + items + " from " + path + " (each hook entry running that command and each named key; keep every other entry)",
				},
			)
		}
	}
	return rows, nil
}

func pfmMCP(env Env) ([]Row, error) {
	var rows []Row
	var candidates []string
	for _, registry := range installer.ClaudeUserRegistries(env.Home, sortedAccounts(env), "") {
		candidates = append(candidates, registry.Path)
	}
	homePath := filepath.Join(env.Home, ".mcp.json")
	homeNames, clients, homeErr := installer.PFMHomeMCPLeftovers(env.Home, env.MCPPort)
	candidates = append(candidates, filepath.Join(env.Home, ".claude.json"), homePath)
	for _, path := range uniquePaths(candidates) {
		names, err := installer.PFMMCPLeftovers(env.Home, env.MCPPort, path)
		if err != nil {
			appendProbeError(&rows, checkPFMMCP, path, err)
			continue
		}
		if paths.PhysicalPath(path) == paths.PhysicalPath(homePath) {
			if homeErr != nil {
				appendProbeError(&rows, checkPFMMCP, homePath, homeErr)
				continue
			}
			seen := map[string]bool{}
			for _, name := range append(names, homeNames...) {
				seen[name] = true
			}
			names = nil
			for name := range seen {
				names = append(names, name)
			}
			sort.Strings(names)
		}
		rows = appendMCPRow(rows, path, names)
	}
	if len(clients) > 0 {
		ledger := filepath.Join(env.ManagedRoot, "mcp-ownership.json")
		rows = append(
			rows,
			Row{
				Block,
				checkPFMMCP,
				ledger,
				"mcp-ownership.json still records pfm clients",
				"remove \"clients\" from " + ledger,
			},
		)
	}
	return rows, nil
}

func appendMCPRow(rows []Row, path string, names []string) []Row {
	if len(names) == 0 {
		return rows
	}
	plain := make([]string, len(names))
	for i, name := range names {
		plain[i] = strings.TrimPrefix(name, "mcpServers.")
	}
	text := strings.Join(plain, ",")
	return append(
		rows,
		Row{
			Block,
			checkPFMMCP,
			path,
			"carries pfm mcpServers." + text,
			"remove mcpServers." + text + " from " + path + "; pfm's server rides " + claudelaunch.MCPConfigFlag + " at launch",
		},
	)
}

func memoryHelpers(env Env) ([]Row, error) {
	var rows []Row
	for _, dir := range claudeDirs(env) {
		for _, name := range []string{"cc-memory-wire.sh", "cc-memory-consolidate.sh"} {
			path := filepath.Join(dir, "scripts", name)
			info := inspectPath(&rows, "memory-helpers", path)
			if info == nil || !info.Mode().IsRegular() {
				continue
			}
			newName, matched, err := installer.RetiredMemoryHelper(path)
			if err != nil {
				rows = append(rows, unreadable("memory-helpers", path, err))
				continue
			}
			if matched {
				target := filepath.Join(dir, "scripts", newName)
				fix := "mv " + path + " " + target + ", then replace " + path + " with " + target + " in every hook command of " + filepath.Join(
					dir,
					"settings.json",
				)
				rows = append(
					rows,
					Row{Block, "memory-helpers", path, "pfm's memory helper under its retired name", fix},
				)
			}
		}
	}
	return rows, nil
}

func thirdPartyMCP(env Env) ([]Row, error) {
	var rows []Row
	var candidates []string
	for _, account := range sortedAccounts(env) {
		candidates = append(candidates, filepath.Join(account.ConfigDir, ".claude.json"))
	}
	candidates = append(candidates, filepath.Join(env.Home, ".claude.json"))
	for _, path := range uniquePaths(candidates) {
		raw, ok := readFile(&rows, checkThirdPartyMCP, path)
		if !ok {
			continue
		}
		var document struct {
			Servers map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			rows = append(rows, unreadable(checkThirdPartyMCP, path, err))
			continue
		}
		owned, err := installer.PFMMCPLeftovers(env.Home, env.MCPPort, path)
		if err != nil {
			appendProbeError(&rows, checkThirdPartyMCP, path, err)
			continue
		}
		excluded := map[string]bool{}
		for _, name := range owned {
			excluded[strings.TrimPrefix(name, "mcpServers.")] = true
		}
		var names []string
		for name := range document.Servers {
			if !excluded[name] {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			reason := ""
			for _, item := range env.AcceptedMCP {
				if item.Path == path && item.Server == name {
					reason = item.Reason
					break
				}
			}
			if reason != "" {
				rows = append(
					rows,
					Row{
						Accepted,
						checkThirdPartyMCP,
						path,
						"mcpServers." + name + " retained by doctor.acceptedMCP: " + reason,
						"",
					},
				)
				continue
			}
			rows = append(
				rows,
				Row{
					Warn,
					checkThirdPartyMCP,
					path,
					"mcpServers." + name + " is declared outside pfm",
					fmt.Sprintf(
						"move it to mcp.thirdParty.%s in %s, then remove mcpServers.%s from %s",
						name,
						env.ConfigPath,
						name,
						path,
					),
				},
			)
		}
	}
	return rows, nil
}
