package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeMCPStepCallsInstalledPfmServer(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	code, _, stderr := runOnce(fix, "claude", claudeArgs("--session-id", fixtureSession, "-p", "Atlas proof"), "")
	if code != 0 {
		t.Fatalf("seed Claude transcript: exit=%d stderr=%q", code, stderr)
	}
	fix.indexedTranscripts()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(fix.home, ".config", "opencode")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "opencode.jsonc")
	config := mustJSON(
		t,
		map[string]any{
			"mcp": map[string]any{
				"chat-professor": map[string]any{
					"type":    "local",
					"enabled": true,
					"command": []string{executable, verbsStdioProfessorArg},
				},
			},
		},
	)
	if err := os.WriteFile(path, config, 0o600); err != nil {
		t.Fatal(err)
	}
	fix.write(
		Scenario{
			SessionID: fixtureOpenCodeSession,
			Pane:      openCodeShapes,
			Steps: []Step{
				{
					Type:  StepMCP,
					Tool:  "last",
					Input: json.RawMessage(`{"target":"b1111111-1111-4111-8111-111111111111"}`),
				},
				{Type: StepTurn, Reply: "MCP finished"},
			},
		},
	)
	session := fix.startTUI("opencode", []string{fix.work}, nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, openCodeShapes.Composer) })
	session.typeLine("read the Atlas response")
	session.waitFrame("MCP reply", func(frame string) bool { return strings.Contains(frame, "⏺ MCP finished") })
	resultPath := filepath.Join(fix.recordDir, "mcp-call-last.json")
	var result struct {
		IsError bool                          `json:"isError"`
		Content []struct{ Type, Text string } `json:"content"`
	}
	if err := json.Unmarshal([]byte(readFile(t, resultPath)), &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("%s: result=%+v", resultPath, result)
	}
	var last struct{ Target, Text string }
	if err := json.Unmarshal([]byte(result.Content[0].Text), &last); err != nil {
		t.Fatal(err)
	}
	if last.Target != fixtureSession || last.Text != "ok" {
		t.Fatalf("%s: last=%+v, want target=%s text=ok", resultPath, last, fixtureSession)
	}
}

func TestOpenCodeMCPStepRefusesDisabledOrRemoteRegistration(t *testing.T) {
	fix := newFixture(t)
	for _, entry := range []string{`{"type":"local","command":["pfm"],"enabled":false}`, `{"type":"remote","url":"https://fixture.invalid/mcp"}`, `{"type":"local","command":[]}`} {
		proc := &process{
			cwd: fix.work,
			env: fix.env(map[string]string{"OPENCODE_CONFIG_CONTENT": `{"mcp":{"chat-professor":` + entry + `}}`}),
		}
		session := &openCodeSession{proc: proc}
		if err := session.mcp(
			Step{Type: StepMCP},
		); err == nil ||
			err.Error() != "OpenCode config has no enabled local mcp.chat-professor command" {
			t.Fatalf("registration=%s: error=%v", entry, err)
		}
	}
}
