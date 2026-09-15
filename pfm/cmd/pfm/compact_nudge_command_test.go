package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/nudge"
)

func nudgePayload(t *testing.T, fields map[string]string) []byte {
	t.Helper()
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func callCompactNudge(t *testing.T, payload []byte, sidDir string, prefs pfmconfig.CompactNudge) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := compactNudge(bytes.NewReader(payload), &stdout, &stderr, sidDir, prefs); code != 0 {
		t.Fatalf("compact-nudge code=%d stderr=%q", code, stderr.String())
	}
	return stdout.String(), stderr.String()
}

func TestCompactNudgeSpeaksOncePerBandFromTheStatuslineSample(t *testing.T) {
	sidDir := t.TempDir()
	prefs := pfmconfig.CompactNudge{Enabled: true, Start: 35, Step: 10}
	payload := nudgePayload(t, map[string]string{"session_id": "sess-a", "transcript_path": "/jail/sess-a.jsonl"})

	if err := nudge.RecordContext(sidDir, "sess-a", 20); err != nil {
		t.Fatal(err)
	}
	if out, _ := callCompactNudge(t, payload, sidDir, prefs); out != "" {
		t.Fatalf("below the first band the hook must stay silent, got %q", out)
	}

	if err := nudge.RecordContext(sidDir, "sess-a", 47); err != nil {
		t.Fatal(err)
	}
	out, _ := callCompactNudge(t, payload, sidDir, prefs)
	var response struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("nudge output is not hook JSON: %v\n%s", err, out)
	}
	if response.HookSpecificOutput.HookEventName != "UserPromptSubmit" ||
		!strings.Contains(response.HookSpecificOutput.AdditionalContext, "47%") ||
		!strings.Contains(response.HookSpecificOutput.AdditionalContext, "chat_self_compact") {
		t.Fatalf("nudge response = %+v", response)
	}
	if again, _ := callCompactNudge(t, payload, sidDir, prefs); again != "" {
		t.Fatalf("same band must not nudge twice, got %q", again)
	}
}

func TestCompactNudgeHonoursTheConfigAndSkipsSubAgents(t *testing.T) {
	sidDir := t.TempDir()
	if err := nudge.RecordContext(sidDir, "sess-b", 60); err != nil {
		t.Fatal(err)
	}
	main := nudgePayload(t, map[string]string{"session_id": "sess-b"})
	if out, _ := callCompactNudge(
		t,
		main,
		sidDir,
		pfmconfig.CompactNudge{Enabled: false, Start: 35, Step: 10},
	); out != "" {
		t.Fatalf("disabled in config must stay silent, got %q", out)
	}
	sub := nudgePayload(t, map[string]string{"session_id": "sess-b", "agent_id": "agent-7", "agent_type": "dev"})
	if out, _ := callCompactNudge(
		t,
		sub,
		sidDir,
		pfmconfig.CompactNudge{Enabled: true, Start: 35, Step: 10},
	); out != "" {
		t.Fatalf("a sub-agent prompt must never be nudged, got %q", out)
	}
	if out, _ := callCompactNudge(
		t,
		main,
		sidDir,
		pfmconfig.CompactNudge{Enabled: true, Start: 70, Step: 10},
	); out != "" {
		t.Fatalf("60%% is below a configured start of 70, got %q", out)
	}
	if out, _ := callCompactNudge(
		t,
		main,
		sidDir,
		pfmconfig.CompactNudge{Enabled: true, Start: 35, Step: 10},
	); !strings.Contains(
		out,
		"60%",
	) {
		t.Fatalf("the main chat at 60%% must be nudged, got %q", out)
	}
}

func TestCompactNudgeNamesAMissingSampleAndABadPayload(t *testing.T) {
	sidDir := t.TempDir()
	prefs := pfmconfig.CompactNudge{Enabled: true, Start: 35, Step: 10}
	out, errText := callCompactNudge(t, nudgePayload(t, map[string]string{"session_id": "sess-c"}), sidDir, prefs)
	if out != "" || !strings.Contains(errText, "no context sample") {
		t.Fatalf("missing sample: stdout=%q stderr=%q, want silence on stdout and the cause on stderr", out, errText)
	}
	var stdout, stderr bytes.Buffer
	if code := compactNudge(
		strings.NewReader("{not json"),
		&stdout,
		&stderr,
		sidDir,
		prefs,
	); code != 0 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "decode hook payload") {
		t.Fatalf("bad payload: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
