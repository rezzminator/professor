package installer

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

var removeInstallJournal = os.RemoveAll

// InstallJournal is one directory in the install migration inventory.
type InstallJournal struct {
	ID, Dir             string
	Pending, RolledBack bool
	Bytes               uint64
	Err                 error
}

// InstallJournals lists journal directories in ascending ID order.
func InstallJournals(home string) ([]InstallJournal, error) {
	root := filepath.Join(home, ".local", "state", "pfm", "migrations")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	journals := []InstallJournal{}
	for _, entry := range entries {
		if !entry.IsDir() || !layoutJournalID.MatchString(entry.Name()) {
			continue
		}
		journal := InstallJournal{ID: entry.Name(), Dir: filepath.Join(root, entry.Name())}
		journal.Bytes, err = layoutApparentBytes(journal.Dir)
		if err != nil {
			return nil, fmt.Errorf("size install journal %s: %w", journal.Dir, err)
		}
		if _, err := os.Stat(filepath.Join(journal.Dir, layoutRolledBackMarker)); err == nil {
			journal.RolledBack = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read rollback marker %s: %w", journal.Dir, err)
		}
		raw, readErr := os.ReadFile(filepath.Join(journal.Dir, "journal.json"))
		if errors.Is(readErr, fs.ErrNotExist) {
			journals = append(journals, journal)
			continue
		}
		if readErr != nil {
			journal.Err = readErr
		} else {
			var records []layoutJournalRecord
			journal.Err = json.Unmarshal(raw, &records)
			for _, record := range records {
				if record.Result == layoutRecordPending || record.Result == layoutRecordUnrestored {
					journal.Pending = true
				}
			}
		}
		journals = append(journals, journal)
	}
	return journals, nil
}

// pruneInstallJournals retains the newest three sealed journals and all young ones.
// An inventory or removal failure is advisory to the completed install.
func pruneInstallJournals(home, current string, now time.Time, stdout io.Writer) {
	journals, err := InstallJournals(home)
	if err != nil {
		fmt.Fprintf(stdout, "  warn    install journals not pruned: %v\n", err)
		return
	}
	sealed := []InstallJournal{}
	for _, journal := range journals {
		if journal.Err != nil || journal.Pending {
			continue
		}
		if _, err := os.Stat(filepath.Join(journal.Dir, "journal.json")); err != nil {
			continue
		}
		sealed = append(sealed, journal)
	}
	slices.SortFunc(sealed, func(a, b InstallJournal) int { return cmp.Compare(b.ID, a.ID) })
	for index, journal := range sealed {
		date, err := time.Parse("20060102T150405Z", journal.ID)
		if err != nil || index < 3 || journal.ID == current || date.After(now.Add(-14*24*time.Hour)) {
			continue
		}
		if err := removeInstallJournal(journal.Dir); err != nil {
			fmt.Fprintf(stdout, "  warn    install journal %s not pruned: %v\n", journal.ID, err)
			continue
		}
		fmt.Fprintf(stdout, "  prune   install journal %s (%d bytes)\n", journal.ID, journal.Bytes)
	}
}
