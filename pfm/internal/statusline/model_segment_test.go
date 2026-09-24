package statusline

import (
	"strings"
	"testing"
)

func TestModelSegmentCarriesEffort(t *testing.T) {
	for _, testcase := range []struct {
		name, payload, want, reject string
	}{
		{name: "with effort", payload: styledPayload, want: "◆ Opus 4·● high │ ", reject: "💠"},
		{
			name:    "thinking off",
			payload: strings.Replace(styledPayload, `"enabled":true`, `"enabled":false`, 1),
			want:    "◆ Opus 4·● high (off) │ ", reject: "💤",
		},
		{
			name:    "without effort",
			payload: strings.Replace(styledPayload, `"effort":{"level":"high"},`, "", 1),
			want:    "◆ Opus 4 │ ", reject: "Opus 4·",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			l1, _ := renderStyled(t, testcase.payload)
			plain := styleANSI.ReplaceAllString(l1, "")
			if !strings.Contains(plain, testcase.want) || strings.Contains(plain, testcase.reject) {
				t.Fatalf("line 1 = %q, want %q and no %q", plain, testcase.want, testcase.reject)
			}
		})
	}
}

// TestEffortLabelUsesClaudeCodesGlyphs pins each level to the symbol Claude
// Code's /effort picker draws for it; a level it does not know shows bare.
func TestEffortLabelUsesClaudeCodesGlyphs(t *testing.T) {
	for level, want := range map[string]string{
		"low": "○ low", "medium": "◐ medium", "high": "● high", "xhigh": "◉ xhigh", "max": "◈ max",
		"turbo": "turbo",
	} {
		if got := effortLabel(level); got != want {
			t.Errorf("effortLabel(%q) = %q, want %q", level, got, want)
		}
	}
}
