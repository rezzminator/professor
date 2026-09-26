package doctor

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// PrintMCPClientCutover reports the historical harvester cutover check for
// Codex and the project-scope .mcp.json, which tells pfm's own legacy entry
// apart from a foreign or standalone one, then each Codex account home's
// config.toml and the OpenCode config with their "professor" state and any pfm
// legacy "chat" / "harvester" entry. Claude has no registry row: its MCP
// servers ride --mcp-config at launch and pfm writes no account .claude.json.
func PrintMCPClientCutover(stdout io.Writer, runtime config.Runtime) int {
	warnings := 0

	codexHomes := make([]string, 0, len(runtime.Config.CodexAccounts))
	for _, account := range runtime.Config.CodexAccounts {
		codexHomes = append(codexHomes, account.Home)
	}
	for _, report := range installer.InspectHarvesterClientCutover(runtime.Paths.Home, runtime.Config.MCP.HTTP.Port, codexHomes) {
		switch report.State {
		case installer.MCPClientAbsent, installer.MCPClientPFM:
			continue
		case installer.MCPClientUnreadable:
			warnings++
			fmt.Fprintf(
				stdout,
				"doctor: mcp client=%s harvester=unreadable error=%v path=%s\n",
				report.Client,
				report.Error,
				report.Path,
			)
		case installer.MCPClientLegacyPFM:
			warnings++
			fmt.Fprintf(
				stdout,
				"doctor: mcp client=%s harvester=%s remediation=run pfm install --yes path=%s\n",
				report.Client,
				report.State,
				report.Path,
			)
		default:
			warnings++
			fmt.Fprintf(
				stdout,
				"doctor: mcp client=%s harvester=%s warning=consumer cutover incomplete remediation=repoint to PFM, verify it, then remove the foreign registration path=%s\n",
				report.Client,
				report.State,
				report.Path,
			)
		}
	}
	for _, home := range codexHomes {
		warnings += printCodexRow(stdout, runtime, filepath.Join(home, "config.toml"))
	}
	warnings += printOpenCodeRows(stdout, runtime.Paths.Home, runtime.Config.MCP.HTTP.Port)
	if warnings == 0 {
		fmt.Fprintln(stdout, "doctor: mcp client-cutover=complete")
	}
	return warnings
}

func printOpenCodeRows(stdout io.Writer, home string, port int) int {
	path := installer.OpenCodeConfigPath(home)
	state, legacy, inspectionError := professorState(installer.InspectOpenCodeServers(
		path,
		home,
		port,
		config.MCPServerProfessor,
		config.MCPServerChat,
		config.MCPServerHarvester,
	))
	base := fmt.Sprintf("doctor: mcp client=opencode config=%s professor=%s%s", path, state, legacy)
	switch {
	case state == installer.MCPClientUnreadable:
		fmt.Fprintf(stdout, "%s error=%v\n", base, inspectionError)
		return 1
	case legacy == "" && (state == installer.MCPClientPFM || state == installer.MCPClientAbsent):
		fmt.Fprintln(stdout, base)
		return 0
	default:
		fmt.Fprintf(stdout, "%s %s\n", base, openCodeRemediation(home, path, state))
		return 1
	}
}

// printCodexRow prints one Codex account home's config.toml row: its
// `professor` state and any pfm legacy `chat` table still present (the
// harvester key is the cutover check's, so nothing is counted twice). pfm
// install replaces only the tables it wrote, so a foreign `professor` is named
// as user-owned; an absent one warns only while an MCP family is enabled.
func printCodexRow(stdout io.Writer, runtime config.Runtime, path string) int {
	state, legacy, inspectionError := professorState(installer.InspectCodexServers(
		path,
		runtime.Paths.Home,
		runtime.Config.MCP.HTTP.Port,
		config.MCPServerProfessor,
		config.MCPServerChat,
	))
	base := fmt.Sprintf("doctor: mcp client=codex config=%s professor=%s%s", path, state, legacy)
	const reinstall = "remediation=run pfm install --yes"
	switch {
	case state == installer.MCPClientUnreadable:
		fmt.Fprintf(stdout, "%s error=%v\n", base, inspectionError)
	case state == installer.MCPClientForeignRegistration:
		fmt.Fprintf(
			stdout,
			"%s remediation=%s in %s is a user-owned entry pfm install will not replace — remove or rename it, "+
				"then run pfm install --yes\n",
			base,
			config.MCPServerProfessor,
			path,
		)
	case legacy != "" || (state == installer.MCPClientAbsent && mcpConfigured(runtime)):
		fmt.Fprintf(stdout, "%s %s\n", base, reinstall)
	default:
		fmt.Fprintln(stdout, base)
		return 0
	}
	return 1
}

// professorState folds one file's reports — `professor` and the legacy
// `chat` / `harvester` keys — into the professor state, the ` legacy=<keys>`
// suffix (the sorted keys still holding pfm's legacy shape, or ""), and the
// error that made the file unreadable. A legacy key that is merely not pfm's
// shape is someone else's entry and not this row's business.
func professorState(reports []installer.MCPClientCutover) (state, legacy string, err error) {
	keys := []string{}
	for _, report := range reports {
		if report.Name == config.MCPServerProfessor {
			state, err = report.State, report.Error
			continue
		}
		if report.State == installer.MCPClientLegacyPFM {
			keys = append(keys, report.Name)
		}
	}
	if len(keys) > 0 {
		sort.Strings(keys)
		legacy = " legacy=" + strings.Join(keys, ",")
	}
	return state, legacy, err
}

// openCodeRemediation names what the operator can actually do about an
// unhealthy OpenCode registration. `pfm install --yes` rewrites a `professor`
// entry install itself wrote, removes pfm's legacy entries, and PRESERVES a
// `professor` it did not write — so an entry the ownership ledger does not
// claim is named as user-owned with its file and key; prescribing the
// reinstall there would be advice that can never fix what it named. A ledger
// that could not be read says so rather than passing for "no user-owned entry
// here".
func openCodeRemediation(home, path, state string) string {
	const reinstall = "remediation=run pfm install --yes"
	if state == installer.MCPClientPFM {
		// pfm's own professor shape needs no replacing; only the legacy
		// entries remain, and install removes those whoever wrote them.
		return reinstall
	}
	unowned, err := installer.OpenCodeUnownedEntries(home, path, config.MCPServerProfessor)
	switch {
	case err != nil:
		return fmt.Sprintf("ownership=unreadable error=%v %s", err, reinstall)
	case len(unowned) > 0:
		return fmt.Sprintf(
			"remediation=%s in %s is a user-owned entry pfm install will not replace — remove or rename it, "+
				"then run pfm install --yes",
			strings.Join(unowned, " and "),
			path,
		)
	default:
		return reinstall
	}
}
