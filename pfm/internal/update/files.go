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

// updateConfigPathAfterInstall resolves the --config path the candidate's OWN
// install left behind (issue #24 finding 4): runtime.Config.Path was
// resolved BEFORE the migration renamed it inside the candidate process only.
// A non-ENOENT stat error on either candidate path (issue #24 F2 — EACCES,
// ENOTDIR, or anything else) is returned as an error, exactly like sibling
// listUpdateInstallJournals: it is never folded into the "gone/migrated" notes,
// which would misreport a stat failure as an absent file. The caller treats
// a returned error as a failed update step.
func updateConfigPathAfterInstall(runtime config.Runtime) (path, note string, err error) {
	original := runtime.Config.Path
	if original == "" {
		return "", "", nil
	}
	if _, statErr := os.Stat(original); statErr == nil {
		return original, "", nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return "", "", fmt.Errorf("stat config %s: %w", original, statErr)
	}
	migrated := filepath.Join(filepath.Dir(original), config.FileName)
	if _, statErr := os.Stat(migrated); statErr == nil {
		return migrated, fmt.Sprintf("config migrated by the update: %s → %s", original, migrated), nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return "", "", fmt.Errorf("stat migrated config %s: %w", migrated, statErr)
	}
	return "", fmt.Sprintf("config %s is gone after install and no %s replaced it", original, config.FileName), nil
}
