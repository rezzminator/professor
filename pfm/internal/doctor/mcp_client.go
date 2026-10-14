package doctor

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// PrintMCPClientCutover reports the historical harvester cutover check for
// Codex and the project-scope .mcp.json, which tells pfm's own legacy entry
// apart from a foreign or standalone one, then each Codex account home's
// config.toml and the OpenCode config with their "professor" state and any pfm
// legacy "chat" / "harvester" entry. Claude has no registry row: its MCP
// servers ride --mcp-config at launch and pfm writes no account .claude.json.
func PrintMCPClientCutover(stdout io.Writer, runtime config.Runtime) int {
	warnings := printClaudeMCPRows(stdout, runtime)

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
	warnings += printOpenCodeRows(stdout, runtime)
	if warnings == 0 {
		fmt.Fprintln(stdout, "doctor: mcp client-cutover=complete")
	}
	return warnings
}

func printOpenCodeRows(stdout io.Writer, runtime config.Runtime) int {
	home, port := runtime.Paths.Home, runtime.Config.MCP.HTTP.Port
	warnings := 0
	for _, family := range []string{config.MCPServerChat, config.MCPServerHarvester} {
		warnings += printOpenCodeFamilyRow(
			stdout,
			home,
			port,
			config.MCPRegistrationName(family),
			runtime.Config.MCPServers[family].Enabled,
		)
	}
	warnings += printLegacyProfessorRow(
		stdout,
		installer.InspectOpenCodeServers(installer.OpenCodeConfigPath(home), home, port, "professor"),
	)
	return warnings
}

func printOpenCodeFamilyRow(stdout io.Writer, home string, port int, name string, enabled bool) int {
	path := installer.OpenCodeConfigPath(home)
	state, legacy, inspectionError := professorState(installer.InspectOpenCodeServers(
		path,
		home,
		port,
		name,
		"professor",
		strings.TrimSuffix(name, "-professor"),
		config.MCPServerHarvester,
	), name)
	base := fmt.Sprintf("doctor: mcp client=opencode config=%s %s=%s%s", path, name, state, legacy)
	switch {
	case state == installer.MCPClientUnreadable:
		fmt.Fprintf(stdout, "%s error=%v\n", base, inspectionError)
		return 1
	case legacy == "" && ((state == installer.MCPClientPFM && enabled) || (state == installer.MCPClientAbsent && !enabled)):
		fmt.Fprintln(stdout, base)
		return 0
	default:
		fmt.Fprintf(stdout, "%s %s\n", base, openCodeRemediation(home, path, state, name))
		return 1
	}
}

// printCodexRow prints one Codex account home's config.toml row: its
// `professor` state and any pfm legacy `chat` table still present (the
// harvester key is the cutover check's, so nothing is counted twice). pfm
// install replaces only the tables it wrote, so a foreign `professor` is named
// as user-owned; an absent one warns only while an MCP family is enabled.
func printCodexRow(stdout io.Writer, runtime config.Runtime, path string) int {
	warnings := 0
	for _, family := range []string{config.MCPServerChat, config.MCPServerHarvester} {
		warnings += printCodexFamilyRow(
			stdout,
			runtime,
			path,
			config.MCPRegistrationName(family),
			runtime.Config.MCPServers[family].Enabled,
		)
	}
	warnings += printLegacyProfessorRow(
		stdout,
		installer.InspectCodexServers(path, runtime.Paths.Home, runtime.Config.MCP.HTTP.Port, "professor"),
	)
	return warnings
}

func printCodexFamilyRow(stdout io.Writer, runtime config.Runtime, path, name string, enabled bool) int {
	state, legacy, inspectionError := professorState(installer.InspectCodexServers(
		path,
		runtime.Paths.Home,
		runtime.Config.MCP.HTTP.Port,
		name,
		"professor",
		strings.TrimSuffix(name, "-professor"),
	), name)
	base := fmt.Sprintf("doctor: mcp client=codex config=%s %s=%s%s", path, name, state, legacy)
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
			name,
			path,
		)
	case legacy != "" || (state == installer.MCPClientAbsent && enabled):
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
func professorState(reports []installer.MCPClientCutover, names ...string) (state, legacy string, err error) {
	name := "professor"
	if len(names) > 0 {
		name = names[0]
	}
	keys := []string{}
	for _, report := range reports {
		if report.Name == name {
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
func openCodeRemediation(home, path, state string, names ...string) string {
	name := "professor"
	if len(names) > 0 {
		name = names[0]
	}
	const reinstall = "remediation=run pfm install --yes"
	if state == installer.MCPClientPFM {
		// pfm's own professor shape needs no replacing; only the legacy
		// entries remain, and install removes those whoever wrote them.
		return reinstall
	}
	unowned, err := installer.OpenCodeUnownedEntries(home, path, name)
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

func printLegacyProfessorRow(stdout io.Writer, reports []installer.MCPClientCutover) int {
	for _, report := range reports {
		if report.State == installer.MCPClientForeignRegistration {
			fmt.Fprintf(
				stdout,
				"doctor: mcp client=%s config=%s professor=%s warning=user-owned entry preserved; remove or rename it if unwanted\n",
				report.Client,
				report.Path,
				report.State,
			)
			return 1
		}
	}
	return 0
}

func printClaudeMCPRows(stdout io.Writer, runtime config.Runtime) int {
	warnings := 0
	for _, registry := range installer.ClaudeMCPRegistries(runtime.Paths.Home, runtime.Config.Accounts, paths.OSEnv{}.Get("CLAUDE_CONFIG_DIR")) {
		reports := installer.InspectClaudeServers(
			registry.Path,
			runtime.Paths.Home,
			runtime.Config.MCP.HTTP.Port,
			"chat-professor",
			"harvester-professor",
			"professor",
			"chat",
			"harvester",
		)
		for _, report := range reports {
			if report.State == installer.MCPClientAbsent {
				continue
			}
			if report.State == installer.MCPClientUnreadable {
				fmt.Fprintf(
					stdout,
					"doctor: mcp client=claude config=%s state=unreadable error=%v\n",
					registry.Path,
					report.Error,
				)
				warnings++
				break
			}
			fmt.Fprintf(stdout, "doctor: mcp client=claude config=%s %s=%s", registry.Path, report.Name, report.State)
			switch report.State {
			case installer.MCPClientPFM:
			case installer.MCPClientLegacyPFM:
				fmt.Fprint(stdout, " remediation=run pfm install --yes")
				warnings++
			default:
				fmt.Fprint(stdout, " warning=user-owned entry preserved; remove or rename it if unwanted")
				warnings++
			}
			fmt.Fprintln(stdout)
		}
	}
	return warnings
}
