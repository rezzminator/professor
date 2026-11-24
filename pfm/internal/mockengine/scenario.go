package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Environment the mock reads. MOCK_ENGINE_* is the only namespace it owns:
// PFM_* belongs to internal/paths (arch-check C16) and CHAT_*/CC_* are the
// foreign prefixes C19 forbids.
const (
	// EnvScenario names the JSON scenario file. Unset means the defaults: every
	// prompt is answered with one word at once.
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
	DefaultBusyMS       = 0
	DefaultClaudeModel  = "claude-sonnet-4-5"
	DefaultCodexModel   = "codex-fixture-model"
	DefaultOpenCodeMode = "opencode-fixture-model"
	// DefaultClaudeVersion is the --version line pfm's own fakes pin
	// (internal/doctor/harness_prompt_capture_test.go writes the same string).
	DefaultClaudeVersion   = "2.1.270 (Claude Code)"
	DefaultCodexVersion    = "codex-cli 0.154.0"
	DefaultOpenCodeVersion = "1.14.30"
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

// Scenario is the whole script one mock process follows.
type Scenario struct {
	// Version is the --version line.
	Version string `json:"version,omitempty"`
	// SessionID is the Claude session id, Codex thread id or OpenCode session
	// id. Empty means a fresh UUID; --session-id/--resume/resume override it.
	SessionID string `json:"session_id,omitempty"`
	Model     string `json:"model,omitempty"`
	// Reply and BusyMS are the defaults a step or an exhausted list falls back to.
	Reply  string `json:"reply,omitempty"`
	BusyMS *int   `json:"busy_ms,omitempty"`
	Tokens Tokens `json:"tokens"`
	Steps  []Step `json:"steps,omitempty"`
	// TrustDialog holds a Claude pane at Claude Code's folder-trust dialog
	// before its composer, as a real one boots in a folder its account has not
	// trusted: 1 trusts the folder and boots on; Enter, Escape or 2 — "No,
	// exit" is the default row — ends the engine.
	TrustDialog bool `json:"trust_dialog,omitempty"`
	// LaunchCrash ends a Claude pane at launch: the line printed, exit 1,
	// before any hook, record or prompt is read.
	LaunchCrash  string     `json:"launch_crash,omitempty"`
	Pane         PaneShapes `json:"pane"`
	Jail         Jail       `json:"jail"`
	Quiet        bool       `json:"quiet,omitempty"`
	NoTranscript bool       `json:"no_transcript,omitempty"`
	// RateLimits is the account/rateLimits/read result supplied by the scenario.
	RateLimits json.RawMessage `json:"rate_limits,omitempty"`
	// RecordDir receives the mock's own telemetry — argv, prompts, hook
	// answers, MCP tool lists — for a test to read. It is evidence of what the
	// mock saw, never the judge of what pfm read.
	RecordDir string `json:"record_dir,omitempty"`
	// Control names the FIFO a test moves the mock's clock through (control.go, internal/mockengine/control);
	// control.PIDToken in it stands for the reading process's id. Every timed act waits on that clock, which
	// only the test moves: a scenario declaring one (timedAct) and naming no control is refused at load.
	Control string `json:"control,omitempty"`
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
	if act := scenario.timedAct(); act != "" && scenario.Control == "" {
		return Scenario{}, fmt.Errorf("scenario %s %s: %w", path, act, errUncontrolled)
	}
	var fields map[string]json.RawMessage
	if err := json.NewDecoder(strings.NewReader(string(content))).Decode(&fields); err != nil {
		return Scenario{}, fmt.Errorf("decode scenario fields %s: %w", path, err)
	}
	if raw, present := fields["rate_limits"]; present && !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		return Scenario{}, fmt.Errorf("scenario %s field rate_limits must be an object", path)
	}
	for _, door := range []struct {
		name    string
		enabled bool
	}{
		{"quiet", scenario.Quiet}, {"no_transcript", scenario.NoTranscript},
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
	return scenario, nil
}

// errUncontrolled refuses a timed act in a scenario that names no control FIFO: nothing could ever end it.
var errUncontrolled = errors.New("is a timed act, and the scenario names no control FIFO to move its clock")

// timedAct names the first act of the scenario that waits on the mock's clock: a busy stretch, a hold, a
// gated agent, a crash, a compact or a launch crash; "" when it has none.
func (scenario Scenario) timedAct() string {
	if scenario.BusyMS != nil && *scenario.BusyMS > 0 {
		return fmt.Sprintf("busy_ms %d", *scenario.BusyMS)
	}
	if scenario.LaunchCrash != "" {
		return "launch_crash"
	}
	return timedStep(scenario.Steps)
}

// timedStep names the first step that waits on the mock's clock, "step {index} ({type})"; "" when none does.
func timedStep(steps []Step) string {
	for index := range steps {
		if steps[index].timed() {
			return fmt.Sprintf("step %d (%s)", index, steps[index].Type)
		}
	}
	return ""
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
