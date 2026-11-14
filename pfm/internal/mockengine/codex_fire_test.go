package mockengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexappendix"
)

// codexHandler is one hooks.json handler a test registers: its event in the document's spelling.
type codexHandler struct{ event, matcher, command string }

// writeCodexHooks writes handlers as the account's hooks.json and records the trust of those whose
// trust is true through pfm's own trust door against the fake Codex's app-server, as pfm install does.
func writeCodexHooks(t *testing.T, fix *fixture, handlers []codexHandler, trust []bool) {
	t.Helper()
	document := map[string][]any{}
	for _, handler := range handlers {
		document[handler.event] = append(document[handler.event], map[string]any{
			"matcher": handler.matcher,
			"hooks":   []any{map[string]any{"type": "command", "command": handler.command}},
		})
	}
	path := filepath.Join(fix.codexHome, "hooks.json")
	if err := os.WriteFile(path, mustJSON(t, map[string]any{"hooks": document}), 0o600); err != nil {
		t.Fatal(err)
	}
	var trusted []codexappendix.Handler
	for index, handler := range handlers {
		if trust[index] {
			event := strings.ToLower(handler.event[:1]) + handler.event[1:]
			trusted = append(
				trusted,
				codexappendix.Handler{Event: event, Matcher: handler.matcher, Command: handler.command},
			)
		}
	}
	if len(trusted) == 0 {
		return
	}
	setScenarioField(t, fix, "rate_limits", json.RawMessage(fixtureRateLimits))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := codexappendix.RegisterHookTrusts(
		ctx,
		filepath.Join(fix.bin, "codex"),
		fix.codexHome,
		trusted,
	); err != nil {
		t.Fatalf("record the trust of %v: %v", trusted, err)
	}
}

// codexToolOutputs is every function_call_output of the rollout at path, keyed by its call's command.
func codexToolOutputs(t *testing.T, path string) map[string]string {
	t.Helper()
	commands, outputs := map[string]string{}, map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, path)), "\n") {
		var record struct {
			Payload struct {
				Type      string `json:"type"`
				Arguments string `json:"arguments"`
				CallID    string `json:"call_id"`
				Output    string `json:"output"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode %s: %v; %q", path, err, line)
		}
		switch record.Payload.Type {
		case "function_call":
			var input struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal([]byte(record.Payload.Arguments), &input); err != nil {
				t.Fatalf("decode the arguments in %s: %v; %q", path, err, line)
			}
			commands[record.Payload.CallID] = input.Command
		case "function_call_output":
			outputs[commands[record.Payload.CallID]] = record.Payload.Output
		}
	}
	return outputs
}
