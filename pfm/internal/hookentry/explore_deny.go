package hookentry

import (
	"encoding/json"
	"fmt"
	"io"
)

type exploreDenyHookInput struct {
	ToolInput struct {
		SubagentType string `json:"subagent_type"`
		Model        string `json:"model"`
	} `json:"tool_input"`
}

const exploreDenyReason = "Explore is disabled — use the `tracer` agent instead.\n\nSpawn `subagent_type: \"tracer\"` with the mission in the prompt. It returns the\nsame read-only map with evidence Explore cannot give you: every edge quoted at\nfile:line, every bucket file dispositioned (EDGE / RED-HERRING / FRONTIER /\nFAILED-TO-LOOK), caller greps behind every named consumer, and a coverage close\nthat distinguishes \"looked and found nothing\" from \"failed to look\".\n\nThe tracer dispatches its own Explore children internally; that path is allowed\nand needs nothing from you."

// ExploreDeny is the fail-open PreToolUse hook.
func ExploreDeny(input io.Reader, stdout, stderr io.Writer) int {
	raw, err := io.ReadAll(input)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal explore-deny: read hook payload (fail-open): %v\n", err)
		return 0
	}
	if len(raw) == 0 {
		return 0
	}
	var hook exploreDenyHookInput
	if err := json.Unmarshal(raw, &hook); err != nil {
		fmt.Fprintf(stderr, "pfm internal explore-deny: decode hook payload (fail-open): %v\n", err)
		return 0
	}
	if hook.ToolInput.SubagentType != "Explore" || hook.ToolInput.Model == "haiku" {
		return 0
	}
	if err := json.NewEncoder(stdout).Encode(preToolUseDenyResponse(exploreDenyReason)); err != nil {
		return 1
	}
	return 0
}

// preToolUseDenyResponse builds the PreToolUse deny hookSpecificOutput
// payload shared by every PreToolUse deny hook in this package.
func preToolUseDenyResponse(reason string) map[string]any {
	return map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": reason,
	}}
}
