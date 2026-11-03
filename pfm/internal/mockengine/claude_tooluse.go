package mockengine

import (
	"encoding/json"
	"errors"
	"time"
)

func (session *claudeSession) postToolUse(
	tool string, input json.RawMessage, useID string, duration time.Duration,
) error {
	response, err := session.toolResponse(tool, input)
	if err != nil {
		return err
	}
	return session.postToolResult(tool, input, useID, response, duration)
}

func (session *claudeSession) postToolResult(
	tool string,
	input json.RawMessage,
	useID string,
	response json.RawMessage,
	duration time.Duration,
) error {
	payload := session.payload(hookPostToolUse)
	payload.ToolName, payload.ToolInput, payload.ToolUseID = tool, input, useID
	payload.ToolResponse = response
	ms := duration.Milliseconds()
	payload.DurationMS = &ms
	_, err := session.fire(hookPostToolUse, tool, payload)
	return err
}

func (session *claudeSession) toolResponse(tool string, input json.RawMessage) (json.RawMessage, error) {
	result, err := session.executeTool(tool, input)
	if err != nil {
		return nil, err
	}
	if result.Failure != "" {
		return nil, errors.New(result.Failure)
	}
	return result.Response, nil
}
