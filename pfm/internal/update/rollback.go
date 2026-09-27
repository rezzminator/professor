package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
)

// updateInstallJournalRoot is where an applying `pfm install` writes its one
// journal per run: {home}/.local/state/pfm/migrations/{id}/. The candidate
// binary owns the journal's format; update only tells the ids apart.
func updateInstallJournalRoot(home string) string {
	return filepath.Join(home, ".local", "state", "pfm", "migrations")
}

// listUpdateInstallJournals returns the journal ids under the root. A missing
// root holds no journal; any other listing failure is an error, never read
// as "no journal".
func listUpdateInstallJournals(home string) (map[string]bool, error) {
	root := updateInstallJournalRoot(home)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list install journals in %s: %w", root, err)
	}
	ids := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			ids[entry.Name()] = true
		}
	}
	return ids, nil
}

// rollbackUpdateInstallJournal replays the journal the candidate's
// `install --yes` wrote — every install write, config migration included —
// through the candidate's own `install --rollback {id}`, with the --config
// the install was given. journalsBefore is the id set listed right before
// that install; nil means no install ran and there is nothing to replay. A
// run that recorded nothing left no new id. More than one new id is never
// guessed between: each is named with its command. A refused replay is
// returned with the candidate's own stderr; the caller keeps rolling back.
func rollbackUpdateInstallJournal(
	ctx context.Context,
	candidate string,
	journalsBefore map[string]bool,
	repo, sourceRepo string,
	runtime config.Runtime,
	stdout, stderr io.Writer,
) error {
	if journalsBefore == nil {
		return nil
	}
	journalsAfter, err := listUpdateInstallJournals(runtime.Paths.Home)
	if err != nil {
		return fmt.Errorf("find the update's install journal: %w", err)
	}
	var created []string
	for id := range journalsAfter {
		if !journalsBefore[id] {
			created = append(created, id)
		}
	}
	sort.Strings(created)
	switch len(created) {
	case 0:
		return nil
	case 1:
	default:
		commands := make([]string, 0, len(created))
		for _, id := range created {
			commands = append(commands, "pfm install --rollback "+id)
		}
		return fmt.Errorf(
			"the update's install left %d install journals (%s); none was replayed — roll each back by hand: %s",
			len(created),
			strings.Join(created, ", "),
			strings.Join(commands, "; "),
		)
	}
	id := created[0]
	var refusal bytes.Buffer
	if err := runUpdateCandidateCommand(
		ctx,
		candidate,
		updateInstallConfigPath(runtime),
		repo,
		sourceRepo,
		stdout,
		io.MultiWriter(stderr, &refusal),
		installCommand,
		"--rollback",
		id,
	); err != nil {
		return fmt.Errorf(
			"install journal %s was not rolled back: pfm install --rollback %s failed: %s: %w",
			id,
			id,
			strings.TrimSpace(refusal.String()),
			err,
		)
	}
	fmt.Fprintf(stderr, "pfm update: rolled back install journal %s\n", id)
	return nil
}

type updateReplacement struct {
	target   string
	backup   string
	replaced bool
}

func updateFailure(primary, rollbackErr error) error {
	if rollbackErr != nil {
		return fmt.Errorf(
			"%w; rollback residue: %v; manually repair the reported update-owned state",
			primary,
			rollbackErr,
		)
	}
	return fmt.Errorf("%w; rolled back update-owned changes", primary)
}

func rollbackUpdateReplacements(replacements []updateReplacement, stderr io.Writer) error {
	var rollbackErr error
	for index := len(replacements) - 1; index >= 0; index-- {
		replacement := replacements[index]
		if !replacement.replaced {
			continue
		}
		if err := copyUpdateFile(replacement.backup, replacement.target); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("%s: %w", replacement.target, err))
			continue
		}
		fmt.Fprintf(stderr, "pfm update: rolled back %s\n", replacement.target)
	}
	return rollbackErr
}

// rollbackUpdateState first replays the candidate's install journal through
// the candidate itself, while it is still staged — the journal is the one
// owner of every write that install made, config files included. It then
// restores every owned binary and the source, and only then uses the prior
// binary's embedded installer to converge installer-owned host wiring back to
// the previous release. The journal replay has to come first: that installer
// recognises only what IT generates, so a write only the newer release makes
// would survive it. A refused replay is residue; the rest still runs.
// journalsBefore is the journal id set listed before the install, nil when no
// install ran. A clean doctor is part of rollback proof; without it,
// updateFailure reports residue instead of claiming a safe rollback.
func rollbackUpdateState(
	ctx context.Context,
	repo, installSourceRepo, previousRef string,
	sourceAdvanced bool,
	replacements []updateReplacement,
	candidate string,
	journalsBefore map[string]bool,
	runtime config.Runtime,
	skipHarvest bool,
	stdout, stderr io.Writer,
) error {
	rollbackErr := rollbackUpdateInstallJournal(
		ctx,
		candidate,
		journalsBefore,
		repo,
		installSourceRepo,
		runtime,
		stdout,
		stderr,
	)
	rollbackErr = errors.Join(rollbackErr, rollbackUpdateReplacements(replacements, stderr))
	if sourceAdvanced {
		if err := updateGitRun(ctx, repo, "reset", "--keep", previousRef); err != nil {
			return errors.Join(rollbackErr, fmt.Errorf("restore source revision %s: %w", previousRef, err))
		}
		fmt.Fprintf(stderr, "pfm update: rolled back source to %s\n", previousRef)
	}
	if len(replacements) == 0 {
		return errors.Join(rollbackErr, errors.New("no previous binary is available to restore installer state"))
	}
	previousBinary := replacements[0].backup
	if err := updateRollbackInstall(
		ctx,
		previousBinary,
		repo,
		installSourceRepo,
		runtime,
		skipHarvest,
		stdout,
		stderr,
	); err != nil {
		return errors.Join(rollbackErr, fmt.Errorf("reapply previous installer state: %w", err))
	}
	rollbackOutcome, doctorErr := updateRollbackDoctor(
		ctx,
		previousBinary,
		runtime,
		runtime.Config.Path,
		skipHarvest,
		stdout,
		stderr,
	)
	if doctorErr != nil {
		return errors.Join(rollbackErr, fmt.Errorf("doctor after rollback: %w", doctorErr))
	}
	switch rollbackOutcome.Exit {
	case 0:
		// Clean — nothing to report.
	case 1:
		// Warnings alone are never residue; they are reported (via the tee to
		// stdout above), not claimed as a rollback failure. A rollback to a
		// binary that predates M2's failure tiers ALSO exits 1 on warnings
		// alone, so that case is named rather than misreported as residue.
		if rollbackDoctorPredatesFailureTiers(rollbackOutcome.Output) {
			return errors.Join(
				rollbackErr,
				errors.New(
					"doctor after rollback exited 1 (an older pfm exits 1 on warnings alone; read the rows above before repairing anything)",
				),
			)
		}
	default:
		return errors.Join(
			rollbackErr,
			fmt.Errorf("doctor after rollback: exited %d — see the doctor rows above", rollbackOutcome.Exit),
		)
	}
	return rollbackErr
}
