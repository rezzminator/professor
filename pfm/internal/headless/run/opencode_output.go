package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type openCodeAssistantMarker struct {
	SessionID     string          `json:"sessionID"`
	UserMessageID string          `json:"userMessageID"`
	AssistantIDs  []string        `json:"assistantIDs"`
	Error         json.RawMessage `json:"error"`
}

type openCodeMessageSnapshot struct {
	Info  json.RawMessage   `json:"info"`
	Parts []json.RawMessage `json:"parts"`
}

// collectOpenCodeRunOutput reads the persisted message after an attached CLI
// invocation. OpenCode's attached run returns after submitting the prompt and
// does not drain the server event stream, so its JSONL stdout may contain only
// step_start. The session message API is the authoritative completed result.
func collectOpenCodeRunOutput(
	ctx context.Context,
	address, cwd string,
	environment []string,
	assistantPath string,
	request Request,
) (string, error) {
	ticker := request.Clock.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		markerBody, markerErr := os.ReadFile(assistantPath)
		if markerErr == nil {
			var marker openCodeAssistantMarker
			if err := json.Unmarshal(markerBody, &marker); err != nil {
				return "", fmt.Errorf("decode OpenCode assistant marker: %w", err)
			}
			if len(marker.Error) != 0 && string(marker.Error) != jsonNull {
				var output bytes.Buffer
				if err := appendOpenCodeErrorEvent(&output, marker.Error); err != nil {
					return "", err
				}
				return output.String(), nil
			}
			if marker.SessionID != "" && len(marker.AssistantIDs) != 0 {
				var output bytes.Buffer
				complete := false
				allSnapshotsComplete := true
				for _, assistantID := range marker.AssistantIDs {
					if assistantID == "" {
						continue
					}
					body, err := openCodeJSON(
						ctx,
						http.MethodGet,
						address+"/session/"+url.PathEscape(marker.SessionID)+"/message/"+url.PathEscape(assistantID),
						cwd,
						environment,
						nil,
					)
					if err != nil {
						return "", fmt.Errorf("read OpenCode assistant message: %w", err)
					}
					snapshotComplete, err := openCodeMessageSnapshotComplete(body, request)
					if err != nil {
						return "", err
					}
					allSnapshotsComplete = allSnapshotsComplete && snapshotComplete
					partOutput, partComplete, err := openCodeMessageAsJSONL(body, request)
					if err != nil {
						return "", err
					}
					output.WriteString(partOutput)
					complete = complete || partComplete
				}
				if complete && allSnapshotsComplete {
					return output.String(), nil
				}
			}
		} else if !errors.Is(markerErr, os.ErrNotExist) {
			return "", fmt.Errorf("read OpenCode assistant marker: %w", markerErr)
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("wait for OpenCode run result: %w", ctx.Err())
		case <-ticker.C():
		}
	}
}

// applyOpenCodeRunOutput replaces a normalized run's stdout with the persisted
// assistant snapshot the CLI's own stream does not carry. A native run keeps
// OpenCode's stream verbatim, but its success is still gated on that snapshot:
// `run --attach` returns after submitting the prompt, so its exit code alone
// says nothing about whether the model actually answered.
func applyOpenCodeRunOutput(
	ctx context.Context,
	address, cwd string,
	environment []string,
	result *Result,
	request Request,
) error {
	assistantPath := environmentValue(environment, "PFM_OPENCODE_ASSISTANTS_FILE")
	if assistantPath == "" {
		return errors.New("OpenCode assistant marker path is missing")
	}
	collected, err := collectOpenCodeRunOutput(ctx, address, cwd, environment, assistantPath, request)
	if err != nil {
		return err
	}
	if !request.Native {
		result.Stdout = collected
		return nil
	}
	validation := *result
	validation.Stdout = collected
	validationRequest := request
	validationRequest.Native = false
	return parseOpenCodeJSONL(&validation, validationRequest)
}

func openCodeMessageAsJSONL(body []byte, request Request) (string, bool, error) {
	var message openCodeMessageSnapshot
	if err := json.Unmarshal(body, &message); err != nil {
		return "", false, fmt.Errorf("decode OpenCode assistant message: %w", err)
	}
	wrapped, err := json.Marshal([]openCodeMessageSnapshot{message})
	if err != nil {
		return "", false, fmt.Errorf("encode OpenCode assistant message: %w", err)
	}
	return openCodeMessagesAsJSONL(wrapped, "", request)
}

func openCodeMessageSnapshotComplete(body []byte, request Request) (bool, error) {
	var message struct {
		Info struct {
			Role       string          `json:"role"`
			Finish     string          `json:"finish"`
			Structured json.RawMessage `json:"structured"`
			Time       struct {
				Completed json.RawMessage `json:"completed"`
			} `json:"time"`
		} `json:"info"`
		Parts []struct {
			Type  string `json:"type"`
			Tool  string `json:"tool"`
			State struct {
				Status string `json:"status"`
			} `json:"state"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(body, &message); err != nil {
		return false, fmt.Errorf("decode OpenCode assistant completion: %w", err)
	}
	if message.Info.Role != "assistant" {
		return false, nil
	}
	if len(message.Info.Time.Completed) != 0 && string(message.Info.Time.Completed) != jsonNull {
		return true, nil
	}
	if message.Info.Finish == "stop" {
		return true, nil
	}
	if request.Schema != nil {
		for _, part := range message.Parts {
			if part.Type == openCodePartTool && part.Tool == openCodeStructuredTool &&
				part.State.Status == openCodeStateCompleted {
				// A completed StructuredOutput part can be persisted before the
				// assistant's final step metadata. Wait for the message timestamp
				// or a persisted step before treating this snapshot as stable.
				for _, candidate := range message.Parts {
					if candidate.Type == "step-finish" {
						return true, nil
					}
				}
				return false, nil
			}
		}
		return false, nil
	}
	return false, nil
}

func openCodeMessagesAsJSONL(body []byte, userMessageID string, request Request) (string, bool, error) {
	var messages []openCodeMessageSnapshot
	if err := json.Unmarshal(body, &messages); err != nil {
		return "", false, fmt.Errorf("decode OpenCode run messages: %w", err)
	}
	var output bytes.Buffer
	complete := false
	assistantFound := false
	for _, message := range messages {
		var info struct {
			Role       string          `json:"role"`
			ParentID   string          `json:"parentID"`
			Finish     string          `json:"finish"`
			Error      json.RawMessage `json:"error"`
			Structured json.RawMessage `json:"structured"`
			Cost       json.RawMessage `json:"cost"`
			Tokens     json.RawMessage `json:"tokens"`
			Time       struct {
				Completed json.RawMessage `json:"completed"`
			} `json:"time"`
		}
		if err := json.Unmarshal(message.Info, &info); err != nil {
			return "", false, fmt.Errorf("decode OpenCode run message info: %w", err)
		}
		if info.Role != "assistant" || (userMessageID != "" && info.ParentID != userMessageID) {
			continue
		}
		assistantFound = true
		hasStepFinish := false
		structuredComplete := false
		for _, rawPart := range message.Parts {
			var part struct {
				Type   string          `json:"type"`
				Tool   string          `json:"tool"`
				State  json.RawMessage `json:"state"`
				Reason string          `json:"reason"`
			}
			if err := json.Unmarshal(rawPart, &part); err != nil {
				return "", false, fmt.Errorf("decode OpenCode run message part: %w", err)
			}
			switch part.Type {
			case jsonEventText:
				if err := appendOpenCodeEvent(&output, jsonEventText, rawPart); err != nil {
					return "", false, err
				}
			case openCodePartTool:
				if part.Tool == openCodeStructuredTool {
					var state struct {
						Status string `json:"status"`
					}
					if err := json.Unmarshal(part.State, &state); err != nil {
						return "", false, fmt.Errorf("decode OpenCode structured-output state: %w", err)
					}
					structuredComplete = state.Status == openCodeStateCompleted
				}
				if err := appendOpenCodeEvent(&output, "tool_use", rawPart); err != nil {
					return "", false, err
				}
			case "step-finish":
				hasStepFinish = true
				if err := appendOpenCodeEvent(&output, "step_finish", rawPart); err != nil {
					return "", false, err
				}
			case jsonEventError:
				var partError struct {
					Error json.RawMessage `json:"error"`
				}
				if err := json.Unmarshal(rawPart, &partError); err != nil {
					return "", false, fmt.Errorf("decode OpenCode run part error: %w", err)
				}
				if len(partError.Error) != 0 && string(partError.Error) != jsonNull {
					if err := appendOpenCodeErrorEvent(&output, partError.Error); err != nil {
						return "", false, err
					}
				} else if err := appendOpenCodeEvent(&output, jsonEventError, rawPart); err != nil {
					return "", false, err
				}
			}
		}
		if len(info.Structured) != 0 && string(info.Structured) != jsonNull && !structuredComplete {
			structuredPart := map[string]any{
				openCodePartTool: openCodeStructuredTool,
				"state":          map[string]any{"status": openCodeStateCompleted, "input": info.Structured},
			}
			if err := appendOpenCodeEvent(&output, "tool_use", structuredPart); err != nil {
				return "", false, err
			}
		}
		if !hasStepFinish && info.Finish != "" {
			step := map[string]any{"reason": info.Finish}
			if len(info.Cost) != 0 && string(info.Cost) != jsonNull {
				step["cost"] = info.Cost
			}
			if len(info.Tokens) != 0 && string(info.Tokens) != jsonNull {
				step["tokens"] = info.Tokens
			}
			if err := appendOpenCodeEvent(&output, "step_finish", step); err != nil {
				return "", false, err
			}
		}
		if len(info.Error) != 0 && string(info.Error) != jsonNull {
			if err := appendOpenCodeErrorEvent(&output, info.Error); err != nil {
				return "", false, err
			}
		}
		if len(info.Error) != 0 && string(info.Error) != jsonNull {
			complete = true
		}
		messageCompleted := len(info.Time.Completed) != 0 && string(info.Time.Completed) != jsonNull
		if request.Schema != nil &&
			(structuredComplete || (len(info.Structured) != 0 && string(info.Structured) != jsonNull)) &&
			(hasStepFinish || messageCompleted) {
			complete = true
		}
		if info.Finish != "" && info.Finish != "tool-calls" && info.Finish != "unknown" {
			complete = true
		}
	}
	if !assistantFound {
		return "", false, nil
	}
	if !complete {
		return output.String(), false, nil
	}
	return output.String(), true, nil
}

func appendOpenCodeEvent(output *bytes.Buffer, eventType string, part any) error {
	event := map[string]any{jsonKeyType: eventType, "part": part}
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode OpenCode normalized event: %w", err)
	}
	output.Write(encoded)
	output.WriteByte('\n')
	return nil
}

func appendOpenCodeErrorEvent(output *bytes.Buffer, errorValue json.RawMessage) error {
	event := map[string]any{jsonKeyType: jsonEventError, jsonEventError: errorValue}
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode OpenCode normalized error event: %w", err)
	}
	output.Write(encoded)
	output.WriteByte('\n')
	return nil
}

func parseOpenCodeJSONL(result *Result, request Request) error {
	var text string
	var terminalReason string
	var fatalError bool
	usage := &TokenUsage{}
	usageKnown := false
	var cost float64
	costKnown := false
	var structured json.RawMessage
	texts := map[string]string{}
	textParts := map[string]bool{}
	lastMessageID := ""
	for lineNo, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			Type      string          `json:"type"`
			Error     json.RawMessage `json:"error"`
			Message   string          `json:"message"`
			Part      json.RawMessage `json:"part"`
			SessionID string          `json:"sessionID"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return fmt.Errorf("parse OpenCode JSONL event %d: %w", lineNo+1, err)
		}
		if event.Type == "" {
			return fmt.Errorf("OpenCode event %d has no type", lineNo+1)
		}
		switch event.Type {
		case jsonEventError, "session.error":
			fatalError = true
			message := openCodeErrorMessage(event.Message, event.Error)
			if message == "" {
				message = "OpenCode reported an error"
			}
			result.Diagnostics = append(result.Diagnostics, message)
		case jsonEventText:
			var part struct {
				ID        string `json:"id"`
				MessageID string `json:"messageID"`
				Text      string `json:"text"`
				Time      struct {
					End int64 `json:"end"`
				} `json:"time"`
			}
			if err := json.Unmarshal(event.Part, &part); err != nil {
				return fmt.Errorf("parse OpenCode text event %d: %w", lineNo+1, err)
			}
			if part.Time.End != 0 || part.Text != "" {
				messageID := part.MessageID
				if messageID == "" {
					messageID = "_default"
				}
				lastMessageID = messageID
				partKey := messageID + "\x00" + part.ID
				if part.ID == "" || !textParts[partKey] {
					texts[messageID] += part.Text
					textParts[partKey] = true
				}
			}
		case "tool_use":
			var part struct {
				Tool  string `json:"tool"`
				State struct {
					Status string          `json:"status"`
					Input  json.RawMessage `json:"input"`
					Error  string          `json:"error"`
				} `json:"state"`
			}
			if err := json.Unmarshal(event.Part, &part); err != nil {
				return fmt.Errorf("parse OpenCode tool event %d: %w", lineNo+1, err)
			}
			if part.State.Status == "error" {
				if part.State.Error != "" {
					result.Diagnostics = append(result.Diagnostics, part.State.Error)
				} else {
					result.Diagnostics = append(result.Diagnostics, "OpenCode tool reported an error")
				}
			}
			if part.Tool == openCodeStructuredTool && part.State.Status == openCodeStateCompleted {
				structured = append(json.RawMessage(nil), part.State.Input...)
			}
		case "step_finish":
			var part struct {
				Reason    string          `json:"reason"`
				MessageID string          `json:"messageID"`
				Cost      json.RawMessage `json:"cost"`
				Tokens    json.RawMessage `json:"tokens"`
			}
			var tokens struct {
				Input     int `json:"input"`
				Output    int `json:"output"`
				Reasoning int `json:"reasoning"`
				Cache     struct {
					Read  int `json:"read"`
					Write int `json:"write"`
				} `json:"cache"`
			}
			if err := json.Unmarshal(event.Part, &part); err != nil {
				return fmt.Errorf("parse OpenCode step_finish event %d: %w", lineNo+1, err)
			}
			if len(part.Tokens) != 0 && string(part.Tokens) != jsonNull {
				if err := json.Unmarshal(part.Tokens, &tokens); err != nil {
					return fmt.Errorf("parse OpenCode step_finish event %d tokens: %w", lineNo+1, err)
				}
				if tokens.Input < 0 || tokens.Output < 0 || tokens.Reasoning < 0 || tokens.Cache.Read < 0 ||
					tokens.Cache.Write < 0 {
					return fmt.Errorf("OpenCode step_finish event %d contains negative token counts", lineNo+1)
				}
				usage.Input += tokens.Input
				usage.Output += tokens.Output + tokens.Reasoning
				usage.CachedInput += tokens.Cache.Read
				usage.CacheCreation += tokens.Cache.Write
				usageKnown = true
			}
			if len(part.Cost) != 0 && string(part.Cost) != jsonNull {
				var value float64
				if err := json.Unmarshal(part.Cost, &value); err != nil {
					return fmt.Errorf("OpenCode step_finish event %d cost: %w", lineNo+1, err)
				}
				if value < 0 {
					return fmt.Errorf("OpenCode step_finish event %d contains negative cost", lineNo+1)
				}
				costKnown = true
				cost += value
			}
			terminalReason = part.Reason
			if part.MessageID != "" {
				lastMessageID = part.MessageID
			}
		}
	}
	if fatalError {
		return fmt.Errorf("OpenCode headless output reported an error")
	}
	if terminalReason == "" {
		return fmt.Errorf("OpenCode headless output missing terminal stop event")
	}
	if request.Schema != nil {
		if len(structured) == 0 {
			return fmt.Errorf("%w: OpenCode did not return StructuredOutput", ErrStructuredOutput)
		}
		if err := validateInstance(request.Schema, structured); err != nil {
			return fmt.Errorf("%w: %v", ErrStructuredOutput, err)
		}
		result.StructuredOutput = structured
		result.Answer = string(structured)
	} else {
		if terminalReason != "stop" {
			return fmt.Errorf(
				"OpenCode headless output ended with %q and contains no structured result",
				terminalReason,
			)
		}
		if lastMessageID != "" {
			text = texts[lastMessageID]
		}
		result.Answer = text
	}
	if usageKnown {
		result.Usage = usage
	}
	if costKnown {
		result.TotalCostUSD = &cost
	}
	return nil
}

func openCodeErrorMessage(message string, raw json.RawMessage) string {
	if message != "" {
		return message
	}
	if len(raw) == 0 || string(raw) == jsonNull {
		return ""
	}
	var value struct {
		Message string `json:"message"`
		Data    struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &value) == nil {
		if value.Message != "" {
			return value.Message
		}
		return value.Data.Message
	}
	return "OpenCode reported an unreadable error event"
}
