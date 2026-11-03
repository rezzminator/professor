package mockengine

import (
	"encoding/json"
	"fmt"
	"strings"
	"uuid"
)

func (session *openCodeSession) compact(step Step) error {
	if step.Reply == "" {
		return fmt.Errorf("OpenCode compaction needs a scripted reply")
	}
	if err := session.status("busy"); err != nil {
		return err
	}
	if err := session.compacting(); err != nil {
		return err
	}
	now := session.nextMessageTime()
	userID := "msg_" + strings.ReplaceAll(uuid.NewV4().String(), "-", "")[:20]
	user := map[string]any{
		openCodeRoleKey:  "user",
		openCodeAgentKey: openCodeBuildAgent,
		openCodeModelKey: map[string]string{
			openCodeProviderIDKey: session.provider,
			openCodeModelIDKey:    session.modelID,
		},
	}
	userData, err := json.Marshal(user)
	if err != nil {
		return err
	}
	if err := insertOpenCodeMessage(session.proc.ctx, session.database, userID, session.id, now, userData); err != nil {
		return err
	}
	part, err := json.Marshal(map[string]any{keyType: openCodeCompactionPart, "auto": false})
	if err != nil {
		return err
	}
	if err := insertOpenCodePart(
		session.proc.ctx,
		session.database,
		"prt_"+strings.ReplaceAll(uuid.NewV4().String(), "-", "")[:20],
		userID,
		session.id,
		now,
		part,
	); err != nil {
		return err
	}
	summaryID := "msg_" + strings.ReplaceAll(uuid.NewV4().String(), "-", "")[:20]
	summary := map[string]any{
		openCodeRoleKey:       "assistant",
		openCodeAgentKey:      openCodeCompactionPart,
		"mode":                openCodeCompactionPart,
		"summary":             true,
		"parentID":            userID,
		openCodeProviderIDKey: session.provider,
		openCodeModelIDKey:    session.modelID,
		"path":                map[string]string{"cwd": session.proc.cwd, "root": session.proc.cwd},
		"cost":                0,
		"tokens": map[string]any{
			openCodeInputKey:  0,
			openCodeOutputKey: 0,
			"reasoning":       0,
			"cache":           map[string]int{openCodeCacheReadKey: 0, "write": 0},
		},
	}
	summaryData, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	if err := insertOpenCodeMessage(
		session.proc.ctx,
		session.database,
		summaryID,
		session.id,
		now+1,
		summaryData,
	); err != nil {
		return err
	}
	text, err := json.Marshal(map[string]string{keyType: blockText, keyText: step.Reply})
	if err != nil {
		return err
	}
	if err := insertOpenCodePart(
		session.proc.ctx,
		session.database,
		"prt_"+strings.ReplaceAll(uuid.NewV4().String(), "-", "")[:20],
		summaryID,
		session.id,
		now+1,
		text,
	); err != nil {
		return err
	}
	if err := session.touch(now + 1); err != nil {
		return err
	}
	if err := session.compacted(); err != nil {
		return err
	}
	return session.stop()
}

func (session *openCodeSession) compacted() error {
	return session.event("session.compacted", map[string]any{openCodeSessionIDKey: session.id})
}

func (session *openCodeSession) compacting() error {
	return session.plugin(
		"experimental.session.compacting",
		map[string]string{openCodeSessionIDKey: session.id},
		map[string]any{"context": []string{}},
	)
}
