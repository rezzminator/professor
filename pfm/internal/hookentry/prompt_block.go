package hookentry

import (
	"encoding/json"
	"fmt"
	"io"
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
