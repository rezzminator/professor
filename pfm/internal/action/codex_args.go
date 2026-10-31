package action

import (
	"errors"
	"fmt"
)

// The two thread verbs a Codex launch can carry after the override words.
const (
	CodexResume = "resume"
	CodexFork   = "fork"
)

// CodexInstructions is the instructions argument of a door where an empty text
// means no override: nil for "", else a pointer to text.
func CodexInstructions(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}

// CodexArgs builds the post-autonomy Codex words every launch door shares, in
// order: `--model M`, the reasoning-effort override, the developer-instructions
// override, then `verb thread`. Each empty string adds no words; effort arrives
// as the caller resolved it; a non-nil instructions emits the override even for
// an empty body; a thread names CodexResume or CodexFork and carries no line
// break or NUL.
func CodexArgs(model, home, effort string, instructions *string, verb, thread string) ([]string, error) {
	words, err := codexModelArgs(model, home)
	if err != nil {
		return nil, err
	}
	if effort != "" {
		words = append(words, CodexEffortArg(effort)...)
	}
	if instructions != nil {
		words = append(words, CodexDeveloperInstructionsArg(*instructions)...)
	}
	if thread != "" {
		if verb != CodexResume && verb != CodexFork {
			return nil, fmt.Errorf("codex thread verb %q is neither %s nor %s", verb, CodexResume, CodexFork)
		}
		if hasUnsafeCodexID(thread) {
			return nil, errors.New("codex " + verb + " requires one safe session id")
		}
		words = append(words, verb, thread)
	}
	return words, nil
}

func hasUnsafeCodexID(id string) bool {
	for _, r := range id {
		if r == '\r' || r == '\n' || r == 0 {
			return true
		}
	}
	return false
}
