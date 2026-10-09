package transcript

import (
	"strings"
	"testing"
)

func TestTheLastTitleRecordOfEitherKindNamesTheChat(t *testing.T) {
	for _, test := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"a rename keeps only its last title", []string{
			`{"type":"custom-title","customTitle":"Old Desk"}`, `{"type":"agent-name","agentName":"Old Desk"}`,
			`{"type":"custom-title","customTitle":"New Desk"}`,
		}, "New Desk"},
		{"an agent name alone names the chat", []string{`{"type":"agent-name","agentName":"Night Shift"}`}, "Night Shift"},
		{"an agent name after a custom title wins", []string{
			`{"type":"custom-title","customTitle":"Desk"}`, `{"type":"agent-name","agentName":"Agent"}`,
		}, "Agent"},
		{"an empty or non-string name names nothing", []string{
			`{"type":"custom-title","customTitle":"Kept"}`, `{"type":"custom-title","customTitle":""}`,
			`{"type":"agent-name","agentName":7}`, `{"type":"custom-title","agentName":"wrong member"}`,
		}, "Kept"},
		{"a cut first line and junk are skipped", []string{`le":"Cut"}`, `{not json`, `{"type":"user"}`}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := TailTitle([]byte(strings.Join(test.lines, "\n") + "\n")); got != test.want {
				t.Errorf("TailTitle = %q, want %q", got, test.want)
			}
		})
	}
}
