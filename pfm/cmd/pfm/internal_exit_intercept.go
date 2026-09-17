package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// exitInterceptRun runs the kill FRONT in-process — the same runKill a human's
// own `pfm chat kill self --exit` call would reach. A package var, not a direct
// call, so a test can assert the exact argument slice the hook derived from the
// typed prompt without closing a live chat.
var exitInterceptRun = runKill

// exitWords are the prompts that mean "close this chat". Both spellings are
// here on purpose: `e` is the muscle memory from the shell alias this hook
// replaces, and `/e` is the slash form a human reaches for inside a composer.
var exitWords = map[string]bool{"e": true, "/e": true}

// runExitIntercept is the UserPromptSubmit hook body for
// `pfm internal exit-intercept`. A prompt of exactly "e" or "/e" is the human
// asking to close the chat, so it runs the close here — before the model ever
// sees the prompt — instead of spending a whole turn having the model type the
// identical call into Bash.
//
// Claude Code's UserPromptSubmit contract: exit 0 lets the prompt through
// (stdout is added as context); exit 2 BLOCKS it behind the full banner with
// stderr; exit 0 with the quiet JSON block (quietPromptBlock) blocks it with
// a bare notice. A matched prompt never reaches the model: a close that ran
// is swallowed quietly, a close that failed keeps the exit-2 banner so its
// text reaches the human. Codex has no UserPromptSubmit hook, so a Codex
// seat's `e` still goes through the model. This is a Claude-only shortcut,
// not the only path to a close.
func runExitIntercept(stdin io.Reader, stdout, stderr io.Writer, runtime commandRuntime) int {
	var payload struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		fmt.Fprintf(stderr, "pfm internal exit-intercept: decode hook payload: %v\n", err)
		return 0
	}
	if !exitWords[strings.TrimSpace(payload.Prompt)] {
		return 0
	}
	// One buffer for both streams so the front's success line and any error it
	// writes land in the order the front itself produced them.
	var captured bytes.Buffer
	if exitInterceptRun([]string{"--self", "--exit"}, &captured, &captured, runtime) == 0 {
		return blockPromptQuietly(stdout)
	}
	fmt.Fprintf(stderr, "exit: %s", captured.String())
	return 2
}
