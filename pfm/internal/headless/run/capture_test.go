package run

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// An engine's answer, terminal event and error line come last: a stream past the capture limit
// costs its oldest bytes, never the parsed answer of a finished, paid run.
func TestRunKeepsTheAnswerPastTheCaptureLimit(t *testing.T) {
	headlessJail(t)
	answer := `printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"kept"}}' '{"type":"turn.completed"}'`
	cases := []struct {
		name        string
		body        string
		diagnostics []string
	}{
		{
			name: "stderr",
			body: `head -c 9000000 /dev/zero | tr '\0' x >&2; ` + answer,
			diagnostics: []string{
				"stderr exceeded the 8388608-byte capture limit; only its last 8388608 bytes were kept",
			},
		},
		{
			name: "stdout",
			body: `head -c 9000000 /dev/zero | tr '\0' x; echo; ` + answer,
			diagnostics: []string{
				"stdout exceeded the 8388608-byte capture limit; only its last 8388608 bytes were kept",
				"could not read Codex JSONL event 1: invalid character 'x' looking for beginning of value",
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			binary := writeEngineStub(t, testCase.body)
			result, err := Run(context.Background(), Request{
				Config: codexMachine(
					binary,
					filepath.Join(t.TempDir(), "cx"),
				),
				Engine: pfmengine.Codex,
				Prompt: "hello",
			})
			if err != nil {
				t.Fatalf("Run() error = %v, want the answer kept", err)
			}
			if result.Answer != "kept" {
				t.Fatalf("answer = %q, want kept", result.Answer)
			}
			if !reflect.DeepEqual(result.Diagnostics, testCase.diagnostics) {
				t.Fatalf("diagnostics = %#v, want %#v", result.Diagnostics, testCase.diagnostics)
			}
		})
	}
}
