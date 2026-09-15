package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/nudge"
)

// compactNudgePayload is the slice of the UserPromptSubmit hook JSON the
// reminder needs. agent_id / agent_type only appear on a sub-agent's turn: the
// milestone is the main chat's, never its helper's, so those are skipped.
type compactNudgePayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	AgentID        string `json:"agent_id"`
	AgentType      string `json:"agent_type"`
}

// runCompactNudge is the `pfm internal compact-nudge` hook: Claude only (it is
// wired into Claude settings alone), main chat only, governed by the account's
// claude.compactNudge policy.
func runCompactNudge(stdin io.Reader, stdout, stderr io.Writer, runtime commandRuntime) int {
	account := accountForConfig(runtime.Config, os.Getenv("CLAUDE_CONFIG_DIR"))
	prefs := runtime.Config.EffectiveClaude(account).CompactNudge
	return compactNudge(stdin, stdout, stderr, runtime.Paths.SIDDir, prefs)
}

func compactNudge(stdin io.Reader, stdout, stderr io.Writer, sidDir string, prefs pfmconfig.CompactNudge) int {
	var payload compactNudgePayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		fmt.Fprintf(stderr, "pfm internal compact-nudge: decode hook payload: %v\n", err)
		return 0
	}
	if !prefs.Enabled || payload.AgentID != "" || payload.AgentType != "" {
		return 0
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" && payload.TranscriptPath != "" {
		sessionID = strings.TrimSuffix(filepath.Base(payload.TranscriptPath), filepath.Ext(payload.TranscriptPath))
	}
	if sessionID == "" {
		fmt.Fprintln(stderr, "pfm internal compact-nudge: hook payload names no session")
		return 0
	}
	percent, found, err := nudge.ReadContext(sidDir, sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal compact-nudge: read context sample: %v\n", err)
		return 0
	}
	if !found {
		fmt.Fprintf(
			stderr,
			"pfm internal compact-nudge: no context sample for %s yet — the statusline records one after the first rendered turn\n",
			sessionID,
		)
		return 0
	}
	band, speak, err := nudge.Decide(sidDir, sessionID, percent, prefs.Start, prefs.Step)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal compact-nudge: decide band: %v\n", err)
		return 0
	}
	if !speak {
		return 0
	}
	response := struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}{}
	response.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	response.HookSpecificOutput.AdditionalContext = nudge.Text(percent, band, prefs.Step)
	encoded, err := json.Marshal(response)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal compact-nudge: encode context: %v\n", err)
		return 0
	}
	if _, err := fmt.Fprintln(stdout, string(encoded)); err != nil {
		fmt.Fprintf(stderr, "pfm internal compact-nudge: write context: %v\n", err)
		return 1
	}
	return 0
}
