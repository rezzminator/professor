package mockengine

import (
	"encoding/json"
	"fmt"
	"uuid"
)

const (
	hookPermissionRequest  = "PermissionRequest"
	hookNotification       = "Notification"
	hookSubagentStart      = "SubagentStart"
	hookSubagentStop       = "SubagentStop"
	hookPostToolUseFailure = "PostToolUseFailure"
)

func (session *claudeSession) requestAttention(step Step) error {
	session.approvalDecision = ""
	if step.Type == StepQuestion {
		step.Selected = 0
		step = claudeQuestion(step)
	}
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	pre := session.payload(hookPreToolUse)
	pre.ToolName, pre.ToolInput = step.Tool, input
	session.pendingToolID = "toolu_" + uuid.NewV4().String()[:8]
	pre.ToolUseID = session.pendingToolID
	answers, err := session.fire(hookPreToolUse, step.Tool, pre)
	if err != nil {
		return err
	}
	for index := range answers {
		if denied, _ := answers[index].denies(); denied {
			session.approvalDecision = permissionDeny
			return nil
		}
	}
	if step.Type == StepQuestion {
		return session.notification(Step{NotificationType: "elicitation_dialog", Message: step.Message})
	}
	permission := session.payload(hookPermissionRequest)
	permission.PermissionMode = "default"
	permission.ToolName, permission.ToolInput = step.Tool, input
	if step.AgentID != "" {
		permission.AgentID = step.AgentID
	}
	answers, err = session.fire(hookPermissionRequest, step.Tool, permission)
	if err != nil {
		return err
	}
	for index := range answers {
		switch decision := answers[index].Specific.Decision.Behavior; decision {
		case permissionDeny:
			session.approvalDecision = decision
			return nil
		case permissionAllow:
			session.approvalDecision = decision
		}
	}
	if session.approvalDecision == "" {
		return session.notification(step)
	}
	return nil
}

func (session *claudeSession) attentionDecision() (bool, bool) {
	return session.approvalDecision == permissionAllow, session.approvalDecision != ""
}

func (session *claudeSession) answerAttention(step Step, approved bool) error {
	if !approved {
		session.pendingToolID = ""
		return nil
	}
	if step.Type == StepQuestion {
		step = claudeQuestion(step)
	}
	_, err := session.tool(step)
	return err
}

func (session *claudeSession) notification(step Step) error {
	payload := session.payload(hookNotification)
	payload.NotificationType, payload.Message = step.NotificationType, step.Message
	if payload.NotificationType == "" {
		payload.NotificationType = "permission_prompt"
	}
	if payload.Message == "" {
		payload.Message = "Claude needs your permission"
	}
	// Permission notifications from subagents carry the parent session and no agent_id.
	if payload.NotificationType == "permission_prompt" {
		payload.AgentID, payload.AgentType = "", ""
	}
	_, err := session.fire(hookNotification, payload.NotificationType, payload)
	return err
}

func (session *claudeSession) hook(step Step) error {
	var payload map[string]any
	if err := json.Unmarshal(step.Payload, &payload); err != nil {
		return fmt.Errorf("decode scripted %s hook: %w", step.Event, err)
	}
	if payload["hook_event_name"] != step.Event {
		return fmt.Errorf("scripted hook_event_name must equal %s", step.Event)
	}
	environ, err := session.proc.actionEnvironment(session.environ)
	if err != nil {
		return err
	}
	_, err = session.hooks.fire(
		session.proc.ctx,
		step.Event,
		step.Tool,
		payload,
		session.proc.cwd,
		environ,
		session.proc.stderr,
		session.proc,
	)
	return err
}

func claudeQuestion(step Step) Step {
	step.Tool = "AskUserQuestion"
	options := make([]map[string]string, 0, len(step.Options))
	for _, option := range step.Options {
		options = append(options, map[string]string{"label": option, "description": option})
	}
	input := map[string]any{
		"questions": []any{
			map[string]any{StepQuestion: step.Message, "header": "Question", "options": options, "multiSelect": false},
		},
	}
	if step.Selected > 0 && step.Selected <= len(step.Options) {
		input["answers"] = map[string]string{step.Message: step.Options[step.Selected-1]}
	}
	step.Input, _ = json.Marshal(input)
	return step
}
