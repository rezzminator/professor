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
//
// The request is named by tool and input, never by the tool_use_id: Claude Code
// 2.1.301 builds its PermissionRequest hook input from tool_name, tool_input,
// permission_suggestions and the mcp server, and only the PostToolUse and
// PostToolUseFailure inputs carry tool_use_id (binary probe, `hook_event_name:
// "PermissionRequest"` against `hook_event_name:"PostToolUse"` ... tool_use_id),
// so the id cannot pair the two. Two identical calls of one batch therefore
// share a hold until one finishes.

// keepsStored reports whether the stored state outlives this event unchanged:
// nothing is stored, sent or watched anew.
func keepsStored(event herdr.Event, payload herdrPayload, state herdrStored) bool {
	return keepsToolMessage(event, payload, state) || holdsBlock(event, payload, state) ||
		holdsAgentBlock(event, payload, state)
}

// holdsAgentBlock reports whether the main thread's end of turn (Stop, or the
// idle prompt) arrives over a sub-agent's open dialog: a background agent's
// dialog outlives the main turn, and only its own call or its stop closes it.
func holdsAgentBlock(event herdr.Event, payload herdrPayload, state herdrStored) bool {
	idle := event == herdr.EventIdle ||
		event == herdr.EventNotification && payload.NotificationType == idlePromptNotification
	return idle && state.State == string(herdr.StateBlocked) && strings.HasPrefix(state.Tool, subagentCallPrefix)
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
// A sub-agent's call is also named by its agent, so the main thread's or another
// agent's identical call never answers it.
func callFingerprint(payload herdrPayload) string {
	call := payload.ToolName
	if !claudeDialogTools[payload.ToolName] {
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
		call += " " + hex.EncodeToString(sum[:8])
	}
	if payload.AgentID != "" {
		call = subagentCallPrefix + payload.AgentID + " " + call
	}
	return call
}

// subagentCallPrefix opens a sub-agent call's fingerprint, before its agent id.
const subagentCallPrefix = "agent "

// A sub-agent's permission dialog blocks the whole chat: Claude Code 2.1.316
// builds every hook input in one function that adds the tool context's agentId,
// so the sub-agent's PermissionRequest, PostToolUse and PostToolUseFailure carry
// agent_id, while the dialog's permission_prompt Notification (built from the
// session alone) carries none (binary probe, `function wf(e,n,r,s)` ...
// `agent_id:s?.agentId`; `hook_event_name:"Notification"` from `wf(e,oh())`).
// subagentToolEvent reports whether a sub-agent hook is one the reporter reads:
// its tool request, which blocks as the main thread's does, and its tool's
// working report, which counts only as the answer to that request (answersBlock).
// Every other sub-agent event, as before, changes nothing.
func subagentToolEvent(engine pfmengine.ID, event herdr.Event, payload herdrPayload) bool {
	return engine == pfmengine.Claude && payload.AgentID != "" && payload.ToolName != "" &&
		(event == herdr.EventBlocked || event == herdr.EventWorking)
}

// answersBlock reports whether a tool's working report is the very call the
// stored block asks about.
func answersBlock(payload herdrPayload, state herdrStored) bool {
	return state.State == string(herdr.StateBlocked) && state.Tool != "" && callFingerprint(payload) == state.Tool
}

// ownsBlock reports whether the stored block asks about a call of agent: Claude
// fires no hook when the user answers No or Esc, so that agent's stop is the
// signal its dialog is closed.
func ownsBlock(agent string, state herdrStored) bool {
	return agent != "" && strings.HasPrefix(state.Tool, subagentCallPrefix+agent+" ")
}

// themeUnlessHeld marks the server's agent state for an event the stored state
// does not hold: a held working report, or a main Stop over a sub-agent's open
// dialog, leaves the blocked colour on.
func (reporter *herdrReporter) themeUnlessHeld(
	ctx context.Context,
	themeOn bool,
	payload herdrPayload,
	state herdrStored,
) {
	if themeOn && !holdsBlock(reporter.options.event, payload, state) &&
		!holdsAgentBlock(reporter.options.event, payload, state) {
		reporter.themeState(ctx, payload)
	}
}
