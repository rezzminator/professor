package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/update"
)

// openProjectWorkers bounds the project reports built at once: each reads the
// project's files for retired names.
const openProjectWorkers = 4

// openProjectDeadline bounds the whole ledger: a project whose report is not
// built by then (a FIFO baseline, a git that never returns) prints its own
// UNREADABLE row instead of holding doctor — and `pfm update`, which runs
// doctor — open. A variable so a test can shorten it.
var openProjectDeadline = 60 * time.Second

// printOpenProjectsDoctor is the host-wide update ledger: every project with
// a .professor/baseline.json that a chat on this host ran in (the cache
// database's transcript and rollout cwds, plus the current directory) gets its
// report built, and each one with a row no verb has closed prints one warning
// row naming every such file. The rows are advisory and add nothing to the
// tally: an unresolved update never changes doctor's exit code. A project whose
// report cannot be built — or a directory that cannot be inspected for any
// cause but absence — prints its own row, and a chat index that cannot be
// read says so, never a shorter list.
func printOpenProjectsDoctor(
	ctx context.Context,
	stdout io.Writer,
	database *store.Store,
	databaseErr error,
	home string,
) {
	var cwds []string
	if cwd, err := os.Getwd(); err != nil {
		fmt.Fprintf(stdout, "doctor: warning project_updates current directory UNREADABLE: %v\n", err)
	} else {
		cwds = append(cwds, cwd)
	}
	indexErr := databaseErr
	if database != nil {
		var indexed []string
		indexed, indexErr = indexedChatCwds(ctx, database)
		cwds = append(cwds, indexed...)
	}
	if indexErr != nil {
		fmt.Fprintf(
			stdout,
			"doctor: warning project_updates chat index unreadable (%v); only the chats it did read are checked, plus the current directory\n",
			indexErr,
		)
	}
	roots, unreadable := adopterRoots(cwds)
	for _, row := range unreadable {
		fmt.Fprint(stdout, row)
	}
	results := buildOpenProjectRows(roots, home)
	open := len(unreadable)
	for _, row := range results {
		if row != "" {
			open++
			fmt.Fprint(stdout, row)
		}
	}
	fmt.Fprintf(
		stdout,
		"doctor: project_updates projects=%d unresolved=%d (every project a chat ran in; pfm doctor --project-updates --root DIR lists the rows)\n",
		len(roots)+len(unreadable),
		open,
	)
}

// buildOpenProjectRows builds each root's row on a bounded pool within
// openProjectDeadline; a root still building then gets a timed-out row and
// its worker is abandoned, never waited for.
func buildOpenProjectRows(roots []string, home string) []string {
	type built struct {
		index int
		row   string
	}
	results := make([]string, len(roots))
	finished := make([]bool, len(roots))
	done := make(chan built, len(roots))
	go func() {
		slots := make(chan struct{}, openProjectWorkers)
		for index, root := range roots {
			slots <- struct{}{}
			go func() {
				defer func() { <-slots }()
				done <- built{index: index, row: openProjectRow(root, home)}
			}()
		}
	}()
	deadline := clock.Real.NewTimer(openProjectDeadline)
	defer deadline.Stop()
	for received := 0; received < len(roots); received++ {
		select {
		case result := <-done:
			results[result.index] = result.row
			finished[result.index] = true
		case <-deadline.C():
			for index, root := range roots {
				if !finished[index] {
					results[index] = fmt.Sprintf(
						"doctor: warning project_updates root=%s UNREADABLE: report not built within %s\n",
						root,
						openProjectDeadline,
					)
				}
			}
			return results
		}
	}
	return results
}

// indexedChatCwds is every working directory the cache database recorded for a
// Claude transcript or a Codex rollout.
func indexedChatCwds(ctx context.Context, database *store.Store) ([]string, error) {
	var cwds []string
	transcripts, err := database.Transcripts(ctx)
	if err != nil {
		return nil, fmt.Errorf("read transcripts: %w", err)
	}
	for index := range transcripts {
		cwds = append(cwds, transcripts[index].CWD)
	}
	rollouts, err := database.Rollouts(ctx)
	if err != nil {
		return cwds, fmt.Errorf("read rollouts: %w", err)
	}
	for index := range rollouts {
		cwds = append(cwds, rollouts[index].CWD)
	}
	return cwds, nil
}

// adopterRoots maps each directory to the project root that owns it — the
// nearest ancestor with a baseline, a flight worktree standing for the project
// it was cut from, symlinks resolved so two spellings name one project — and
// returns each root once, sorted. A directory under no baseline, or one that no
// longer exists, names no project; one that cannot be inspected for any other
// cause is an UNREADABLE row of its own.
func adopterRoots(cwds []string) (roots, unreadable []string) {
	seenCwd := map[string]bool{}
	seen := map[string]bool{}
	for _, cwd := range cwds {
		cwd = strings.TrimSpace(cwd)
		if cwd == "" || seenCwd[cwd] {
			continue
		}
		seenCwd[cwd] = true
		if cut := strings.Index(filepath.ToSlash(cwd), "/.worktrees/"); cut >= 0 {
			cwd = cwd[:cut]
		}
		if _, err := os.Stat(cwd); err != nil {
			if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
				unreadable = append(unreadable, unreadableCwdRow(cwd, err))
			}
			continue
		}
		root, found, err := professor.ResolveProjectRoot(cwd)
		if err != nil {
			unreadable = append(unreadable, unreadableCwdRow(cwd, err))
			continue
		}
		if !found {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
		if seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	sort.Strings(roots)
	sort.Strings(unreadable)
	return roots, unreadable
}

func unreadableCwdRow(cwd string, err error) string {
	return fmt.Sprintf("doctor: warning project_updates cwd=%s UNREADABLE: %v\n", cwd, err)
}

// openProjectRow is one project's warning row, or "" when every row is closed.
func openProjectRow(root, home string) string {
	rows, err := professor.OpenProjectRows(root, home, update.ScanRetiredNames)
	if err != nil {
		return fmt.Sprintf("doctor: warning project_updates root=%s UNREADABLE: %v\n", root, err)
	}
	if len(rows) == 0 {
		return ""
	}
	var order []string
	byStatus := map[string][]string{}
	for _, row := range rows {
		if _, ok := byStatus[row.Status]; !ok {
			order = append(order, row.Status)
		}
		byStatus[row.Status] = append(byStatus[row.Status], row.Name)
	}
	parts := make([]string, 0, len(order))
	for _, status := range order {
		parts = append(parts, status+" "+strings.Join(byStatus[status], ", "))
	}
	return fmt.Sprintf(
		"doctor: warning project_updates root=%s unresolved=%d — %s — close each with a pfm update verb: pfm doctor --project-updates --root %s\n",
		root,
		len(rows),
		strings.Join(parts, " · "),
		root,
	)
}
