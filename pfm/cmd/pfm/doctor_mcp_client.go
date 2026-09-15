package main

import (
	"fmt"
	"io"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/installer"
)

// printMCPClientCutover reports two disjoint surfaces: every user-scope
// Claude registry a pfm-launched Claude can actually read (installer.
// ClaudeUserRegistries — one row per file, naming why pfm considers it a
// registry, with both the "harvester" and "chat" server states so a
// registry pfm never reached reads as visibly absent rather than silently
// skipped), and the historical standalone-harvester cutover check for Codex
// and the project-scope .mcp.json (unaffected by CLAUDE_CONFIG_DIR).
func printMCPClientCutover(stdout io.Writer, runtime commandRuntime) int {
	warnings := 0
	registries := installer.ClaudeUserRegistries(
		runtime.Paths.Home,
		runtime.Config.Accounts,
		config.AmbientClaudeConfigDir(),
	)
	warnings += printClaudeRegistryRows(stdout, registries, runtime.Config.MCP.HTTP.Port)

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
	if warnings == 0 {
		fmt.Fprintln(stdout, "doctor: mcp client-cutover=complete")
	}
	return warnings
}

// printClaudeRegistryRows prints one row per registry ClaudeUserRegistries
// resolved — every row, not only the unhealthy ones, so a registry pfm never
// reaches is visible rather than silently absent from the report. A registry
// whose harvester/chat state is neither fully pfm nor fully absent-while-no-
// sibling-is-pfm is a warning naming the remediation; an unreadable file
// reports its parse error instead of guessing a state.
func printClaudeRegistryRows(stdout io.Writer, registries []installer.ClaudeRegistry, port int) int {
	type outcome struct {
		registry        installer.ClaudeRegistry
		harvester, chat string
		err             error
	}
	outcomes := make([]outcome, 0, len(registries))
	anyPFM := false
	for _, registry := range registries {
		out := outcome{registry: registry}
		for _, report := range installer.InspectClaudeServers(registry.Path, port, harvesterServer, chatCommand) {
			switch report.Name {
			case harvesterServer:
				out.harvester = report.State
			case chatCommand:
				out.chat = report.State
			}
			if report.Error != nil {
				out.err = report.Error
			}
			if report.State == installer.MCPClientPFM {
				anyPFM = true
			}
		}
		outcomes = append(outcomes, out)
	}
	warnings := 0
	for _, out := range outcomes {
		base := fmt.Sprintf("doctor: mcp client=claude registry=%s (%s) harvester=%s chat=%s",
			out.registry.Path, out.registry.Reason, out.harvester, out.chat)
		switch {
		case out.harvester == installer.MCPClientUnreadable || out.chat == installer.MCPClientUnreadable:
			warnings++
			fmt.Fprintf(stdout, "%s error=%v\n", base, out.err)
		case out.harvester == installer.MCPClientPFM && out.chat == installer.MCPClientPFM:
			fmt.Fprintln(stdout, base)
		case out.harvester == installer.MCPClientAbsent && out.chat == installer.MCPClientAbsent && !anyPFM:
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
