package mockengine

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// paneEngine is what one engine's TUI contributes to the shared pane loop:
// its glyphs and footer spellings, and the files it writes for every beat.
type paneEngine interface {
	// composerGlyph is the prompt marker (❯ for Claude, › for Codex).
	composerGlyph() string
	// busyLine renders the working footer for an elapsed turn; "" is unpinned.
	busyLine(elapsed time.Duration, usage Tokens) string
	// compactedLine is the receipt a finished compaction leaves on the pane.
	compactedLine() string
	// start fires SessionStart and lays down the session's first records.
	start(resumed bool) error
	// prompt fires UserPromptSubmit; a non-empty reason blocks the prompt.
	prompt(text string) (blocked string, err error)
	recordUser(text string) error
	recordAssistant(reply string, usage Tokens) error
	// tool fires PreToolUse and records the call; a non-empty reason means it
	// was denied.
	tool(step Step) (denied string, err error)
	compact(step Step) error
	rename(name string) error
	background(step Step) (row string, err error)
	mcp(step Step) error
	// clear ends the session with reason "clear" and starts a fresh one.
	clear() error
	// finish fires SessionEnd with the given reason and releases the seat.
	finish(reason string) error
	// statusLine refreshes the line shown under the composer.
	statusLine(usage Tokens) (string, error)
}

// pane is the screen: the last lines of conversation, an optional busy
// footer or open menu, the background-agent rows, the composer and the
// statusline. Every change repaints the whole thing, so a capture sees only
// what is true now.
type pane struct {
	history   []string
	footer    string
	menu      *Step
	selected  int
	agentRows []string
	draft     composer
	queued    []string
	status    string
	glyph     string
}

const historyKeep = 12

func (screen *pane) say(line string) {
	screen.history = append(screen.history, line)
	if len(screen.history) > historyKeep {
		screen.history = screen.history[len(screen.history)-historyKeep:]
	}
}

func (screen *pane) render() string {
	var out strings.Builder
	out.WriteString("\x1b[2J\x1b[H")
	for _, line := range screen.history {
		out.WriteString(line)
		out.WriteString("\r\n")
	}
	if screen.footer != "" {
		out.WriteString("\r\n" + screen.footer + "\r\n")
	}
	if screen.menu != nil {
		out.WriteString("\r\n")
		for index, option := range screen.menu.Options {
			marker := "  "
			if index+1 == screen.selected {
				marker = screen.glyph + " "
			}
			fmt.Fprintf(&out, "%s%d. %s\r\n", marker, index+1, option)
		}
		out.WriteString("Enter to confirm · Esc to cancel\r\n")
	} else {
		out.WriteString("\r\n" + screen.glyph + " " + screen.draft.text() + "\r\n")
		if len(screen.queued) > 0 {
			out.WriteString("  Press up to edit queued messages\r\n")
		}
	}
	for _, row := range screen.agentRows {
		out.WriteString(row + "\r\n")
	}
	if screen.status != "" {
		out.WriteString(screen.status + "\r\n")
	}
	return out.String()
}

// runPane drives an engine's TUI until it exits and returns the exit code.
func runPane(proc *process, engine paneEngine, launchPrompt string, resumed bool) int {
	if engine.busyLine(0, Tokens{}) == "" || engine.compactedLine() == "" {
		warn(
			proc.stderr,
			"%s pane shapes are unpinned — supply pane.busy and pane.compacted in the scenario",
			proc.engine,
		)
		return ExitUnpinned
	}
	restore, err := rawTerminal(proc.stdin, proc.stderr)
	if err != nil {
		warn(proc.stderr, "%v", err)
		return ExitUsage
	}
	defer restore()
	if err := engine.start(resumed); err != nil {
		warn(proc.stderr, "start: %v", err)
		return ExitUsage
	}
	loop := &paneLoop{proc: proc, engine: engine, screen: &pane{glyph: engine.composerGlyph()}}
	loop.keys = readKeys(proc.ctx, proc.stdin)
	loop.refreshStatus(proc.script.Tokens)
	loop.paint()
	if launchPrompt != "" {
		if code, exited := loop.submit(launchPrompt); exited {
			return code
		}
	}
	return loop.run()
}

// paneLoop owns the key loop and the turn machine over one pane.
type paneLoop struct {
	proc     *process
	engine   paneEngine
	screen   *pane
	keys     <-chan keyEvent
	usage    Tokens
	exiting  bool
	exitCode int
}

func (loop *paneLoop) paint() {
	if _, err := loop.proc.stdout.Write([]byte(loop.screen.render())); err != nil {
		warn(loop.proc.stderr, "paint: %v", err)
	}
}

func (loop *paneLoop) refreshStatus(usage Tokens) {
	line, err := loop.engine.statusLine(usage)
	if err != nil {
		warn(loop.proc.stderr, "%v", err)
		return
	}
	loop.screen.status = line
}

// run is the idle loop: keys edit the composer, Enter submits, a menu takes
// digits and Enter, Escape cancels a menu. It returns the exit code once the
// engine ends (by /exit, a step, EOF or cancellation).
func (loop *paneLoop) run() int {
	for {
		select {
		case <-loop.proc.ctx.Done():
			return loop.finish("other", 0)
		case event, ok := <-loop.keys:
			if !ok {
				return loop.finish("other", 0)
			}
			if code, exited := loop.idleKey(event); exited {
				return code
			}
		}
	}
}

func (loop *paneLoop) idleKey(event keyEvent) (int, bool) {
	screen := loop.screen
	if screen.menu != nil {
		switch {
		case event.kind == keyEnter:
			loop.closeMenu(true)
		case event.kind == keyEscape:
			loop.closeMenu(false)
		case event.kind == keyRune && event.r >= '1' && event.r <= '9' && int(event.r-'0') <= len(screen.menu.Options):
			screen.selected = int(event.r - '0')
		}
		loop.paint()
		return 0, false
	}
	if event.kind != keyEnter {
		screen.draft.apply(event)
		loop.paint()
		return 0, false
	}
	line := screen.draft.take()
	loop.paint()
	if line == "" {
		return 0, false
	}
	return loop.submit(line)
}

// closeMenu answers the open menu. A menu opened by /exit with background
// work is the one whose confirmation ends the process.
func (loop *paneLoop) closeMenu(confirmed bool) {
	screen := loop.screen
	if confirmed && screen.menu.Name == exitMenuName && screen.selected == 1 {
		screen.menu = nil
		loop.exitCode, loop.exiting = 0, true
		return
	}
	screen.menu = nil
}

// exitMenuName tags the /exit confirmation Claude Code shows over background
// work (internal/reload/reload.go:535 presses Enter on its ❯ row).
const exitMenuName = "exit-dialog"

// submit handles one submitted line: a slash command or a prompt.
func (loop *paneLoop) submit(line string) (int, bool) {
	proc, screen := loop.proc, loop.screen
	if err := proc.recorder.write("prompts", line+"\n"); err != nil {
		warn(proc.stderr, "%v", err)
	}
	word, rest, _ := strings.Cut(line, " ")
	switch word {
	case "/exit":
		return loop.exit()
	case "/compact":
		step := Step{Type: StepCompact}
		if next := loop.peekStep(); next != nil && next.Type == StepCompact {
			step = *next
			proc.script.advance(1)
		}
		if err := loop.engine.compact(step); err != nil {
			warn(proc.stderr, "compact: %v", err)
		}
		screen.say(loop.engine.compactedLine())
		loop.paint()
		return 0, false
	case "/rename":
		if err := loop.engine.rename(strings.TrimSpace(rest)); err != nil {
			warn(proc.stderr, "rename: %v", err)
		}
		loop.paint()
		return 0, false
	case "/clear":
		if err := loop.engine.clear(); err != nil {
			warn(proc.stderr, "clear: %v", err)
		}
		screen.history = nil
		loop.paint()
		return 0, false
	}
	blocked, err := loop.engine.prompt(line)
	if err != nil {
		warn(proc.stderr, "prompt hooks: %v", err)
	}
	if blocked != "" {
		screen.say("⎿ Blocked by hook: " + blocked)
		loop.paint()
		return 0, false
	}
	screen.say("> " + line)
	if err := loop.engine.recordUser(line); err != nil {
		warn(proc.stderr, "record prompt: %v", err)
	}
	loop.paint()
	code, exited := loop.turn()
	if exited {
		return code, true
	}
	for len(screen.queued) > 0 {
		next := screen.queued[0]
		screen.queued = screen.queued[1:]
		if code, exited := loop.submit(next); exited {
			return code, true
		}
	}
	return 0, false
}

func (loop *paneLoop) peekStep() *Step {
	running := loop.proc.script
	if running.position >= len(running.Steps) {
		return nil
	}
	return &running.Steps[running.position]
}

// turn runs scenario steps until a terminal one. Keys typed while the turn
// is busy edit the composer; Enter queues the draft; Escape interrupts.
func (loop *paneLoop) turn() (int, bool) {
	proc, screen := loop.proc, loop.screen
	started := time.Now()
	for {
		step := proc.script.next()
		switch step.Type {
		case StepTurn:
			reply, busyMS, usage := proc.script.turnReply(step)
			if !loop.busy(started, time.Duration(busyMS)*time.Millisecond, "") {
				return loop.interrupted()
			}
			loop.usage = usage
			if err := loop.engine.recordAssistant(reply, usage); err != nil {
				warn(proc.stderr, "record reply: %v", err)
			}
			screen.footer = ""
			screen.say("⏺ " + reply)
			loop.refreshStatus(usage)
			loop.paint()
			return 0, false
		case StepHold:
			if !loop.busy(started, 0, step.UntilGone) {
				return loop.interrupted()
			}
		case StepToolCall:
			denied, err := loop.engine.tool(step)
			if err != nil {
				warn(proc.stderr, "tool %s: %v", step.Tool, err)
			}
			if denied != "" {
				screen.say("⏺ " + step.Tool + " — denied by hook: " + denied)
			} else {
				screen.say("⏺ " + step.Tool + "(…)")
			}
			loop.paint()
		case StepBackgroundAgent:
			row, err := loop.engine.background(step)
			if err != nil {
				warn(proc.stderr, "background agent: %v", err)
			}
			if row != "" {
				screen.agentRows = append(screen.agentRows, row)
			}
			loop.paint()
		case StepCompact:
			if err := loop.engine.compact(step); err != nil {
				warn(proc.stderr, "compact: %v", err)
			}
			screen.say(loop.engine.compactedLine())
			loop.paint()
		case StepMCP:
			if err := loop.engine.mcp(step); err != nil {
				warn(proc.stderr, "mcp: %v", err)
				return ExitUnpinned, true
			}
		case StepMenu:
			screen.footer = ""
			screen.menu = &step
			screen.selected = step.Selected
			if screen.selected == 0 {
				screen.selected = 1
			}
			loop.paint()
			return 0, false
		case StepCrash:
			loop.busy(started, 150*time.Millisecond, "")
			return step.ExitCode, true
		case StepExit:
			return loop.finish("other", step.ExitCode), true
		}
	}
}

func (loop *paneLoop) interrupted() (int, bool) {
	loop.screen.footer = ""
	loop.screen.say("⎿ Interrupted")
	loop.paint()
	return 0, false
}

// busy paints the working footer until the duration passes (or, with a gate
// file, until it disappears), pumping keys meanwhile. It returns false when
// Escape interrupted the turn or the context ended.
func (loop *paneLoop) busy(started time.Time, duration time.Duration, gate string) bool {
	screen := loop.screen
	deadline := time.Now().Add(duration)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		screen.footer = loop.engine.busyLine(time.Since(started), loop.proc.script.Tokens)
		loop.paint()
		done := false
		if gate != "" {
			_, err := os.Stat(gate)
			done = err != nil
		} else {
			done = !time.Now().Before(deadline)
		}
		if done {
			return true
		}
		select {
		case <-loop.proc.ctx.Done():
			return false
		case event, ok := <-loop.keys:
			if !ok {
				return false
			}
			switch event.kind {
			case keyEscape:
				return false
			case keyEnter:
				if line := screen.draft.take(); line != "" {
					screen.queued = append(screen.queued, line)
				}
			default:
				screen.draft.apply(event)
			}
		case <-ticker.C:
		}
	}
}

// exit is the /exit choreography: SessionEnd fires, and with background work
// live a confirmation menu opens whose Enter ends the process.
func (loop *paneLoop) exit() (int, bool) {
	screen := loop.screen
	if len(screen.agentRows) > 0 {
		screen.menu = &Step{
			Type: StepMenu, Name: exitMenuName, Options: []string{"Exit and stop tasks", "Stay"}, Selected: 1,
		}
		screen.selected = 1
		loop.paint()
		return loop.awaitExitMenu()
	}
	return loop.finish("prompt_input_exit", 0), true
}

// awaitExitMenu keeps the loop running until the exit menu is confirmed or
// cancelled; the ordinary idle loop cannot express "confirmed means quit".
func (loop *paneLoop) awaitExitMenu() (int, bool) {
	for loop.screen.menu != nil {
		select {
		case <-loop.proc.ctx.Done():
			return loop.finish("other", 0), true
		case event, ok := <-loop.keys:
			if !ok {
				return loop.finish("other", 0), true
			}
			loop.idleKey(event)
			if loop.exiting {
				return loop.finish("prompt_input_exit", loop.exitCode), true
			}
		}
	}
	return 0, false
}

func (loop *paneLoop) finish(reason string, code int) int {
	if err := loop.engine.finish(reason); err != nil {
		warn(loop.proc.stderr, "finish: %v", err)
	}
	return code
}

// claudeBusyLine is the working footer in the words internal/inject/guards.go:13
// matches: "esc to interrupt", "(Ns ·" and "N tokens" are each a pin.
func claudeBusyLine(elapsed time.Duration, usage Tokens) string {
	seconds := int(elapsed.Seconds())
	return "✻ Thinking… (" + strconv.Itoa(seconds) + "s · ↑ " + strconv.FormatInt(usage.Output+usage.Input, 10) +
		" tokens · esc to interrupt)"
}
