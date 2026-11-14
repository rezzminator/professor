package statusline

import (
	"reflect"
	"testing"
	"time"
)

func TestAgentEndsFoldLine(t *testing.T) {
	var zero time.Time
	stamp := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		line string
		want map[string]time.Time
	}{
		{"completed", endNotice("a", "completed"), map[string]time.Time{"a": zero}},
		{"failed", endNotice("a", "failed"), map[string]time.Time{"a": zero}},
		{"killed", endNotice("a", "killed"), map[string]time.Time{"a": zero}},
		{
			"a stamped line ends at its timestamp",
			endNoticeAt("a", "completed", stamp),
			map[string]time.Time{"a": stamp},
		},
		{"a running status ends nothing", endNotice("a", "running"), map[string]time.Time{}},
		{
			"two notices on one line",
			endNotice("a", "completed") + endNotice("b", "killed"),
			map[string]time.Time{"a": zero, "b": zero},
		},
		{
			"a status belongs to the task id before it",
			`<task-id>a</task-id> <task-id>b</task-id><status>completed</status>`,
			map[string]time.Time{"b": zero},
		},
		{"an unclosed task id", `<task-id>a<status>completed</status>`, map[string]time.Time{}},
		{"an unclosed status", `<task-id>a</task-id><status>completed`, map[string]time.Time{}},
		{"a line naming no task", `{"type":"user","message":{"content":"go"}}`, map[string]time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fold := &agentEndsFold{}
			fold.reset()
			fold.line([]byte(tc.line))
			if !reflect.DeepEqual(fold.Ended, tc.want) {
				t.Fatalf("ended = %v, want %v for %q", fold.Ended, tc.want, tc.line)
			}
		})
	}
}
