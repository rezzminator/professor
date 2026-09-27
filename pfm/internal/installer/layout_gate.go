package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func installGate(env LayoutEnv, findings []LayoutFinding) error {
	lines := []string{"refused before any change:"}
	blocked := false
	updaterBlocked := false
	for _, finding := range findings {
		if finding.Row != layoutRowManagedCleanup && (finding.Err != nil || finding.Verdict != VerdictOK) {
			updaterBlocked = true
			break
		}
	}
	if updaterBlocked {
		updaterBlocked = false
		if env.invocation == nil || env.invocation.Get(paths.EnvUpdateInstall) != "1" {
			executable := env.executable
			if executable == nil {
				executable = os.Executable
			}
			exe, err := executable()
			if err != nil {
				lines = append(lines,
					"  refuse  updater — cannot tell which program runs this install: "+err.Error(),
				)
				updaterBlocked = true
			} else {
				exe = paths.PhysicalPath(exe)
				stage := filepath.Dir(exe)
				root := paths.PhysicalPath(filepath.Join(env.Home, ".local", "share", "pfm"))
				if filepath.Base(exe) == "pfm-a" && strings.HasPrefix(filepath.Base(stage), "update-") &&
					filepath.Dir(stage) == root {
					lines = append(lines,
						"  refuse  updater — this install migrates the host layout, and the pfm update running it "+
							"predates the install journal",
					)
					updaterBlocked = true
				}
			}
		}
	}
	blocked = updaterBlocked
	journals, inventoryErr := InstallJournals(env.Home)
	if inventoryErr != nil {
		root := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
		lines = append(lines, fmt.Sprintf("  refuse  journal %s — UNREADABLE %v", root, inventoryErr))
		blocked = true
	}
	for _, journal := range journals {
		if journal.Err != nil {
			lines = append(lines, fmt.Sprintf("  refuse  journal %s — UNREADABLE %v", journal.ID, journal.Err))
			blocked = true
		} else if journal.Pending && !journal.RolledBack {
			lines = append(lines, fmt.Sprintf(
				"  refuse  journal %s — pending records from a crashed or failed install: run pfm install --rollback %s",
				journal.ID,
				journal.ID,
			))
			blocked = true
		}
	}
	layoutBlocked := false
	for _, finding := range findings {
		if finding.Row == layoutRowManagedCleanup {
			continue
		}
		if finding.Err == nil && finding.Verdict != VerdictRefuse {
			continue
		}
		if finding.Err == nil && (finding.Row == layoutRowStateDB || finding.Row == layoutRowCacheDB) &&
			strings.HasPrefix(finding.Detail, "held by pid ") {
			continue
		}
		detail := finding.Detail
		if finding.Err != nil {
			detail = "UNREADABLE " + finding.Err.Error()
		}
		lines = append(lines, fmt.Sprintf("  refuse  layout %s %s — %s", finding.Row, finding.Path, detail))
		blocked = true
		layoutBlocked = true
	}
	if !blocked {
		return nil
	}
	if updaterBlocked {
		lines = append(lines,
			"cross by hand:",
			"  1. close every chat, the one running this command included",
			"  2. from a plain shell outside tmux, run:",
			"     git -C "+env.Clone+" pull --ff-only",
			"     make -C "+env.Clone+"/pfm host-install",
			"     pfm install --yes",
		)
	}
	if layoutBlocked {
		lines = append(
			lines,
			"close every chat (the one running this command included), resolve any other refusal above by hand, "+
				"then rerun pfm install --yes from a plain shell",
		)
	}
	return errors.New(strings.Join(lines, "\n"))
}
