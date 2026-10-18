package hookentry

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// quietPromptBlock is blockPromptQuietly's fixed encoding — kept as a
// literal so tests can assert against it without re-deriving the JSON.
const quietPromptBlock = `{"decision":"block","reason":"","suppressOriginalPrompt":true}` + "\n"

type promptBlockDecision struct {
	Decision               string `json:"decision"`
	Reason                 string `json:"reason"`
	SuppressOriginalPrompt bool   `json:"suppressOriginalPrompt"`
}

// blockPrompt answers a UserPromptSubmit hook with a block decision carrying
// reason. Claude Code renders an empty reason identically to a hook FAILURE
// ("Blocked by hook · Original prompt: ..."), so a caller blocking a prompt
// on a SUCCESSFUL action must say why, or the user reads success as breakage.
func blockPrompt(stdout io.Writer, reason string) int {
	encoded, err := json.Marshal(promptBlockDecision{
		Decision:               "block",
		Reason:                 reason,
		SuppressOriginalPrompt: true,
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

// isReloadPrompt reports whether reload-intercept takes prompt as a /reload.
func isReloadPrompt(prompt string) bool {
	trimmed := strings.TrimSpace(prompt)
	return trimmed == "/reload" || strings.HasPrefix(trimmed, "/reload ")
}

// isExitPrompt reports whether exit-intercept takes prompt as an exit word.
func isExitPrompt(prompt string) bool {
	return exitWords[strings.TrimSpace(prompt)]
}

// interceptedPrompt reports whether reload-intercept or exit-intercept blocks
// prompt — on success and on refusal alike — so it never starts a turn.
func interceptedPrompt(prompt string) bool {
	return isReloadPrompt(prompt) || isExitPrompt(prompt)
}

// interceptedSubmit reports whether a Claude prompt submission is one its
// intercept blocks: it starts no turn, and no Stop would report the pane idle
// again, so the Herdr state must stand.
func interceptedSubmit(engine pfmengine.ID, payload herdrPayload) bool {
	return engine == pfmengine.Claude &&
		payload.HookEventName == claudelaunch.HookEventUserPromptSubmit &&
		interceptedPrompt(payload.Prompt)
}
