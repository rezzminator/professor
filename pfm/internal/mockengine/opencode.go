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
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// openCodeInvocation is the OpenCode command line the mock understands:
// `opencode run [--model provider/model] [--session id] <prompt>` writes the
// session store; a bare TUI takes a positional working directory.
type openCodeInvocation struct {
	subcommand string
	model      string
	sessionID  string
	prompt     string
	cwd        string
	hostname   string
	port       string
	attach     string
}

const (
	openCodeRunSubcommand = "run"
	openCodeRoleKey       = "role"
)

var openCodeValueFlags = map[string]bool{
	flagModel:    true,
	"-m":         true,
	"--session":  true,
	"-s":         true,
	"--agent":    true,
	"--prompt":   true,
	"--hostname": true,
	"--port":     true,
	"--attach":   true,
	"--format":   true,
	"--variant":  true,
}

func parseOpenCodeArgs(args []string) openCodeInvocation {
	var call openCodeInvocation
	for index := 0; index < len(args); {
		argument := args[index]
		if !strings.HasPrefix(argument, "-") {
			switch {
			case call.subcommand == "" && call.prompt == "" &&
				(argument == openCodeRunSubcommand || argument == "serve"):
				call.subcommand = argument
			case call.subcommand == openCodeRunSubcommand && call.prompt == "":
				call.prompt = argument
			case call.cwd == "":
				call.cwd = argument
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
		case "--prompt":
			call.prompt = value
		case "--hostname":
			call.hostname = value
		case "--port":
			call.port = value
		case "--attach":
			call.attach = value
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
	if call.subcommand == "serve" {
		return openCodeServe(proc, call)
	}
	if call.subcommand == openCodeRunSubcommand && call.attach != "" {
		return openCodeAttach(proc, call)
	}
	if call.subcommand != "run" {
		pane := proc.script.Pane
		if pane.Busy != "" && pane.Compacted != "" && pane.Composer != "" {
			if call.cwd != "" {
				cwd, err := filepath.Abs(call.cwd)
				if err != nil {
					warn(proc.stderr, "resolve opencode cwd %q: %v", call.cwd, err)
					return ExitUsage
				}
				info, err := os.Stat(cwd)
				if err != nil {
					warn(proc.stderr, "stat opencode cwd %q: %v", cwd, err)
					return ExitUsage
				}
				if !info.IsDir() {
					warn(proc.stderr, "opencode cwd %q is not a directory", cwd)
					return ExitUsage
				}
				proc.cwd = cwd
			}
			return runPane(proc, newOpenCodeSession(proc, call), call.prompt, call.sessionID != "")
		}
		warn(proc.stderr, "opencode pane shapes are unpinned — pfm matches no OpenCode pane text (map § 2c); "+
			"supply pane.busy, pane.compacted and pane.composer in the scenario "+
			"(have busy=%q compacted=%q composer=%q)",
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
	session := newOpenCodeSession(proc, call)
	if err := session.start(call.sessionID != ""); err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, session.finish("other")) }()
	if err := session.recordUser(call.prompt); err != nil {
		return err
	}
	if err := session.recordAssistant(reply, usage); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(proc.stdout, reply); err != nil {
		return fmt.Errorf("write opencode run reply: %w", err)
	}
	return nil
}

type openCodeSession struct {
	seat                  *seat
	proc                  *process
	call                  openCodeInvocation
	id, provider, modelID string
	database              *sql.DB
	title                 string
	lastMessageMS         int64
}

func newOpenCodeSession(proc *process, call openCodeInvocation) *openCodeSession {
	session := &openCodeSession{
		proc:     proc,
		call:     call,
		id:       call.sessionID,
		provider: "fixture",
		modelID:  proc.script.Model,
	}
	if session.id == "" {
		session.id = proc.script.SessionID
	}
	if session.id == "" {
		session.id = "ses_" + strings.ReplaceAll(newUUID(), "-", "")[:24]
	}
	if call.model != "" {
		if provider, model, found := strings.Cut(call.model, "/"); found {
			session.provider, session.modelID = provider, model
		} else {
			session.modelID = call.model
		}
	}
	return session
}

func (session *openCodeSession) composerGlyph() string { return session.proc.script.Pane.Composer }
func (session *openCodeSession) busyLine(time.Duration, Tokens) string {
	return session.proc.script.Pane.Busy
}
func (session *openCodeSession) compactedLine() string { return session.proc.script.Pane.Compacted }

func (session *openCodeSession) start(bool) (returnErr error) {
	proc := session.proc
	storePath := openCodeStore(proc)
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		return fmt.Errorf("create opencode data root: %w", err)
	}
	database, err := sqlitedb.OpenReadWrite(storePath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("open opencode store: %w", err)
	}
	defer func() {
		if returnErr != nil {
			if err := database.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close opencode store: %w", err))
			}
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
		session.id,
		projectID,
		session.id,
		proc.cwd,
		firstWords(session.call.prompt),
		proc.script.Version,
		now,
		now+1,
	); err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}
	if err := database.QueryRowContext(ctx, "SELECT title FROM session WHERE id = ?", session.id).
		Scan(&session.title); err != nil {
		return fmt.Errorf("read session title: %w", err)
	}
	session.database = database
	if session.call.subcommand != "run" {
		bound, err := bindSeat(proc, engineOpenCode, "")
		if err != nil {
			return err
		}
		session.seat = bound
		return session.paintTitle()
	}
	return nil
}

func (session *openCodeSession) prompt(string) (string, error) { return "", nil }

func (session *openCodeSession) nextMessageTime() int64 {
	now := time.Now().UnixMilli()
	if now <= session.lastMessageMS {
		now = session.lastMessageMS + 1
	}
	session.lastMessageMS = now
	return now
}

func (session *openCodeSession) recordUser(text string) error {
	ctx, database := session.proc.ctx, session.database
	now := session.nextMessageTime()
	userData, err := json.Marshal(map[string]any{
		openCodeRoleKey: roleUser,
		"agent":         "build",
		"model":         map[string]string{"providerID": session.provider, "modelID": session.modelID},
	})
	if err != nil {
		return fmt.Errorf("encode user message: %w", err)
	}
	userID := "msg_" + strings.ReplaceAll(newUUID(), "-", "")[:20]
	if err := insertOpenCodeMessage(ctx, database, userID, session.id, now, userData); err != nil {
		return err
	}
	partData, err := json.Marshal(map[string]any{keyType: blockText, blockText: text})
	if err != nil {
		return fmt.Errorf("encode text part: %w", err)
	}
	if _, err := database.ExecContext(
		ctx,
		"INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)",
		"prt_"+strings.ReplaceAll(newUUID(), "-", "")[:20],
		userID,
		session.id,
		now,
		now,
		string(partData),
	); err != nil {
		return fmt.Errorf("insert part: %w", err)
	}
	if session.title == "" {
		session.title = firstWords(text)
		if err := session.rename(session.title); err != nil {
			return err
		}
	}
	return session.touch(now)
}

func (session *openCodeSession) recordAssistant(_ string, usage Tokens) error {
	ctx, database := session.proc.ctx, session.database
	now := session.nextMessageTime()
	assistantData, err := json.Marshal(map[string]any{
		"role": roleAssistant, "agent": "build", "providerID": session.provider, "modelID": session.modelID,
		"tokens": map[string]int64{"input": usage.Input, "output": usage.Output}, "cost": usageCost(usage),
	})
	if err != nil {
		return fmt.Errorf("encode assistant message: %w", err)
	}
	if err := insertOpenCodeMessage(
		ctx,
		database,
		"msg_"+strings.ReplaceAll(newUUID(), "-", "")[:20],
		session.id,
		now,
		assistantData,
	); err != nil {
		return err
	}
	return session.touch(now)
}

func (session *openCodeSession) touch(now int64) error {
	if _, err := session.database.ExecContext(
		session.proc.ctx,
		"UPDATE session SET time_updated = ? WHERE id = ?",
		now,
		session.id,
	); err != nil {
		return fmt.Errorf("update session timestamp: %w", err)
	}
	return nil
}

func (session *openCodeSession) paintTitle() error {
	if _, err := fmt.Fprintf(
		session.proc.stdout,
		"\x1b]0;%s%s\a",
		gather.OpenCodePaneTitlePrefix,
		session.title,
	); err != nil {
		return fmt.Errorf("paint opencode title: %w", err)
	}
	return nil
}

func (session *openCodeSession) rename(name string) error {
	if _, err := session.database.ExecContext(
		session.proc.ctx,
		"UPDATE session SET title = ? WHERE id = ?",
		name,
		session.id,
	); err != nil {
		return fmt.Errorf("rename opencode session: %w", err)
	}
	session.title = name
	if session.call.subcommand != "run" {
		return session.paintTitle()
	}
	return nil
}

func (session *openCodeSession) tool(Step) (string, error) {
	return "", errors.New("opencode tool records are unpinned")
}

func (session *openCodeSession) compact(Step) error {
	return errors.New("opencode compaction records are unpinned")
}

func (session *openCodeSession) background(Step) (string, error) {
	return "", errors.New("opencode background records are unpinned")
}

func (session *openCodeSession) mcp(Step) error {
	return errors.New("opencode MCP wiring is unpinned in this mock; the step is refused, not guessed")
}

func (session *openCodeSession) clear() error                      { return errors.New("opencode clear is unpinned") }
func (session *openCodeSession) statusLine(Tokens) (string, error) { return "", nil }
func (session *openCodeSession) finish(string) error {
	var result error
	if session.database != nil {
		if err := session.database.Close(); err != nil {
			result = fmt.Errorf("close opencode store: %w", err)
		}
		session.database = nil
	}
	if session.seat != nil {
		result = errors.Join(result, session.seat.release())
		session.seat = nil
	}
	return result
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
