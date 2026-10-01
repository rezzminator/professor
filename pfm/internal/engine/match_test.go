package engine

import "testing"

func TestMatchCommandLiteralVerdicts(t *testing.T) {
	tests := []struct {
		id           ID
		argv         []string
		versionNamed bool
		binaries     []string
		want         bool
	}{
		{id: Claude, argv: []string{"claude"}, want: true},
		{id: Claude, argv: []string{"/opt/claude/versions/2.5.11"}, want: true},
		{id: Claude, argv: []string{"2.5.11"}, versionNamed: true, want: true},
		{id: Claude, argv: []string{"2.5.11"}, want: false},
		{id: Claude, argv: []string{"vim"}, want: false},
		{id: Claude, argv: []string{}, want: false},
		{id: Claude, argv: []string{"v2.5"}, versionNamed: true, want: false},
		{id: Claude, argv: []string{"/usr/local/bin/cc-wrapper"}, binaries: []string{"/opt/x/cc-wrapper"}, want: true},
		{id: Codex, argv: []string{"codex"}, want: true},
		{id: Codex, argv: []string{"claude"}, want: false},
		{id: OpenCode, argv: []string{"/opt/opencode/bin/opencode"}, want: true},
	}
	for _, tc := range tests {
		if got := MatchCommand(tc.id, tc.argv, tc.versionNamed, tc.binaries...); got != tc.want {
			t.Errorf(
				"MatchCommand(%s, %q, versionNamed=%t, binaries=%q) = %t, want %t",
				tc.id,
				tc.argv,
				tc.versionNamed,
				tc.binaries,
				got,
				tc.want,
			)
		}
	}
}
