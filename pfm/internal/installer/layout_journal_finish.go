package installer

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// Seal recomputes the fingerprint of every applied record once the install
// has finished. A record for a directory the install created and then wrote
// into keeps its empty-directory fingerprint otherwise, and rollback reads
// the install's own later writes as newer work.
func (journal *Journal) Seal(stdout io.Writer) error {
	if journal == nil || journal.dryRun || journal.dir == "" {
		return nil
	}
	indexes := []int{}
	for index, record := range journal.records {
		if record.Result == layoutRecordApplied {
			indexes = append(indexes, index)
		}
	}
	if len(indexes) == 0 {
		return nil
	}
	if err := journal.markRecordsApplied(indexes); err != nil {
		return err
	}
	currentClock := journal.clock
	if currentClock == nil {
		currentClock = clock.Real
	}
	pruneInstallJournals(journal.env.Home, filepath.Base(journal.dir), currentClock.Now(), stdout)
	return nil
}

// MigrationBackups lists the pre-migration backups beside the journal's
// state and cache databases, taken before the install's database migration
// so JournalMigrationBackups can tell the ones that migration wrote. An
// interrupted install lists none: the interrupt is its error, so the database
// migration never starts.
func (journal *Journal) MigrationBackups() ([]string, error) {
	if err := journal.Interrupted(); err != nil {
		return nil, err
	}
	return journal.migrationBackups()
}

// migrationBackups is MigrationBackups past its interrupt check.
func (journal *Journal) migrationBackups() ([]string, error) {
	backups := []string{}
	for _, database := range []string{journal.env.StateDB, journal.env.CacheDB} {
		matches, err := filepath.Glob(database + ".bak-before-v*")
		if err != nil {
			return nil, fmt.Errorf("list backups of %s: %w", database, err)
		}
		backups = append(backups, matches...)
	}
	return backups, nil
}

// JournalMigrationBackups journals what the install's own database migration
// changed: the moved databases are refingerprinted, and every backup absent
// from before (MigrationBackups) is either refingerprinted, when the layout
// journaled it under its predicted name, or recorded as created, so rollback
// removes it. A backup that existed before the install is never touched.
func (journal *Journal) JournalMigrationBackups(before []string) error {
	if journal == nil || journal.dryRun || journal.dir == "" {
		return nil
	}
	after, err := journal.migrationBackups()
	if err != nil {
		return err
	}
	start := len(journal.records)
	for _, path := range after {
		if slices.Contains(before, path) {
			continue
		}
		row := layoutRowStateDB
		if strings.HasPrefix(path, journal.env.CacheDB+".bak-before-v") {
			row = layoutRowCacheDB
		}
		if journal.journaled(path) {
			if err := journal.Refingerprint(path); err != nil {
				return err
			}
			continue
		}
		journal.records = append(journal.records, layoutJournalRecord{
			Row: row, Verdict: verdictInstallWrite, Source: path, Destination: path, Result: layoutRecordPending,
		})
	}
	indexes := []int{}
	for index := start; index < len(journal.records); index++ {
		indexes = append(indexes, index)
	}
	if err := journal.markRecordsApplied(indexes); err != nil {
		return err
	}
	return journal.Refingerprint(journal.env.StateDB, journal.env.CacheDB)
}

// journaled reports whether path already has an applied record.
func (journal *Journal) journaled(path string) bool {
	for _, record := range journal.records {
		if record.Result == layoutRecordApplied && record.Destination == path {
			return true
		}
	}
	return false
}
