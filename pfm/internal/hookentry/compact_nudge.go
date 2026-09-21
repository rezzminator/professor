package hookentry

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/nudge"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type compactNudgePayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	AgentID        string `json:"agent_id"`
	AgentType      string `json:"agent_type"`
}

// CompactNudge is the Claude-only main-chat compact reminder hook.
func CompactNudge(stdin io.Reader, stdout, stderr io.Writer, runtime config.Runtime, env paths.Env) int {
	if env == nil {
		env = paths.OSEnv{}
	}
	account := runtime.Config.AccountForConfigDir(env.Get("CLAUDE_CONFIG_DIR"))
	prefs := runtime.Config.EffectiveClaude(account).CompactNudge
	return compactNudgeWith(stdin, stdout, stderr, runtime.Paths.SIDDir, prefs)
}

func compactNudgeWith(stdin io.Reader, stdout, stderr io.Writer, sidDir string, prefs config.CompactNudge) int {
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
