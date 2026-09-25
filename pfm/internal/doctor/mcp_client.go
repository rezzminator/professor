package doctor

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// PrintMCPClientCutover reports three disjoint surfaces: every user-scope
// Claude registry a pfm-launched Claude can actually read (installer.
// ClaudeUserRegistries — one row per file, naming why pfm considers it a
// registry, with its "professor" state and any pfm legacy "chat" / "harvester"
// entry still present, so a registry pfm never reached reads as visibly absent
// rather than silently skipped), the OpenCode config the same way, and the
// historical harvester cutover check for Codex and the project-scope .mcp.json
// (unaffected by CLAUDE_CONFIG_DIR), which tells pfm's own legacy entry apart
// from a foreign or standalone one.
func PrintMCPClientCutover(stdout io.Writer, runtime config.Runtime) int {
	warnings := 0
	registries := installer.ClaudeUserRegistries(
		runtime.Paths.Home,
		runtime.Config.Accounts,
		config.AmbientClaudeConfigDir(),
	)
	warnings += printClaudeRegistryRows(stdout, registries, runtime.Paths.Home, runtime.Config.MCP.HTTP.Port)

	codexHomes := make([]string, 0, len(runtime.Config.CodexAccounts))
	for _, account := range runtime.Config.CodexAccounts {
		codexHomes = append(codexHomes, account.Home)
	}
	for _, report := range installer.InspectHarvesterClientCutover(runtime.Paths.Home, runtime.Config.MCP.HTTP.Port, []string{}, codexHomes) {
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

// printClaudeRegistryRows prints one row per registry ClaudeUserRegistries
// resolved — every row, not only the unhealthy ones, so a registry pfm never
// reaches is visible rather than silently absent from the report. A registry
// whose `professor` is not pfm's — absent while a sibling registry holds pfm's,
// or foreign — or that still holds a pfm legacy entry is a warning naming the
// remediation; an unreadable file reports its parse error instead of guessing
// a state.
func printClaudeRegistryRows(stdout io.Writer, registries []installer.ClaudeRegistry, home string, port int) int {
	type outcome struct {
		registry      installer.ClaudeRegistry
		state, legacy string
		err           error
	}
	outcomes := make([]outcome, 0, len(registries))
	anyPFM := false
	for _, registry := range registries {
		out := outcome{registry: registry}
		out.state, out.legacy, out.err = professorState(installer.InspectClaudeServers(
			registry.Path,
			home,
			port,
			config.MCPServerProfessor,
			config.MCPServerChat,
			config.MCPServerHarvester,
		))
		if out.state == installer.MCPClientPFM {
			anyPFM = true
		}
		outcomes = append(outcomes, out)
	}
	warnings := 0
	for _, out := range outcomes {
		base := fmt.Sprintf("doctor: mcp client=claude registry=%s (%s) professor=%s%s",
			out.registry.Path, out.registry.Reason, out.state, out.legacy)
		switch {
		case out.state == installer.MCPClientUnreadable:
			warnings++
			fmt.Fprintf(stdout, "%s error=%v\n", base, out.err)
		case out.legacy == "" && out.state == installer.MCPClientPFM:
			fmt.Fprintln(stdout, base)
		case out.legacy == "" && out.state == installer.MCPClientAbsent && !anyPFM:
			fmt.Fprintln(stdout, base)
		default:
			warnings++
			fmt.Fprintf(
				stdout,
				"%s remediation=run pfm install --yes (registers every registry a pfm-launched Claude reads)\n",
				base,
			)
		}
	}
	return warnings
}
