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
		if screen.placeholder != "" {
			loop.refreshStatus(loop.usage)
		}
		loop.paint()
		return 0, false
	case "/stats":
		if stats, ok := loop.engine.(interface{ stats() error }); ok {
			if err := stats.stats(); err != nil {
				warn(proc.stderr, "stats: %v", err)
				return ExitUnpinned, true
			}
			screen.say("Statistics updated")
			loop.paint()
			return 0, false
		}
		warn(proc.stderr, "unscripted /stats for %s", proc.engine)
		return ExitUnpinned, true
	case "/clear":
		if err := loop.engine.clear(); err != nil {
			warn(proc.stderr, "clear: %v", err)
		}
		screen.history = nil
		loop.paint()
		return 0, false
	}
	if proc.engine == engineCodex && strings.HasPrefix(word, "/") {
		screen.say(fmt.Sprintf("Unrecognized command '%s'. Type \"/\" for a list of supported commands.", word))
		screen.draft.draft = []rune(line)
		loop.paint()
		return 0, false
	}
	blocked, err := loop.engine.prompt(line)
	if err != nil {
		warn(proc.stderr, "prompt hooks: %v", err)
		return ExitUnpinned, true
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
			if len(step.RateLimits) > 0 {
				proc.script.RateLimits = step.RateLimits
			}
			reply, busyMS, usage := running.turnReply(step)
			if proceed, _ := loop.busy(started, time.Duration(busyMS)*time.Millisecond, ""); !proceed {
				return loop.interrupted()
			}
			if step.Error != "" {
				failed, ok := loop.engine.(interface{ turnError(Step) error })
				if !ok {
					warn(proc.stderr, "error turn unsupported")
					return ExitUnpinned, true
				}
				if err := failed.turnError(step); err != nil {
					warn(proc.stderr, "error turn: %v", err)
					return ExitUnpinned, true
				}
				screen.footer = ""
				screen.say("API Error: " + step.Error)
				loop.paint()
				return 0, false
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
			if pending, ok := loop.engine.(interface{ afterStop(*paneLoop) error }); ok {
				if err := pending.afterStop(loop); err != nil {
					warn(proc.stderr, "background Agent: %v", err)
					return ExitUnpinned, true
				}
			}
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
		case StepPermission, StepQuestion:
			proceed, err := loop.attention(step)
			if err != nil {
				warn(proc.stderr, "attention: %v", err)
				return ExitUnpinned, true
			}
			if !proceed {
				return loop.interrupted()
			}
		case StepNotification:
			notifier, ok := loop.engine.(interface{ notification(Step) error })
			if !ok {
				warn(proc.stderr, "notification unsupported")
				return ExitUnpinned, true
			}
			if err := notifier.notification(step); err != nil {
				warn(proc.stderr, "notification: %v", err)
				return ExitUnpinned, true
			}
		case StepHook:
			emitter, ok := loop.engine.(interface{ hook(Step) error })
			if !ok {
				warn(proc.stderr, "raw hook unsupported")
				return ExitUnpinned, true
			}
			if err := emitter.hook(step); err != nil {
				warn(proc.stderr, "hook: %v", err)
				return ExitUnpinned, true
			}
		case StepToolCall:
			denied, err := loop.engine.tool(step)
			if err != nil {
				warn(proc.stderr, "tool %s: %v", step.Tool, err)
				return ExitUnpinned, true
			}
			if denied != "" {
				screen.say("⏺ " + step.Tool + " — denied by hook: " + denied)
			} else {
				screen.say("⏺ " + step.Tool + "(…)")
			}
			loop.paint()
		case StepNativeTitle:
			title, ok := loop.engine.(interface{ nativeTitle(Step) error })
			if !ok {
				warn(proc.stderr, "native_title is OpenCode-only")
				return ExitUnpinned, true
			}
			if err := title.nativeTitle(step); err != nil {
				warn(proc.stderr, "native_title: %v", err)
				return ExitUnpinned, true
			}
		case StepBackgroundHandoff:
			handoff, ok := loop.engine.(interface{ backgroundHandoff(Step) error })
			if !ok {
				warn(proc.stderr, "background_handoff is Claude-only")
				return ExitUnpinned, true
			}
			if err := handoff.backgroundHandoff(step); err != nil {
				warn(proc.stderr, "background_handoff: %v", err)
				return ExitUnpinned, true
			}
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
			if step.Reply != "" {
				fmt.Fprintln(proc.stderr, step.Reply)
			}
			_, _ = loop.busy(started, 150*time.Millisecond, "")
			return step.ExitCode, true
		case StepExit:
			return loop.finish("other", step.ExitCode), true
		}
	}
}

func (loop *paneLoop) interrupted() (int, bool) {
	if engine, ok := loop.engine.(interface{ interrupt() error }); ok {
		if err := engine.interrupt(); err != nil {
			warn(loop.proc.stderr, "interrupt: %v", err)
			return ExitUnpinned, true
		}
	}
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
		loop.paint()
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
		case <-ticker.C:
		}
	}
}

// exit is the /exit choreography: SessionEnd fires, and with background work
// live a confirmation menu opens whose Enter ends the process.

// claudeBusyLine is the working footer in the words internal/call/guards.go:13
// matches: "esc to interrupt", "(Ns ·" and "N tokens" are each a pin.
func claudeBusyLine(elapsed time.Duration, usage Tokens) string {
	seconds := int(elapsed.Seconds())
	return "✻ Thinking… (" + strconv.Itoa(seconds) + "s · ↑ " + strconv.FormatInt(usage.Output+usage.Input, 10) +
		" tokens · esc to interrupt)"
}
