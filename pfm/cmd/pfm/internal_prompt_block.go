package main

import "io"

// quietPromptBlock is the UserPromptSubmit answer that swallows a prompt with
// the least the harness will render: exit 0 plus this JSON blocks the prompt,
// shows no reason, and omits the prompt echo — where exit 2 always paints the
// full "operation blocked by hook: [command]: stderr … Original prompt" banner.
// Only a front that SUCCEEDED earns the quiet path; a failure stays on exit 2
// so its text reaches the human.
const quietPromptBlock = `{"decision":"block","reason":"","suppressOriginalPrompt":true}` + "\n"

func blockPromptQuietly(stdout io.Writer) int {
	_, _ = io.WriteString(stdout, quietPromptBlock)
	return 0
}
