package statusline

import (
	"strings"
	"testing"
)

func TestModelSegmentCarriesEffort(t *testing.T) {
	for _, testcase := range []struct {
		name, payload, want string
	}{
		{name: "with effort", payload: styledPayload, want: "◆ Opus 4·🏎️ high │ "},
		{
			name:    "thinking off",
			payload: strings.Replace(styledPayload, `"enabled":true`, `"enabled":false`, 1),
			want:    "◆ Opus 4·💤 high (off) │ ",
		},
		{
			name:    "without effort",
			payload: strings.Replace(styledPayload, `"effort":{"level":"high"},`, "", 1),
			want:    "◆ Opus 4 │ ",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			l1, _ := renderStyled(t, testcase.payload)
			plain := styleANSI.ReplaceAllString(l1, "")
			if !strings.Contains(plain, testcase.want) {
				t.Fatalf("line 1 = %q, want %q", plain, testcase.want)
			}
		})
	}
}
