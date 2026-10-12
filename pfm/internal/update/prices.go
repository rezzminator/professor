package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// updateBackupDir is where an update saves a file of the user's it is about to
// discard, beside the update stages under the pfm share directory. Only the
// newest updateBackupsKept saved price tables stay (pruneSavedPrices); what
// lands here is otherwise the only copy of a discarded edit.
func updateBackupDir(home string) string {
	return filepath.Join(filepath.Dir(installer.ManagedRoot(home)), "update-backups")
}

// updateBackupsKept is how many saved price tables the backup directory keeps:
// the newest ones, by the timestamp in their names.
const updateBackupsKept = 10

const savedPriceTimestamp = "20060102T150405.000000000Z"

// restoreRefreshedPrices puts the clone's tracked prices.json back to HEAD
// when only its working copy changed — what `pfm model-cost` writes on a
// refresh — so a price change never blocks an update; the release carries its
// own table. A staged change is the user's and stays for the dirty check. An
// unstaged edit cannot be told from a refresh, so the working copy is saved in
// backupDir first and the progress line names the saved file: a hand edit is
// never lost silently.
func restoreRefreshedPrices(ctx context.Context, repo, backupDir string, stdout io.Writer) error {
	status, err := updateGitOutput(ctx, repo, "status", "--porcelain", "--", pricing.FileRel)
	if err != nil {
		return fmt.Errorf("inspect refreshed prices: %w", err)
	}
	if !strings.HasPrefix(status, " M ") {
		return nil
	}
	saved, err := saveDiscardedPrices(repo, backupDir)
	if err != nil {
		return fmt.Errorf("save %s before restoring it: %w", pricing.FileRel, err)
	}
	if err := updateGitRun(ctx, repo, "checkout", "--", pricing.FileRel); err != nil {
		return fmt.Errorf("restore refreshed prices: %w", err)
	}
	fmt.Fprintf(
		stdout,
		"update: restored the refreshed %s; the release carries its own price table; the replaced working copy is saved at %s\n",
		pricing.FileRel,
		saved,
	)
	if err := pruneSavedPrices(backupDir, stdout); err != nil {
		fmt.Fprintf(stdout, "update: warning: older saved price tables in %s were not pruned: %v\n", backupDir, err)
	}
	return nil
}

// pruneSavedPrices removes all but the newest updateBackupsKept saved price
// tables in backupDir; the names carry a fixed-width UTC timestamp, so name
// order is save order. Files of other names are left alone.
func pruneSavedPrices(backupDir string, stdout io.Writer) error {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return fmt.Errorf("list saved price tables in %s: %w", backupDir, err)
	}
	var saved []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasPrefix(name, "prices-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(name, "prices-"), ".json")
		at, err := time.Parse(savedPriceTimestamp, stamp)
		if err != nil || at.Format(savedPriceTimestamp) != stamp {
			continue // A prefix match is not necessarily a saved table we own.
		}
		saved = append(saved, filepath.Join(backupDir, name))
	}
	sort.Strings(saved)
	var pruneErr error
	for len(saved) > updateBackupsKept {
		if err := os.Remove(saved[0]); err != nil {
			pruneErr = errors.Join(pruneErr, fmt.Errorf("remove saved price table %s: %w", saved[0], err))
		} else {
			fmt.Fprintf(stdout, "update: pruned saved price table %s\n", saved[0])
		}
		saved = saved[1:]
	}
	return pruneErr
}

// saveDiscardedPrices copies the clone's prices.json working copy to a new
// file in backupDir, named by the moment it was saved, and returns its path.
func saveDiscardedPrices(repo, backupDir string) (string, error) {
	content, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(pricing.FileRel)))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", err
	}
	saved := filepath.Join(backupDir, "prices-"+clock.Real.Now().UTC().Format(savedPriceTimestamp)+".json")
	if err := atomicfile.Write(saved, content, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", saved, err)
	}
	return saved, nil
}
