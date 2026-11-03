package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Environment the mock reads. MOCK_ENGINE_* is the only namespace it owns:
// PFM_* belongs to internal/paths (arch-check C16) and CHAT_*/CC_* are the
// foreign prefixes C19 forbids.
const (
	// EnvScenario names the JSON scenario file. Unset refuses every prompt.
	EnvScenario = "MOCK_ENGINE_SCENARIO"
	// EnvEngine picks the engine when argv[0]'s basename is not one — the
	// launcher execs Claude by its versions/<v> path, whose basename is a
	// version number.
	EnvEngine = "MOCK_ENGINE_ENGINE"
)

// Exit codes the mock itself returns. Anything else comes from a scenario step.
const (
	// ExitUsage is an argv or scenario the mock cannot read.
	ExitUsage = 2
	// ExitUnpinned is a pane or protocol shape pfm has never pinned: the mock
	// refuses to render it from a guess and names what the scenario must supply.
	ExitUnpinned = 3
)

// Defaults when the scenario leaves a field empty.
const (
	DefaultReply        = "ok"
	DefaultBusyMS       = 200
	DefaultClaudeModel  = "claude-sonnet-4-5"
	DefaultCodexModel   = "codex-fixture-model"
	DefaultOpenCodeMode = "opencode-fixture-model"
	// DefaultClaudeVersion is the --version line pfm's own fakes pin
	// (internal/doctor/harness_prompt_capture_test.go writes the same string).
	DefaultClaudeVersion   = "2.1.270 (Claude Code)"
	DefaultCodexVersion    = "codex-cli 0.154.0"
	DefaultOpenCodeVersion = "1.14.30"
)

// Step kinds.
const (
	StepTurn              = "turn"
	StepHold              = "hold"
	StepToolCall          = "tool_call"
	StepBackgroundAgent   = "background_agent"
	StepMenu              = "menu"
	StepCompact           = "compact"
	StepCrash             = "crash"
	StepExit              = "exit"
	StepMCP               = "mcp"
	StepPermission        = "permission"
	StepQuestion          = "question"
	StepNotification      = "notification"
	StepHook              = "hook"
	StepBackgroundHandoff = "background_handoff"
	StepNativeTitle       = "native_title"
)

// Tokens is the usage one turn reports, in the engine's own field names when
// written (Claude usage / modelUsage, Codex token_count).
type Tokens struct {
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	CacheRead     int64 `json:"cache_read"`
	CacheCreation int64 `json:"cache_creation"`
	// ContextWindow is the model's window; Codex reports it on every
	// token_count, Claude only through the statusline.
	ContextWindow int64 `json:"context_window"`
}

// Step is one scripted beat. Steps are consumed in order: each prompt the
// engine receives runs steps until a terminal one (turn, menu, crash, exit);
// hold, tool_call, background_agent, compact and mcp happen inside that turn.
// An exhausted list refuses the request.
type Step struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	// turn: the reply text, busy duration, and usage shown.
	Reply      string          `json:"reply,omitempty"`
	BusyMS     int             `json:"busy_ms,omitempty"`
	Tokens     *Tokens         `json:"tokens,omitempty"`
	RateLimits json.RawMessage `json:"rate_limits,omitempty"`
	During     []Step          `json:"during,omitempty"`
	// Structured is the structured_output a headless -p --json-schema run prints.
	Structured json.RawMessage `json:"structured,omitempty"`
	// hold: stay busy until this file no longer exists.
	UntilGone string `json:"until_gone,omitempty"`
	// tool_call: the tool name PreToolUse matchers see and its input.
	Tool  string          `json:"tool,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// background_agent: sidechain identity/status; input.command runs its real background shell.
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
	Server           string `json:"server,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	AgentType        string `json:"agent_type,omitempty"`
	NotificationType string `json:"notification_type,omitempty"`
	Message          string `json:"message,omitempty"`
	// Error on a turn models an API failure; on a tool it models execution failure.
	Error   string          `json:"error,omitempty"`
	Event   string          `json:"event,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// PaneShapes carries the pane strings pfm has not pinned for an engine. Codex
// and OpenCode refuse to render a TUI without them (ExitUnpinned): the mock
// never invents a spelling a real engine may not print.
type PaneShapes struct {
	Busy      string `json:"busy,omitempty"`
	Compacted string `json:"compacted,omitempty"`
	// Composer is the OpenCode composer glyph line; Claude's ❯ and Codex's ›
	// are pinned by internal/call/guards.go and need no value here.
	Composer string `json:"composer,omitempty"`
}

// Jail names the seat bindings a real host derives without help — the engine's
// own /proc entry and the sid crumb pfm-statusline's breadcrumb writes — so a
// jailed pfm (PFM_PROC_ROOT, PFM_SID_DIR) can find the chat. Empty fields
// write nothing.
type Jail struct {
	ProcRoot string `json:"proc_root,omitempty"`
	SIDDir   string `json:"sid_dir,omitempty"`
}

type statuslineRefresh struct {
	Count          int    `json:"count"`
	IntervalMS     int    `json:"interval_ms"`
	StopWhenExists string `json:"stop_when_exists,omitempty"`
}

// Scenario is the whole script one mock process follows.
type Scenario struct {
	// Version is the --version line.
	Version string `json:"version,omitempty"`
	// SessionID is the Claude session id, Codex thread id or OpenCode session
	// id. Empty means a fresh UUID; --session-id/--resume/resume override it.
	SessionID string `json:"session_id,omitempty"`
	Model     string `json:"model,omitempty"`
	// Reply and BusyMS supply omitted fields of an explicitly scripted turn.
	Reply  string `json:"reply,omitempty"`
	BusyMS *int   `json:"busy_ms,omitempty"`
	Tokens Tokens `json:"tokens"`
	Steps  []Step `json:"steps,omitempty"`
	// TrustDialog holds a Claude pane at Claude Code's folder-trust dialog
	// before its composer, as a real one boots in a folder its account has not
	// trusted: 1 trusts the folder and boots on; Enter, Escape or 2 — "No,
	// exit" is the default row — ends the engine.
	TrustDialog           bool `json:"trust_dialog,omitempty"`
	TrustDialogUnnumbered bool `json:"trust_dialog_unnumbered,omitempty"`
	// ReadyHold delays all native readiness signals until this absolute path exists.
	ReadyHold      string `json:"ready_hold,omitempty"`
	ReadyTimeoutMS int    `json:"ready_timeout_ms,omitempty"`
	// StartupExitCode exits after the readiness gate and before native session startup.
	StartupExitCode *int `json:"startup_exit_code,omitempty"`
	// AppServerStdoutHold leaves a real descendant holding stdout until this path exists.
	AppServerStdoutHold string     `json:"app_server_stdout_hold,omitempty"`
	Pane                PaneShapes `json:"pane"`
	Jail                Jail       `json:"jail"`
	Quiet               bool       `json:"quiet,omitempty"`
	NoTranscript        bool       `json:"no_transcript,omitempty"`
	HeadlessUsageError  bool       `json:"headless_usage_error,omitempty"`
	// RateLimits is the account/rateLimits/read result supplied by the scenario.
	RateLimits json.RawMessage `json:"rate_limits,omitempty"`
	// SourceCheckouts supplies offline GitHub marketplace and git-subdir contents; the
	// mock still owns registration, installation and versioned cache writes.
	SourceCheckouts map[string]string `json:"source_checkouts,omitempty"`
	// RecordDir receives the mock's own telemetry — argv, prompts, hook
	// answers, MCP tool lists — for a test to read. It is evidence of what the
	// mock saw, never the judge of what pfm read.
	RecordDir             string             `json:"record_dir,omitempty"`
	ActionFile            string             `json:"action_file,omitempty"`
	HookGates             []hookGate         `json:"hook_gates,omitempty"`
	IdleStatuslineRefresh *statuslineRefresh `json:"idle_statusline_refresh,omitempty"`
}

// LoadScenario reads the file EnvScenario names. An empty path is the default
// scenario; a path that does not exist or does not parse is an error the
// caller reports as ExitUsage — a missing scenario is never silently the
// default one.
func LoadScenario(path, engine string) (Scenario, error) {
	if strings.TrimSpace(path) == "" {
		return Scenario{}, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("read scenario %s: %w", path, err)
	}
	var scenario Scenario
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&scenario); err != nil {
		return Scenario{}, fmt.Errorf("decode scenario %s: %w", path, err)
	}
	for index := range scenario.Steps {
		if err := scenario.Steps[index].validate(); err != nil {
			return Scenario{}, fmt.Errorf("scenario %s step %d: %w", path, index, err)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.NewDecoder(strings.NewReader(string(content))).Decode(&fields); err != nil {
		return Scenario{}, fmt.Errorf("decode scenario fields %s: %w", path, err)
	}
	if err := scenario.validateStartup(fields); err != nil {
		return Scenario{}, fmt.Errorf("scenario %s: %w", path, err)
	}
	if raw, present := fields["rate_limits"]; present && !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		return Scenario{}, fmt.Errorf("scenario %s field rate_limits must be an object", path)
	}
	for _, door := range []struct {
		name    string
		enabled bool
	}{
		{"quiet", scenario.Quiet},
		{"no_transcript", scenario.NoTranscript},
		{"trust_dialog_unnumbered", scenario.TrustDialogUnnumbered},
	} {
		if raw, present := fields[door.name]; present && strings.TrimSpace(string(raw)) == "null" {
			return Scenario{}, fmt.Errorf("scenario %s field %s must be boolean", path, door.name)
		}
		if door.enabled && engine != engineClaude {
			return Scenario{}, fmt.Errorf(
				"scenario %s field %s is Claude-only, refused for %s",
				path,
				door.name,
				engine,
			)
		}
	}
	seenGates := map[string]bool{}
	for _, gate := range scenario.HookGates {
		key := gate.Event + "\x00" + gate.Command
		if gate.Event == "" || gate.Command == "" || !filepath.IsAbs(gate.UntilGone) || seenGates[key] {
			return Scenario{}, fmt.Errorf(
				"scenario %s hook_gates needs unique event+command and absolute until_gone",
				path,
			)
		}
		seenGates[key] = true
	}
	if refresh := scenario.IdleStatuslineRefresh; refresh != nil {
		if refresh.StopWhenExists != "" && !filepath.IsAbs(refresh.StopWhenExists) {
			return Scenario{}, fmt.Errorf("scenario %s idle_statusline_refresh stop_when_exists must be absolute", path)
		}
		if refresh.Count <= 0 || refresh.IntervalMS <= 0 ||
			time.Duration(refresh.IntervalMS) > time.Duration(1<<63-1)/time.Millisecond {
			return Scenario{}, fmt.Errorf(
				"scenario %s idle_statusline_refresh requires positive count and representable positive interval_ms",
				path,
			)
		}
	}
	return scenario, nil
}

// Write serialises the scenario for a test to hand to a mock process.
func (scenario Scenario) Write(path string) error {
	content, err := json.MarshalIndent(scenario, "", "  ")
	if err != nil {
		return fmt.Errorf("encode scenario: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create scenario directory: %w", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write scenario %s: %w", path, err)
	}
	return nil
}

func (step Step) validate() error {
	if len(step.RateLimits) > 0 && !strings.HasPrefix(strings.TrimSpace(string(step.RateLimits)), "{") {
		return errors.New("rate_limits must be an object")
	}
	if len(step.During) > 0 && step.Type != StepPermission && step.Type != StepQuestion {
		return errors.New("during is supported only for permission or question")
	}
	for index := range step.During {
		child := step.During[index]
		if len(child.During) > 0 {
			return errors.New("nested during is unsupported")
		}
		if child.Type != StepHook && child.Type != StepNotification && child.Type != StepHold {
			return errors.New("during permits only hook, notification, or hold")
		}
		if err := child.validate(); err != nil {
			return fmt.Errorf("during step %d: %w", index, err)
		}
	}
	switch step.Type {
	case StepTurn, StepBackgroundAgent, StepCompact, StepCrash, StepExit, StepMCP, StepNotification, StepQuestion:
		return nil
	case StepNativeTitle:
		if strings.TrimSpace(step.Name) == "" {
			return errors.New("native_title needs name")
		}
	case StepBackgroundHandoff:
		if !pfmengine.IsUUID(step.SessionID) {
			return errors.New("background_handoff needs a UUID session_id")
		}
	case StepHook:
		if step.Event == "" || !json.Valid(step.Payload) {
			return errors.New("hook needs event and JSON payload")
		}
	case StepHold:
		if step.UntilGone == "" {
			return errors.New("hold needs until_gone")
		}
	case StepToolCall, StepPermission:
		if step.Tool == "" {
			return errors.New("tool_call needs tool")
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
	case StepTurn, StepMenu, StepCrash, StepExit:
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
	case StepToolCall, StepBackgroundAgent, StepBackgroundHandoff, StepNativeTitle, StepCompact, StepMCP:
		return true
	}
	return false
}
