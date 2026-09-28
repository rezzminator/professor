package installer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// InstallCheckBlocked is `pfm install --check`'s exit when the install gate
// would refuse this host: 0 is a pass, 1 a refusal before the gate or a check
// that could not answer, 2 a usage error. make host-install reads it before
// swapping a binary in.
const InstallCheckBlocked = 4

// gateUnreadableError is a gate refusal made only of what the gate could not
// read (an unreadable finding or journal, an unknown executable): `pfm install
// --check` answers it with 1, since closing chats would not clear it.
type gateUnreadableError struct{ error }

// RunInstallCheck ends `pfm install --check` at the apply's gate (installGate
// over the apply's findings), after the caller ran the apply's pre-gate
// refusals: nothing is moved, stopped or journaled. A refusal prints the
// gate's lines and returns InstallCheckBlocked; a refusal made only of
// unreadable rows returns 1, never ok.
func RunInstallCheck(env LayoutEnv, findings []LayoutFinding, stdout, stderr io.Writer) int {
	clone := env.Clone
	if clone == "" {
		clone = "<clone>"
	}
	err := installGate(env, findings)
	if err == nil {
		fmt.Fprintln(stdout, "install check: ok — the install gate would pass")
		return 0
	}
	fmt.Fprintf(stderr, "pfm install: %v\n", err)
	if errors.As(err, new(gateUnreadableError)) {
		fmt.Fprintf(
			stderr,
			"install check: cannot answer — fix what it names, then rerun make -C %s/pfm host-install\n",
			clone,
		)
		return 1
	}
	fmt.Fprintf(
		stderr,
		"install check: blocked — close what it names, then rerun make -C %s/pfm host-install\n",
		clone,
	)
	return InstallCheckBlocked
}

func installGate(env LayoutEnv, findings []LayoutFinding) error {
	lines := []string{"refused before any change:"}
	blocked := false
	// answered: at least one refusal is a real verdict, not an unreadable row.
	answered := false
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
					updaterBlocked, answered = true, true
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
			blocked, answered = true, true
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
		} else {
			answered = true
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
	if !answered {
		return gateUnreadableError{errors.New(strings.Join(lines, "\n"))}
	}
	return errors.New(strings.Join(lines, "\n"))
}
