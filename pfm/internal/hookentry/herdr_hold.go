package hookentry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// Claude fires PostToolUse for a parallel sibling tool (another call of the
// same batch, or a concurrent sub-agent's) while a permission dialog is open.
// Its async working report starts after the PermissionRequest's blocked one,
// so the seq order alone lets it replace blocked with working while the dialog
// still waits. A Claude tool request therefore stores the tool it asked about,
// and a tool's working report leaves that block only when it is that very tool:
// its own PostToolUse, or its PostToolUseFailure. Every event that is no tool's
// (a prompt, Stop, the idle prompt, the deny watch's idle, a new request)
// leaves it as before.

// keepsStored reports whether the stored state outlives this event unchanged:
// nothing is stored, sent or watched anew.
func keepsStored(event herdr.Event, payload herdrPayload, state herdrStored) bool {
	return keepsToolMessage(event, payload, state) || holdsBlock(event, payload, state)
}

// keepsToolMessage reports whether a permission_prompt Notification arrives
// over a blocked state whose message already names the tool (Claude's
// PermissionRequest hook carries the tool; the Notification only Claude's
// generic text): the stored message stays and nothing is sent.
func keepsToolMessage(event herdr.Event, payload herdrPayload, state herdrStored) bool {
	return event == herdr.EventNotification && payload.NotificationType == "permission_prompt" &&
		state.State == string(herdr.StateBlocked) && strings.HasPrefix(state.Message, approvalPrefix)
}

// holdsBlock reports whether a tool's working report arrives over a stored
// tool request that names another tool call.
func holdsBlock(event herdr.Event, payload herdrPayload, state herdrStored) bool {
	return event == herdr.EventWorking && payload.ToolName != "" &&
		state.State == string(herdr.StateBlocked) && state.Tool != "" && callFingerprint(payload) != state.Tool
}

// toolBlock records on a Claude tool request's block the tool call it asks
// about; another engine's block, or one naming no tool, records none and any
// later working report leaves it.
func (reporter *herdrReporter) toolBlock(payload herdrPayload, action *herdrAction) *herdrAction {
	if reporter.options.engine == pfmengine.Claude && payload.ToolName != "" {
		action.stored.Tool = callFingerprint(payload)
	}
	return action
}

// claudeDialogTools are Claude's tools whose permission dialog is the tool's own
// interaction: the dialog writes the user's answer into the input the tool runs
// with (Claude Code 2.1.301: AskUserQuestion's answers, ExitPlanMode's plan), so
// their call is named by the tool alone. Claude shows one such dialog at a time.
var claudeDialogTools = map[string]bool{"AskUserQuestion": true, "ExitPlanMode": true}

// callFingerprint names one tool call: the tool and a digest of its input, decoded
// and re-encoded so key order and spacing never tell two payloads of one call
// apart. An input that is not JSON is digested as sent.
func callFingerprint(payload herdrPayload) string {
	if claudeDialogTools[payload.ToolName] {
		return payload.ToolName
	}
	input := []byte(payload.ToolInput)
	var value any
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err == nil {
		if encoded, err := json.Marshal(value); err == nil {
			input = encoded
		}
	}
	sum := sha256.Sum256(input)
	return payload.ToolName + " " + hex.EncodeToString(sum[:8])
}

// themeUnlessHeld marks the server's agent state for an event the stored state
// does not hold: a held working report leaves the blocked colour on.
func (reporter *herdrReporter) themeUnlessHeld(
	ctx context.Context,
	themeOn bool,
	payload herdrPayload,
	state herdrStored,
) {
	if themeOn && !holdsBlock(reporter.options.event, payload, state) {
		reporter.themeState(ctx, payload)
	}
}
