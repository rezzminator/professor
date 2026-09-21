package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// Environment the mock reads. MOCK_ENGINE_* is the only namespace it owns:
// PFM_* belongs to internal/paths (arch-check C16) and CHAT_*/CC_* are the
// foreign prefixes C19 forbids.
const (
	// EnvScenario names the JSON scenario file. Unset means the defaults: every
	// prompt is answered with one word after DefaultBusyMS.
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
	StepTurn            = "turn"
	StepHold            = "hold"
	StepToolCall        = "tool_call"
	StepBackgroundAgent = "background_agent"
	StepMenu            = "menu"
	StepCompact         = "compact"
	StepCrash           = "crash"
	StepExit            = "exit"
	StepMCP             = "mcp"
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
// An exhausted list falls back to a default turn.
type Step struct {
	Type string `json:"type"`
	// turn: the reply text, busy duration, and usage shown.
	Reply  string  `json:"reply,omitempty"`
	BusyMS int     `json:"busy_ms,omitempty"`
	Tokens *Tokens `json:"tokens,omitempty"`
	// Structured is the structured_output a headless -p --json-schema run prints.
	Structured json.RawMessage `json:"structured,omitempty"`
	// hold: stay busy until this file no longer exists.
	UntilGone string `json:"until_gone,omitempty"`
	// tool_call: the tool name PreToolUse matchers see and its input.
	Tool  string          `json:"tool,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// background_agent: the sidechain row's name and status text.
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
	// menu: the numbered options and the 1-based preselected row.
	Options  []string `json:"options,omitempty"`
	Selected int      `json:"selected,omitempty"`
	// compact: the postTokens the compact_boundary record carries. Codex's
	// PreserveAppendix re-emits the still-live SessionStart developer message
	// in replacement_history, so codexappendix's found==true branch is
	// reachable; the default omits it, exercising the absent branch instead.
	PostTokens       int64 `json:"post_tokens,omitempty"`
	PreserveAppendix bool  `json:"preserve_appendix,omitempty"`
	// crash / exit: the process exit code.
	ExitCode int `json:"exit_code,omitempty"`
	// mcp: the [mcp_servers.<server>] block of config.toml to connect to.
	Server string `json:"server,omitempty"`
}

// PaneShapes carries the pane strings pfm has not pinned for an engine. Codex
// and OpenCode refuse to render a TUI without them (ExitUnpinned): the mock
// never invents a spelling a real engine may not print.
type PaneShapes struct {
	Busy      string `json:"busy,omitempty"`
	Compacted string `json:"compacted,omitempty"`
	// Composer is the OpenCode composer glyph line; Claude's ❯ and Codex's ›
	// are pinned by internal/inject/guards.go and need no value here.
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

// Scenario is the whole script one mock process follows.
type Scenario struct {
	// Version is the --version line.
	Version string `json:"version,omitempty"`
	// SessionID is the Claude session id, Codex thread id or OpenCode session
	// id. Empty means a fresh UUID; --session-id/--resume/resume override it.
	SessionID string `json:"session_id,omitempty"`
	Model     string `json:"model,omitempty"`
	// Reply and BusyMS are the defaults a step or an exhausted list falls back to.
	Reply  string     `json:"reply,omitempty"`
	BusyMS *int       `json:"busy_ms,omitempty"`
	Tokens Tokens     `json:"tokens"`
	Steps  []Step     `json:"steps,omitempty"`
	Pane   PaneShapes `json:"pane"`
	Jail   Jail       `json:"jail"`
	// RecordDir receives the mock's own telemetry — argv, prompts, hook
	// answers, MCP tool lists — for a test to read. It is evidence of what the
	// mock saw, never the judge of what pfm read.
	RecordDir string `json:"record_dir,omitempty"`
}

// LoadScenario reads the file EnvScenario names. An empty path is the default
// scenario; a path that does not exist or does not parse is an error the
// caller reports as ExitUsage — a missing scenario is never silently the
// default one.
func LoadScenario(path string) (Scenario, error) {
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
	switch step.Type {
	case StepTurn, StepBackgroundAgent, StepCompact, StepCrash, StepExit, StepMCP:
		return nil
	case StepHold:
		if step.UntilGone == "" {
			return errors.New("hold needs until_gone")
		}
	case StepToolCall:
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
	case StepToolCall, StepBackgroundAgent, StepCompact, StepMCP:
		return true
	}
	return false
}

// cursorSuffix names the file beside a scenario that remembers how many
// steps earlier processes consumed, so one script spans a resume, a headless
// call and its follow-up: the ordered list is the session's, not one
// process's.
const cursorSuffix = ".cursor"

// script is a scenario with its defaults resolved and a cursor over its steps.
type script struct {
	Scenario
	engine   string
	position int
	cursor   string
}

// newScript resolves defaults; cursor is the scenario path ("" keeps the
// position in memory only).
func newScript(scenario Scenario, engine string) *script {
	resolved := &script{Scenario: scenario, engine: engine}
	if resolved.Reply == "" {
		resolved.Reply = DefaultReply
	}
	if resolved.BusyMS == nil {
		busy := DefaultBusyMS
		resolved.BusyMS = &busy
	}
	if resolved.Model == "" {
		switch engine {
		case engineClaude:
			resolved.Model = DefaultClaudeModel
		case engineCodex:
			resolved.Model = DefaultCodexModel
		default:
			resolved.Model = DefaultOpenCodeMode
		}
	}
	if resolved.Version == "" {
		switch engine {
		case engineClaude:
			resolved.Version = DefaultClaudeVersion
		case engineCodex:
			resolved.Version = DefaultCodexVersion
		default:
			resolved.Version = DefaultOpenCodeVersion
		}
	}
	if resolved.Tokens.Input == 0 && resolved.Tokens.Output == 0 {
		resolved.Tokens = Tokens{Input: 12, Output: 3, CacheRead: 1000, CacheCreation: 40, ContextWindow: 200000}
	}
	return resolved
}

// resume attaches the cursor file and reads the position earlier processes
// left. A cursor that does not parse is an error: a stale count would replay
// or skip steps silently.
func (running *script) resume(scenarioPath string) error {
	if scenarioPath == "" {
		return nil
	}
	running.cursor = scenarioPath + cursorSuffix
	content, err := os.ReadFile(running.cursor)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read scenario cursor: %w", err)
	}
	position, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil || position < 0 {
		return fmt.Errorf(
			"scenario cursor %s holds %q, not a step count",
			running.cursor,
			strings.TrimSpace(string(content)),
		)
	}
	running.position = position
	return nil
}

// next pops the next step, or a default turn once the list is exhausted.
func (running *script) next() Step {
	if running.position >= len(running.Steps) {
		return Step{Type: StepTurn}
	}
	step := running.Steps[running.position]
	running.advance(1)
	return step
}

// advance moves the cursor and persists it when the script has a file.
func (running *script) advance(count int) {
	running.position += count
	if running.cursor == "" {
		return
	}
	if err := atomicfile.Write(running.cursor, []byte(strconv.Itoa(running.position)), 0o600); err != nil {
		// The next process would replay this step; the error is loud on the
		// next read rather than swallowed here.
		if writeErr := os.WriteFile(running.cursor, []byte("unwritable: "+err.Error()), 0o600); writeErr != nil {
			fmt.Fprintf(
				os.Stderr,
				"mock-engine: cursor %s unwritable (%v) and the fallback write also failed: %v\n",
				running.cursor, err, writeErr,
			)
		}
	}
}

// turnReply resolves a turn step's reply, busy time and usage against the
// scenario defaults.
func (running *script) turnReply(step Step) (reply string, busyMS int, usage Tokens) {
	reply, busyMS, usage = running.Reply, *running.BusyMS, running.Tokens
	if step.Reply != "" {
		reply = step.Reply
	}
	if step.BusyMS != 0 {
		busyMS = step.BusyMS
	}
	if step.Tokens != nil {
		usage = *step.Tokens
	}
	return reply, busyMS, usage
}
