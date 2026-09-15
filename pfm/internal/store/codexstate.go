package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"hostops/pfm/internal/resolve"
	"hostops/pfm/internal/sqlitedb"
)

// CodexThread is one conversation as the Codex CLI's own SQLite state store
// records it. Codex 0.146.1 moved thread identity into that store and a
// paginated thread may never write a rollout file, so the state store is the
// authority on which Codex conversations exist and what they are named.
// Rollout files remain the authority on sizes and content.
type CodexThread struct {
	ID          string
	Name        string
	FirstPrompt string
	// Title is the threads.title column, trimmed. It is the text Codex's own
	// TUI renders as the pane's status-line name once a thread is named — the
	// same title a bare thread id yields to the moment a rename lands — so it
	// is the index a status-line NAME can be matched against, distinct from
	// Name (an entirely separate rename channel) and from FirstPrompt (which
	// never changes once Codex records it).
	Title       string
	CWD         string
	RolloutPath string
	// CreatedAt and ActivityAt are epoch seconds, the unit Codex stores.
	CreatedAt  int64
	ActivityAt int64
	UserThread bool
	Archived   bool
	// Prompted records that the store holds evidence of at least one user
	// message, which is all a thread without a rollout file can prove.
	Prompted bool
	// Source is the threads.source column: the entry point Codex was started
	// through. "cli" and "vscode" are the interactive front ends; "exec" is
	// `codex exec`, the one-shot non-interactive entry.
	Source string
	// Renamed records that the owner gave this thread a name of their own.
	// Codex seeds threads.title with the first prompt verbatim, so a title
	// that differs from that prompt is a rename and nothing else.
	Renamed bool
	// StateFile is the state_<N>.sqlite generation the row came from.
	StateFile string
}

// codexExecSource is the threads.source value Codex writes for `codex exec`,
// its one-shot non-interactive entry point.
const codexExecSource = "exec"

// Listed reports whether the thread belongs to the population the picker
// shows: the user's own conversations that were never archived in Codex.
func (thread CodexThread) Listed() bool {
	return thread.UserThread && !thread.Archived
}

// MachineSpawned reports whether a workflow lane or agent runner started this
// conversation instead of a person, which makes it background work rather than
// one of the owner's chats.
//
// Codex records the entry point in threads.source, and thread_source stays
// "user" for every one of them: a workflow's verify twins, its probe lanes and
// its worktree agents all arrive as thread_source='user' and are
// indistinguishable from a real chat by that column alone. The entry point is
// not: every interactive chat comes through "cli" or "vscode", and "exec" is
// `codex exec`, which no person types at a picker.
//
// The one exec thread that IS a chat is the one the owner adopted by naming
// it, and a rename is the only thing that makes threads.title differ from the
// first prompt Codex seeds it with. So a renamed thread is a real chat
// whatever started it — the exemption errs toward listing, which is the safe
// direction: a machine row that stays visible is noise, a chat that vanishes
// is a loss.
func (thread CodexThread) MachineSpawned() bool {
	return thread.Source == codexExecSource && !thread.Renamed
}

// CodexStateFiles lists the Codex state stores under codexRoot, newest
// generation first. Codex leaves older generations behind when it migrates,
// and the highest N is the live store.
func CodexStateFiles(codexRoot string) ([]string, error) {
	if codexRoot == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(codexRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Codex root %q: %w", codexRoot, err)
	}
	type generation struct {
		number int
		path   string
	}
	generations := make([]generation, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		number, ok := codexStateGeneration(entry.Name())
		if !ok {
			continue
		}
		generations = append(generations, generation{
			number: number,
			path:   filepath.Join(codexRoot, entry.Name()),
		})
	}
	sort.Slice(generations, func(left, right int) bool {
		if generations[left].number != generations[right].number {
			return generations[left].number > generations[right].number
		}
		return generations[left].path < generations[right].path
	})
	files := make([]string, 0, len(generations))
	for _, entry := range generations {
		files = append(files, entry.path)
	}
	return files, nil
}

func codexStateGeneration(name string) (int, bool) {
	rest, found := strings.CutPrefix(name, "state_")
	if !found {
		return 0, false
	}
	rest, found = strings.CutSuffix(rest, ".sqlite")
	if !found {
		return 0, false
	}
	number, err := strconv.Atoi(rest)
	if err != nil || number < 0 {
		return 0, false
	}
	return number, true
}

// ReadCodexThreads unions the threads of every state store in files, which
// CodexStateFiles orders newest generation first: a thread id recorded by
// several generations keeps the newest generation's row. A store that cannot
// be opened or whose threads table is too old to classify is skipped, because
// one unreadable generation must never blank the Codex half of the fleet.
func ReadCodexThreads(ctx context.Context, files []string) ([]CodexThread, error) {
	threadByID := make(map[string]CodexThread)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		threads, err := readCodexState(ctx, file)
		if err != nil {
			continue
		}
		for _, thread := range threads {
			if _, newer := threadByID[thread.ID]; newer {
				continue
			}
			threadByID[thread.ID] = thread
		}
	}
	threads := make([]CodexThread, 0, len(threadByID))
	for _, thread := range threadByID {
		threads = append(threads, thread)
	}
	sort.Slice(threads, func(left, right int) bool {
		return threads[left].ID < threads[right].ID
	})
	return threads, nil
}

// CodexPaneBound answers whether one live Codex pane (socket, paneID) has a
// fleet-recorded thread binding — kill.Manager.CodexPaneBinding is the
// authoritative writer, advanced by fleet.ReconcileCodexPanes on every
// gather pass. Consulting it here is what lets a rollout-less live-process
// scan follow a pane through /clear instead of matching the pane's own
// process birth time back to whichever thread it started with, forever.
type CodexPaneBound func(socket, paneID string) (id string, found bool)

// NewCodexThreadResolver names the Codex conversation behind a live process
// that holds no rollout file descriptor. The state stores are read once, on
// the first such process, so an ordinary scan never pays for the query. The
// returned function is what gather.Dependencies.CodexThread and
// gather.DetectCodexThreads expect.
func NewCodexThreadResolver(
	ctx context.Context,
	codexRoot string,
	bound CodexPaneBound,
) func(exported, cwd string, birth int64, socket, paneID string) (id, rolloutPath string) {
	return NewCodexThreadResolverRoots(ctx, []string{codexRoot}, bound)
}

// NewCodexThreadResolverRoots resolves rollout-less live processes across the
// complete config-owned Codex roster.
func NewCodexThreadResolverRoots(
	ctx context.Context,
	codexRoots []string,
	bound CodexPaneBound,
) func(exported, cwd string, birth int64, socket, paneID string) (id, rolloutPath string) {
	candidates := sync.OnceValue(func() []resolve.CodexThread {
		files := make([]string, 0)
		for _, codexRoot := range codexRoots {
			rootFiles, err := CodexStateFiles(codexRoot)
			if err != nil {
				continue
			}
			files = append(files, rootFiles...)
		}
		threads, err := ReadCodexThreads(ctx, files)
		if err != nil {
			return nil
		}
		rows := make([]resolve.CodexThread, 0, len(threads))
		for _, thread := range threads {
			if !thread.Listed() {
				continue
			}
			rows = append(rows, resolve.CodexThread{
				ID:          thread.ID,
				CWD:         thread.CWD,
				CreatedAt:   thread.CreatedAt,
				RolloutPath: thread.RolloutPath,
			})
		}
		return rows
	})
	return func(exported, cwd string, birth int64, socket, paneID string) (string, string) {
		boundID := ""
		if bound != nil {
			if id, found := bound(socket, paneID); found {
				boundID = id
			}
		}
		thread, err := resolve.CodexThreadID(exported, boundID, cwd, birth, candidates())
		if err != nil {
			return "", ""
		}
		return thread.ID, thread.RolloutPath
	}
}

// readCodexState reads one state store, read-only while Codex writes it.
func readCodexState(ctx context.Context, file string) (threads []CodexThread, returnErr error) {
	db, err := sqlitedb.OpenReadOnly(file, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("open Codex state store %q: %w", file, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close Codex state store %q: %w", file, err))
		}
	}()

	columns, err := codexStateColumns(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("read Codex state schema %q: %w", file, err)
	}
	for _, required := range []string{"id", "cwd", "created_at", "thread_source"} {
		if _, found := columns[required]; !found {
			return nil, fmt.Errorf(
				"Codex state store %q has no threads.%s column",
				file,
				required,
			)
		}
	}

	query := "SELECT id, COALESCE(cwd, ''), COALESCE(created_at, 0), " +
		codexStateColumn(columns, "source", "''") + ", " +
		codexStateColumn(columns, "thread_source", "''") + ", " +
		codexStateColumn(columns, "archived", "0") + ", " +
		codexStateColumn(columns, "rollout_path", "''") + ", " +
		codexStateColumn(columns, "name", "''") + ", " +
		codexStateColumn(columns, "title", "''") + ", " +
		codexStateColumn(columns, "first_user_message", "''") + ", " +
		codexStateColumn(columns, "preview", "''") + ", " +
		codexStateColumn(columns, "updated_at", "0") + ", " +
		codexStateColumn(columns, "recency_at", "0") + ", " +
		codexStateColumn(columns, "tokens_used", "0") +
		" FROM threads ORDER BY id"
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query Codex state store %q: %w", file, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close Codex state rows %q: %w", file, err))
		}
	}()

	threads = make([]CodexThread, 0)
	for rows.Next() {
		var thread CodexThread
		var threadSource, title, firstUserMessage, preview string
		var archived, updatedAt, recencyAt, tokensUsed int64
		if err := rows.Scan(
			&thread.ID,
			&thread.CWD,
			&thread.CreatedAt,
			&thread.Source,
			&threadSource,
			&archived,
			&thread.RolloutPath,
			&thread.Name,
			&title,
			&firstUserMessage,
			&preview,
			&updatedAt,
			&recencyAt,
			&tokensUsed,
		); err != nil {
			return nil, fmt.Errorf("scan Codex state store %q: %w", file, err)
		}
		if thread.ID == "" {
			continue
		}
		thread.UserThread = threadSource == "user"
		thread.Archived = archived != 0
		// Codex seeds title with the first prompt verbatim, so any difference
		// is the owner's own rename. An empty title is Codex's own blank, not
		// a name.
		thread.Renamed = title != "" && title != firstUserMessage
		thread.Title = strings.TrimSpace(title)
		thread.FirstPrompt = firstNonEmptyText(firstUserMessage, title, preview)
		thread.Prompted = thread.FirstPrompt != "" || tokensUsed > 0
		thread.ActivityAt = max(thread.CreatedAt, max(updatedAt, recencyAt))
		thread.StateFile = file
		threads = append(threads, thread)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Codex state store %q: %w", file, err)
	}
	return threads, nil
}

// codexStateColumns reports the threads columns this generation actually has.
// Codex grows the table over releases, so an older store is read through the
// columns it carries instead of failing the whole pass.
func codexStateColumns(ctx context.Context, db *sql.DB) (columns map[string]struct{}, returnErr error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(threads)")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close Codex schema rows: %w", err))
		}
	}()

	columns = make(map[string]struct{})
	for rows.Next() {
		var identifier int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(
			&identifier,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func codexStateColumn(
	columns map[string]struct{},
	name string,
	fallback string,
) string {
	if _, found := columns[name]; found {
		return "COALESCE(" + name + ", " + fallback + ")"
	}
	return fallback
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
