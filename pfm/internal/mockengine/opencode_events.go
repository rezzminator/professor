package mockengine

import (
	"encoding/json"
	"fmt"
)

type openCodeEvent struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
}

func (session *openCodeSession) info() map[string]any {
	return map[string]any{
		"id":                 session.id,
		"slug":               session.id,
		"projectID":          session.projectID,
		openCodeDirectoryKey: session.proc.cwd,
		openCodeTitleKey:     session.title,
		"version":            session.proc.script.Version,
		openCodeTimeKey:      map[string]int64{openCodeCreatedKey: session.createdMS, "updated": session.updatedMS},
	}
}

func (session *openCodeSession) plugin(hook string, input, output any) error {
	if session.plugins == nil {
		return nil
	}
	return session.plugins.call(
		map[string]any{
			"hook":                    hook,
			"input":                   input,
			openCodeCallbackOutputKey: output,
			"sessions":                map[string]any{session.id: session.info()},
		},
	)
}

func (session *openCodeSession) event(name string, properties map[string]any) error {
	event := openCodeEvent{Type: name, Properties: properties}
	return session.plugin("event", map[string]any{"event": event}, nil)
}

func (session *openCodeSession) status(kind string) error {
	return session.event(
		"session.status",
		map[string]any{openCodeSessionIDKey: session.id, openCodeStatusKey: map[string]string{keyType: kind}},
	)
}

func (session *openCodeSession) prompt(text string) (string, error) {
	input := map[string]any{
		openCodeSessionIDKey: session.id,
		openCodeAgentKey:     openCodeBuildAgent,
		openCodeModelKey: map[string]string{
			openCodeProviderIDKey: session.provider,
			openCodeModelIDKey:    session.modelID,
		},
	}
	output := map[string]any{
		openCodeMessageKey: map[string]any{
			"id":                 "msg_fixture",
			openCodeSessionIDKey: session.id,
			openCodeRoleKey:      "user",
			openCodeTimeKey:      map[string]int64{openCodeCreatedKey: session.createdMS},
			openCodeAgentKey:     openCodeBuildAgent,
			openCodeModelKey: map[string]string{
				openCodeProviderIDKey: session.provider,
				openCodeModelIDKey:    session.modelID,
			},
		},
		openCodePartsKey: []any{
			map[string]string{
				"id":                 "prt_fixture",
				openCodeSessionIDKey: session.id,
				"messageID":          "msg_fixture",
				keyType:              blockText,
				keyText:              text,
			},
		},
	}
	if err := session.plugin("chat.message", input, output); err != nil {
		return "", err
	}
	return "", session.status("busy")
}

func (session *openCodeSession) stop() error {
	if err := session.status("idle"); err != nil {
		return err
	}
	return session.event("session.idle", map[string]any{openCodeSessionIDKey: session.id})
}

func (session *openCodeSession) interrupt() error { return session.stop() }

func (session *openCodeSession) requestAttention(step Step) error {
	session.requestNumber++
	session.pendingRequestID = fmt.Sprintf("per_%08d", session.requestNumber)
	kind, properties := session.attentionEvent(step)
	return session.event(kind, properties)
}

func (session *openCodeSession) attentionEvent(step Step) (string, map[string]any) {
	properties := map[string]any{"id": session.pendingRequestID, openCodeSessionIDKey: session.id}
	if step.Type == StepQuestion {
		options := []map[string]string{}
		for _, option := range step.Options {
			options = append(options, map[string]string{"label": option, "description": option})
		}
		properties["questions"] = []map[string]any{
			{StepQuestion: step.Message, "header": "Question", "options": options},
		}
		return "question.asked", properties
	}
	properties["permission"] = step.Tool
	properties["patterns"] = []string{"*"}
	properties["always"] = []string{"*"}
	properties["metadata"] = map[string]any{}
	return "permission.asked", properties
}

func (session *openCodeSession) answerAttention(step Step, approved bool) error {
	properties := map[string]any{openCodeSessionIDKey: session.id, "requestID": session.pendingRequestID}
	kind := "permission.replied"
	if step.Type == StepQuestion {
		kind = "question.replied"
		if !approved {
			kind = "question.rejected"
		} else {
			answer := "Approve"
			if len(step.Options) > 0 {
				selected := step.Selected
				if selected == 0 {
					selected = 1
				}
				if selected < 1 || selected > len(step.Options) {
					return fmt.Errorf("OpenCode question selected %d is outside 1..%d", selected, len(step.Options))
				}
				answer = step.Options[selected-1]
			}
			properties["answers"] = [][]string{{answer}}
		}
	} else {
		reply := "once"
		if !approved {
			reply = "reject"
		}
		properties["reply"] = reply
	}
	if err := session.event(kind, properties); err != nil {
		return err
	}
	if approved && step.Type == StepPermission {
		denied, err := session.tool(step)
		if err != nil {
			return err
		}
		if denied != "" {
			return fmt.Errorf("OpenCode approved tool denied by guard: %s", denied)
		}
	}
	return nil
}

func (session *openCodeSession) notification(step Step) error {
	switch step.NotificationType {
	case "session.status.retry":
		return session.event(
			"session.status",
			map[string]any{
				openCodeSessionIDKey: session.id,
				openCodeStatusKey: map[string]any{
					keyType:            "retry",
					"attempt":          1,
					openCodeMessageKey: step.Message,
					"next":             0,
				},
			},
		)
	case "global.disposed":
		return session.event("global.disposed", map[string]any{})
	case "session.deleted":
		return session.event(
			"session.deleted",
			map[string]any{openCodeSessionIDKey: session.id, openCodeInfoKey: session.info()},
		)
	default:
		return fmt.Errorf("unscripted OpenCode notification %q", step.NotificationType)
	}
}

func (session *openCodeSession) tool(step Step) (string, error) {
	var args map[string]any
	if err := json.Unmarshal(step.Input, &args); err != nil {
		return "", fmt.Errorf("decode OpenCode tool input: %w", err)
	}
	input := map[string]any{
		openCodeSessionIDKey: session.id,
		openCodeToolKey:      step.Tool,
		openCodeCallIDKey:    "call_fixture",
	}
	if err := session.plugin("tool.execute.before", input, map[string]any{openCodeArgsKey: args}); err != nil {
		return err.Error(), nil
	}
	result, err := session.proc.executeTool(step.Tool, step.Input)
	if err != nil {
		return "", err
	}
	if result.Failure != "" {
		return result.Failure, nil
	}
	input[openCodeArgsKey] = args
	if err := session.plugin(
		"tool.execute.after",
		input,
		map[string]any{
			openCodeTitleKey:          step.Tool,
			openCodeCallbackOutputKey: result.Text,
			"metadata":                map[string]any{},
		},
	); err != nil {
		return "", err
	}
	return "", session.status("busy")
}

func (session *openCodeSession) headlessSteps(running *script) (Step, error) {
	for {
		step := running.next()
		switch step.Type {
		case StepTurn:
			return step, nil
		case StepCrash, StepExit:
			return step, fmt.Errorf("crash step: exit %d: %s", step.ExitCode, step.Reply)
		case StepNativeTitle:
			if err := session.nativeTitle(step); err != nil {
				return step, err
			}
		case StepToolCall:
			if denied, err := session.tool(step); err != nil {
				return step, err
			} else if denied != "" {
				return step, fmt.Errorf("OpenCode tool denied: %s", denied)
			}
		case StepCompact:
			if err := session.compact(step); err != nil {
				return step, err
			}
		case StepMCP:
			if err := session.mcp(step); err != nil {
				return step, err
			}
		case StepHook:
			if err := session.hook(step); err != nil {
				return step, err
			}
		case StepNotification:
			if err := session.notification(step); err != nil {
				return step, err
			}
		default:
			return step, fmt.Errorf("OpenCode headless request refuses scripted %s step", step.Type)
		}
	}
}

func (session *openCodeSession) created() error {
	return session.event(
		"session.created",
		map[string]any{openCodeSessionIDKey: session.id, openCodeInfoKey: session.info()},
	)
}

func (session *openCodeSession) updated() error {
	return session.event(
		"session.updated",
		map[string]any{openCodeSessionIDKey: session.id, openCodeInfoKey: session.info()},
	)
}

func (session *openCodeSession) disposed() error {
	return session.event("server.instance.disposed", map[string]any{openCodeDirectoryKey: session.proc.cwd})
}

func (session *openCodeSession) background(step Step) (string, error) {
	id := step.AgentID
	if id == "" {
		id = "ses_child_atlas"
	}
	info := session.info()
	info["id"], info["slug"], info["parentID"], info[openCodeTitleKey] = id, id, session.id, step.Name
	if err := session.event(
		"session.created",
		map[string]any{openCodeSessionIDKey: id, openCodeInfoKey: info},
	); err != nil {
		return "", err
	}
	for _, kind := range []string{"busy", "idle"} {
		if err := session.event(
			"session.status",
			map[string]any{openCodeSessionIDKey: id, openCodeStatusKey: map[string]string{keyType: kind}},
		); err != nil {
			return "", err
		}
	}
	return step.Name + " — " + step.Status, nil
}

func (session *openCodeSession) hook(step Step) error {
	var event openCodeEvent
	if err := json.Unmarshal(step.Payload, &event); err != nil {
		return fmt.Errorf("decode scripted OpenCode event: %w", err)
	}
	if event.Type != step.Event || event.Properties == nil {
		return fmt.Errorf("scripted OpenCode event %q requires matching type and properties", step.Event)
	}
	return session.plugin("event", map[string]any{"event": event}, nil)
}
