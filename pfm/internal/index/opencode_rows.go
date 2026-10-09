package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// The rows of OpenCode's store beyond the session mirror: one session's
// messages and parts for `pfm transcript`, every session's messages for
// `pfm tokens --opencode`, and the one decoding of a message's data both read.
// Every read opens the store READ-ONLY through openOpenCodeStore and runs ONE
// statement, so it sees one consistent WAL snapshot and never blocks a running
// OpenCode.

// openCodeStoreName is the session store's file name under an OpenCode root.
const openCodeStoreName = "opencode.db"

// ErrNoOpenCodeStore is a root holding no opencode.db: OpenCode never ran there.
var ErrNoOpenCodeStore = errors.New("no opencode store")

// ErrNoOpenCodeSession is a session id the store does not hold.
var ErrNoOpenCodeSession = errors.New("no such opencode session")

// OpenCodeStorePath is the session store under an OpenCode root.
func OpenCodeStorePath(root string) string { return filepath.Join(root, openCodeStoreName) }

// openOpenCodeStore opens root's store read-only. A missing store is
// ErrNoOpenCodeStore naming the path; a present one that cannot be opened is
// an error that says so.
func openOpenCodeStore(root string) (*sql.DB, error) {
	dbPath := OpenCodeStorePath(root)
	info, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w at %s", ErrNoOpenCodeStore, dbPath)
		}
		return nil, fmt.Errorf("stat opencode store %s: %w", dbPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("opencode store %s is a directory", dbPath)
	}
	// Direct read-only access to the LIVE store: WAL-mode readers never block
	// writers or tear state, and one statement pins one consistent snapshot. A
	// bounded busy timeout converts a hot-writer moment into an error we
	// report, never an unbounded wait.
	db, err := sqlitedb.OpenReadOnly(dbPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("open opencode store read-only: %w", err)
	}
	return db, nil
}

// OpenCodeRow is one message or part row as the store keeps it: a part's
// MessageID names its message, a message's is empty; Data is the row's JSON.
type OpenCodeRow struct {
	ID, MessageID string
	TimeCreatedMS int64
	Data          string
}

// OpenCodeSessionRows is one session row and the message and part rows read
// with it: messages by (time_created, id), parts by id.
type OpenCodeSessionRows struct {
	ID, ParentID, Title, Directory string
	TimeCreatedMS, TimeUpdatedMS   int64
	Messages                       []OpenCodeRow
	Parts                          []OpenCodeRow
}

// The row kinds one statement returns, in the order a session's rows sort.
const (
	openCodeSessionKind = iota
	openCodeMessageKind
	openCodePartKind
)

const openCodeSessionRowsQuery = `
SELECT 0, id, COALESCE(parent_id, ''), title, directory, time_created, time_updated, ''
  FROM session WHERE id = ?
UNION ALL
SELECT 1, id, '', '', '', time_created, time_updated, data FROM message WHERE session_id = ?
UNION ALL
SELECT 2, id, message_id, '', '', time_created, time_updated, data FROM part WHERE session_id = ?
`

// ReadOpenCodeSessionRows reads one session's row, messages and parts in one
// statement. A missing store is ErrNoOpenCodeStore, an id the store does not
// hold ErrNoOpenCodeSession, each naming what it looked for.
func ReadOpenCodeSessionRows(ctx context.Context, root, sessionID string) (OpenCodeSessionRows, error) {
	var session OpenCodeSessionRows
	found := false
	err := queryOpenCodeStore(ctx, root, openCodeSessionRowsQuery, []any{sessionID, sessionID, sessionID},
		func(rows *sql.Rows) error {
			var (
				kind                      int
				id, ref, title, directory string
				created, updated          int64
				data                      string
			)
			if err := rows.Scan(&kind, &id, &ref, &title, &directory, &created, &updated, &data); err != nil {
				return fmt.Errorf("scan opencode session row: %w", err)
			}
			switch kind {
			case openCodeSessionKind:
				found = true
				session.ID, session.ParentID, session.Title, session.Directory = id, ref, title, directory
				session.TimeCreatedMS, session.TimeUpdatedMS = created, updated
			case openCodeMessageKind:
				session.Messages = append(session.Messages, OpenCodeRow{ID: id, TimeCreatedMS: created, Data: data})
			case openCodePartKind:
				session.Parts = append(session.Parts,
					OpenCodeRow{ID: id, MessageID: ref, TimeCreatedMS: created, Data: data})
			default:
				return fmt.Errorf("scan opencode session row: unknown row kind %d", kind)
			}
			return nil
		})
	if err != nil {
		return OpenCodeSessionRows{}, err
	}
	if !found {
		return OpenCodeSessionRows{}, fmt.Errorf("%w %s in %s", ErrNoOpenCodeSession, sessionID,
			OpenCodeStorePath(root))
	}
	sortMessages(session.Messages)
	sort.Slice(session.Parts, func(left, right int) bool { return session.Parts[left].ID < session.Parts[right].ID })
	return session, nil
}

const openCodeUsageQuery = `
SELECT s.id, COALESCE(s.parent_id, ''), s.title, s.directory, s.time_created, s.time_updated,
       m.id, m.time_created, m.data
  FROM session s
  LEFT JOIN message m ON m.session_id = s.id AND m.time_created >= ?
`

// ReadOpenCodeUsage reads every session, by id, each with its messages created
// at or after sinceMS, in one statement: a message older than the window is
// not the window's spend, whatever session it belongs to.
func ReadOpenCodeUsage(ctx context.Context, root string, sinceMS int64) ([]OpenCodeSessionRows, error) {
	byID := map[string]*OpenCodeSessionRows{}
	err := queryOpenCodeStore(ctx, root, openCodeUsageQuery, []any{sinceMS}, func(rows *sql.Rows) error {
		var (
			session   OpenCodeSessionRows
			messageID sql.NullString
			created   sql.NullInt64
			data      sql.NullString
		)
		if err := rows.Scan(&session.ID, &session.ParentID, &session.Title, &session.Directory,
			&session.TimeCreatedMS, &session.TimeUpdatedMS, &messageID, &created, &data); err != nil {
			return fmt.Errorf("scan opencode usage row: %w", err)
		}
		held := byID[session.ID]
		if held == nil {
			held = &session
			byID[session.ID] = held
		}
		if messageID.Valid {
			held.Messages = append(held.Messages,
				OpenCodeRow{ID: messageID.String, TimeCreatedMS: created.Int64, Data: data.String})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sessions := make([]OpenCodeSessionRows, 0, len(byID))
	for _, session := range byID {
		sortMessages(session.Messages)
		sessions = append(sessions, *session)
	}
	sort.Slice(sessions, func(left, right int) bool { return sessions[left].ID < sessions[right].ID })
	return sessions, nil
}

// sortMessages orders messages as OpenCode lists them: by creation, then id.
func sortMessages(messages []OpenCodeRow) {
	sort.Slice(messages, func(left, right int) bool {
		if messages[left].TimeCreatedMS != messages[right].TimeCreatedMS {
			return messages[left].TimeCreatedMS < messages[right].TimeCreatedMS
		}
		return messages[left].ID < messages[right].ID
	})
}

// queryOpenCodeStore runs one read-only statement over root's store, handing
// each row to scan; every failure names the store.
func queryOpenCodeStore(
	ctx context.Context,
	root, query string,
	args []any,
	scan func(*sql.Rows) error,
) (returnErr error) {
	db, err := openOpenCodeStore(root)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close opencode store read-only: %w", closeErr))
		}
	}()
	read := obs.SQL(ctx, pfmengine.MustLookup(pfmengine.OpenCode).LongName, query)
	rows, err := db.QueryContext(ctx, query, args...)
	read.End(-1, err)
	if err != nil {
		return fmt.Errorf("query opencode store %s: %w", OpenCodeStorePath(root), err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close opencode rows: %w", closeErr))
		}
	}()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate opencode store %s: %w", OpenCodeStorePath(root), err)
	}
	return nil
}

// OpenCodeModel is the model a user message asked for.
type OpenCodeModel struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	Variant    string `json:"variant"`
}

// OpenCodeTokens is an assistant message's token record. Input is UNCACHED
// input; Output excludes Reasoning; Total, when present, is all five summed.
type OpenCodeTokens struct {
	Total     *int64 `json:"total"`
	Input     int64  `json:"input"`
	Output    int64  `json:"output"`
	Reasoning int64  `json:"reasoning"`
	Cache     struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

// OpenCodeMessage is a message row's data as OpenCode's SDK types it
// (UserMessage | AssistantMessage): a user message carries Model, an
// assistant ProviderID, ModelID, Tokens, Cost and, on a failed turn, Error.
type OpenCodeMessage struct {
	Role       string          `json:"role"`
	Agent      string          `json:"agent"`
	Mode       string          `json:"mode"`
	ProviderID string          `json:"providerID"`
	ModelID    string          `json:"modelID"`
	Variant    string          `json:"variant"`
	Finish     string          `json:"finish"`
	Model      *OpenCodeModel  `json:"model"`
	Cost       *float64        `json:"cost"`
	Tokens     *OpenCodeTokens `json:"tokens"`
	Error      json.RawMessage `json:"error"`
}

// DecodeOpenCodeMessage decodes a message row's data; a value that is no
// object, or a field of another type than the SDK's, is an error.
func DecodeOpenCodeMessage(data string) (OpenCodeMessage, error) {
	var message OpenCodeMessage
	if err := json.Unmarshal([]byte(data), &message); err != nil {
		return OpenCodeMessage{}, fmt.Errorf("decode opencode message: %w", err)
	}
	return message, nil
}
