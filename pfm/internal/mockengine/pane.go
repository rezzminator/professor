package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
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
	history     []string
	footer      string
	menu        *Step
	selected    int
	agentRows   []string
	draft       composer
	queued      []string
	status      string
	glyph       string
	placeholder string
	rule        string
	renameDraft *composer
	renameError string
}

const historyKeep = 12

// claudeComposerRule is a horizontal rule of Claude Code's input box, drawn
// above and below its composer row; the kill-exit finisher reads the box down
// to the closing one (pfmcall.ComposerBoxEmpty). Codex draws no box.
const claudeComposerRule = "────────────────────────────────────────\r\n"

// These dialog strings are read by internal/spawn/spawn.go's Codex birth.
const (
	codexRenameCommand = "/rename"
	codexRenameOffer   = "rename the current thread"
	codexRenameTitle   = "Rename thread"
	codexRenameHint    = "Type a name and press Enter"
	codexRenameEmpty   = "Thread name cannot be empty."
	codexRenameFooter  = "Press enter to confirm or esc to go back"
)

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
	if screen.renameDraft != nil {
		out.WriteString("\r\n" + codexRenameTitle + "\r\n" + codexRenameHint + "\r\n")
		out.WriteString(screen.glyph + " " + screen.renameDraft.text() + "\r\n")
		if screen.renameError != "" {
			out.WriteString(screen.renameError + "\r\n")
		}
		out.WriteString(codexRenameFooter + "\r\n")
		return out.String()
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
		draft := screen.draft.text()
		text := draft
		if text == "" {
			if screen.placeholder != "" {
				text = "\x1b[2m" + screen.placeholder + "\x1b[0m"
			}
		}
		out.WriteString("\r\n" + screen.rule + screen.glyph + " " + text + "\r\n" + screen.rule)
		if screen.placeholder != "" && draft != "" && strings.HasPrefix(codexRenameCommand, draft) {
			out.WriteString(codexRenameOffer + "\r\n")
		}
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
	loop := &paneLoop{
		proc:   proc,
		engine: engine,
		usage:  proc.script.Tokens,
		screen: &pane{glyph: engine.composerGlyph()},
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
	if proc.engine == engineClaude && (proc.script.TrustDialog || proc.script.TrustDialogUnnumbered) {
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
	frame := claudeTrustFrame(loop.proc.cwd)
	if loop.proc.script.TrustDialogUnnumbered {
		frame = strings.ReplaceAll(strings.ReplaceAll(frame, "1. Yes", "Yes"), "2. No", "No")
	}
	if _, err := loop.proc.stdout.Write([]byte(frame)); err != nil {
		warn(loop.proc.stderr, "paint the folder-trust dialog: %v", err)
		return ExitUsage, true
	}
	if err := loop.proc.recorder.write("trust-frame.txt", frame); err != nil {
		warn(loop.proc.stderr, "record the folder-trust dialog: %v", err)
		return ExitUsage, true
	}
	if err := loop.proc.startupEvent("trust-frame-written"); err != nil {
		warn(loop.proc.stderr, "record trust ordering: %v", err)
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
	var refresh <-chan time.Time
	var timer *time.Timer
	remaining := 0
	interval := time.Duration(0)
	if settings := loop.proc.script.IdleStatuslineRefresh; loop.proc.engine == engineClaude && settings != nil {
		remaining, interval = settings.Count, time.Duration(settings.IntervalMS)*time.Millisecond
		timer = time.NewTimer(interval)
		defer timer.Stop()
		refresh = timer.C
	}
	for {
		select {
		case <-loop.proc.ctx.Done():
			return loop.finish("other", 0)
		case <-refresh:
			if path := loop.proc.script.IdleStatuslineRefresh.StopWhenExists; path != "" {
				_, err := os.Stat(path)
				if err == nil || !os.IsNotExist(err) {
					refresh, remaining = nil, 0
					if err != nil {
						warn(loop.proc.stderr, "idle statusline stop gate %s: %v", path, err)
					} else {
						receipt, _ := json.Marshal(struct {
							Event string `json:"event"`
							Path  string `json:"path"`
						}{Event: "stopped", Path: path})
						if err := loop.proc.recorder.write("statusline-idle.jsonl", string(receipt)+"\n"); err != nil {
							warn(loop.proc.stderr, "%v", err)
						}
					}
					continue
				}
			}
			loop.refreshStatus(loop.usage)
			loop.paint()
			remaining--
			if remaining == 0 {
				refresh = nil
			} else {
				timer.Reset(interval)
			}
		case event, ok := <-loop.keys:
			if !ok {
				return loop.finish("other", 0)
			}
			if timer != nil && remaining > 0 {
				timer.Stop()
			}
			if code, exited := loop.idleKey(event); exited {
				return code
			}
			if timer != nil && remaining > 0 {
				timer.Reset(interval)
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
