package mockengine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless/run"
)

func openCodeHeadlessRequest(fix *fixture, prompt string) run.Request {
	home := filepath.Join(fix.root, "opencode")
	if err := os.MkdirAll(home, 0o700); err != nil {
		fix.t.Fatal(err)
	}
	return run.Request{
		Config: pfmconfig.Config{
			OpenCode:         pfmconfig.OpenCodePrefs{Binary: filepath.Join(fix.bin, "opencode")},
			OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 7, Home: home}},
		},
		Engine: pfmengine.OpenCode, Prompt: prompt, CWD: fix.work,
		TempDir: fix.root, Timeout: 5 * time.Second,
	}
}

func TestOpenCodeServeIsReadByPfmsOwnRunner(t *testing.T) {
	for _, row := range []struct {
		name, prompt string
		step         Step
		schema       bool
	}{
		{"answer", "what is the answer", Step{Type: StepTurn, Reply: "forty-two", Tokens: &Tokens{Input: 11, Output: 4, CacheRead: 100, CacheCreation: 9}}, false},
		{"inline", `mock-engine: {"type":"turn","reply":"inline"}`, Step{Type: StepTurn}, false},
		{"refused inline", `mock-engine: {"type":"nope"}`, Step{Type: StepTurn}, false},
		{"crash", "crash now", Step{Type: StepCrash, ExitCode: 17}, false},
		{"schema", "format now", Step{Type: StepTurn}, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			fix.write(Scenario{BusyMS: intPtr(0), Steps: []Step{row.step}})
			request := openCodeHeadlessRequest(fix, row.prompt)
			if row.schema {
				request.Schema = []byte(`{"type":"object"}`)
			}
			result, err := run.Run(context.Background(), request)
			switch row.name {
			case "refused inline":
				if err == nil || result.ExitCode != ExitUnpinned {
					t.Fatalf("unscripted inline result=%+v err=%v", result, err)
				}
			case "crash":
				if err == nil || result.ExitCode != 17 {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			case "schema":
				if err == nil || !strings.Contains(err.Error(), "HTTP status 501") ||
					!strings.Contains(err.Error(), "mock-engine: opencode schema seeding is unpinned") ||
					strings.Contains(err.Error(), "remove OpenCode schema seed session") {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			default:
				if err != nil || result.ExitCode != 0 || result.IsError {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				want := row.step.Reply
				if row.name == "inline" {
					want = "inline"
				}
				if row.name == "refused inline" {
					want = "mock-engine: inline steps refused — "
				}
				if !strings.Contains(result.Answer, want) {
					t.Fatalf("answer=%q want %q", result.Answer, want)
				}
				if row.name == "answer" &&
					(result.Usage == nil || result.Usage.Input != 11 || result.Usage.Output != 4 || result.Usage.CachedInput != 100 || result.Usage.CacheCreation != 9 || result.TotalCostUSD == nil) {
					t.Fatalf("usage=%+v cost=%v", result.Usage, result.TotalCostUSD)
				}
			}
		})
	}
}

func TestOpenCodeServeRefusesExhaustedScript(t *testing.T) {
	fix := newFixture(t)
	fix.write(Scenario{Steps: []Step{}})
	result, err := run.Run(context.Background(), openCodeHeadlessRequest(fix, "unscripted prompt"))
	if err == nil || result.ExitCode != ExitUnpinned || !strings.Contains(err.Error(), "unscripted") {
		t.Fatalf("exhausted script: result=%+v error=%v, want exit 3 naming unscripted request", result, err)
	}
}
