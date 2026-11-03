package mockengine

import (
	"encoding/json"
	"fmt"
)

func (session *claudeSession) statusLine(usage Tokens) (string, error) {
	if session.proc.script.Quiet {
		return "", nil
	}
	window := usage.ContextWindow
	if window == 0 {
		window = session.proc.script.Tokens.ContextWindow
	}
	used := usage.contextTokens()
	percent := 0.0
	if window > 0 {
		percent = float64(used) / float64(window) * 100
	}
	input := statusInput{
		SessionID:      session.sessionID,
		TranscriptPath: session.transcript,
		CWD:            session.proc.cwd,
		SessionName:    session.title,
	}
	input.Model.ID, input.Model.DisplayName = session.model, claudeDisplayName(session.model)
	input.Workspace.CurrentDir = session.proc.cwd
	input.ContextWindow.UsedPercentage = percent
	input.ContextWindow.TotalInputTokens, input.ContextWindow.TotalOutputTokens = used, usage.Output
	input.ContextWindow.CurrentUsage.InputTokens = usage.Input
	input.ContextWindow.CurrentUsage.CacheReadInputTokens = usage.CacheRead
	input.ContextWindow.CurrentUsage.CacheCreationInputTokens = usage.CacheCreation
	input.Cost.TotalCostUSD = usageCost(usage)
	input.Effort.Level = session.call.effort
	if session.responseSeen && len(session.proc.script.RateLimits) > 0 {
		if err := json.Unmarshal(session.proc.script.RateLimits, &input.RateLimits); err != nil {
			return "", fmt.Errorf("decode statusline rate_limits: %w", err)
		}
	}
	return invokeStatusLine(
		session.proc.ctx,
		session.statusCmd,
		input,
		session.proc.cwd,
		session.environ,
		session.proc.recorder,
	)
}

type statusInput struct {
	Model struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Workspace struct {
		CurrentDir  string `json:"current_dir"`
		GitWorktree string `json:"git_worktree,omitempty"`
	} `json:"workspace"`
	CWD           string `json:"cwd"`
	ContextWindow struct {
		UsedPercentage    float64 `json:"used_percentage"`
		TotalInputTokens  int64   `json:"total_input_tokens"`
		TotalOutputTokens int64   `json:"total_output_tokens"`
		CurrentUsage      struct {
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			InputTokens              int64 `json:"input_tokens"`
		} `json:"current_usage"`
	} `json:"context_window"`
	Cost struct {
		TotalCostUSD      float64 `json:"total_cost_usd"`
		TotalDurationMS   int64   `json:"total_duration_ms"`
		TotalLinesAdded   int64   `json:"total_lines_added"`
		TotalLinesRemoved int64   `json:"total_lines_removed"`
	} `json:"cost"`
	Effort struct {
		Level string `json:"level"`
	} `json:"effort"`
	RateLimits     map[string]json.RawMessage `json:"rate_limits,omitempty"`
	SessionName    string                     `json:"session_name,omitempty"`
	SessionID      string                     `json:"session_id"`
	TranscriptPath string                     `json:"transcript_path"`
}

type statuslineCallback struct {
	Input     json.RawMessage `json:"input"`
	Stdout    string          `json:"stdout"`
	Stderr    string          `json:"stderr"`
	PID       int             `json:"pid"`
	ExitCode  int             `json:"exit_code"`
	ConfigDir string          `json:"config_dir"`
	Argv      []string        `json:"argv"`
}
