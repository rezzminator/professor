package statusline

import (
	"context"
	"io"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestParityRejectsNegativeInput(t *testing.T) {
	r := parityRuntime(t)
	for _, raw := range []string{
		`{"context_window":{"current_usage":{"input_tokens":-1}}}`,
		`{"rate_limits":{"five_hour":{"used_percentage":101}}}`,
		`{"rate_limits":{"limits":[{"kind":"weekly_scoped","percent":-1,"is_active":true,"resets_at":"2030-01-01T00:00:00Z","scope":{"model":{"display_name":"Fable"}}}]}}`,
	} {
		if _, err := Render(context.Background(), []byte(raw), r); err == nil {
			t.Errorf("accepted invalid input %s", raw)
		}
	}
}

func TestSubagentRejectsNegativeContext(t *testing.T) {
	r := parityRuntime(t)
	if _, err := RenderSubagents(
		[]byte(`{"tasks":[{"id":"bad","tokenCount":-1}]}`),
		r.now(),
		r.SIDDir,
		nil,
		r.Home,
		&paths.MapEnv{Values: r.Env},
		"",
		io.Discard,
	); err == nil {
		t.Fatal("accepted negative subagent counter")
	}
}
