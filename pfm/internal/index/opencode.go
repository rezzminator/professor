package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"hostops/pfm/internal/sqlitedb"
	"hostops/pfm/internal/store"
)

// The OpenCode mirror. OpenCode keeps its sessions in a SQLite database at
// <root>/opencode.db (tables `session`, `project`, `message`, and `part`), which is
// LIVE while any OpenCode TUI runs. The reader opens it READ-ONLY and uses one
// statement, so WAL concurrency gives it a consistent, non-blocking snapshot;
// a bounded busy timeout turns a hot-writer moment into an error instead of a
// hang. An index seconds behind a live chat is the same contract the Claude
// and Codex walkers already have.

type opencodeRow struct {
	validationOnly  int64
	sessionID       string
	title           string
	directory       string
	projectDir      sql.NullString
	parentID        sql.NullString
	agent           string
	providerID      string
	modelID         string
	tokensInput     int64
	tokensOutput    int64
	cost            float64
	timeCreatedMS   int64
	timeUpdatedMS   int64
	timeArchivedMS  sql.NullInt64
	promptCount     int64
	assistantCount  int64
	firstPrompt     sql.NullString
	badMessageJSON  int64
	badMessageShape int64
	badPartJSON     int64
	badPartShape    int64
}

const opencodeSessionsQuery = `
WITH raw_message AS MATERIALIZED (
    SELECT id, session_id, time_created,
           json_valid(data) AS data_valid,
           CASE WHEN json_valid(data) THEN data ELSE '{}' END AS safe_data
      FROM message
),
message_shape AS MATERIALIZED (
    SELECT id, session_id, time_created, data_valid,
           json_type(safe_data, '$.role') AS role_type,
           CASE WHEN json_type(safe_data, '$.role') = 'text'
                THEN json_extract(safe_data, '$.role') END AS role,
           json_type(safe_data, '$.agent') AS agent_type,
           CASE WHEN json_type(safe_data, '$.agent') = 'text'
                THEN json_extract(safe_data, '$.agent') END AS agent,
           json_type(safe_data, '$.model') AS model_type,
           json_type(safe_data, '$.model.providerID') AS provider_type,
           CASE WHEN json_type(safe_data, '$.model.providerID') = 'text'
                THEN json_extract(safe_data, '$.model.providerID') END AS provider_id,
		   json_type(safe_data, '$.model.modelID') AS model_id_type,
		   CASE WHEN json_type(safe_data, '$.model.modelID') = 'text'
				THEN json_extract(safe_data, '$.model.modelID') END AS model_id,
		   json_type(safe_data, '$.providerID') AS assistant_provider_type,
		   json_type(safe_data, '$.modelID') AS assistant_model_id_type,
           json_type(safe_data, '$.tokens') AS tokens_type,
           json_type(safe_data, '$.tokens.input') AS input_type,
           CASE WHEN json_type(safe_data, '$.tokens.input') = 'integer'
                THEN json_extract(safe_data, '$.tokens.input') ELSE 0 END AS tokens_input,
           json_type(safe_data, '$.tokens.output') AS output_type,
           CASE WHEN json_type(safe_data, '$.tokens.output') = 'integer'
                THEN json_extract(safe_data, '$.tokens.output') ELSE 0 END AS tokens_output,
           json_type(safe_data, '$.cost') AS cost_type,
           CASE WHEN json_type(safe_data, '$.cost') IN ('integer', 'real')
                THEN json_extract(safe_data, '$.cost') ELSE 0 END AS cost
      FROM raw_message
),
raw_part AS MATERIALIZED (
    SELECT id, message_id, session_id, time_created,
           json_valid(data) AS data_valid,
           CASE WHEN json_valid(data) THEN data ELSE '{}' END AS safe_data
      FROM part
),
part_shape AS MATERIALIZED (
    SELECT id, message_id, session_id, time_created, data_valid,
           json_type(safe_data, '$.type') AS part_type_type,
           CASE WHEN json_type(safe_data, '$.type') = 'text'
                THEN json_extract(safe_data, '$.type') END AS part_type,
           json_type(safe_data, '$.text') AS text_type,
           CASE WHEN json_type(safe_data, '$.text') = 'text'
                THEN json_extract(safe_data, '$.text') END AS text
      FROM raw_part
),
usage AS MATERIALIZED (
    SELECT session_id,
           SUM(CASE WHEN role = 'user' THEN 1 ELSE 0 END) AS prompt_count,
           SUM(CASE WHEN role = 'assistant' THEN 1 ELSE 0 END) AS assistant_count,
           SUM(CASE WHEN role = 'assistant' THEN tokens_input ELSE 0 END) AS tokens_input,
           SUM(CASE WHEN role = 'assistant' THEN tokens_output ELSE 0 END) AS tokens_output,
           SUM(CASE WHEN role = 'assistant' THEN cost ELSE 0 END) AS cost
      FROM message_shape
     GROUP BY session_id
),
latest_user_candidates AS MATERIALIZED (
    SELECT session_id, agent, provider_id, model_id,
           row_number() OVER (
               PARTITION BY session_id ORDER BY time_created DESC, id DESC
           ) AS row_number
      FROM message_shape
     WHERE role = 'user'
),
latest_user AS MATERIALIZED (
    SELECT session_id, agent, provider_id, model_id
      FROM latest_user_candidates
     WHERE row_number = 1
),
first_prompt_candidates AS MATERIALIZED (
    SELECT message_shape.session_id, part_shape.text,
           row_number() OVER (
               PARTITION BY message_shape.session_id
               ORDER BY message_shape.time_created, message_shape.id,
                        part_shape.time_created, part_shape.id
           ) AS row_number
      FROM message_shape
      JOIN part_shape
        ON part_shape.message_id = message_shape.id
       AND part_shape.session_id = message_shape.session_id
     WHERE message_shape.role = 'user'
       AND part_shape.part_type = 'text'
       AND part_shape.text <> ''
),
first_prompt AS MATERIALIZED (
    SELECT session_id, text
      FROM first_prompt_candidates
     WHERE row_number = 1
),
validation AS MATERIALIZED (
    SELECT
      (SELECT COUNT(*) FROM message_shape WHERE data_valid = 0) AS bad_message_json,
      (SELECT COUNT(*)
         FROM message_shape
        WHERE data_valid <> 0
          AND CASE
                WHEN role_type IS NOT 'text' THEN 1
                WHEN role NOT IN ('user', 'assistant') THEN 1
                WHEN role = 'user' AND (
                     agent_type IS NOT 'text'
                     OR model_type IS NOT 'object'
                     OR provider_type IS NOT 'text'
                     OR model_id_type IS NOT 'text'
                ) THEN 1
				WHEN role = 'assistant' AND (
				     agent_type IS NOT 'text'
				     OR assistant_provider_type IS NOT 'text'
				     OR assistant_model_id_type IS NOT 'text'
				     OR
				     tokens_type IS NOT 'object'
                     OR input_type IS NOT 'integer'
                     OR output_type IS NOT 'integer'
                     OR (cost_type IS NOT 'integer' AND cost_type IS NOT 'real')
                ) THEN 1
                ELSE 0
              END = 1) AS bad_message_shape,
      (SELECT COUNT(*) FROM part_shape WHERE data_valid = 0) AS bad_part_json,
      (SELECT COUNT(*)
         FROM part_shape
        WHERE data_valid <> 0
          AND (part_type_type IS NOT 'text'
               OR (part_type = 'text' AND text_type IS NOT 'text'))) AS bad_part_shape
)
SELECT 0 AS validation_only,
       s.id, s.title, s.directory, p.worktree, s.parent_id,
       COALESCE(latest_user.agent, ''),
       COALESCE(latest_user.provider_id, ''),
       COALESCE(latest_user.model_id, ''),
       COALESCE(usage.tokens_input, 0),
       COALESCE(usage.tokens_output, 0),
       COALESCE(usage.cost, 0),
       s.time_created, s.time_updated, s.time_archived,
       COALESCE(usage.prompt_count, 0), COALESCE(usage.assistant_count, 0), first_prompt.text,
       validation.bad_message_json, validation.bad_message_shape,
       validation.bad_part_json, validation.bad_part_shape
FROM session s
LEFT JOIN project p ON p.id = s.project_id
LEFT JOIN usage ON usage.session_id = s.id
LEFT JOIN latest_user ON latest_user.session_id = s.id
LEFT JOIN first_prompt ON first_prompt.session_id = s.id
CROSS JOIN validation
UNION ALL
SELECT 1 AS validation_only,
       '', '', '', NULL, NULL, '', '', '',
       0, 0, 0, 0, 0, NULL, 0, 0, NULL,
       validation.bad_message_json, validation.bad_message_shape,
       validation.bad_part_json, validation.bad_part_shape
  FROM validation
 WHERE NOT EXISTS (SELECT 1 FROM session)
`

// ReadOpencodeSessions reads OpenCode's session store into OcSession rows.
// A missing opencode.db means the engine is not installed — that is a real,
// checkable answer ("no store exists"), not a silent failure, so it returns
// zero sessions and a nil error; a PRESENT database that cannot be opened or
// parsed is an error and says so.
func ReadOpencodeSessions(ctx context.Context, root string) (
	sessions []store.OcSession,
	returnErr error,
) {
	dbPath := filepath.Join(root, "opencode.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat opencode store %s: %w", dbPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("opencode store %s is a directory", dbPath)
	}

	// Direct read-only access to the LIVE store: WAL-mode readers never block
	// writers or tear state, and the one statement pins one consistent snapshot
	// across every materialized shape and validation CTE. A bounded busy timeout
	// converts a hot-writer moment into an error we report, never an unbounded wait.
	db, err := sqlitedb.OpenReadOnly(dbPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("open opencode store read-only: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("close opencode store read-only: %w", closeErr),
			)
		}
	}()
	rows, err := db.QueryContext(ctx, opencodeSessionsQuery)
	if err != nil {
		return nil, fmt.Errorf("query opencode sessions: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("close opencode session rows: %w", closeErr),
			)
		}
	}()

	sessions = make([]store.OcSession, 0)
	for rows.Next() {
		var row opencodeRow
		if err := rows.Scan(
			&row.validationOnly,
			&row.sessionID, &row.title, &row.directory,
			&row.projectDir, &row.parentID, &row.agent,
			&row.providerID, &row.modelID,
			&row.tokensInput, &row.tokensOutput, &row.cost,
			&row.timeCreatedMS, &row.timeUpdatedMS, &row.timeArchivedMS,
			&row.promptCount, &row.assistantCount, &row.firstPrompt,
			&row.badMessageJSON, &row.badMessageShape,
			&row.badPartJSON, &row.badPartShape,
		); err != nil {
			return nil, fmt.Errorf("scan opencode session: %w", err)
		}
		switch {
		case row.badMessageJSON != 0:
			return nil, errors.New("validate opencode message JSON: malformed rows present")
		case row.badMessageShape != 0:
			return nil, errors.New("validate opencode message shape: required role fields have invalid types")
		case row.badPartJSON != 0:
			return nil, errors.New("validate opencode part JSON: malformed rows present")
		case row.badPartShape != 0:
			return nil, errors.New("validate opencode part shape: required type fields have invalid types")
		}
		if row.validationOnly != 0 {
			continue
		}
		sessions = append(sessions, store.OcSession{
			ID:             row.sessionID,
			Title:          row.title,
			Directory:      row.directory,
			ProjectDir:     nonEmpty(row.projectDir),
			ParentID:       nonEmpty(row.parentID),
			Agent:          row.agent,
			Model:          compactModel(row.providerID, row.modelID),
			FirstPrompt:    clip(nonEmpty(row.firstPrompt)),
			PromptCount:    row.promptCount,
			AssistantCount: row.assistantCount,
			TokensInput:    row.tokensInput,
			TokensOutput:   row.tokensOutput,
			CostMillicents: int64(math.Round(row.cost * 100000)),
			TimeCreatedMS:  row.timeCreatedMS,
			TimeUpdatedMS:  row.timeUpdatedMS,
			TimeArchivedMS: row.timeArchivedMS.Int64,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate opencode sessions: %w", err)
	}
	return sessions, nil
}

// ProbeOpencodeStore runs the production reader and discards its rows. Doctor
// therefore proves that both the native schema and the stored JSON are readable;
// a schema-only prepare could report healthy while every real index pass fails.
func ProbeOpencodeStore(ctx context.Context, root string) error {
	_, err := ReadOpencodeSessions(ctx, root)
	return err
}

func nonEmpty(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

// compactModel flattens OpenCode's validated user-message model fields to the
// provider/model word used by the picker.
func compactModel(providerID, modelID string) string {
	if modelID == "" {
		return ""
	}
	if providerID == "" {
		return modelID
	}
	return providerID + "/" + modelID
}

// clip bounds a first prompt to what a picker row can show.
func clip(prompt string) string {
	runes := []rune(prompt)
	const maxPromptRunes = 200
	if len(runes) > maxPromptRunes {
		return string(runes[:maxPromptRunes])
	}
	return prompt
}

// syncOpencodeMirror replaces the oc_sessions mirror with one pass's view.
func syncOpencodeMirror(
	ctx context.Context,
	database *store.Store,
	root string,
	counters *Counters,
) error {
	sessions, err := ReadOpencodeSessions(ctx, root)
	if err != nil {
		return fmt.Errorf("read opencode sessions: %w", err)
	}
	if err := database.ReplaceOcSessions(ctx, sessions); err != nil {
		return fmt.Errorf("replace opencode mirror: %w", err)
	}
	counters.OcSessions = len(sessions)
	return nil
}
