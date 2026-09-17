package hookentry

import "io"

const quietPromptBlock = `{"decision":"block","reason":"","suppressOriginalPrompt":true}` + "\n"

func blockPromptQuietly(stdout io.Writer) int {
	_, _ = io.WriteString(stdout, quietPromptBlock)
	return 0
}
