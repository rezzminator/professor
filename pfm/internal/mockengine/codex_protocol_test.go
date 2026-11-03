package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless/run"
	"github.com/rezzminator/professor/pfm/internal/statusline"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

func TestCodexHeadlessDoorsAreRefusedByName(t *testing.T) {
	fix := newFixture(t)
	for _, args := range [][]string{{"mcp-server"}, {"app-server"}} {
		code, _, stderr := runOnce(fix, "codex", args, "")
		if code != ExitUnpinned || !strings.Contains(stderr, "codex "+args[0]) ||
			!strings.Contains(stderr, "unpinned") {
			t.Fatalf("codex %v exit=%d stderr=%q, want %d naming the door", args, code, stderr, ExitUnpinned)
		}
		if args[0] == "app-server" && !strings.Contains(stderr, "rate_limits") {
			t.Fatalf("unscripted app-server must name rate_limits: %q", stderr)
		}
	}
}

const fixtureRateLimits = `{"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":4102444800},"secondary":{"usedPercent":34,"windowDurationMins":10080,"resetsAt":4102444800},"planType":"plus"}`

func TestCodexAppServerFeedsPfmsStatuslineCache(t *testing.T) {
	fix := newFixture(t)
	setScenarioField(t, fix, "rate_limits", json.RawMessage(fixtureRateLimits))
	setScenarioField(t, fix, "idle_statusline_refresh", map[string]int{"count": 1, "interval_ms": 20})
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
		var gotLimits, wantLimits any
		if i == 1 {
			if err := json.Unmarshal(reply.Result.RateLimits, &gotLimits); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(fixtureRateLimits), &wantLimits); err != nil {
				t.Fatal(err)
			}
		}
		if reply.ID != i || (i == 1 && !reflect.DeepEqual(gotLimits, wantLimits)) ||
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

func TestCodexExecInlineAndStdinPrompts(t *testing.T) {
	for _, directive := range []string{`{"type":"turn","reply":"inline"}`, `{"type":"nope"}`} {
		for _, stdin := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stdin=%t", directive, stdin), func(t *testing.T) {
				fix := newFixture(t)
				fix.write(
					Scenario{
						SessionID: fixtureThread,
						BusyMS:    intPtr(0),
						Steps:     []Step{{Type: StepTurn, Reply: "positional"}},
					},
				)
				prompt := "query mock-engine: " + directive
				args, input := []string{"exec", "--json", "--ephemeral"}, ""
				if stdin {
					input = prompt
				} else {
					args = append(args, prompt)
				}
				code, stdout, stderr := runOnce(fix, "codex", args, input)
				if strings.Contains(directive, "nope") {
					want := "mock-engine: inline steps refused — step 0: unknown step type \"nope\"\n"
					if code != ExitUnpinned || stderr != want {
						t.Fatalf("invalid inline exit=%d stderr=%q, want %d %q", code, stderr, ExitUnpinned, want)
					}
					return
				}
				if code != 0 {
					t.Fatalf("exec exit=%d stderr=%s", code, stderr)
				}
				var answer string
				for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
					var event struct{ Item struct{ Text string } }
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatal(err)
					}
					if event.Item.Text != "" {
						answer = event.Item.Text
					}
				}
				if answer != "inline" {
					t.Fatalf("answer=%q", answer)
				}
			})
		}
	}
}

func TestCodexExecPersistenceFollowsEphemeral(t *testing.T) {
	for _, ephemeral := range []bool{false, true} {
		t.Run(fmt.Sprint(ephemeral), func(t *testing.T) {
			fix := newFixture(t)
			scenario := Scenario{SessionID: fixtureThread, BusyMS: intPtr(0), Reply: "stored reply"}
			if ephemeral {
				scenario.Steps = []Step{{Type: StepBackgroundAgent, Name: "rr", Status: "done"}, {Type: StepTurn}}
			}
			fix.write(scenario)
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

func TestCodexExecRefusesExhaustedScenario(t *testing.T) {
	fix := newFixture(t)
	fix.write(Scenario{SessionID: fixtureThread, Steps: []Step{}})
	code, stdout, stderr := runOnce(fix, "codex", []string{"exec", "--json", "--ephemeral", "unscripted"}, "")
	wantError := "mock-engine: unscripted request: scenario exhausted\n"
	wantOutput := `{"thread_id":"` + fixtureThread + `","type":"thread.started"}` + "\n" + `{"type":"turn.started"}` + "\n"
	if code != ExitUnpinned || stderr != wantError {
		t.Fatalf("unscripted exit=%d stderr=%q, want %d %q (stdout=%s)", code, stderr, ExitUnpinned, wantError, stdout)
	}
	if stdout != wantOutput {
		t.Fatalf("unscripted stdout=%q, want %q", stdout, wantOutput)
	}
}

func TestCodexExecRunsScriptedHooks(t *testing.T) {
	fix := newFixture(t)
	captured := filepath.Join(fix.recordDir, "exec-hooks")
	settings := map[string]any{}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "SessionEnd"} {
		command := `printf '%s\n' '` + event + `' >> "` + captured + `"`
		if event == "Stop" {
			command += `; cat > "` + filepath.Join(fix.recordDir, "exec-stop.json") + `"`
		}
		settings[event] = commandHook(command)
	}
	writeHookSettings(t, filepath.Join(fix.codexHome, "hooks.json"), settings)
	fix.write(
		Scenario{
			SessionID: fixtureThread,
			Steps: []Step{
				{Type: StepToolCall, Tool: "Bash", Input: json.RawMessage(`{"command":"printf atlas"}`)},
				{Type: StepTurn, Reply: "finished"},
			},
		},
	)
	code, _, stderr := runOnce(fix, "codex", []string{"exec", "--json", "--ephemeral", "atlas check"}, "")
	if code != 0 || stderr != "" {
		t.Fatalf("exec exit=%d stderr=%q, want 0 and empty", code, stderr)
	}
	raw, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	want := "SessionStart\nUserPromptSubmit\nPreToolUse\nPostToolUse\nStop\nSessionEnd\n"
	if string(raw) != want {
		t.Fatalf("%s: hooks=%q, want %q", captured, raw, want)
	}
	stopRaw, err := os.ReadFile(filepath.Join(fix.recordDir, "exec-stop.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stop map[string]any
	if err := json.Unmarshal(stopRaw, &stop); err != nil {
		t.Fatal(err)
	}
	if actual := stop["last_assistant_message"]; actual != "finished" {
		t.Fatalf("exec-stop.json: last_assistant_message=%v, want finished (raw=%s)", actual, stopRaw)
	}
}
