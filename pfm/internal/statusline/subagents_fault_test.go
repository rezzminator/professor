package statusline

import (
	"strings"
	"testing"
)

func TestSubagentTaskIDs(t *testing.T) {
	cases := []struct {
		name, raw string
		want      []string
	}{
		{"whole payload", `{"session_id":"s","tasks":[{"id":"a"},{"id":"b","tokenCount":3}]}`, []string{"a", "b"}},
		{"torn after a task", `{"tasks":[{"id":"a"},{"id":"b"},{"id":"c","label":"half`, []string{"a", "b"}},
		{"tasks before other keys", `{"tasks":[{"id":"a"}],"columns":"wide"`, []string{"a"}},
		{"another key first", `{"transcript_path":"/x","cwd":"/y","tasks":[{"id":"a"}`, []string{"a"}},
		{"wrong field type is still a row", `{"tasks":[{"id":"a","tokenCount":"x"},{"id":"b"}]}`, []string{"a", "b"}},
		{"no id, no row", `{"tasks":[{"label":"x"},{"id":""},{"id":"b"}]}`, []string{"b"}},
		{"a repeated id shows once", `{"tasks":[{"id":"a"},{"id":"a"}]}`, []string{"a"}},
		{"no tasks yet", `{"tasks":`, nil},
		{"not an object", `[1,2`, nil},
		{"empty", ``, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := subagentTaskIDs([]byte(tc.raw))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("subagentTaskIDs(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
