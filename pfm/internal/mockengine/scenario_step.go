package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Step kinds.
const (
	StepTurn            = "turn"
	StepHold            = "hold"
	StepToolCall        = "tool_call"
	StepBackgroundAgent = "background_agent"
	StepMenu            = "menu"
	StepCompact         = "compact"
	StepCrash           = "crash"
	StepExit            = "exit"
	StepMCP             = "mcp"
	// StepAPIError ends a turn as Claude Code does when the model server
	// refuses the request: its synthetic assistant record, no Stop hook, the
	// engine left alive at its composer (claude_turn_end.go).
	StepAPIError = "api_error"
	// StepAITitle writes Claude Code's automatic title record (ai-title) inside
	// the turn, as Claude does once a conversation has a first exchange.
	StepAITitle = "ai_title"
	// StepPermission opens Claude Code's tool permission dialog over a tool
	// call (claude_permission.go): Yes runs the tool and the turn goes on;
	// No or Escape writes the rejection and fires no hook.
	StepPermission = "permission"
	// StepBackgroundJob sends the chat to a Claude Code background job inside the turn
	// (claude_background_job.go): the turn and the chat go on under a new session id.
	StepBackgroundJob = "background_job"
)

// Step is one scripted beat. Steps are consumed in order: each prompt the
// engine receives runs steps until a terminal one (turn, menu, crash, exit,
// api_error, permission); hold, tool_call, background_agent, compact, mcp, ai_title and
// background_job happen inside that turn. An exhausted list falls back to a default turn.
type Step struct {
	Type string `json:"type"`
	// turn: the reply text, busy duration, and usage shown; api_error: the error text and its busy time.
	Reply  string  `json:"reply,omitempty"`
	BusyMS int     `json:"busy_ms,omitempty"`
	Tokens *Tokens `json:"tokens,omitempty"`
	// Structured is the structured_output a headless -p --json-schema run prints.
	Structured json.RawMessage `json:"structured,omitempty"`
	// hold: stay busy until this file no longer exists; background_agent: the agent runs until it is gone.
	UntilGone string `json:"until_gone,omitempty"`
	// tool_call, permission: the tool name PreToolUse matchers see and its input.
	Tool  string          `json:"tool,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_call: carry an allowed Bash or Write out (claude_tooluse.go perform); unset, its result is "done".
	Perform bool `json:"perform,omitempty"`
	// background_agent: the sidechain row's name and status text; ai_title: the title.
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
	// menu: the numbered options and the 1-based preselected row.
	Options  []string `json:"options,omitempty"`
	Selected int      `json:"selected,omitempty"`
	// compact: the postTokens the compact_boundary record carries.
	PostTokens int64 `json:"post_tokens,omitempty"`
	// crash / exit: the process exit code.
	ExitCode int `json:"exit_code,omitempty"`
	// mcp: the [mcp_servers.<server>] block of config.toml to connect to.
	Server string `json:"server,omitempty"`
	// api_error: the error kind the synthetic record carries ("server_error", "rate_limit", ...).
	Error string `json:"error,omitempty"`
}

func (step Step) validate() error {
	switch step.Type {
	case StepTurn, StepBackgroundAgent, StepCompact, StepCrash, StepExit, StepMCP, StepAPIError, StepBackgroundJob:
		return nil
	case StepHold:
		if step.UntilGone == "" {
			return errors.New("hold needs until_gone")
		}
	case StepAITitle:
		if step.Name == "" {
			return errors.New("ai_title needs name")
		}
	case StepToolCall:
		return step.validateToolCall()
	case StepPermission:
		if step.Tool == "" {
			return errors.New("permission needs tool")
		}
	case StepMenu:
		if len(step.Options) == 0 {
			return errors.New("menu needs options")
		}
		if step.Selected < 0 || step.Selected > len(step.Options) {
			return fmt.Errorf("menu selected %d is outside 1..%d", step.Selected, len(step.Options))
		}
	default:
		return fmt.Errorf("unknown step type %q", step.Type)
	}
	return nil
}

// terminal reports whether the step ends the turn it belongs to.
func (step Step) terminal() bool {
	switch step.Type {
	case StepTurn, StepMenu, StepCrash, StepExit, StepAPIError, StepPermission:
		return true
	}
	return false
}

// sideEffecting reports whether the step fires a hook, writes a record or
// does an MCP handshake — the beats `-p`'s fast-forward (claude.go:headless)
// consumes from the cursor without ever running, so it must name what it
// skipped rather than silently dropping it.
func (step Step) sideEffecting() bool {
	switch step.Type {
	case StepToolCall, StepBackgroundAgent, StepCompact, StepMCP, StepAITitle, StepPermission, StepBackgroundJob:
		return true
	}
	return false
}
