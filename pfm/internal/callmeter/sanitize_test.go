package callmeter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeInput(t *testing.T) {
	long := strings.Repeat("x", LongField+1)
	longCommand := "echo " + long
	cases := []struct {
		name, tool, raw, want string
	}{
		{
			"write drops content",
			"Write",
			`{"file_path":"/tmp/demo-proj/a.txt","content":"héllo"}`,
			`{"content_bytes":6,"file_path":"/tmp/demo-proj/a.txt"}`,
		},
		{
			"bash keeps command and description",
			"Bash",
			`{"command":"wc -l a.txt","description":"Count lines","timeout":5000}`,
			`{"command":"wc -l a.txt","description":"Count lines","timeout":5000}`,
		},
		{"bash keeps a long command", "Bash", `{"command":"` + longCommand + `"}`, `{"command":"` + longCommand + `"}`},
		{
			"edit drops strings",
			"Edit",
			`{"file_path":"/tmp/demo-proj/a.go","old_string":"ab","new_string":"abc","replace_all":true}`,
			`{"file_path":"/tmp/demo-proj/a.go","new_string_bytes":3,"old_string_bytes":2,"replace_all":true}`,
		},
		{
			"multiedit drops edits",
			"MultiEdit",
			`{"file_path":"/tmp/demo-proj/a.go","edits":[{"old_string":"a","new_string":"b"}]}`,
			`{"edits_bytes":37,"file_path":"/tmp/demo-proj/a.go"}`,
		},
		{
			"notebook drops new_source",
			"NotebookEdit",
			`{"notebook_path":"/tmp/demo-proj/n.ipynb","new_source":"x=1"}`,
			`{"new_source_bytes":3,"notebook_path":"/tmp/demo-proj/n.ipynb"}`,
		},
		{
			"agent drops prompt",
			"Agent",
			`{"description":"scan","prompt":"do it","subagent_type":"general"}`,
			`{"description":"scan","prompt_bytes":5,"subagent_type":"general"}`,
		},
		{
			"read keeps range",
			"Read",
			`{"file_path":"/tmp/demo-proj/a.go","offset":10,"limit":20}`,
			`{"file_path":"/tmp/demo-proj/a.go","limit":20,"offset":10}`,
		},
		{
			"grep keeps pattern",
			"Grep",
			`{"pattern":"func [A-Z]","path":"/tmp/demo-proj","glob":"*.go"}`,
			`{"glob":"*.go","path":"/tmp/demo-proj","pattern":"func [A-Z]"}`,
		},
		{
			"other long field is sized",
			"WebFetch",
			`{"url":"https://example.com","query":"` + long + `"}`,
			`{"query_bytes":4097,"url":"https://example.com"}`,
		},
		{"non-bash long command is sized", "Custom", `{"command":"` + longCommand + `"}`, `{"command_bytes":4102}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SanitizeInput(c.tool, json.RawMessage(c.raw))
			if err != nil {
				t.Fatalf("SanitizeInput: %v", err)
			}
			if got != c.want {
				t.Fatalf("SanitizeInput = %s\nwant             %s", got, c.want)
			}
		})
	}
}

func TestSanitizeInputRejectsNonObject(t *testing.T) {
	for _, raw := range []string{``, `null`, `[1]`, `"text"`, `{"broken"`} {
		if got, err := SanitizeInput("Bash", json.RawMessage(raw)); err == nil {
			t.Errorf("SanitizeInput(%q) = %q, want an error", raw, got)
		}
	}
}
