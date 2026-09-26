package doctor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const layoutAccountMCPRow = "account-mcp"

// printLayoutChecks reports the same ordered findings the installer will apply.
func printLayoutChecks(stdout io.Writer, runtime config.Runtime, environment paths.Env) (warnings, failures int) {
	layout, err := installer.NewLayoutEnv(runtime, environment)
	if err != nil {
		fmt.Fprintf(stdout, "layout: environment UNREADABLE error=%v\n", err)
		return 0, 1
	}
	ledgerReported := map[string]bool{}
	for _, finding := range installer.ClassifyLayout(layout) {
		if finding.Err != nil {
			switch {
			case finding.Detail == "ownership ledger":
				if !ledgerReported[finding.Source] {
					fmt.Fprintf(
						stdout,
						"legacy: ownership ledger UNREADABLE error=%v — ledger entries unjudged\n",
						finding.Err,
					)
					ledgerReported[finding.Source] = true
					failures++
				}
			case finding.Row == "account-settings" || finding.Row == layoutAccountMCPRow:
				fmt.Fprintf(stdout, "legacy: %s UNREADABLE error=%v\n", finding.Path, finding.Err)
				failures++
			default:
				fmt.Fprintf(stdout, "layout: %s UNREADABLE %s error=%v\n", finding.Row, finding.Path, finding.Err)
				failures++
			}
			continue
		}
		switch finding.Row {
		case "managed-cleanup":
			switch {
			case finding.Detail == "check off by config":
				fmt.Fprintln(stdout, "managed-cleanup: check off by config")
			case finding.Verdict == installer.VerdictCreate:
				fmt.Fprintf(
					stdout,
					"managed-cleanup: %s missing — transcripts older than 30 days are deleted by any Claude launch outside pfm\n",
					finding.Path,
				)
				warnings++
			case finding.Verdict != installer.VerdictOK:
				if strings.HasPrefix(finding.Detail, "cleanupPeriodDays=") {
					fmt.Fprintf(stdout, "managed-cleanup: %s %s\n", finding.Path, finding.Detail)
				} else {
					fmt.Fprintf(
						stdout,
						"layout: managed-cleanup %s %s — %s\n",
						finding.Verdict,
						finding.Path,
						finding.Detail,
					)
				}
				warnings++
			}
		case "session-store":
			if finding.Verdict == installer.VerdictOK {
				continue
			}
			switch finding.Verdict {
			case installer.VerdictCreate:
				fmt.Fprintf(stdout, "session-store: %s missing — run pfm install\n", finding.Path)
			case installer.VerdictMerge:
				fmt.Fprintf(
					stdout,
					"session-store: %s is a real dir (%s) — run pfm install\n",
					finding.Path,
					finding.Detail,
				)
			case installer.VerdictRepoint, installer.VerdictRefuse:
				switch {
				case strings.HasPrefix(finding.Detail, "live chats:"):
					fmt.Fprintf(stdout, "session-store: %s %s\n", finding.Path, finding.Detail)
				case finding.Source != "":
					fmt.Fprintf(stdout, "session-store: %s points at %s, want %s\n", finding.Path, finding.Source,
						filepath.Join(layout.Home, ".claude", filepath.Base(finding.Path)))
				default:
					fmt.Fprintf(
						stdout,
						"layout: session-store %s %s — %s\n",
						finding.Verdict,
						finding.Path,
						finding.Detail,
					)
				}
			default:
				fmt.Fprintf(stdout, "layout: session-store %s %s\n", finding.Verdict, finding.Path)
			}
			failures++
		case "account-settings", layoutAccountMCPRow:
			if finding.Verdict == installer.VerdictOK {
				continue
			}
			if finding.Verdict == installer.VerdictStrip {
				for _, leftover := range strings.Split(finding.Detail, ",") {
					if finding.Row == layoutAccountMCPRow && !strings.HasPrefix(leftover, "mcpServers.") {
						leftover = "mcpServers." + leftover
					}
					fmt.Fprintf(stdout, "legacy: %s still carries pfm %s — run pfm install\n", finding.Path, leftover)
					failures++
				}
			} else {
				fmt.Fprintf(
					stdout,
					"layout: %s %s %s — %s\n",
					finding.Row,
					finding.Verdict,
					finding.Path,
					finding.Detail,
				)
				failures++
			}
		case "memory-helpers":
			if finding.Verdict == installer.VerdictOK {
				continue
			}
			if finding.Verdict == installer.VerdictMove {
				for _, source := range strings.Split(finding.Detail, ",") {
					fmt.Fprintf(stdout, "legacy: %s names %s — run pfm install\n", source, filepath.Base(source))
					failures++
				}
			} else {
				fmt.Fprintf(
					stdout,
					"layout: memory-helpers %s %s — %s\n",
					finding.Verdict,
					finding.Path,
					finding.Detail,
				)
				failures++
			}
		case "state-db", "cache-db":
			if finding.Source != "" {
				fmt.Fprintf(stdout, "state: legacy %s still present — run pfm install\n", finding.Source)
				warnings++
				if finding.Verdict == installer.VerdictRefuse {
					printOtherLayoutFinding(stdout, finding)
					warnings++
				}
			} else if finding.Verdict != installer.VerdictOK {
				printOtherLayoutFinding(stdout, finding)
				warnings++
			}
			if _, err := os.Stat(finding.Path); os.IsNotExist(err) {
				key := "state.db"
				if finding.Row == "cache-db" {
					key = "state.cacheDb"
				}
				fmt.Fprintf(stdout, "state: %s=%s missing\n", key, finding.Path)
			}
		default:
			if finding.Verdict != installer.VerdictOK {
				printOtherLayoutFinding(stdout, finding)
				warnings++
			}
		}
	}
	return warnings, failures
}

func printOtherLayoutFinding(stdout io.Writer, finding installer.LayoutFinding) {
	fmt.Fprintf(stdout, "layout: %s %s %s", finding.Row, finding.Verdict, finding.Path)
	if finding.Detail != "" {
		fmt.Fprintf(stdout, " — %s", finding.Detail)
	}
	fmt.Fprintln(stdout)
}
