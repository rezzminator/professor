package installer

import (
	"slices"
	"testing"
)

// TestHerdrKeysMissingReadsEveryShapeABindingTakes covers a binding as one
// key, a list of keys, and a command table among others.
func TestHerdrKeysMissingReadsEveryShapeABindingTakes(t *testing.T) {
	const command = `[[keys.command]] key = "prefix+a" (new pfm chat in this space)`
	cases := []struct {
		name string
		keys map[string]any
		want []string
	}{
		{
			name: "no keys table",
			keys: nil,
			want: []string{`previous_workspace = "prefix+i"`, `next_workspace = "prefix+m"`, command},
		},
		{
			name: "strings and a command table",
			keys: map[string]any{
				"previous_workspace": "prefix+i", "next_workspace": "prefix+m",
				"command": []map[string]any{{"key": "prefix+z"}, {"key": "prefix+a"}},
			},
		},
		{
			name: "lists and a command among others",
			keys: map[string]any{
				"previous_workspace": []any{"cmd+shift+up", "prefix+i"}, "next_workspace": []any{"cmd+shift+down"},
				"command": []any{map[string]any{"key": "prefix+z"}},
			},
			want: []string{`next_workspace = "prefix+m"`, command},
		},
	}
	for _, testCase := range cases {
		if got := herdrKeysMissing(testCase.keys); !slices.Equal(got, testCase.want) {
			t.Errorf("%s: herdrKeysMissing = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}
