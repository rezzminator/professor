package mockengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// openCodeInvocation is the OpenCode command line the mock understands:
// `opencode run [--model provider/model] [--session id] <prompt>` writes the
// session store; the bare TUI is refused because its pane is unpinned.
type openCodeInvocation struct {
	subcommand string
	model      string
	sessionID  string
	prompt     string
}

var openCodeValueFlags = map[string]bool{flagModel: true, "-m": true, "--session": true, "-s": true, "--agent": true}

func parseOpenCodeArgs(args []string) openCodeInvocation {
	var call openCodeInvocation
	for index := 0; index < len(args); {
		argument := args[index]
		if !strings.HasPrefix(argument, "-") {
			if call.subcommand == "" && call.prompt == "" && argument == "run" {
				call.subcommand = argument
			} else if call.prompt == "" {
				call.prompt = argument
			}
			index++
			continue
		}
		name, inline, hasInline := strings.Cut(argument, "=")
		value, next := inline, index+1
		if openCodeValueFlags[name] && !hasInline && next < len(args) {
			value = args[next]
			next++
		}
		switch name {
		case flagModel, "-m":
			call.model = value
		case "--session", "-s":
			call.sessionID = value
		}
		index = next
	}
	return call
}

func serveOpenCode(proc *process) int {
	call := parseOpenCodeArgs(proc.args)
	for index := range proc.script.Steps {
		if proc.script.Steps[index].Type == StepMCP {
			warn(proc.stderr, "opencode MCP wiring is unpinned in this mock — pfm has no opencode.json writer or "+
				"reader (map § 2c UNRESOLVED); the step is refused, not guessed")
			return ExitUnpinned
		}
	}
	if call.subcommand != "run" {
		pane := proc.script.Pane
		warn(proc.stderr, "opencode pane shapes are unpinned — pfm matches no OpenCode pane text (map § 2c); "+
			"supply pane.busy, pane.compacted and pane.composer in the scenario "+
			"(have busy=%q compacted=%q composer=%q) — the TUI is refused, not guessed",
			pane.Busy, pane.Compacted, pane.Composer)
		return ExitUnpinned
	}
	if call.prompt == "" {
		warn(proc.stderr, "opencode run needs a prompt")
		return ExitUsage
	}
	if err := openCodeRun(proc, call); err != nil {
		warn(proc.stderr, "%v", err)
		return ExitUsage
	}
	return 0
}

// openCodeStore is opencode.db under the descriptor's default root
// (internal/engine/builtin.go: <HOME>/.local/share/opencode), where
// internal/index/opencode.go:216 opens it. NAMED GAP: XDG_DATA_HOME is not
// consulted, because pfm's reader does not consult it either.
func openCodeStore(proc *process) string {
	roots := pfmengine.MustLookup(pfmengine.OpenCode).DefaultRoots(proc.env("HOME"))
	return filepath.Join(roots[0], "opencode.db")
}

// openCodeSchema is the v1.14.30 Drizzle schema pfm's reader was written
// against (internal/index/opencode_test.go seeds the same tables). Only the
// columns index/opencode.go:50-205 reads carry meaning here.
const openCodeSchema = `
CREATE TABLE IF NOT EXISTS project (
  id TEXT PRIMARY KEY, worktree TEXT NOT NULL, vcs TEXT, name TEXT,
  time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, sandboxes TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS session (
  id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, slug TEXT NOT NULL, directory TEXT NOT NULL,
  title TEXT NOT NULL, version TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
  time_archived INTEGER
);
CREATE TABLE IF NOT EXISTS message (
  id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
  data TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS part (
  id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL, data TEXT NOT NULL
);`

// openCodeRun is one non-interactive exchange: it creates the project and
// session rows on first use, then a user message with a text part and an
// assistant message with the usage the reader sums.
func openCodeRun(proc *process, call openCodeInvocation) (returnErr error) {
	step := proc.script.next()
	for !step.terminal() {
		step = proc.script.next()
	}
	if step.Type == StepCrash {
		return fmt.Errorf("crash step: exit %d", step.ExitCode)
	}
	reply, busyMS, usage := proc.script.turnReply(step)
	if !sleepOrCancel(proc.ctx, time.Duration(busyMS)*time.Millisecond) {
		return errors.New("cancelled")
	}
	provider, modelID := "fixture", proc.script.Model
	if call.model != "" {
		if before, after, found := strings.Cut(call.model, "/"); found {
			provider, modelID = before, after
		} else {
			modelID = call.model
		}
	}
	sessionID := call.sessionID
	if sessionID == "" {
		sessionID = proc.script.SessionID
	}
	if sessionID == "" {
		sessionID = "ses_" + strings.ReplaceAll(newUUID(), "-", "")[:24]
	}
	storePath := openCodeStore(proc)
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		return fmt.Errorf("create opencode data root: %w", err)
	}
	database, err := sqlitedb.OpenReadWrite(storePath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("open opencode store: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close opencode store: %w", err))
		}
	}()
	ctx := proc.ctx
	if _, err := database.ExecContext(ctx, openCodeSchema); err != nil {
		return fmt.Errorf("create opencode schema: %w", err)
	}
	now := time.Now().UnixMilli()
	projectID := "prj_" + strings.ReplaceAll(newUUID(), "-", "")[:20]
	var existingProject string
	err = database.QueryRowContext(ctx, "SELECT id FROM project WHERE worktree = ?", proc.cwd).Scan(&existingProject)
	switch {
	case err == nil:
		projectID = existingProject
	case errors.Is(err, sql.ErrNoRows):
		if _, err := database.ExecContext(ctx,
			"INSERT INTO project (id, worktree, time_created, time_updated, sandboxes) VALUES (?, ?, ?, ?, '[]')",
			projectID, proc.cwd, now, now); err != nil {
			return fmt.Errorf("insert project: %w", err)
		}
	default:
		return fmt.Errorf("look up project: %w", err)
	}
	if _, err := database.ExecContext(
		ctx,
		`INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated)
		 VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET time_updated = excluded.time_updated`,
		sessionID,
		projectID,
		sessionID,
		proc.cwd,
		firstWords(call.prompt),
		proc.script.Version,
		now,
		now+1,
	); err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}
	userData, err := json.Marshal(map[string]any{
		"role": roleUser, "agent": "build", "model": map[string]string{"providerID": provider, "modelID": modelID},
	})
	if err != nil {
		return fmt.Errorf("encode user message: %w", err)
	}
	userID := "msg_" + strings.ReplaceAll(newUUID(), "-", "")[:20]
	if err := insertOpenCodeMessage(ctx, database, userID, sessionID, now, userData); err != nil {
		return err
	}
	partData, err := json.Marshal(map[string]any{keyType: blockText, blockText: call.prompt})
	if err != nil {
		return fmt.Errorf("encode text part: %w", err)
	}
	if _, err := database.ExecContext(ctx,
		"INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)",
		"prt_"+strings.ReplaceAll(newUUID(), "-", "")[:20], userID, sessionID, now, now, string(partData)); err != nil {
		return fmt.Errorf("insert part: %w", err)
	}
	assistantData, err := json.Marshal(map[string]any{
		"role": roleAssistant, "agent": "build", "providerID": provider, "modelID": modelID,
		"tokens": map[string]int64{"input": usage.Input, "output": usage.Output}, "cost": usageCost(usage),
	})
	if err != nil {
		return fmt.Errorf("encode assistant message: %w", err)
	}
	if err := insertOpenCodeMessage(
		ctx,
		database,
		"msg_"+strings.ReplaceAll(newUUID(), "-", "")[:20],
		sessionID,
		now+1,
		assistantData,
	); err != nil {
		return err
	}
	fmt.Fprintln(proc.stdout, reply)
	return nil
}

func insertOpenCodeMessage(ctx context.Context, database *sql.DB, id, sessionID string, at int64, data []byte) error {
	if _, err := database.ExecContext(ctx,
		"INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)",
		id, sessionID, at, at, string(data)); err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

// firstWords is the session title OpenCode derives from the first prompt.
func firstWords(prompt string) string {
	words := strings.Fields(prompt)
	if len(words) > 6 {
		words = words[:6]
	}
	return strings.Join(words, " ")
}
