package mockengine

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// headless is `claude -p`: the prompt from argv or stdin, one turn, the JSON
// envelope internal/headless/run/run.go:677-683 decodes.
func (session *claudeSession) headless() int {
	proc := session.proc
	prompt := session.call.prompt
	if prompt == "" {
		content, err := io.ReadAll(io.LimitReader(proc.stdin, 1<<20))
		if err != nil {
			warn(proc.stderr, "read headless prompt: %v", err)
			return ExitUsage
		}
		prompt = strings.TrimSpace(string(content))
	}
	if handled, err := session.captureRequest(prompt); handled {
		if err != nil {
			warn(proc.stderr, "%v", err)
		}
		return ExitUsage
	}
	if session.call.noSessionPersistence {
		proc.script.NoTranscript = true
	}
	if err := session.start(session.call.resumed); err != nil {
		warn(proc.stderr, "start: %v", err)
		return ExitUsage
	}
	defer func() {
		if err := session.finish("other"); err != nil {
			warn(proc.stderr, "finish: %v", err)
		}
	}()
	if blocked, err := session.prompt(prompt); err != nil || blocked != "" {
		warn(proc.stderr, "prompt refused: %s %v", blocked, err)
		return ExitUnpinned
	}
	if !session.call.noSessionPersistence {
		if err := session.recordUser(prompt); err != nil {
			warn(proc.stderr, "record prompt: %v", err)
			return ExitUsage
		}
	}
	started := time.Now()
	running := proc.script.forPrompt(prompt)
	step := running.next()
	for !step.terminal() {
		if err := session.headlessStep(step); err != nil {
			warn(proc.stderr, "%v", err)
			return ExitUnpinned
		}
		step = running.next()
	}
	if step.Type == StepCrash {
		if step.Reply != "" {
			fmt.Fprintln(proc.stderr, step.Reply)
		}
		return step.ExitCode
	}
	reply, busyMS, usage := running.turnReply(step)
	if !sleepOrCancel(proc.ctx, time.Duration(busyMS)*time.Millisecond) {
		return ExitUsage
	}
	if step.Error != "" {
		if err := session.turnError(step); err != nil {
			warn(proc.stderr, "failed turn: %v", err)
			return ExitUsage
		}
		if session.call.outputFormat == "json" {
			encoded, err := json.Marshal(
				map[string]any{
					"type":       "result",
					"subtype":    "error_during_execution",
					"is_error":   true,
					"session_id": session.sessionID,
					"errors":     []string{step.Error},
					"num_turns":  1,
				},
			)
			if err != nil {
				return ExitUsage
			}
			fmt.Fprintln(proc.stdout, string(encoded))
		} else {
			fmt.Fprintln(proc.stderr, step.Error)
		}
		return 1
	}
	if !session.call.noSessionPersistence {
		if err := session.recordAssistant(reply, usage); err != nil {
			warn(proc.stderr, "%v", err)
			return ExitUsage
		}
	}
	if step.Type == StepTurn {
		if err := session.stop(); err != nil {
			warn(proc.stderr, "Stop: %v", err)
		}
	}
	if session.call.outputFormat != "json" {
		fmt.Fprintln(proc.stdout, reply)
		return 0
	}
	envelope := headlessEnvelope{
		Type:         "result",
		Subtype:      "success",
		DurationMS:   time.Since(started).Milliseconds(),
		NumTurns:     1,
		Result:       reply,
		SessionID:    session.sessionID,
		TotalCostUSD: usageCost(usage),
		Usage: claudeUsage{
			Input:         usage.Input,
			CacheCreation: usage.CacheCreation,
			CacheRead:     usage.CacheRead,
			Output:        usage.Output,
		},
		ModelUsage: map[string]modelUsage{session.model: {
			Input: usage.Input, Output: usage.Output, CacheRead: usage.CacheRead, CacheCreation: usage.CacheCreation,
		}},
	}
	if len(step.Structured) > 0 && (session.call.jsonSchema != "" || running != proc.script) {
		envelope.StructuredOutput = step.Structured
	}
	var output any = envelope
	if proc.script.HeadlessUsageError {
		output = struct {
			headlessEnvelope
			Usage string `json:"usage"`
		}{envelope, "bad"}
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		warn(proc.stderr, "encode envelope: %v", err)
		return ExitUsage
	}
	fmt.Fprintln(proc.stdout, string(encoded))
	return 0
}

type headlessEnvelope struct {
	Type             string                `json:"type"`
	Subtype          string                `json:"subtype"`
	IsError          bool                  `json:"is_error"`
	DurationMS       int64                 `json:"duration_ms"`
	NumTurns         int                   `json:"num_turns"`
	Result           string                `json:"result"`
	StructuredOutput json.RawMessage       `json:"structured_output,omitempty"`
	SessionID        string                `json:"session_id"`
	TotalCostUSD     float64               `json:"total_cost_usd"`
	Usage            claudeUsage           `json:"usage"`
	ModelUsage       map[string]modelUsage `json:"modelUsage"`
}

type modelUsage struct {
	Input         int64 `json:"inputTokens"`
	Output        int64 `json:"outputTokens"`
	CacheRead     int64 `json:"cacheReadInputTokens"`
	CacheCreation int64 `json:"cacheCreationInputTokens"`
}

// Transcript records, in the keys internal/transcript/transcript.go:49-75,
// meta.go:47-84 and internal/index/claude.go:13-25 read.
type claudeRecord struct {
	Type              string           `json:"type"`
	IsAPIErrorMessage bool             `json:"isApiErrorMessage,omitempty"`
	Error             string           `json:"error,omitempty"`
	UUID              string           `json:"uuid,omitempty"`
	SessionID         string           `json:"sessionId,omitempty"`
	Timestamp         string           `json:"timestamp,omitempty"`
	CWD               string           `json:"cwd,omitempty"`
	IsSidechain       bool             `json:"isSidechain"`
	IsMeta            bool             `json:"isMeta,omitempty"`
	IsCompactSummary  bool             `json:"isCompactSummary,omitempty"`
	AgentID           string           `json:"agentId,omitempty"`
	Entrypoint        string           `json:"entrypoint,omitempty"`
	PromptSource      string           `json:"promptSource,omitempty"`
	CustomTitle       string           `json:"customTitle,omitempty"`
	Subtype           string           `json:"subtype,omitempty"`
	CompactMetadata   *compactMetadata `json:"compactMetadata,omitempty"`
	Message           *claudeMessage   `json:"message,omitempty"`
}

type compactMetadata struct {
	Trigger    string `json:"trigger"`
	PreTokens  int64  `json:"preTokens"`
	PostTokens int64  `json:"postTokens"`
}

type claudeMessage struct {
	Role       string       `json:"role"`
	StopReason string       `json:"stop_reason,omitempty"`
	Model      string       `json:"model,omitempty"`
	Content    any          `json:"content"`
	Usage      *claudeUsage `json:"usage,omitempty"`
}

type claudeUsage struct {
	Input         int64 `json:"input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	Output        int64 `json:"output_tokens"`
}

type contentPart struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}
