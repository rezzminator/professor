package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
	"uuid"
)

func (session *claudeSession) start(resumed bool) error {
	source := sourceStartup
	if resumed {
		source = sourceResume
	}
	return session.begin(source)
}

// begin opens (or reopens) the session: the seat bindings, the title record,
// then SessionStart with its additionalContext folded into the transcript.
func (session *claudeSession) begin(source string) error {
	if !session.proc.script.NoTranscript {
		if err := touchFile(session.transcript); err != nil {
			return err
		}
	}
	seat, err := bindSeat(session.proc, engineClaude, session.transcript)
	if err != nil {
		return err
	}
	session.seat = seat
	if err := session.presence(); err != nil {
		return err
	}
	// Claude carries a chat's title into its /clear successor's transcript.
	if session.title != "" && (source == sourceStartup || source == sourceClear || session.call.fork) {
		if err := session.write(
			claudeRecord{Type: claudeCustomTitle, CustomTitle: session.title, SessionID: session.sessionID},
		); err != nil {
			return err
		}
	}
	payload := session.payload(hookSessionStart)
	payload.Source = source
	answers, err := session.fire(hookSessionStart, source, payload)
	if err != nil {
		return err
	}
	for index := range answers {
		if err := session.foldContext(&answers[index]); err != nil {
			return err
		}
	}
	return session.proc.startupEvent("SessionStart-completed")
}

func (session *claudeSession) tool(step Step) (string, error) {
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	payload := session.payload(hookPreToolUse)
	payload.ToolName, payload.ToolInput = step.Tool, input
	use := contentPart{Type: blockToolUse, ID: "toolu_" + uuid.NewV4().String()[:8], Name: step.Tool, Input: input}
	payload.ToolUseID = use.ID
	var answers []hookAnswer
	var err error
	if session.pendingToolID != "" {
		use.ID = session.pendingToolID
		session.pendingToolID = ""
	} else {
		answers, err = session.fire(hookPreToolUse, step.Tool, payload)
		if err != nil {
			return "", err
		}
	}
	preReturned := time.Now()
	if err := session.write(
		session.assistantRecord([]contentPart{use}, session.proc.script.Tokens, false),
	); err != nil {
		return "", err
	}
	result := contentPart{Type: "tool_result", ToolUseID: use.ID, Content: toolResultDone}
	denied := ""
	deniedFound := false
	for index := range answers {
		if refused, reason := answers[index].denies(); refused && !deniedFound {
			deniedFound = true
			denied = reason
			result.Content, result.IsError = reason, true
		}
	}
	if !deniedFound {
		execution := toolExecution{Failure: step.Error}
		if step.Error == "" {
			execution, err = session.executeTool(step.Tool, input)
			if err != nil {
				execution.Failure = err.Error()
			}
		}
		if execution.Failure != "" {
			failure := session.payload(hookPostToolUseFailure)
			failure.ToolName, failure.ToolInput, failure.ToolUseID = step.Tool, input, use.ID
			failure.Error = execution.Failure
			interrupted := false
			failure.IsInterrupt = &interrupted
			if _, err := session.fire(hookPostToolUseFailure, step.Tool, failure); err != nil {
				return "", err
			}
			result.Content, result.IsError = execution.Failure, true
		} else {
			result.Content = execution.Text
			if err := session.postToolResult(
				step.Tool,
				input,
				use.ID,
				execution.Response,
				time.Since(preReturned),
			); err != nil {
				return "", err
			}
		}
	}
	record := claudeRecord{
		Type: roleUser, SessionID: session.sessionID, Timestamp: stamp(time.Now()), CWD: session.proc.cwd,
		Message: &claudeMessage{Role: roleUser, Content: []contentPart{result}},
	}
	return denied, session.write(record)
}

// compact writes what Claude Code leaves after /compact: the boundary record
// internal/transcript/meta.go:161 reads for postTokens, then the summary as
// an isCompactSummary user turn every reader skips.
func (session *claudeSession) compact(step Step) error {
	post := step.PostTokens
	if post == 0 {
		post = 1000
	}
	boundary := claudeRecord{
		Type:      "system",
		Subtype:   "compact_boundary",
		SessionID: session.sessionID,
		Timestamp: stamp(time.Now()),
		CompactMetadata: &compactMetadata{
			Trigger:    "manual",
			PreTokens:  session.turnUsage.contextTokens(),
			PostTokens: post,
		},
	}
	if err := session.write(boundary); err != nil {
		return err
	}
	summary := session.userRecord("Summary of the conversation so far (fixture).", false)
	summary.IsCompactSummary = true
	return session.write(summary)
}

func (session *claudeSession) rename(name string) error {
	session.title = name
	return session.write(claudeRecord{Type: claudeCustomTitle, CustomTitle: name, SessionID: session.sessionID})
}

// background starts a sidechain. Its records carry isSidechain and live in
// the session's subagents/ file, below the two levels index/walk.go:56-63
// descends, so no reader counts them; the pane shows the agent row
// internal/call/guards.go:24 exempts from the composer.
func (session *claudeSession) background(step Step) (string, error) {
	var fields struct {
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		SubagentType string `json:"subagent_type"`
	}
	if len(step.Input) > 0 {
		if err := json.Unmarshal(step.Input, &fields); err != nil {
			return "", fmt.Errorf("decode background Agent input: %w", err)
		}
	}
	if fields.Prompt != "" && session.call.headless {
		return "", errors.New("background Agent model steps require an interactive pane")
	}
	modelPrompt := fields.Prompt
	agentType := fields.SubagentType
	if agentType == "" {
		agentType = step.AgentType
	}
	if agentType == "" {
		agentType = step.Name
	}
	input := step.Input
	if fields.Prompt == "" {
		fields.Description, fields.SubagentType = step.Name, agentType
		fields.Prompt = "Sidechain task for " + step.Name
		input, _ = json.Marshal(fields)
	}
	use := contentPart{Type: blockToolUse, ID: "toolu_" + uuid.NewV4().String()[:8], Name: "Agent", Input: input}
	pre := session.payload(hookPreToolUse)
	pre.ToolName, pre.ToolInput, pre.ToolUseID = use.Name, use.Input, use.ID
	answers, err := session.fire(hookPreToolUse, use.Name, pre)
	if err != nil {
		return "", err
	}
	for index := range answers {
		if denied, reason := answers[index].denies(); denied {
			return "", fmt.Errorf("background Agent denied: %s", reason)
		}
	}
	if err := session.write(
		session.assistantRecord([]contentPart{use}, session.proc.script.Tokens, false),
	); err != nil {
		return "", err
	}
	child, payload, err := session.startSubagent(step.AgentID, agentType)
	if err != nil {
		return "", err
	}
	agentID, sidechain := child.agentID, child.transcript
	status := step.Status
	if status == "" {
		status = "running"
	}
	row := "❯ ● " + step.Name + "  " + status + " (background)"
	if modelPrompt != "" {
		steps, err := parseInlineSteps(modelPrompt)
		if err != nil {
			return "", err
		}
		if len(steps) == 0 {
			return "", errors.New("background Agent prompt requires mock-engine inline steps")
		}
		if err := child.recordUser(modelPrompt); err != nil {
			return "", err
		}
		session.backgroundModels = append(session.backgroundModels, claudeBackgroundModel{child, payload, steps, row})
		return row, nil
	}
	ask := session.userRecord("Sidechain task for "+step.Name, false)
	ask.IsSidechain, ask.AgentID = true, agentID
	reply := session.assistantRecord(
		[]contentPart{{Type: blockText, Text: step.Status}},
		session.proc.script.Tokens,
		true,
	)
	reply.AgentID = agentID
	if step.Status != agentCompleted && step.Status != "done" {
		reply.Message.StopReason = ""
	}
	for _, record := range []*claudeRecord{&ask, &reply} {
		encoded, err := json.Marshal(record)
		if err != nil {
			return "", fmt.Errorf("encode sidechain record: %w", err)
		}
		if !session.proc.script.NoTranscript {
			if err := appendLine(sidechain, encoded); err != nil {
				return "", err
			}
		}
	}
	if len(step.Input) > 0 {
		snapshot := *session
		if _, _, err := session.proc.startShell(step.Input, func(result toolExecution, runErr error) {
			if session.proc.ctx.Err() != nil {
				return
			}
			status := result.Text
			if result.Failure != "" {
				status = result.Failure
			}
			if runErr != nil {
				warn(session.proc.stderr, "background shell: %v", runErr)
				return
			}
			completed := snapshot.assistantRecord([]contentPart{{Type: blockText, Text: status}}, Tokens{}, true)
			completed.AgentID = agentID
			raw, err := json.Marshal(completed)
			if err == nil && !snapshot.proc.script.NoTranscript {
				err = appendLine(sidechain, raw)
			}
			if err == nil {
				err = snapshot.subagentStop(payload, sidechain, status)
			}
			if err != nil {
				warn(snapshot.proc.stderr, "background completion: %v", err)
			}
		}); err != nil {
			return "", err
		}
	} else if step.Status == agentCompleted || step.Status == "done" {
		if err := session.subagentStop(payload, sidechain, step.Status); err != nil {
			return "", err
		}
	}
	return row, nil
}

func (session *claudeSession) clear() error {
	if err := session.finish("clear"); err != nil {
		return err
	}
	return session.changeSession(uuid.NewV4().String(), sourceClear)
}

func (session *claudeSession) changeSession(id, source string) error {
	session.sessionID = id
	session.transcript = filepath.Join(session.configDir, "projects", claudeProjectSlug(session.proc.cwd), id+".jsonl")
	return session.begin(source)
}

func (session *claudeSession) backgroundHandoff(step Step) error {
	if step.SessionID == session.sessionID {
		return errors.New("background_handoff requires a new session_id")
	}
	record := struct {
		Type                 string `json:"type"`
		Timestamp            string `json:"timestamp"`
		SessionID            string `json:"sessionId"`
		ContinuedInSessionID string `json:"continuedInSessionId"`
	}{"continued-in", time.Now().UTC().Format(time.RFC3339Nano), session.sessionID, step.SessionID}
	if err := session.write(record); err != nil {
		return err
	}
	if session.seat != nil {
		if err := session.seat.release(); err != nil {
			return err
		}
		session.seat = nil
	}
	return session.changeSession(step.SessionID, sourceResume)
}

func (session *claudeSession) finish(reason string) error {
	payload := session.payload(hookSessionEnd)
	payload.Reason = reason
	_, err := session.fire(hookSessionEnd, "", payload)
	if session.seat != nil {
		err = errors.Join(err, session.seat.release())
		session.seat = nil
	}
	return errors.Join(err, session.removePresence())
}

// statusLine feeds the statusLine command the struct
// internal/statusline/render.go:41-84 decodes.

func (session *claudeSession) subagentStop(payload hookPayload, sidechain, reply string) error {
	payload.Event = hookSubagentStop
	payload.AgentTranscriptPath = sidechain
	payload.LastAssistantMessage = reply
	active := false
	payload.StopHookActive = &active
	_, err := session.fire(hookSubagentStop, payload.AgentType, payload)
	return err
}
