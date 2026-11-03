package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"uuid"
)

type claudeBackgroundModel struct {
	child *claudeSession
	start hookPayload
	steps []Step
	row   string
}

func (session *claudeSession) startSubagent(id, agentType string) (*claudeSession, hookPayload, error) {
	child := *session
	child.agentID, child.transcript = session.subagentIdentity(id)
	child.agentType = agentType
	child.pendingToolID, child.lastReply, child.backgroundModels = "", "", nil
	start := session.payload(hookSubagentStart)
	start.AgentID, start.AgentType = child.agentID, child.agentType
	_, err := session.fire(hookSubagentStart, child.agentType, start)
	return &child, start, err
}

func (session *claudeSession) subagentIdentity(id string) (string, string) {
	if id == "" {
		id = "agent-" + uuid.NewV4().String()[:8]
	}
	return id, filepath.Join(strings.TrimSuffix(session.transcript, ".jsonl"), "subagents", id+".jsonl")
}

func (session *claudeSession) executeTool(tool string, input json.RawMessage) (toolExecution, error) {
	if tool != "Agent" && tool != "Task" {
		return session.proc.executeTool(tool, input)
	}
	var fields struct {
		Prompt       string `json:"prompt"`
		SubagentType string `json:"subagent_type"`
		Background   bool   `json:"run_in_background"`
	}
	if err := json.Unmarshal(input, &fields); err != nil {
		return toolExecution{}, fmt.Errorf("decode Agent input: %w", err)
	}
	// Legacy hook-only probes carry no task; an opted-in Agent prompt must pin its model response.
	if fields.Prompt == "" {
		return session.proc.executeTool(tool, input)
	}
	if fields.Background {
		return toolExecution{}, errors.New(
			"background Agent model steps are unpinned; use background_agent for a scripted shell task",
		)
	}
	steps, err := parseInlineSteps(fields.Prompt)
	if err != nil {
		return toolExecution{}, err
	}
	if len(steps) == 0 {
		return toolExecution{}, errors.New("agent prompt requires mock-engine inline steps")
	}
	if fields.SubagentType == "" {
		fields.SubagentType = "general-purpose"
	}
	child, start, err := session.startSubagent("", fields.SubagentType)
	if err != nil {
		return toolExecution{}, err
	}
	if err := child.recordUser(fields.Prompt); err != nil {
		return toolExecution{}, err
	}
	running := session.proc.script.forPrompt(fields.Prompt)
	for {
		step := running.next()
		if step.Type == StepTurn {
			if step.Error != "" {
				return toolExecution{}, fmt.Errorf("subagent API error: %s", step.Error)
			}
			reply, _, usage := running.turnReply(step)
			if err := child.recordAssistant(reply, usage); err != nil {
				return toolExecution{}, err
			}
			if err := session.subagentStop(start, child.transcript, reply); err != nil {
				return toolExecution{}, err
			}
			response, err := json.Marshal(
				map[string]any{
					"agentId": child.agentID,
					"status":  "completed",
					"content": []contentPart{{Type: blockText, Text: reply}},
				},
			)
			return toolExecution{Response: response, Text: reply}, err
		}
		if step.terminal() {
			return toolExecution{}, fmt.Errorf("agent response ended with %s instead of turn", step.Type)
		}
		if err := child.headlessStep(step); err != nil {
			return toolExecution{}, err
		}
	}
}

func (session *claudeSession) afterStop(loop *paneLoop) error {
	tasks := session.backgroundModels
	session.backgroundModels = nil
	for index := range tasks {
		task := &tasks[index]
		childLoop := *loop
		childLoop.engine = task.child
		finished := false
		for index := range task.steps {
			step := &task.steps[index]
			if step.Type == StepTurn {
				reply, _, usage := session.proc.script.turnReply(*step)
				if step.Error != "" {
					return fmt.Errorf("background subagent API error: %s", step.Error)
				}
				if err := task.child.recordAssistant(reply, usage); err != nil {
					return err
				}
				if err := session.subagentStop(task.start, task.child.transcript, reply); err != nil {
					return err
				}
				finished = true
				break
			}
			if step.Type == StepPermission || step.Type == StepQuestion {
				approved, err := childLoop.attention(*step)
				if err != nil {
					return err
				}
				if !approved {
					if err := session.subagentStop(task.start, task.child.transcript, "Interrupted"); err != nil {
						return err
					}
					finished = true
					break
				}
			} else if err := task.child.headlessStep(*step); err != nil {
				return err
			}
		}
		if !finished {
			return errors.New("background Agent prompt requires a terminal turn")
		}
		loop.screen.agentRows = slices.DeleteFunc(
			loop.screen.agentRows,
			func(row string) bool { return row == task.row },
		)
		loop.paint()
	}
	return nil
}
