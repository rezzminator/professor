package hookentry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// quietPromptBlock is blockPromptQuietly's fixed encoding — kept as a
// literal so tests can assert against it without re-deriving the JSON.
const quietPromptBlock = `{"decision":"block","reason":"",` +
	`"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","suppressOriginalPrompt":true}}` + "\n"

// promptBlockDecision is a UserPromptSubmit block. Claude Code reads
// suppressOriginalPrompt only under hookSpecificOutput: a top-level one is
// ignored and the block message gains an "Original prompt: ..." line.
type promptBlockDecision struct {
	Decision           string                `json:"decision"`
	Reason             string                `json:"reason"`
	HookSpecificOutput promptBlockHookOutput `json:"hookSpecificOutput"`
}

type promptBlockHookOutput struct {
	HookEventName          string `json:"hookEventName"`
	SuppressOriginalPrompt bool   `json:"suppressOriginalPrompt"`
}

// blockPrompt answers a UserPromptSubmit hook with a block decision carrying
// reason. Claude Code renders an empty reason identically to a hook FAILURE
// ("Blocked by hook · Original prompt: ..."), so a caller blocking a prompt
// on a SUCCESSFUL action must say why, or the user reads success as breakage.
func blockPrompt(stdout io.Writer, reason string) int {
	encoded, err := json.Marshal(promptBlockDecision{
		Decision: "block",
		Reason:   reason,
		HookSpecificOutput: promptBlockHookOutput{
			HookEventName:          claudelaunch.HookEventUserPromptSubmit,
			SuppressOriginalPrompt: true,
		},
	})
	if err != nil {
		// promptBlockDecision holds only strings and a bool — json.Marshal
		// cannot fail on it. Fall back to the known-good quiet encoding
		// rather than write malformed JSON to a hook stdout.
		_, _ = io.WriteString(stdout, quietPromptBlock)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "%s\n", encoded)
	return 0
}

// blockPromptQuietly blocks a prompt with no reason shown to the user — kept
// for callers (exit-intercept) where the blocked prompt IS the action asked
// for and no additional line would be truthful to add.
func blockPromptQuietly(stdout io.Writer) int {
	return blockPrompt(stdout, "")
}

// exitWords are the prompts exit-intercept turns into the end of the chat.
var exitWords = map[string]bool{"e": true, "/e": true}

// isCommandPrompt reports whether command-intercept takes prompt as the
// typed command: the command word alone or followed by a space.
func isCommandPrompt(prompt, command string) bool {
	trimmed := strings.TrimSpace(prompt)
	return trimmed == command || strings.HasPrefix(trimmed, command+" ")
}

// isExitPrompt reports whether exit-intercept takes prompt as an exit word.
func isExitPrompt(prompt string) bool {
	return exitWords[strings.TrimSpace(prompt)]
}

// interceptedPrompt reports whether command-intercept or exit-intercept blocks
// prompt — on success and on refusal alike — so it never starts a turn.
func interceptedPrompt(prompt string) bool {
	return isCommandPrompt(prompt, reloadCommand) || isCommandPrompt(prompt, spaceCommand) || isExitPrompt(prompt)
}

// interceptedSubmit reports whether a Claude prompt submission is one its
// intercept blocks: it starts no turn, and no Stop would report the pane idle
// again, so the Herdr state must stand.
func interceptedSubmit(engine pfmengine.ID, payload herdrPayload) bool {
	return engine == pfmengine.Claude &&
		payload.HookEventName == claudelaunch.HookEventUserPromptSubmit &&
		interceptedPrompt(payload.Prompt)
}

// recordTypedCommand logs, under event, a typed command a prompt hook refused
// or could not clear from the input box: the hook's output reaches only the
// user's screen, and the detached clear has none, so this record is what a
// reader finds.
func recordTypedCommand(event, prompt, reason string) {
	ctx := obs.Component(context.Background(), "hooks")
	obs.Logger(ctx).LogAttrs(ctx, slog.LevelWarn, event,
		slog.String("prompt", prompt),
		slog.String(obs.FieldErr, reason),
	)
}
