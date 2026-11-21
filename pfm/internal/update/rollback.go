package update

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

type updateReplacement struct {
	target        string
	backup        string
	replaced      bool
	restoreFailed bool
}

func updateFailure(primary, rollbackErr error) error {
	if rollbackErr != nil {
		return fmt.Errorf(
			"%w; rollback residue: %v; manually repair the reported update-owned state",
			primary,
			rollbackErr,
		)
	}
	return fmt.Errorf("%w; rolled back what the lines above name, then re-ran the previous install", primary)
}

func rollbackUpdateReplacements(replacements []updateReplacement, stderr io.Writer) error {
	var rollbackErr error
	for index := len(replacements) - 1; index >= 0; index-- {
		replacement := replacements[index]
		if !replacement.replaced {
			continue
		}
		if err := copyUpdateFile(replacement.backup, replacement.target); err != nil {
			replacements[index].restoreFailed = true
			rollbackErr = errors.Join(
				rollbackErr,
				fmt.Errorf("%s (backup retained at %s): %w", replacement.target, replacement.backup, err),
			)
			continue
		}
		fmt.Fprintf(stderr, "pfm update: rolled back %s\n", replacement.target)
	}
	return rollbackErr
}

// updateStateSnapshot is the state database's copy from before the candidate
// ran ("" when there was none); restoreFailed keeps the stage, and the copy in
// it, as the manual repair source.
type updateStateSnapshot struct {
	path          string
	restoreFailed bool
}

// rollbackUpdateStateDatabase returns the state database to its pre-update copy
// when the candidate migrated it past the schema of this binary, the one the
// rollback restores, which refuses a newer database.
func rollbackUpdateStateDatabase(
	ctx context.Context,
	path string,
	state *updateStateSnapshot,
	stderr io.Writer,
) error {
	restore, err := fleetdb.RestoreSharedState(ctx, path, state.path)
	if err != nil {
		if state.path == "" {
			return fmt.Errorf("restore state database: %w", err)
		}
		state.restoreFailed = true
		return fmt.Errorf("restore state database (its pre-update copy is kept at %s): %w", state.path, err)
	}
	if restore.Restored {
		fmt.Fprintf(
			stderr,
			"pfm update: rolled back state database %s from schema %d to %d; "+
				"the candidate's database, with every write since the update began, is kept at %s\n",
			path, restore.From, restore.To, restore.Kept,
		)
	}
	return nil
}

// rollbackUpdateState restores binaries, the state database, source and files
// before re-running the previous install and its doctor; failures are joined as
// rollback residue.
func rollbackUpdateState(
	ctx context.Context,
	repo, installSourceRepo, previousRef string,
	sourceAdvanced bool,
	replacements []updateReplacement,
	snapshots []updateFileSnapshot,
	state *updateStateSnapshot,
	runtime config.Runtime,
	stdout, stderr io.Writer,
) error {
	rollbackErr := rollbackUpdateReplacements(replacements, stderr)
	rollbackErr = errors.Join(rollbackErr, rollbackUpdateStateDatabase(ctx, runtime.Paths.StateDB, state, stderr))
	if sourceAdvanced {
		// A refresh since the fast-forward leaves prices.json modified, and
		// reset --keep refuses to overwrite a modified file the two revisions
		// differ in.
		if err := restoreRefreshedPrices(ctx, repo, updateBackupDir(runtime.Paths.Home), stdout); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
		if err := updateGitRun(ctx, repo, "reset", "--keep", previousRef); err != nil {
			return errors.Join(rollbackErr, fmt.Errorf("restore source revision %s: %w", previousRef, err))
		}
		fmt.Fprintf(stderr, "pfm update: rolled back source to %s\n", previousRef)
	}
	rollbackErr = errors.Join(rollbackErr, restoreUpdateOwnedFiles(snapshots, stderr))
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
