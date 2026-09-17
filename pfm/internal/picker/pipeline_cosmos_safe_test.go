package picker

import "testing"

// The --safe contract: "on" and "off" are absolute, and "auto" arms exactly
// when VS Code's terminal declares itself — any other terminal (including an
// empty TERM_PROGRAM) keeps the full-rate sky.
func TestResolveCosmosSafe(t *testing.T) {
	cases := []struct {
		name        string
		flagValue   string
		termProgram string
		want        bool
	}{
		{"on ignores terminal", "on", "", true},
		{"off ignores vscode", "off", "vscode", false},
		{"auto arms in vscode", "auto", "vscode", true},
		{"auto stays off elsewhere", "auto", "iTerm.app", false},
		{"auto stays off with no terminal program", "auto", "", false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveCosmosSafe(test.flagValue, test.termProgram); got != test.want {
				t.Fatalf("resolveCosmosSafe(%q, %q) = %v, want %v",
					test.flagValue, test.termProgram, got, test.want)
			}
		})
	}
}
