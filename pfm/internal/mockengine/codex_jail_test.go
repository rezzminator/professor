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

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless/run"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
	"github.com/rezzminator/professor/pfm/internal/statusline"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/transcript"
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

func TestCodexExecIsReadByPfmsOwnRunner(t *testing.T) {
	for _, row := range []struct {
		name   string
		step   Step
		schema json.RawMessage
	}{
		{"answer", Step{Type: StepTurn, Reply: "forty-two", Tokens: &Tokens{Input: 11, Output: 4, CacheRead: 100, CacheCreation: 9}}, nil},
		{"structured", Step{Type: StepTurn, Structured: json.RawMessage(`{"ok":true}`)}, json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`)},
		{"crash", Step{Type: StepCrash, ExitCode: 17}, nil},
		{"missing terminal", Step{Type: StepCrash}, nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			fix.write(Scenario{SessionID: fixtureThread, BusyMS: intPtr(0), Steps: []Step{row.step}})
			request := codexHeadlessRequest(fix, "what is the answer")
			request.Schema = row.schema
			result, err := run.Run(context.Background(), request)
			if row.step.Type == StepCrash {
				cause := "missing terminal success event"
				if row.step.ExitCode != 0 {
					cause = "exit status 17"
				}
				if err == nil || result.ExitCode != row.step.ExitCode || !strings.Contains(err.Error(), cause) ||
					!strings.Contains(result.Stdout, `"type":"turn.started"`) ||
					strings.Contains(result.Stdout, `"type":"turn.completed"`) {
					t.Fatalf("crash result=%+v err=%v", result, err)
				}
				return
			}
			if err != nil || result.ExitCode != 0 || result.IsError {
				t.Fatalf("headless result=%+v err=%v", result, err)
			}
			if row.schema != nil {
				var object map[string]bool
				if err := json.Unmarshal(
					result.StructuredOutput,
					&object,
				); err != nil || len(object) != 1 ||
					!object["ok"] {
					t.Fatalf("structured output=%s err=%v", result.StructuredOutput, err)
				}
			} else if result.Answer != "forty-two" || result.Usage == nil || result.Usage.Input != 11 || result.Usage.Output != 4 || result.Usage.CachedInput != 100 || result.Usage.CacheCreation != 9 {
				t.Fatalf("answer/usage=%+v", result)
			}
			if !strings.HasPrefix(result.Stdout, `{"thread_id":"`+fixtureThread+`","type":"thread.started"}`) &&
				!strings.HasPrefix(result.Stdout, `{"type":"thread.started","thread_id":"`+fixtureThread+`"}`) {
				t.Fatalf("first event=%q", result.Stdout)
			}
		})
	}
}

func TestCodexExecPersistenceFollowsEphemeral(t *testing.T) {
	for _, ephemeral := range []bool{false, true} {
		t.Run(fmt.Sprint(ephemeral), func(t *testing.T) {
			fix := newFixture(t)
			fix.write(Scenario{SessionID: fixtureThread, BusyMS: intPtr(0), Reply: "stored reply"})
			request := codexHeadlessRequest(fix, "persist this exchange")
			request.NoSessionPersistence = ephemeral
			result, err := run.Run(context.Background(), request)
			if err != nil || result.Answer != "stored reply" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if ephemeral {
				for _, name := range []string{"sessions", codexmeta.SessionIndexFile} {
					if _, err := os.Stat(filepath.Join(fix.codexHome, name)); !os.IsNotExist(err) {
						t.Fatalf("ephemeral wrote %s: %v", name, err)
					}
				}
				return
			}
			rows, names := fix.indexedRollouts()
			if len(rows) != 1 || rows[0].ID != fixtureThread || rows[0].PromptCount != 1 ||
				names[fixtureThread] != "persist this exchange" {
				t.Fatalf("rollouts=%+v names=%v", rows, names)
			}
			meta, err := transcript.ReadMeta(fix.rolloutPath(), string(pfmengine.Codex))
			if err != nil || meta.Model != DefaultCodexModel || meta.ContextTokens != 15 {
				t.Fatalf("rollout meta=%+v err=%v", meta, err)
			}
		})
	}
}

func TestCodexAppServerFeedsPfmsStatuslineCache(t *testing.T) {
	fix := newFixture(t)
	setScenarioField(t, fix, "rate_limits", json.RawMessage(fixtureRateLimits))
	frames := "{\"jsonrpc\":\"2.0\",\"id\":0,\"method\":\"initialize\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"method\":\"initialized\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"account/rateLimits/read\"}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"fixture/unknown\"}\n"
	code, stdout, stderr := runOnce(fix, "codex", []string{"app-server"}, frames)
	if code != 0 || stderr != "" {
		t.Fatalf("app-server EOF exit=%d stderr=%q", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("requests produced %d replies: %q", len(lines), stdout)
	}
	for i, line := range lines {
		var reply struct {
			ID     int
			Result struct{ RateLimits json.RawMessage }
			Error  struct {
				Code    int
				Message string
			}
		}
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			t.Fatal(err)
		}
		if reply.ID != i || (i == 1 && string(reply.Result.RateLimits) != fixtureRateLimits) ||
			(i == 2 && (reply.Error.Code != -32601 || !strings.Contains(reply.Error.Message, "fixture/unknown"))) {
			t.Fatalf("reply %d = %s", i, line)
		}
	}
	cache := filepath.Join(fix.root, "rate-limits.json")
	if err := statusline.RefreshCodex(context.Background(), statusline.CodexOptions{
		CachePath: cache, Binary: filepath.Join(fix.bin, "codex"),
	}); err != nil {
		t.Fatalf("pfm statusline refresh: %v", err)
	}
	var got struct {
		Primary struct {
			UsedPercent        int
			WindowDurationMins int
			ResetsAt           int64
		}
		Secondary struct {
			UsedPercent        int
			WindowDurationMins int
			ResetsAt           int64
		}
		PlanType string
	}
	if err := json.Unmarshal([]byte(readFile(t, cache)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Primary.UsedPercent != 12 || got.Primary.WindowDurationMins != 300 || got.Primary.ResetsAt != 4102444800 ||
		got.Secondary.UsedPercent != 34 || got.Secondary.WindowDurationMins != 10080 || got.PlanType != "plus" {
		t.Fatalf("pfm cache = %+v", got)
	}
}

func TestCodexMCPStepHandshakesWithPfmsOwnServer(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeCodexConfig(t, fix, fmt.Sprintf("[mcp_servers.chat-professor]\ncommand = %q\nargs = [%q]\n",
		executable, stdioProfessorArg))
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepMCP},
		{Type: StepTurn, Reply: "tools listed"},
	}})
	session := fix.startTUI("codex", codexArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "›") })
	session.typeLine("list the fleet tools")
	session.waitFrame("the reply after the handshake", func(frame string) bool {
		return strings.Contains(frame, "tools listed")
	})
	recorded := waitFile(t, filepath.Join(fix.recordDir, "mcp-chat-professor.json"), func(content string) bool {
		return strings.Contains(content, "whoami")
	})
	var tools []string
	if err := json.Unmarshal([]byte(recorded), &tools); err != nil {
		t.Fatal(err)
	}
	if want := mcpserv.ToolNames(); strings.Join(tools, ",") != strings.Join(want, ",") {
		t.Fatalf("tools/list from pfm's stdio server = %v, want %v", tools, want)
	}
}

func TestCodexMCPFailuresAreRecordedShownAndKeepPaneLive(t *testing.T) {
	for _, row := range []struct{ name, table, tool, cause, result, output string }{
		{"missing server", "", "last", "professor", "{\"content\":[{\"type\":\"text\",\"text\":\"{root}/codex/config.toml has no [mcp_servers.chat-professor] command\"}],\"isError\":true}\n", "\"{\\\"content\\\":[{\\\"type\\\":\\\"text\\\",\\\"text\\\":\\\"{root}/codex/config.toml has no [mcp_servers.chat-professor] command\\\"}],\\\"isError\\\":true}\""},
		{"call", "configured", "unknown_fixture_tool", "tools/call", "{\"content\":[{\"type\":\"text\",\"text\":\"tools/call unknown_fixture_tool on MCP chat-professor: calling \\\"tools/call\\\": unknown tool \\\"unknown_fixture_tool\\\"\"}],\"isError\":true}\n", "\"{\\\"content\\\":[{\\\"type\\\":\\\"text\\\",\\\"text\\\":\\\"tools/call unknown_fixture_tool on MCP chat-professor: calling \\\\\\\"tools/call\\\\\\\": unknown tool \\\\\\\"unknown_fixture_tool\\\\\\\"\\\"}],\\\"isError\\\":true}\""},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			table := row.table
			if table == "configured" {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				table = fmt.Sprintf(
					"[mcp_servers.chat-professor]\ncommand = %q\nargs = [%q]\n",
					executable,
					verbsStdioProfessorArg,
				)
			}
			writeCodexConfig(t, fix, table)
			fix.write(
				Scenario{
					SessionID: fixtureThread,
					Pane:      codexShapes,
					BusyMS:    intPtr(0),
					Steps:     []Step{{Type: StepMCP, Tool: row.tool}},
				},
			)
			session := fix.startTUI("codex", codexArgs(), nil)
			session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "›") })
			session.typeLine("call the tool")
			session.waitFrame("failed tool and completed turn", func(frame string) bool {
				return strings.Contains(frame, "⏺ ok") && strings.Contains(frame, row.cause)
			})
			result := readFile(t, filepath.Join(fix.recordDir, "mcp-call-"+row.tool+".json"))
			if got := strings.ReplaceAll(result, fix.root, "{root}"); got != row.result {
				t.Fatalf("failed result = %q, want %q", got, row.result)
			}
			output := jsonRecords(t, readFile(t, fix.rolloutPath()), "type", "function_call_output", "output")
			if got := strings.ReplaceAll(output, fix.root, "{root}"); got != row.output {
				t.Fatalf("failed rollout output = %q, want %q", got, row.output)
			}
			session.typeLine("still alive")
			waitFile(
				t,
				fix.rolloutPath(),
				func(content string) bool { return strings.Contains(content, "still alive") },
			)
		})
	}
}
