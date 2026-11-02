package hostcheck

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/installer"
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
	if row, unread := retiredUnread(env, "pfm-settings"); unread {
		return []Row{row}, nil
	}
	var rows []Row
	var candidates []string
	for _, dir := range claudeDirs(env) {
		candidates = append(candidates, filepath.Join(dir, "settings.json"))
	}
	for _, path := range uniquePaths(candidates) {
		keys, err := installer.PFMSettingsLeftovers(env.Retired, env.Home, path)
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
		var names []string
		for name := range document.Servers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			reason, accepted := "", false
			for _, item := range env.AcceptedMCP {
				if item.Path == path && item.Server == name {
					reason, accepted = item.Reason, true
					break
				}
			}
			if accepted {
				// The reason is optional: an acceptance without one is still an acceptance.
				detail := "mcpServers." + name + " retained by doctor.acceptedMCP"
				if reason != "" {
					detail += ": " + reason
				}
				rows = append(rows, Row{Accepted, checkThirdPartyMCP, path, detail, ""})
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
