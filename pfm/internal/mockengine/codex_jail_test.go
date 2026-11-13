//go:build jail

package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestCodexMCPStepCallsPfmsOwnServerWithPaneIdentity(t *testing.T) {
	fix := newFixture(t)
	testjail.DatabaseFiles(t, fix.root)
	t.Chdir(fix.work)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(fix.recordDir, "mcp-identity")
	command := fmt.Sprintf(`printf '%%s\n' "$TMUX" "$TMUX_PANE" "$CODEX_HOME" "$PWD" > %q; exec "$@"`, identity)
	writeCodexConfig(t, fix, fmt.Sprintf("[mcp_servers.chat-professor]\ncommand = \"sh\"\nargs = [%q,%q,%q,%q,%q]\n",
		"-c", command, "fixture", executable, verbsStdioProfessorArg))
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes, BusyMS: intPtr(0)})
	// Use a completed Claude exchange so last has a stable answer while
	// this Codex turn exercises the protocol and its pane identity.
	code, _, stderr := runOnce(
		fix,
		"claude",
		claudeArgs("--session-id", fixtureSession, "-p", "violet archive fixture"),
		"",
	)
	if code != 0 {
		t.Fatalf("seed Claude transcript: exit=%d stderr=%q", code, stderr)
	}
	session := fix.startTUI(
		"codex",
		codexArgs(),
		map[string]string{"TMUX": "/fixture/cx-fixture,42,0", "TMUX_PANE": "%8"},
	)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "›") })
	session.typeLine("violet archive fixture")
	session.waitFrame("seed reply", func(frame string) bool { return strings.Contains(frame, "⏺ ok") })
	fix.indexedRollouts()
	session.typeLine(
		fmt.Sprintf(
			`query mock-engine: [{"type":"mcp","tool":"last","input":{"target":%q}},{"type":"turn","reply":"MCP finished"}]`,
			fixtureSession,
		),
	)
	session.waitFrame("MCP reply", func(frame string) bool { return strings.Contains(frame, "⏺ MCP finished") })
	result := readFile(t, filepath.Join(fix.recordDir, "mcp-call-last.json"))
	if !strings.Contains(result, fixtureSession) || !strings.Contains(result, `"text":"ok"`) ||
		strings.Contains(result, `"isError":true`) {
		t.Fatalf("pfm tools/call=%s", result)
	}
	var callID string
	var outputs int
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, fix.rolloutPath())), "\n") {
		var payload struct {
			Type   string
			Name   string
			CallID string `json:"call_id"`
			Output string
		}
		var envelope struct{ Payload json.RawMessage }
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Type == "function_call" && payload.Name == "last" {
			callID = payload.CallID
		}
		if payload.Type == "function_call_output" && payload.CallID == callID &&
			strings.Contains(payload.Output, fixtureSession) {
			outputs++
		}
	}
	if callID == "" || outputs != 1 {
		t.Fatalf("MCP rollout call=%q outputs=%d", callID, outputs)
	}
	if got := readFile(t, identity); got != "/fixture/cx-fixture,42,0\n%8\n"+fix.codexHome+"\n"+fix.work+"\n" {
		t.Fatalf("MCP child identity=%q", got)
	}
}
