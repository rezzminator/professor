package installer

import (
	"errors"
	"fmt"
)

const (
	// layoutRecordRestored closes a write whose destination is back at its pre-image.
	layoutRecordRestored = "restored"
	// layoutRecordUnrestored keeps a write whose restore failed open for
	// rollback: a later write's markApplied never sweeps it into applied.
	layoutRecordUnrestored = "unrestored"
)

// WriteOrRestore snapshots every target, then restores only this write's
// records if the action fails. An unsuccessful restore stays open as unrestored.
func (journal *Journal) WriteOrRestore(targets []string, action func() error) error {
	if journal == nil {
		return action()
	}
	if journal.dryRun {
		journal.plan(targets)
		return nil
	}
	start := len(journal.records)
	for _, target := range targets {
		resolved := installRecordPath(target)
		for _, record := range journal.records[:start] {
			if record.Row == layoutRowInstall && record.Result == layoutRecordPending &&
				record.Destination == resolved {
				return fmt.Errorf("pending install record for %s", resolved)
			}
		}
	}
	for _, path := range targets {
		if err := journal.before(path); err != nil {
			return errors.Join(err, journal.restoreWrite(start))
		}
	}
	if err := action(); err != nil {
		return errors.Join(err, journal.restoreWrite(start))
	}
	indexes := make([]int, 0, len(journal.records)-start)
	for index := start; index < len(journal.records); index++ {
		indexes = append(indexes, index)
	}
	return journal.markRecordsApplied(indexes)
}

func (journal *Journal) restoreWrite(start int) error {
	if start >= len(journal.records) {
		return nil
	}
	var failures []error
	for index := len(journal.records) - 1; index >= start; index-- {
		record := &journal.records[index]
		if err := restoreLayoutRecord(journal.ctx, *record); err != nil {
			failures = append(failures, fmt.Errorf("restore %s: %w", record.Destination, err))
			record.Result = layoutRecordUnrestored
			continue
		}
		record.Result = layoutRecordRestored
	}
	return errors.Join(append(failures, journal.flush())...)
}

// changePathsOrRestore journals a plugin write and restores its pre-images
// before reporting a failed command. Preview and nil journals match changePaths.
func (installer *engine) changePathsOrRestore(message string, targets []string, action func() error) error {
	journal := installer.options.Journal
	if journal == nil || action == nil {
		return installer.change(message, action)
	}
	if !installer.apply {
		journal.plan(targets)
		return installer.change(message, action)
	}
	return installer.change(message, func() error { return journal.WriteOrRestore(targets, action) })
}
