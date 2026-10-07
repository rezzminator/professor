package update

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
)

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
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("%s: %w", replacement.target, err))
			continue
		}
		fmt.Fprintf(stderr, "pfm update: rolled back %s\n", replacement.target)
	}
	return rollbackErr
}

// rollbackUpdateState restores binaries, source and files before re-running
// the previous install and its doctor; failures are joined as rollback residue.
func rollbackUpdateState(
	ctx context.Context,
	repo, installSourceRepo, previousRef string,
	sourceAdvanced bool,
	replacements []updateReplacement,
	snapshots []updateFileSnapshot,
	runtime config.Runtime,
	skipHarvest bool,
	stdout, stderr io.Writer,
) error {
	rollbackErr := rollbackUpdateReplacements(replacements, stderr)
	if sourceAdvanced {
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
