package mockengine

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

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

// next refuses work beyond the declared script.
func (running *script) next() Step {
	if running.position >= len(running.Steps) {
		return Step{
			Type:     StepCrash,
			ExitCode: ExitUnpinned,
			Reply:    "mock-engine: unscripted request: scenario exhausted",
		}
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
