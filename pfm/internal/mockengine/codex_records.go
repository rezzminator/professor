package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
)

func (session *codexSession) write(record codexRecord) error {
	if session.call.ephemeral {
		return nil
	}
	if record.Timestamp == "" {
		record.Timestamp = stamp(time.Now())
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode rollout record: %w", err)
	}
	if session.rolloutFile == nil {
		return fmt.Errorf("rollout %s is not open", session.rollout)
	}
	if _, err := session.rolloutFile.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("append rollout %s: %w", session.rollout, err)
	}
	return nil
}

func (session *codexSession) openRollout() error {
	if codexmeta.Compressed(session.rollout) {
		return fmt.Errorf("open rollout %s: a compressed rollout is never appended to", session.rollout)
	}
	if err := os.MkdirAll(filepath.Dir(session.rollout), 0o700); err != nil {
		return fmt.Errorf("create rollout directory: %w", err)
	}
	file, err := os.OpenFile(session.rollout, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open rollout %s: %w", session.rollout, err)
	}
	session.rolloutFile = file
	return nil
}

// rolloutFor is the rollout path for a thread: sessions/YYYY/MM/DD/
// rollout-<stamp>-<thread>.jsonl, the name codexmeta.RolloutID strips the id off.
func (session *codexSession) rolloutFor(threadID string, now time.Time) string {
	now = now.UTC()
	return filepath.Join(
		session.codexHome, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"),
		"rollout-"+now.Format("2006-01-02T15-04-05")+"-"+threadID+".jsonl",
	)
}

// findRollout locates an existing thread's rollout for resume, plain or
// compressed (codexmeta.WalkRollouts, the plain twin winning).
func (session *codexSession) findRollout(threadID string) (string, error) {
	rollouts, errs := codexmeta.WalkRollouts(session.proc.ctx, session.codexHome, codexmeta.SessionsDir)
	if len(errs) != 0 {
		return "", fmt.Errorf("find rollout for %s: %w", threadID, errors.Join(errs...))
	}
	found := ""
	for _, rollout := range rollouts {
		if codexmeta.RolloutID(filepath.Base(rollout.Path)) == threadID {
			found = rollout.Path
		}
	}
	return found, nil
}

func (session *codexSession) sessionMeta(threadID, parent string, now time.Time) codexRecord {
	payload := map[string]any{
		"id": threadID, "timestamp": stamp(now), keyCWD: session.proc.cwd, "originator": "codex_cli_rs",
		"cli_version": strings.TrimPrefix(session.proc.script.Version, "codex-cli "), "source": sourceCLI,
		"thread_source": roleUser,
	}
	if parent != "" {
		payload["thread_source"] = "subagent"
		payload["parent_thread_id"] = parent
		payload["source"] = map[string]any{
			"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": parent}},
		}
	}
	return codexRecord{Timestamp: stamp(now), Type: "session_meta", Payload: payload}
}

// turnContext carries the model internal/transcript/meta.go:139 reads.
func (session *codexSession) turnContext() codexRecord {
	return codexRecord{Type: "turn_context", Payload: map[string]any{
		keyCWD: session.proc.cwd, "approval_policy": "never", codexModelKey: session.model, "summary": "auto",
	}}
}

func (session *codexSession) recordUser(text string) error {
	return session.write(codexRecord{Type: recordResponseItem, Payload: codexMessage{
		Type: payloadMessage, Role: roleUser, Content: []codexContent{{Type: blockInputText, Text: text}},
	}})
}

func (session *codexSession) recordAssistant(reply string, usage Tokens) error {
	session.lastAssistant = reply
	records := []codexRecord{
		{Type: recordResponseItem, Payload: codexMessage{
			Type: payloadMessage, Role: roleAssistant, Content: []codexContent{{Type: blockOutputText, Text: reply}},
		}},
		{Type: recordEventMsg, Payload: map[string]any{keyType: "agent_message", payloadMessage: reply}},
		{Type: recordEventMsg, Payload: map[string]any{keyType: "token_count", codexInfoKey: map[string]any{
			"total_token_usage": map[string]any{
				codexInputTokens:  usage.Input,
				codexOutputTokens: usage.Output,
				"total_tokens":    usage.Input + usage.Output,
			},
			"last_token_usage": map[string]any{
				codexInputTokens:  usage.Input,
				codexOutputTokens: usage.Output,
				"total_tokens":    usage.Input + usage.Output,
			},
			"model_context_window": usage.ContextWindow,
		}}},
	}
	for index := range records {
		if err := session.write(records[index]); err != nil {
			return err
		}
	}
	return nil
}

// compact writes the `compacted` record a reader of the rollout sees: the
// replacement history is the summary the fixture supplies.
func (session *codexSession) compact(_ Step) error {
	payload := session.payload("PreCompact")
	delete(payload, "permission_mode")
	payload["trigger"] = "manual"
	if _, err := session.fire("PreCompact", "manual", payload); err != nil {
		return err
	}
	history := []codexMessage{{
		Type: payloadMessage, Role: roleUser,
		Content: []codexContent{{Type: blockInputText, Text: "Summary of the thread so far (fixture)."}},
	}}
	if err := session.write(codexRecord{Type: "compacted", Payload: map[string]any{
		payloadMessage:        "Summary of the thread so far (fixture).",
		"replacement_history": history,
	}}); err != nil {
		return err
	}
	payload["hook_event_name"] = "PostCompact"
	_, err := session.fire("PostCompact", "manual", payload)
	return err
}

// rename appends the session_index.jsonl row internal/codexmeta/session_index.go
// decodes: id, thread_name, updated_at.
func (session *codexSession) rename(name string) error {
	entry := codexmeta.SessionIndexEntry{
		ID:         session.threadID,
		ThreadName: name,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode session index row: %w", err)
	}
	if err := appendLine(filepath.Join(session.codexHome, codexmeta.SessionIndexFile), encoded); err != nil {
		return err
	}
	session.threadName = name
	return nil
}

// background spawns a subagent thread: its own rollout whose session_meta
// names this thread as parent (internal/codexmeta/header.go:71-84 reads it).
// Codex's pane shows nothing pfm pins for it, so no row is rendered.
func (session *codexSession) background(step Step) (string, error) {
	child := &codexSession{
		call:      codexInvocation{ephemeral: session.call.ephemeral},
		proc:      session.proc,
		codexHome: session.codexHome,
		threadID:  uuid.NewV4().String(),
		model:     session.model,
	}
	now := time.Now()
	if !child.call.ephemeral {
		child.rollout = child.rolloutFor(child.threadID, now)
		if err := child.openRollout(); err != nil {
			return "", err
		}
	}
	defer func() {
		if err := child.finish("other"); err != nil {
			warn(session.proc.stderr, "finish subagent: %v", err)
		}
	}()
	if err := child.write(session.sessionMeta(child.threadID, session.threadID, now)); err != nil {
		return "", err
	}
	payload := session.payload("SubagentStart")
	payload["agent_id"], payload["agent_type"] = child.threadID, step.Name
	if _, err := session.fire("SubagentStart", step.Name, payload); err != nil {
		return "", err
	}
	if err := child.recordUser("Subagent task for " + step.Name); err != nil {
		return "", err
	}
	if err := child.recordAssistant(step.Status, session.proc.script.Tokens); err != nil {
		return "", err
	}
	payload["hook_event_name"], payload["agent_transcript_path"] = "SubagentStop", child.rollout
	if child.call.ephemeral {
		payload["agent_transcript_path"] = nil
	}
	payload["stop_hook_active"], payload["last_assistant_message"] = false, step.Status
	_, err := session.fire("SubagentStop", step.Name, payload)
	return "", err
}
