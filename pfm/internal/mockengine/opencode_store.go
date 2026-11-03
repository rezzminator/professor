package mockengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

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
		openCodeRoleKey:  roleUser,
		openCodeAgentKey: openCodeBuildAgent,
		openCodeModelKey: map[string]string{
			openCodeProviderIDKey: session.provider,
			openCodeModelIDKey:    session.modelID,
		},
	})
	if err != nil {
		return fmt.Errorf("encode user message: %w", err)
	}
	userID := "msg_" + strings.ReplaceAll(uuid.NewV4().String(), "-", "")[:20]
	if err := insertOpenCodeMessage(ctx, database, userID, session.id, now, userData); err != nil {
		return err
	}
	partData, err := json.Marshal(map[string]any{keyType: blockText, blockText: text})
	if err != nil {
		return fmt.Errorf("encode text part: %w", err)
	}
	if err := insertOpenCodePart(
		ctx,
		database,
		"prt_"+strings.ReplaceAll(uuid.NewV4().String(), "-", "")[:20],
		userID,
		session.id,
		now,
		partData,
	); err != nil {
		return err
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
		openCodeRoleKey:       roleAssistant,
		openCodeAgentKey:      openCodeBuildAgent,
		openCodeProviderIDKey: session.provider,
		openCodeModelIDKey:    session.modelID,
		"tokens": map[string]int64{
			openCodeInputKey:  usage.Input,
			openCodeOutputKey: usage.Output,
		},
		"cost": usageCost(usage),
	})
	if err != nil {
		return fmt.Errorf("encode assistant message: %w", err)
	}
	if err := insertOpenCodeMessage(
		ctx,
		database,
		"msg_"+strings.ReplaceAll(uuid.NewV4().String(), "-", "")[:20],
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
	session.updatedMS = now
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
	return session.setTitle(name, true)
}

func (session *openCodeSession) nativeTitle(step Step) error {
	return session.setTitle(step.Name, false)
}

func (session *openCodeSession) setTitle(name string, notify bool) error {
	if _, err := session.database.ExecContext(
		session.proc.ctx,
		"UPDATE session SET title = ? WHERE id = ?",
		name,
		session.id,
	); err != nil {
		return fmt.Errorf("rename opencode session: %w", err)
	}
	session.title = name
	if notify {
		if err := session.updated(); err != nil {
			return err
		}
	}
	if session.call.subcommand != openCodeRunSubcommand {
		return session.paintTitle()
	}
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

func insertOpenCodePart(
	ctx context.Context,
	database *sql.DB,
	id, messageID, sessionID string,
	at int64,
	data []byte,
) error {
	if _, err := database.ExecContext(
		ctx,
		"INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)",
		id,
		messageID,
		sessionID,
		at,
		at,
		string(data),
	); err != nil {
		return fmt.Errorf("insert part: %w", err)
	}
	return nil
}
