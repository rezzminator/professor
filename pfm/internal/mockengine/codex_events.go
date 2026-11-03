package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
)

func (session *codexSession) start(resumed bool) error {
	now := time.Now()
	source := sourceStartup
	if resumed {
		source = sourceResume
		existing, err := session.findRollout(session.threadID)
		if err != nil {
			return err
		}
		if codexmeta.Compressed(existing) {
			// Codex decompresses a finished rollout before it appends to it.
			if existing, err = codexmeta.DecompressRollout(existing); err != nil {
				return err
			}
		}
		session.rollout = existing
	}
	if !session.call.ephemeral {
		fresh := session.rollout == ""
		if fresh {
			session.rollout = session.rolloutFor(session.threadID, now)
		}
		if err := session.openRollout(); err != nil {
			return err
		}
		if fresh {
			if err := session.write(session.sessionMeta(session.threadID, "", now)); err != nil {
				return err
			}
		}
		if err := session.write(session.turnContext()); err != nil {
			return err
		}
		seat, err := bindSeat(session.proc, engineCodex, session.rollout)
		if err != nil {
			return err
		}
		session.seat = seat
	}
	session.started = true
	payload := session.payload(hookSessionStart)
	payload["source"] = source
	answers, err := session.fire(hookSessionStart, source, payload)
	if err != nil {
		return err
	}
	for index := range answers {
		additional := answers[index].Specific.AdditionalContext
		if additional == "" {
			continue
		}
		// Codex folds additionalContext into history as a developer message.
		if err := session.write(codexRecord{Type: recordResponseItem, Payload: codexMessage{
			Type:    payloadMessage,
			Role:    roleDeveloper,
			Content: []codexContent{{Type: blockInputText, Text: additional}},
		}}); err != nil {
			return err
		}
	}
	return nil
}

func (session *codexSession) prompt(text string) (string, error) {
	session.mcpError = ""
	session.turnID = uuid.NewV4().String()
	payload := session.payload(hookUserPromptSubmit)
	payload["prompt"] = text
	answers, err := session.fire(hookUserPromptSubmit, "", payload)
	if err != nil {
		return "", err
	}
	for index := range answers {
		if blocked, reason := answers[index].blocks(); blocked {
			return reason, nil
		}
	}
	return "", nil
}

func (session *codexSession) preTool(step Step) (map[string]any, string, error) {
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	callID := "call_" + uuid.NewV4().String()[:8]
	payload := session.payload(hookPreToolUse)
	payload["tool_name"], payload["tool_input"], payload["tool_use_id"] = step.Tool, input, callID
	answers, err := session.fire(hookPreToolUse, step.Tool, payload)
	if err != nil {
		return nil, "", err
	}
	for index := range answers {
		if denied, reason := answers[index].denies(); denied {
			return nil, reason, nil
		}
	}
	return payload, "", nil
}

func (session *codexSession) tool(step Step) (string, error) {
	payload := session.pendingTool
	session.pendingTool = nil
	if payload == nil {
		var denied string
		var err error
		payload, denied, err = session.preTool(step)
		if err != nil || denied != "" {
			return denied, err
		}
	}
	input := payload["tool_input"].(json.RawMessage)
	callID := payload["tool_use_id"].(string)
	result, err := session.proc.executeTool(step.Tool, input, session.toolEnvironment(callID)...)
	if err != nil {
		return "", err
	}
	output := result.Text
	if result.Failure != "" {
		output = result.Failure
	}
	records := []codexRecord{
		{
			Type: recordResponseItem,
			Payload: map[string]any{
				keyType:         "function_call",
				openCodeNameKey: step.Tool,
				"arguments":     string(input),
				codexCallIDKey:  callID,
			},
		},
		{
			Type:    recordResponseItem,
			Payload: map[string]any{keyType: "function_call_output", codexCallIDKey: callID, "output": output},
		},
	}
	for index := range records {
		if err := session.write(records[index]); err != nil {
			return "", err
		}
	}
	payload["hook_event_name"], payload["tool_response"] = hookPostToolUse, output
	_, err = session.fire(hookPostToolUse, step.Tool, payload)
	return "", err
}

func (session *codexSession) finish(string) error {
	var hookErr error
	if session.started && !session.ended {
		session.ended = true
		payload := session.payload(hookSessionEnd)
		payload["reason"] = "other"
		_, hookErr = session.fire(hookSessionEnd, "other", payload)
	}
	var closeErr, seatErr error
	if session.rolloutFile != nil {
		if err := session.rolloutFile.Close(); err != nil {
			closeErr = fmt.Errorf("close rollout %s: %w", session.rollout, err)
		}
		session.rolloutFile = nil
	}
	if session.seat != nil {
		seatErr = session.seat.release()
		session.seat = nil
	}
	return errors.Join(hookErr, closeErr, seatErr)
}

func (session *codexSession) payload(event string) map[string]any {
	var transcript any
	if session.rollout != "" {
		transcript = session.rollout
	}
	payload := map[string]any{
		"session_id":      session.threadID,
		"transcript_path": transcript,
		keyCWD:            session.proc.cwd,
		"hook_event_name": event,
	}
	if event != hookSessionEnd {
		payload[codexModelKey], payload["permission_mode"] = session.model, "bypassPermissions"
		if event != hookSessionStart {
			payload["turn_id"] = session.turnID
		}
	}
	return payload
}

func (session *codexSession) fire(event, subject string, payload map[string]any) ([]hookAnswer, error) {
	environ, err := session.proc.actionEnvironment(session.environ)
	if err != nil {
		return nil, err
	}
	return session.hooks.fire(
		session.proc.ctx,
		event,
		subject,
		payload,
		session.proc.cwd,
		environ,
		session.proc.stderr,
		session.proc,
	)
}

func (session *codexSession) stop() error {
	payload := session.payload(hookStop)
	payload["stop_hook_active"], payload["last_assistant_message"] = false, session.lastAssistant
	_, err := session.fire(hookStop, "", payload)
	return err
}

func (session *codexSession) interrupt() error {
	_, err := session.fire("Interrupt", "", session.payload("Interrupt"))
	return err
}

func (session *codexSession) requestAttention(step Step) error {
	if step.Type == StepQuestion {
		return errors.New(
			"codex question attention hook is unpinned; no PermissionRequest is emitted for request_user_input",
		)
	}
	session.attention = ""
	session.pendingTool = nil
	prepared, denied, err := session.preTool(step)
	if err != nil {
		return err
	}
	if denied != "" {
		session.attention = permissionDeny
		return nil
	}
	session.pendingTool = prepared
	payload := session.payload("PermissionRequest")
	payload["tool_name"], payload["tool_input"] = prepared["tool_name"], prepared["tool_input"]
	answers, err := session.fire("PermissionRequest", step.Tool, payload)
	for index := range answers {
		switch answers[index].Specific.Decision.Behavior {
		case permissionDeny:
			session.attention = permissionDeny
		case permissionAllow:
			if session.attention != permissionDeny {
				session.attention = permissionAllow
			}
		}
	}
	return err
}

func (session *codexSession) answerAttention(step Step, approved bool) error {
	if !approved {
		session.pendingTool = nil
		return nil
	}
	_, err := session.tool(step)
	return err
}

func (session *codexSession) attentionDecision() (bool, bool) {
	return session.attention == permissionAllow, session.attention != ""
}

func (session *codexSession) hook(step Step) error {
	var payload map[string]any
	if err := json.Unmarshal(step.Payload, &payload); err != nil {
		return fmt.Errorf("decode scripted Codex %s hook: %w", step.Event, err)
	}
	if event, ok := payload["hook_event_name"].(string); !ok || event != step.Event {
		return fmt.Errorf("scripted Codex hook_event_name=%v, want %s", payload["hook_event_name"], step.Event)
	}
	subject, _ := payload["tool_name"].(string)
	if subject == "" {
		subject, _ = payload["trigger"].(string)
	}
	_, err := session.fire(step.Event, subject, payload)
	return err
}

func (session *codexSession) toolEnvironment(callID string) []string {
	return []string{
		"CODEX_TOOL_CALL_ID=" + callID,
		"NO_COLOR=1",
		"PAGER=cat",
		"GIT_PAGER=cat",
		"GH_PAGER=cat",
		"CODEX_CI=1",
		"CODEX_SESSION_ID=" + session.threadID,
		"CODEX_VERSION=" + strings.TrimPrefix(session.proc.script.Version, "codex-cli "),
		"CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS=1",
	}
}
