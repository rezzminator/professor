// Package spawn starts one detached, named fleet chat and reports what
// actually happened to it.
package spawn

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// SessionSpec is one detached tmux session to create.
type SessionSpec struct {
	Socket  string
	Session string
	Window  string
	CWD     string
	Run     string
	// Binary is the executable word Run launches, stated so NewSession can
	// prove it is reachable BEFORE a server is created around it. A pane
	// whose command cannot resolve dies at launch and takes the fresh server
	// with it, and the visible failure names tmux instead of the engine.
	// Empty skips the preflight: not every Run names a single binary.
	Binary string
	Width  int
	Height int
}

// Tmux is the whole effect surface of a spawn: create the session, read the
// pane, type into it.
type Tmux interface {
	NewSession(ctx context.Context, spec SessionSpec) error
	Capture(ctx context.Context, socket, target string) (string, error)
	SendLiteral(ctx context.Context, socket, target, text string) error
	SendKey(ctx context.Context, socket, target, key string) error
}

// Request is one headless chat to start. Run comes from action.HeadlessRun,
// which owns the launch ceremony; this package owns only the session and the
// keystrokes.
type Request struct {
	Engine pfmengine.ID
	Name   string
	Socket string
	CWD    string
	Run    string
	// Binary mirrors SessionSpec.Binary: the executable word Run launches,
	// preflighted before the server exists. Empty skips the preflight.
	Binary string
	Prompt string
	// PromptOnCommandLine means the launch command already carries Prompt;
	// the spawner records delivery without typing into the TUI.
	PromptOnCommandLine bool
	Width               int
	Height              int
	Timings             Timings
	// Trace, when set, receives one line per step of the choreography. A TUI
	// this code cannot see is the whole difficulty of driving one, so the
	// screen it decided on is printed with the decision.
	Trace io.Writer
}

// tracer prints the choreography's own view of the screen.
type tracer struct {
	writer io.Writer
	start  time.Time
}

// Trace is the launcher's choreography trace value.
type Trace = tracer

func newTracer(writer io.Writer, now time.Time) tracer {
	return tracer{writer: writer, start: now}
}

func (trace tracer) step(format string, arguments ...any) {
	if trace.writer == nil {
		return
	}
	fmt.Fprintf(
		trace.writer,
		"spawn %6dms  %s\n",
		time.Since(trace.start).Milliseconds(),
		fmt.Sprintf(format, arguments...),
	)
}

// screen renders a capture as one greppable line: blank lines dropped, the
// last few rows kept, truncated. Enough to tell a composer from a modal.
func screen(capture string) string {
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(capture, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	joined := strings.Join(lines, " ⏎ ")
	if len(joined) > 160 {
		joined = joined[:160] + "…"
	}
	return joined
}

// Timings bound every wait. Tests set them to microseconds; the CLI leaves
// them zero and takes Defaults.
type Timings struct {
	Poll  time.Duration
	Boot  time.Duration
	Step  time.Duration
	Typed time.Duration
}

// Defaults are the live timings. Boot is generous because a cold Codex or
// Claude TUI on a loaded box takes several seconds before it draws anything,
// and a spawn that gives up early would report a healthy chat as dead.
func Defaults() Timings {
	return Timings{
		Poll:  250 * time.Millisecond,
		Boot:  30 * time.Second,
		Step:  15 * time.Second,
		Typed: 250 * time.Millisecond,
	}
}

func (timings Timings) orDefaults() Timings {
	defaults := Defaults()
	if timings.Poll <= 0 {
		timings.Poll = defaults.Poll
	}
	if timings.Boot <= 0 {
		timings.Boot = defaults.Boot
	}
	if timings.Step <= 0 {
		timings.Step = defaults.Step
	}
	// <= 0, not < 0: a caller that leaves Timings zero must get the default
	// gap. With a zero gap the Enter rides in the same input burst as the text
	// and Codex's rename modal DROPS it — the chat then runs unnamed while
	// every keystroke looks delivered.
	if timings.Typed <= 0 {
		timings.Typed = defaults.Typed
	}
	return timings
}

// Result is what the caller reports to the user: where the chat lives, whether
// its name landed, and every step that did not go to plan.
type Result struct {
	Socket   string
	Session  string
	Window   string
	Name     string
	Named    bool
	Prompted bool
	Warnings []string
}
