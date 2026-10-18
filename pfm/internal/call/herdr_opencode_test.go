package call

import (
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Captured from OpenCode 1.14.30 in an offline Herdr terminal.
func openCodeTestScreen(draft string) string {
	return "┃\n┃  " + draft + "\n┃\n┃  Build · Fixture Model\n╹▀▀▀▀▀▀▀▀\n  tab agents  ctrl+p commands"
}

func herdrOpenCodeCases() []herdrRawCase {
	const hint = "Ask anything... \"Fix a TODO in the codebase\""
	const muted = "\x1b[38;2;130;139;184m"
	const foreground = "\x1b[38;2;200;211;245m"
	styled := strings.Replace(
		openCodeTestScreen(muted+hint+"\x1b[0m"),
		"ctrl+p commands",
		foreground+"ctrl+p \x1b[0m"+muted+"commands\x1b[0m",
		1,
	)
	return []herdrRawCase{
		{
			name:        "opencode-real-placeholder",
			id:          pfmengine.OpenCode,
			body:        "R2",
			before:      openCodeTestScreen(hint),
			styled:      styled,
			after:       "R2\n" + openCodeTestScreen(""),
			wantPayload: "\x1b[200~R2\x1b[201~",
			wantTyped:   true,
		},
		{
			name:     "opencode-real-draft",
			id:       pfmengine.OpenCode,
			body:     "R2",
			before:   openCodeTestScreen("private draft"),
			wantCode: CodeUndelivered,
		},
		{
			name: "opencode-literal-hint-draft", id: pfmengine.OpenCode, body: "R2", before: openCodeTestScreen(hint),
			styled: strings.Replace(styled, muted+hint, foreground+hint, 1), wantCode: CodeUndelivered,
		},
		{
			name:     "opencode-multiline-draft",
			id:       pfmengine.OpenCode,
			body:     "R2",
			before:   openCodeTestScreen("\n┃ private draft\n┃ "),
			wantCode: CodeUndelivered,
		},
		{
			name:      "opencode-no-receipt",
			id:        pfmengine.OpenCode,
			body:      "R2",
			before:    openCodeTestScreen(""),
			after:     openCodeTestScreen(""),
			wantCode:  CodeUndelivered,
			wantTyped: true,
		},
	}
}
