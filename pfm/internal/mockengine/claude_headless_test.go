package mockengine

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless/run"
)

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
	fix.write(Scenario{SessionID: fixtureSession, Steps: []Step{{Type: StepTurn, Reply: "plain", Tokens: &Tokens{}}}})
	setScenarioField(t, fix, "headless_usage_error", true)
	partial, err := run.Run(context.Background(), run.Request{
		Config: machine, Engine: pfmengine.Claude, Prompt: "partial usage", CWD: fix.work, Timeout: 20 * time.Second,
	})
	wantDiagnostics := []string{
		"could not read Claude usage: json: cannot unmarshal string into Go value of type map[string]jsontext.Value",
	}
	if err != nil || partial.Answer != "plain" || partial.ExitCode != 0 || partial.TotalCostUSD == nil ||
		*partial.TotalCostUSD != 0.0001 || !reflect.DeepEqual(partial.Diagnostics, wantDiagnostics) {
		t.Fatalf("partial=%+v err=%v want plain, exit0, cost0.0001, diagnostics=%q", partial, err, wantDiagnostics)
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
			if !strings.Contains(directive, "H1") {
				expected := "mock-engine: inline steps refused — decode step: unexpected EOF\n"
				if directive == `{"type":"nope"}` {
					expected = "mock-engine: inline steps refused — step 0: unknown step type \"nope\"\n"
				}
				if err == nil || result.ExitCode != ExitUnpinned || !result.IsError || result.Stderr != expected ||
					result.Answer != "" {
					t.Fatalf("inline refusal=%+v err=%v, want exit=%d stderr=%q", result, err, ExitUnpinned, expected)
				}
				entries := parsedEntries(t, fix.claudeTranscript(fixtureSession))
				if len(entries) != 1 || entries[0].Text != prompt {
					t.Fatalf("refused headless records=%+v, want submitted prompt only", entries)
				}
				return
			}
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
