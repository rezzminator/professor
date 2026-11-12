package mockengine

import (
	"errors"
	"fmt"
	"io/fs"
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
	// statusRefresh is how often the engine re-runs its statusline besides its
	// own events (Claude's statusLine.refreshInterval); 0 never.
	statusRefresh() time.Duration
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
	if every := engine.statusRefresh(); every > 0 {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		loop.statusDue = ticker.C
	}
	if codex, ok := engine.(interface{ composerPlaceholder() string }); ok {
		loop.screen.placeholder = codex.composerPlaceholder()
	}
	if proc.engine == engineClaude {
		loop.screen.rule = claudeComposerRule
	}
	if proc.engine == engineCodex {
		// Codex repaints in an alternate screen so old busy footers cannot
		// remain in tmux scrollback after the caller's turn ends.
		if _, err := proc.stdout.Write([]byte("\x1b[?1049h")); err != nil {
			warn(proc.stderr, "enter Codex alternate screen: %v", err)
			return ExitUsage
		}
		defer func() {
			if _, err := proc.stdout.Write([]byte("\x1b[?1049l")); err != nil {
				warn(proc.stderr, "leave Codex alternate screen: %v", err)
			}
		}()
	}
	loop.keys = readKeys(proc.ctx, proc.stdin)
	if proc.engine == engineClaude && proc.script.TrustDialog {
		if code, exited := loop.trustDialog(); exited {
			return code
		}
	}
	loop.refreshStatus(proc.script.Tokens)
	loop.paint()
	if launchPrompt != "" {
		if code, exited := loop.submit(launchPrompt); exited {
			return code
		}
	}
	return loop.run()
}

// claudeTrustFrame is Claude Code's folder-trust dialog for directory; its
// confirm and exit rows are what internal/engine/trust_dialog.go reads.
func claudeTrustFrame(directory string) string {
	return "\x1b[2J\x1b[H" + strings.Join([]string{
		"Accessing workspace:",
		"",
		" " + directory,
		"",
		" Quick safety check: Is this a project you created or one you trust?",
		"",
		" ❯ 1. Yes, I trust this folder",
		"   2. No, exit",
		"",
		" Enter to confirm · Esc to cancel",
	}, "\r\n") + "\r\n"
}

// trustDialogDeclined is the exit code of a pane whose folder-trust dialog
// was answered "No, exit".
const trustDialogDeclined = 1

// trustDialog holds the pane at Claude Code's folder-trust dialog until a key
// answers it. It reports exited when the engine ends there: declined, or its
// input or context gone.
func (loop *paneLoop) trustDialog() (int, bool) {
	if _, err := loop.proc.stdout.Write([]byte(claudeTrustFrame(loop.proc.cwd))); err != nil {
		warn(loop.proc.stderr, "paint the folder-trust dialog: %v", err)
		return ExitUsage, true
	}
	for {
		select {
		case <-loop.proc.ctx.Done():
			return 0, true
		case event, ok := <-loop.keys:
			if !ok {
				return 0, true
			}
			switch {
			case event.kind == keyRune && event.r == '1':
				return 0, false
			case event.kind == keyEnter, event.kind == keyEscape,
				event.kind == keyRune && event.r == '2':
				return trustDialogDeclined, true
			}
		}
	}
}

// paneLoop owns the key loop and the turn machine over one pane.
type paneLoop struct {
	proc   *process
	engine paneEngine
	screen *pane
	keys   <-chan keyEvent
	// statusDue ticks every paneEngine.statusRefresh; nil never.
	statusDue <-chan time.Time
	usage     Tokens
	exiting   bool
	exitCode  int
	// painted is the frame on the screen now.
	painted string
}

// paint draws the screen when it changed: a frame equal to the one showing is
// not written again, so a busy turn's 50 ms tick clears and redraws the pane
// once a second, with its counter, and a capture between two ticks reads a
// whole frame.
func (loop *paneLoop) paint() {
	frame := loop.screen.render()
	if frame == loop.painted {
		return
	}
	if _, err := loop.proc.stdout.Write([]byte(frame)); err != nil {
		warn(loop.proc.stderr, "paint: %v", err)
		return
	}
	loop.painted = frame
}

// paintTyped draws after a typed key once no further key waits: like the
// engines' own TUIs, a burst of typed text is drawn once, never a whole frame
// per rune, so the Enter that ends the burst is read as soon as it arrives.
func (loop *paneLoop) paintTyped() {
	if len(loop.keys) == 0 {
		loop.paint()
	}
}

// refreshStatus re-runs the statusline and returns the line now shown; a run
// that fails is named and keeps the line it had.
func (loop *paneLoop) refreshStatus(usage Tokens) string {
	line, err := loop.engine.statusLine(usage)
	if err != nil {
		warn(loop.proc.stderr, "%v", err)
		return loop.screen.status
	}
	loop.screen.status = line
	return line
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
		case <-loop.agentsDue():
			if code, exited := loop.agentsEnded(); exited {
				return code
			}
		case <-loop.statusDue:
			// Repaint only a line that changed: an idle pane stays still.
			if shown := loop.screen.status; loop.refreshStatus(loop.usage) != shown {
				loop.paint()
			}
		}
	}
}

func (loop *paneLoop) idleKey(event keyEvent) (int, bool) {
	screen := loop.screen
	if screen.renameDraft != nil {
		loop.renameKey(event)
		loop.paint()
		return 0, false
	}
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
		loop.paintTyped()
		return 0, false
	}
	line := screen.draft.take()
	loop.paint()
	if line == "" {
		return 0, false
	}
	return loop.submit(line)
}

func (loop *paneLoop) renameKey(event keyEvent) {
	screen := loop.screen
	switch event.kind {
	case keyEscape:
		screen.renameDraft, screen.renameError = nil, ""
	case keyEnter:
		name := strings.TrimSpace(screen.renameDraft.text())
		if name == "" {
			screen.renameError = codexRenameEmpty
			return
		}
		if err := loop.engine.rename(name); err != nil {
			warn(loop.proc.stderr, "rename: %v", err)
			screen.renameError = "Rename failed: " + err.Error()
			return
		}
		screen.renameDraft, screen.renameError = nil, ""
		loop.refreshStatus(loop.usage)
	default:
		screen.renameDraft.apply(event)
		screen.renameError = ""
	}
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
// work (pfmcall.ExitDialogOpen matches its ❯ row; reload and the kill-exit
// finisher press Enter on it).
const exitMenuName = "exit-dialog"

// exitByPrompt is the SessionEnd reason of an engine its user ended with /exit.
const exitByPrompt = "prompt_input_exit"

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
		if codex, ok := loop.engine.(interface{ compactBusyDuration(Step) time.Duration }); ok {
			proceed, err := loop.busy(time.Now(), codex.compactBusyDuration(step), "")
			if err != nil {
				warn(proc.stderr, "compact busy footer: %v", err)
			}
			if !proceed || err != nil {
				screen.footer = ""
				loop.paint()
				return 0, false
			}
		}
		if err := loop.engine.compact(step); err != nil {
			warn(proc.stderr, "compact: %v", err)
			screen.footer = ""
			loop.paint()
			return 0, false
		}
		screen.footer = ""
		screen.say(loop.engine.compactedLine())
		loop.paint()
		return 0, false
	case codexRenameCommand:
		if screen.placeholder != "" && strings.TrimSpace(rest) == "" {
			screen.renameDraft = &composer{}
			loop.paint()
			return 0, false
		}
		if err := loop.engine.rename(strings.TrimSpace(rest)); err != nil {
			warn(proc.stderr, "rename: %v", err)
		}
		// Codex repaints at once, Claude Code within its refreshInterval: either line carries the new name.
		loop.refreshStatus(loop.usage)
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
	// The engines draw a submitted prompt at once and run its hooks behind it; a hook that blocks the
	// prompt erases it. pfm's delivery proof reads the pane half a second after Enter.
	screen.say("> " + line)
	loop.paint()
	blocked, err := loop.engine.prompt(line)
	if err != nil {
		warn(proc.stderr, "prompt hooks: %v", err)
		return ExitUnpinned, true
	}
	if blocked != "" {
		screen.history[len(screen.history)-1] = "⎿ Blocked by hook: " + blocked
		loop.paint()
		return 0, false
	}
	if err := loop.engine.recordUser(line); err != nil {
		warn(proc.stderr, "record prompt: %v", err)
	}
	code, exited := loop.turn(proc.script.forPrompt(line))
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
func (loop *paneLoop) turn(running *script) (int, bool) {
	proc, screen := loop.proc, loop.screen
	started := time.Now()
	for {
		step := running.next()
		switch step.Type {
		case StepTurn:
			reply, busyMS, usage := running.turnReply(step)
			if proceed, _ := loop.busy(started, time.Duration(busyMS)*time.Millisecond, ""); !proceed {
				return loop.interrupted()
			}
			loop.usage = usage
			if err := loop.engine.recordAssistant(reply, usage); err != nil {
				warn(proc.stderr, "record reply: %v", err)
			}
			if stopper, ok := loop.engine.(interface{ stop() error }); ok {
				if err := stopper.stop(); err != nil {
					warn(proc.stderr, "Stop: %v", err)
				}
			}
			screen.footer = ""
			screen.say("⏺ " + reply)
			loop.refreshStatus(usage)
			loop.paint()
			return 0, false
		case StepHold:
			proceed, err := loop.busy(started, 0, step.UntilGone)
			if err != nil {
				warn(proc.stderr, "hold: %v", err)
				return ExitUnpinned, true
			}
			if !proceed {
				return loop.interrupted()
			}
		case StepToolCall:
			denied, err := loop.engine.tool(step)
			if err != nil {
				warn(proc.stderr, "tool %s: %v", step.Tool, err)
				return ExitUnpinned, true
			}
			for _, row := range toolRows(step.Tool, denied) {
				screen.say(row)
			}
			loop.paint()
		case StepBackgroundAgent:
			row, err := loop.engine.background(step)
			if err != nil {
				warn(proc.stderr, "background agent: %v", err)
			}
			if row != "" {
				agent := agentRow{line: row, name: step.Name, gate: step.UntilGone, busyMS: step.BusyMS}
				screen.agents.rows = append(screen.agents.rows, agent)
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
			_, _ = loop.busy(started, 150*time.Millisecond, "")
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
// file, until it disappears), pumping keys meanwhile. It returns (false, nil)
// when Escape interrupted the turn or the context ended, and a non-nil error
// when the gate file could not be statted for a reason other than its
// confirmed absence — a permission error or a vanished parent directory must
// never be read as "the gate is gone, proceed" the same way removal is.
func (loop *paneLoop) busy(started time.Time, duration time.Duration, gate string) (bool, error) {
	screen := loop.screen
	deadline := time.Now().Add(duration)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		screen.footer = loop.engine.busyLine(time.Since(started), loop.proc.script.Tokens)
		loop.paintTyped()
		done := false
		if gate != "" {
			_, err := os.Stat(gate)
			switch {
			case err == nil:
				done = false
			case errors.Is(err, fs.ErrNotExist):
				done = true
			default:
				return false, fmt.Errorf("stat hold gate %s: %w", gate, err)
			}
		} else {
			done = !time.Now().Before(deadline)
		}
		if done {
			return true, nil
		}
		select {
		case <-loop.proc.ctx.Done():
			return false, nil
		case event, ok := <-loop.keys:
			if !ok {
				return false, nil
			}
			switch event.kind {
			case keyEscape:
				return false, nil
			case keyEnter:
				if line := screen.draft.take(); line != "" {
					screen.queued = append(screen.queued, line)
				}
			default:
				screen.draft.apply(event)
			}
		case <-loop.statusDue:
			loop.refreshStatus(loop.usage)
		case <-ticker.C:
		}
	}
}

// exit is the /exit choreography: SessionEnd fires, and with background work
// live a confirmation menu opens whose Enter ends the process.
func (loop *paneLoop) exit() (int, bool) {
	screen := loop.screen
	if len(screen.agents.rows) > 0 {
		screen.menu = &Step{
			Type: StepMenu, Name: exitMenuName, Options: []string{"Exit and stop tasks", "Stay"}, Selected: 1,
		}
		screen.selected = 1
		loop.paint()
		return loop.awaitExitMenu()
	}
	return loop.finish(exitByPrompt, 0), true
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
				return loop.finish(exitByPrompt, loop.exitCode), true
			}
		}
	}
	return 0, false
}

func (loop *paneLoop) finish(reason string, code int) int {
	if reason == exitByPrompt {
		loop.screen.left = true
		loop.paint()
	}
	if err := loop.engine.finish(reason); err != nil {
		warn(loop.proc.stderr, "finish: %v", err)
	}
	return code
}

// claudeBusyLine is the working footer in the words internal/call/guards.go:13
// matches: "esc to interrupt", "(Ns ·" and "N tokens" are each a pin.
func claudeBusyLine(elapsed time.Duration, usage Tokens) string {
	seconds := int(elapsed.Seconds())
	return "✻ Thinking… (" + strconv.Itoa(seconds) + "s · ↑ " + strconv.FormatInt(usage.Output+usage.Input, 10) +
		" tokens · esc to interrupt)"
}
