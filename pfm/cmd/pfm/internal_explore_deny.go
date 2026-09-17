package main

import (
	"encoding/json"
	"io"
)

type exploreDenyHookInput struct {
	ToolInput struct {
		SubagentType string `json:"subagent_type"`
		Model        string `json:"model"`
	} `json:"tool_input"`
}

const exploreDenyReason = "Explore is disabled — use the `tracer` agent instead.\n\nSpawn `subagent_type: \"tracer\"` with the mission in the prompt. It returns the\nsame read-only map with evidence Explore cannot give you: every edge quoted at\nfile:line, every bucket file dispositioned (EDGE / RED-HERRING / FRONTIER /\nFAILED-TO-LOOK), caller greps behind every named consumer, and a coverage close\nthat distinguishes \"looked and found nothing\" from \"failed to look\".\n\nThe tracer dispatches its own Explore children internally; that path is allowed\nand needs nothing from you."

// runExploreDeny is the fail-open PreToolUse hook. Hook input is external
// JSON, so malformed or incomplete input deliberately produces no verdict.
func runExploreDeny(input io.Reader, stdout, _ io.Writer) int {
	raw, err := io.ReadAll(input)
	if err != nil || len(raw) == 0 {
		return 0
	}
	var hook exploreDenyHookInput
	if err := json.Unmarshal(raw, &hook); err != nil {
		return 0
	}
	if hook.ToolInput.SubagentType != "Explore" || hook.ToolInput.Model == "haiku" {
		return 0
	}
	response := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": exploreDenyReason,
		},
	}
	if err := json.NewEncoder(stdout).Encode(response); err != nil {
		return 1
	}
	return 0
}
