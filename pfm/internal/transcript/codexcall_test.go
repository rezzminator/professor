package transcript

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexToolOutputIsTheOneFailedCallRule(t *testing.T) {
	for _, test := range []struct {
		name, output, text string
		failed             bool
	}{
		{"a JSON object reads its output and exit code", `"{\"output\":\"ok\\nFAIL x\",\"metadata\":{\"exit_code\":3}}"`, "ok\nFAIL x", true},
		{"exit code zero is no failure", `"{\"output\":\"Success\",\"metadata\":{\"exit_code\":0}}"`, "Success", false},
		{"a printed exit code", `"Chunk ID: 1\nProcess exited with code 2\nOutput:\nvet: bad"`, "Chunk ID: 1\nProcess exited with code 2\nOutput:\nvet: bad", true},
		{"Exit code: 0 passes", `"Exit code: 0\nclean"`, "Exit code: 0\nclean", false},
		{"a failed script", `[{"type":"input_text","text":"Script failed\nError: boom"}]`, "Script failed\nError: boom", true},
		{"a script failure on a later line is not the rule", `"done\nScript failed"`, "done\nScript failed", false},
		{"an apply_patch verification failure", `"apply_patch verification failed: no hunk"`, "apply_patch verification failed: no hunk", true},
		{"a completed script", `[{"type":"input_text","text":"Script completed"}]`, "Script completed", false},
		{"an output member that is null reads None", `"{\"output\":null}"`, "None", false},
		{"a string exit code is nonzero", `"{\"output\":\"x\",\"metadata\":{\"exit_code\":\"0\"}}"`, "x", true},
		{"false equals zero", `"{\"output\":\"x\",\"metadata\":{\"exit_code\":false}}"`, "x", false},
		{
			"JSON lines are text, not a wrapper",
			`"{\"output\":\"x\",\"metadata\":{\"exit_code\":1}}\n{\"b\":2}"`,
			"{\"output\":\"x\",\"metadata\":{\"exit_code\":1}}\n{\"b\":2}", false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			text, failed := CodexToolOutput(json.RawMessage(test.output))
			if text != test.text || failed != test.failed {
				t.Errorf("CodexToolOutput = %q, %v; want %q, %v", text, failed, test.text, test.failed)
			}
		})
	}
	long := strings.Repeat("x", 400) + " Exit code: 1"
	if _, failed := CodexToolOutput(json.RawMessage(`"` + long + `"`)); failed {
		t.Error("an exit code past the first 400 characters counted")
	}
}
