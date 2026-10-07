package statusline

import (
	"strings"
	"testing"
)

// A row too wide for its panel gives parts up rank by rank, cwd first, the
// effort last; nesting, gauge, identity, status and label always stay.
func TestFitRowDropsByRank(t *testing.T) {
	parts := func() []rowPart {
		return []rowPart{
			{text: "solo"},
			{text: "gauge $1/2/3"},
			{text: "scout"},
			{text: "opus·high", short: "opus", drop: dropEffort},
			{text: "running"},
			{text: "cache", drop: dropCache},
			{text: "⟲1", drop: dropCompactions},
			{text: "pfm", drop: dropCwd},
			{text: "label"},
		}
	}
	full := "solo│gauge $1/2/3│scout│opus·high│running│cache│⟲1│pfm│label"
	cases := []struct {
		name    string
		columns int
		want    string
	}{
		{"no width in the payload keeps the full row", 0, full},
		{"a row that fits keeps every part", len([]rune(full)) + rowPrefixWidth, full},
		{
			"one column short: the cwd goes first", len([]rune(full)) + rowPrefixWidth - 1,
			"solo│gauge $1/2/3│scout│opus·high│running│cache│⟲1│label",
		},
		{
			"narrow: everything ranked goes, the effort shortens to the family", 40,
			"solo│gauge $1/2/3│scout│opus│running│label",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripANSICodes(fitRow(parts(), tc.columns)); got != tc.want {
				t.Fatalf("fitRow(%d) =\n  %q\nwant\n  %q", tc.columns, got, tc.want)
			}
		})
	}
}

// The payload's columns reach the row: a narrow panel drops cache and cwd.
func TestRenderSubagentsFitsColumns(t *testing.T) {
	session := subagentSession(t, map[string][]string{"a1": agentTranscriptLines}, map[string]string{"a1": "tracer"})
	task := `{"id":"a1","type":"local_agent","status":"running","label":"map","cwd":"/work/repo/pfm",` +
		`"model":"claude-opus-5-5","effort":"high","contextWindowSize":1000000,"tokenCount":312000,"startTime":` +
		jsonText(subagentStart.UnixMilli()) + `}`
	got, _ := renderOneSubagentColumns(t, session, task, 100)
	if strings.Contains(got, "💾") || strings.Contains(got, "│pfm│") || !strings.HasSuffix(got, "│map") {
		t.Fatalf("content = %q, want cache and cwd dropped, the label kept", got)
	}
}
