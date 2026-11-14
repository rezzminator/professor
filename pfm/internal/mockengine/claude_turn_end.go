package mockengine

import (
	"encoding/json"
	"fmt"
	"time"
)

// claudeInterruptText is the user record Claude Code writes when Escape cuts a
// turn short; it fires no hook for it, so the record is the only sign pfm's
// transcript watch reads (internal/transcript ClaudeInterruptMarker).
const claudeInterruptText = "[Request interrupted by user]"

// claudeSyntheticModel is the model Claude Code names on the reply it writes
// itself for a turn the model server refused.
const claudeSyntheticModel = "<synthetic>"

// Defaults of an api_error step that leaves its text and kind empty.
const (
	defaultAPIErrorText = `API Error: 500 {"type":"error","error":{"type":"api_error","message":"Internal server error"}}`
	defaultAPIErrorKind = "server_error"
)

// interrupt writes the record an Escape mid-turn leaves in the transcript.
func (session *claudeSession) interrupt() error {
	record := session.userRecord("", false)
	record.PromptSource = ""
	record.Message.Content = []contentPart{{Type: blockText, Text: claudeInterruptText}}
	return session.write(record)
}

// apiError writes Claude Code's synthetic reply to a refused turn and returns
// the text its pane shows. Claude fires no Stop hook for it.
func (session *claudeSession) apiError(step Step) (string, error) {
	text, kind := step.Reply, step.Error
	if text == "" {
		text = defaultAPIErrorText
	}
	if kind == "" {
		kind = defaultAPIErrorKind
	}
	record := session.assistantRecord([]contentPart{{Type: blockText, Text: text}}, Tokens{}, false)
	record.Message.Model = claudeSyntheticModel
	record.IsAPIErrorMessage, record.Error = true, kind
	return text, session.write(record)
}

// aiTitle writes Claude Code's automatic title record.
func (session *claudeSession) aiTitle(title string) error {
	return session.write(claudeRecord{Type: claudeAITitle, AITitle: title, SessionID: session.sessionID})
}

// claudeAITitle is the record type of Claude Code's automatic title.
const claudeAITitle = "ai-title"

// failTurn plays an api_error step on the pane: busy for the step's time, the
// synthetic reply recorded and shown, the composer back. An engine without
// the beat refuses it by name.
func (loop *paneLoop) failTurn(running *script, step Step, started time.Time) (int, bool) {
	engine, plays := loop.engine.(interface{ apiError(Step) (string, error) })
	if !plays {
		warn(loop.proc.stderr, "%s plays no %s step", loop.proc.engine, StepAPIError)
		return ExitUnpinned, true
	}
	busyMS := *running.BusyMS
	if step.BusyMS != 0 {
		busyMS = step.BusyMS
	}
	if proceed, _ := loop.busy(started, time.Duration(busyMS)*time.Millisecond, ""); !proceed {
		return loop.interrupted()
	}
	text, err := engine.apiError(step)
	if err != nil {
		warn(loop.proc.stderr, "record API error: %v", err)
	}
	loop.screen.footer = ""
	loop.screen.say("⎿ " + text)
	loop.paint()
	return 0, false
}

// titleTurn plays an ai_title step; an engine without the record refuses it by name.
func (loop *paneLoop) titleTurn(step Step) (int, bool) {
	engine, plays := loop.engine.(interface{ aiTitle(string) error })
	if !plays {
		warn(loop.proc.stderr, "%s plays no %s step", loop.proc.engine, StepAITitle)
		return ExitUnpinned, true
	}
	if err := engine.aiTitle(step.Name); err != nil {
		warn(loop.proc.stderr, "record AI title: %v", err)
	}
	return 0, false
}

// headlessAPIError is `claude -p` on a refused turn: the prompt and the
// synthetic reply recorded, the error text printed (in the envelope with
// is_error under --output-format json), exit 1.
func (session *claudeSession) headlessAPIError(prompt string, step Step) int {
	proc := session.proc
	text := step.Reply
	if text == "" {
		text = defaultAPIErrorText
	}
	if !session.call.noSessionPersistence {
		if err := session.recordUser(prompt); err != nil {
			warn(proc.stderr, "%v", err)
			return ExitUsage
		}
		if _, err := session.apiError(step); err != nil {
			warn(proc.stderr, "%v", err)
			return ExitUsage
		}
	}
	if session.call.outputFormat != "json" {
		fmt.Fprintln(proc.stdout, text)
		return 1
	}
	encoded, err := json.Marshal(headlessEnvelope{
		Type: "result", Subtype: "success", IsError: true, NumTurns: 1, Result: text, SessionID: session.sessionID,
	})
	if err != nil {
		warn(proc.stderr, "encode envelope: %v", err)
		return ExitUsage
	}
	fmt.Fprintln(proc.stdout, string(encoded))
	return 1
}
