package doctor

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// PrintMCPClientCutover checks each Codex account's config.toml and the
// OpenCode config for pfm's family registrations. Claude receives them through
// --mcp-config at launch, so no Claude registry is checked.
func PrintMCPClientCutover(stdout io.Writer, runtime config.Runtime) int {
	warnings := 0
	for _, account := range runtime.Config.CodexAccounts {
		warnings += printCodexRow(stdout, runtime, filepath.Join(account.Home, "config.toml"))
	}
	warnings += printOpenCodeRows(stdout, runtime)
	if warnings == 0 {
		fmt.Fprintln(stdout, "doctor: mcp client-cutover=complete")
	}
	return warnings
}

func printOpenCodeRows(stdout io.Writer, runtime config.Runtime) int {
	warnings := 0
	for _, family := range []string{config.MCPServerChat} {
		warnings += printOpenCodeFamilyRow(
			stdout,
			runtime.Paths.Home,
			config.MCPRegistrationName(family),
			runtime.Config.MCPServers[family].Enabled,
		)
	}
	return warnings
}

func printOpenCodeFamilyRow(stdout io.Writer, home, name string, enabled bool) int {
	path := installer.OpenCodeConfigPath(home)
	report := installer.InspectOpenCodeServers(path, home, name)[0]
	base := fmt.Sprintf("doctor: mcp client=opencode config=%s %s=%s", path, name, report.State)
	switch {
	case report.State == installer.MCPClientUnreadable:
		fmt.Fprintf(stdout, "%s error=%v\n", base, report.Error)
		return 1
	case (report.State == installer.MCPClientPFM && enabled) || (report.State == installer.MCPClientAbsent && !enabled):
		fmt.Fprintln(stdout, base)
		return 0
	default:
		fmt.Fprintf(stdout, "%s %s\n", base, openCodeRemediation(home, path, name))
		return 1
	}
}

// printCodexRow prints one Codex account home's config.toml row per family.
// pfm install replaces only the tables it wrote, so a foreign table is named
// as user-owned; an absent one warns only while its family is enabled.
func printCodexRow(stdout io.Writer, runtime config.Runtime, path string) int {
	warnings := 0
	for _, family := range []string{config.MCPServerChat} {
		warnings += printCodexFamilyRow(
			stdout,
			runtime,
			path,
			config.MCPRegistrationName(family),
			runtime.Config.MCPServers[family].Enabled,
		)
	}
	return warnings
}

func printCodexFamilyRow(stdout io.Writer, runtime config.Runtime, path, name string, enabled bool) int {
	report := installer.InspectCodexServers(path, runtime.Paths.Home, name)[0]
	base := fmt.Sprintf("doctor: mcp client=codex config=%s %s=%s", path, name, report.State)
	switch {
	case report.State == installer.MCPClientUnreadable:
		fmt.Fprintf(stdout, "%s error=%v\n", base, report.Error)
	case report.State == installer.MCPClientForeignRegistration:
		fmt.Fprintf(
			stdout,
			"%s remediation=%s in %s is a user-owned entry pfm install will not replace — remove or rename it, "+
				"then run pfm install --yes\n",
			base,
			name,
			path,
		)
	case (report.State == installer.MCPClientAbsent && enabled) || (report.State == installer.MCPClientPFM && !enabled):
		fmt.Fprintf(stdout, "%s remediation=run pfm install --yes\n", base)
	default:
		fmt.Fprintln(stdout, base)
		return 0
	}
	return 1
}

// openCodeRemediation names what the operator can actually do about an
// unhealthy OpenCode registration. `pfm install --yes` rewrites an entry
// install itself wrote and PRESERVES one it did not write — so an entry the
// ownership ledger does not claim is named as user-owned with its file and
// key; prescribing the reinstall there would be advice that can never fix what
// it named. A ledger that could not be read says so rather than passing for
// "no user-owned entry here".
func openCodeRemediation(home, path, name string) string {
	const reinstall = "remediation=run pfm install --yes"
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
