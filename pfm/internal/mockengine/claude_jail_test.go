//go:build jail

package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless/run"
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

func TestClaudeHeadlessEnvelopeIsReadByPfmsOwnRunner(t *testing.T) {
	fix := newFixture(t)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepTurn, Reply: "forty-two", Tokens: &Tokens{Input: 11, Output: 4, CacheRead: 100, CacheCreation: 9}},
		{Type: StepTurn, Structured: json.RawMessage(`{"ok":true}`)},
	}})
	machine := pfmconfig.Config{
		Claude:   pfmconfig.ClaudePrefs{Binary: "claude"},
		Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: fix.configDir}},
	}
	result, err := run.Run(context.Background(), run.Request{
		Config:  machine,
		Engine:  pfmengine.Claude,
		Prompt:  "what is the answer",
		CWD:     fix.work,
		Timeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("headless run: %v (stdout=%q stderr=%q)", err, result.Stdout, result.Stderr)
	}
	if result.Answer != "forty-two" || result.ExitCode != 0 || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if result.Usage == nil || result.Usage.Input != 11 || result.Usage.Output != 4 ||
		result.Usage.CachedInput != 100 || result.Usage.CacheCreation != 9 {
		t.Fatalf("usage = %+v, want the step's tokens through modelUsage", result.Usage)
	}
	if result.TotalCostUSD == nil || *result.TotalCostUSD <= 0 {
		t.Fatalf("total cost = %v, want a positive receipt", result.TotalCostUSD)
	}
	if meta := readMeta(t, fix.claudeTranscript(fixtureSession)); meta.HumanPrompts != 1 {
		t.Fatalf("a headless turn left no transcript: %+v", meta)
	}

	structured, err := run.Run(context.Background(), run.Request{
		Config: machine, Engine: pfmengine.Claude, Prompt: "shape it", CWD: fix.work, Timeout: 20 * time.Second,
		Schema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`),
	})
	if err != nil {
		t.Fatalf("structured headless run: %v (stdout=%q)", err, structured.Stdout)
	}
	if string(structured.StructuredOutput) != `{"ok":true}` {
		t.Fatalf("structured_output = %s", structured.StructuredOutput)
	}
}

func TestClaudeHeadlessNoSessionPersistenceWritesNoTranscript(t *testing.T) {
	fix := newFixture(t)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0)})
	machine := pfmconfig.Config{
		Claude:   pfmconfig.ClaudePrefs{Binary: "claude"},
		Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: fix.configDir}},
	}
	result, err := run.Run(context.Background(), run.Request{
		Config: machine, Engine: pfmengine.Claude, Prompt: "ephemeral", CWD: fix.work,
		Timeout: 20 * time.Second, NoSessionPersistence: true,
	})
	if err != nil || result.Answer != DefaultReply {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(fix.claudeTranscript(fixtureSession)); !os.IsNotExist(err) {
		t.Fatalf("a --no-session-persistence run wrote a transcript (stat err=%v)", err)
	}
}

func TestClaudeInlineHeadlessEnvelopeIsReadByPfmsOwnRunner(t *testing.T) {
	for _, directive := range []string{`{"type":"turn","reply":"H1","structured":{"word":"w"}}`, `{"type":"nope"}`, `{"type":`} {
		t.Run(directive, func(t *testing.T) {
			fix := newFixture(t)
			fix.write(
				Scenario{
					SessionID: fixtureSession,
					BusyMS:    intPtr(0),
					Steps:     []Step{{Type: StepTurn, Reply: "positional"}},
				},
			)
			machine := pfmconfig.Config{
				Claude:   pfmconfig.ClaudePrefs{Binary: "claude"},
				Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: fix.configDir}},
			}
			prompt := "headless mock-engine: " + directive
			var schema json.RawMessage
			if strings.Contains(directive, "H1") {
				schema = json.RawMessage(`{"type":"object","properties":{"word":{"type":"string"}}}`)
			}
			result, err := run.Run(context.Background(), run.Request{
				Config: machine, Engine: pfmengine.Claude, Prompt: prompt, CWD: fix.work, Timeout: 20 * time.Second,
				Schema: schema,
			})
			if err != nil || result.ExitCode != 0 || result.IsError {
				t.Fatalf("inline headless=%+v err=%v", result, err)
			}
			if strings.Contains(directive, "H1") {
				if result.Answer != "H1" || string(result.StructuredOutput) != `{"word":"w"}` {
					t.Fatalf("inline envelope=%+v", result)
				}
			} else if !strings.HasPrefix(result.Answer, "mock-engine: inline steps refused — ") {
				t.Fatalf("refusal=%q", result.Answer)
			}
			if _, err := os.Stat(fix.scenario + cursorSuffix); !os.IsNotExist(err) {
				t.Fatalf("headless inline consumed the positional cursor: %v", err)
			}
			entries := parsedEntries(t, fix.claudeTranscript(fixtureSession))
			if len(entries) != 2 || entries[0].Text != prompt {
				t.Fatalf("headless records=%+v", entries)
			}
		})
	}
}

func TestClaudeMCPFailuresAreToolResultsAndKeepThePaneLive(t *testing.T) {
	for _, row := range []struct{ name, config, tool, want string }{
		{"missing server", `{"mcpServers":{}}`, "last", "\"{\\\"content\\\":[{\\\"type\\\":\\\"text\\\",\\\"text\\\":\\\"--mcp-config \\\\\\\"{\\\\\\\\\\\\\\\"mcpServers\\\\\\\\\\\\\\\":{}}\\\\\\\" has no stdio mcpServers.chat-professor command\\\"}],\\\"isError\\\":true}\""},
		{"broken config", `{`, "last", "\"{\\\"content\\\":[{\\\"type\\\":\\\"text\\\",\\\"text\\\":\\\"decode --mcp-config \\\\\\\"{\\\\\\\": unexpected end of JSON input\\\"}],\\\"isError\\\":true}\""},
		{"spawn", `{"mcpServers":{"chat-professor":{"command":"/tmp/mockengine-command-absent"}}}`, "last", "\"{\\\"content\\\":[{\\\"type\\\":\\\"text\\\",\\\"text\\\":\\\"initialize MCP chat-professor with /tmp/mockengine-command-absent: fork/exec /tmp/mockengine-command-absent: no such file or directory\\\"}],\\\"isError\\\":true}\""},
		{"call", "", "unknown_fixture_tool", "\"{\\\"content\\\":[{\\\"type\\\":\\\"text\\\",\\\"text\\\":\\\"tools/call unknown_fixture_tool on MCP chat-professor: calling \\\\\\\"tools/call\\\\\\\": unknown tool \\\\\\\"unknown_fixture_tool\\\\\\\"\\\"}],\\\"isError\\\":true}\""},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			config := row.config
			if config == "" {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				config = string(
					mustJSON(
						t,
						map[string]any{
							"mcpServers": map[string]any{
								"chat-professor": map[string]any{
									"command": executable,
									"args":    []string{verbsStdioProfessorArg},
								},
							},
						},
					),
				)
			}
			fix.write(
				Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{{Type: StepMCP, Tool: row.tool}}},
			)
			session := fix.startTUI("claude", claudeArgs("--mcp-config", config), nil)
			session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			session.typeLine("call the tool")
			session.waitFrame(
				"the turn after the failed tool",
				func(frame string) bool { return strings.Contains(frame, "⏺ ok") },
			)
			content := readFile(t, fix.claudeTranscript(fixtureSession))
			got := strings.ReplaceAll(jsonRecords(t, content, "is_error", true, "content"), fix.root, "{root}")
			if got != row.want {
				t.Fatalf("failure result = %q, want %q", got, row.want)
			}
			session.typeLine("still alive")
			waitFile(
				t,
				fix.claudeTranscript(fixtureSession),
				func(content string) bool { return strings.Contains(content, "still alive") },
			)
		})
	}
}
