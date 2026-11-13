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

func TestClaudeMCPStepCallsPfmsOwnServer(t *testing.T) {
	for _, fileConfig := range []bool{false, true} {
		t.Run(fmt.Sprint(fileConfig), func(t *testing.T) {
			fix := newFixture(t)
			testjail.DatabaseFiles(t, fix.root)
			t.Chdir(fix.work)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			identityFile := filepath.Join(fix.recordDir, "mcp-identity")
			config := string(mustJSON(t, map[string]any{"mcpServers": map[string]any{"chat-professor": map[string]any{
				"type": "stdio", "command": "sh", "args": []string{
					"-c",
					fmt.Sprintf(
						`printf '%%s\n' "$TMUX" "$TMUX_PANE" "$CLAUDE_CODE_SESSION_ID" "$PWD" > %q; exec "$@"`,
						identityFile,
					),
					"fixture",
					executable,
					verbsStdioProfessorArg,
				},
			}}}))
			if fileConfig {
				path := filepath.Join(fix.root, "mcp.json")
				if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
				config = path
			}
			fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
			session := fix.startTUI(
				"claude",
				claudeArgs("--mcp-config", config),
				map[string]string{"TMUX": "/fixture/cc-fixture,42,0", "TMUX_PANE": "%7"},
			)
			session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			session.typeLine("violet archive fixture")
			session.waitFrame("the seed reply", func(frame string) bool { return strings.Contains(frame, "⏺ ok") })
			fix.indexedTranscripts()
			session.typeLine(
				fmt.Sprintf(
					`query mock-engine: [{"type":"mcp","tool":"last","input":{"target":%q}},{"type":"turn","reply":"MCP finished"}]`,
					fixtureSession,
				),
			)
			session.waitFrame(
				"the MCP reply",
				func(frame string) bool { return strings.Contains(frame, "⏺ MCP finished") },
			)
			result := readFile(t, filepath.Join(fix.recordDir, "mcp-call-last.json"))
			if !strings.Contains(result, fixtureSession) || !strings.Contains(result, `"text":"ok"`) {
				t.Fatalf("pfm's tools/call answer=%s", result)
			}
			var uses, results int
			for _, line := range strings.Split(strings.TrimSpace(readFile(t, fix.claudeTranscript(fixtureSession))), "\n") {
				var record struct {
					Message struct{ Content json.RawMessage }
				}
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatal(err)
				}
				if len(record.Message.Content) == 0 || record.Message.Content[0] != '[' {
					continue
				}
				var parts []contentPart
				if err := json.Unmarshal(record.Message.Content, &parts); err != nil {
					t.Fatal(err)
				}
				for _, part := range parts {
					if part.Type == "tool_use" && part.Name == "last" {
						uses++
					}
					if part.Type == "tool_result" && strings.Contains(part.Content, fixtureSession) && !part.IsError {
						results++
					}
				}
			}
			if uses != 1 || results != 1 {
				t.Fatalf("tool records: use=%d result=%d", uses, results)
			}
			if got := readFile(
				t,
				identityFile,
			); got != "/fixture/cc-fixture,42,0\n%7\n"+fixtureSession+"\n"+fix.work+"\n" {
				t.Fatalf("MCP child identity=%q", got)
			}
		})
	}
}
