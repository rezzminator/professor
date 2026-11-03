package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
)

// hookPayload carries the session identity and the event's prompt or tool.
type hookPayload struct {
	SessionID            string          `json:"session_id"`
	TranscriptPath       string          `json:"transcript_path"`
	CWD                  string          `json:"cwd"`
	PermissionMode       string          `json:"permission_mode"`
	Event                string          `json:"hook_event_name"`
	Source               string          `json:"source,omitempty"`
	Reason               string          `json:"reason,omitempty"`
	Prompt               string          `json:"prompt,omitempty"`
	ToolName             string          `json:"tool_name,omitempty"`
	ToolInput            json.RawMessage `json:"tool_input,omitempty"`
	ToolUseID            string          `json:"tool_use_id,omitempty"`
	ToolResponse         json.RawMessage `json:"tool_response,omitempty"`
	DurationMS           *int64          `json:"duration_ms,omitempty"`
	AgentID              string          `json:"agent_id,omitempty"`
	AgentType            string          `json:"agent_type,omitempty"`
	AgentTranscriptPath  string          `json:"agent_transcript_path,omitempty"`
	NotificationType     string          `json:"notification_type,omitempty"`
	Message              string          `json:"message,omitempty"`
	Error                string          `json:"error,omitempty"`
	ErrorDetails         string          `json:"error_details,omitempty"`
	IsInterrupt          *bool           `json:"is_interrupt,omitempty"`
	StopHookActive       *bool           `json:"stop_hook_active,omitempty"`
	LastAssistantMessage string          `json:"last_assistant_message,omitempty"`
}

func (session *claudeSession) payload(event string) hookPayload {
	return hookPayload{
		SessionID: session.sessionID, TranscriptPath: session.transcript, CWD: session.proc.cwd,
		PermissionMode: "bypassPermissions", Event: event,
		AgentID: session.agentID, AgentType: session.agentType,
	}
}

func (session *claudeSession) fire(event, subject string, payload hookPayload) ([]hookAnswer, error) {
	if session.proc.script.Quiet {
		return nil, nil
	}
	environ, err := session.proc.actionEnvironment(session.environ)
	if err != nil {
		return nil, err
	}
	return session.hooks.fire(
		session.proc.ctx, event, subject, payload, session.proc.cwd, environ, session.proc.stderr, session.proc,
	)
}

// foldContext writes a meta user record every pfm transcript reader skips.
func (session *claudeSession) foldContext(answer *hookAnswer) error {
	reminder := answer.Specific.AdditionalContext
	if reminder == "" {
		return nil
	}
	return session.write(session.userRecord("<system-reminder>\n"+reminder+"\n</system-reminder>", true))
}

func (session *claudeSession) prompt(text string) (string, error) {
	payload := session.payload(hookUserPromptSubmit)
	payload.Prompt = text
	answers, err := session.fire(hookUserPromptSubmit, "", payload)
	if err != nil {
		return "", err
	}
	for index := range answers {
		if blocked, reason := answers[index].blocks(); blocked {
			return reason, nil
		}
		if err := session.foldContext(&answers[index]); err != nil {
			return "", err
		}
	}
	return "", nil
}

func (session *claudeSession) stop() error {
	if session.proc.script.Quiet || len(session.hooks[hookStop]) == 0 {
		return nil
	}
	payload := session.payload(hookStop)
	active := false
	payload.StopHookActive = &active
	payload.LastAssistantMessage = session.lastReply
	answers, fireErr := session.fire(hookStop, "", payload)
	type recordedAnswer struct {
		ExitCode int        `json:"exit_code"`
		Stderr   string     `json:"stderr,omitempty"`
		Answer   hookAnswer `json:"answer"`
	}
	record := struct {
		Answers []recordedAnswer `json:"answers"`
		Error   string           `json:"error,omitempty"`
	}{}
	for index := range answers {
		record.Answers = append(
			record.Answers,
			recordedAnswer{answers[index].exitCode, answers[index].stderr, answers[index]},
		)
	}
	if fireErr != nil {
		record.Error = fireErr.Error()
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return errors.Join(fireErr, fmt.Errorf("encode Stop hook answers: %w", err))
	}
	return errors.Join(fireErr, session.proc.recorder.write("hook-Stop.json", string(encoded)+"\n"))
}
