package mockengine

import "testing"

// TestArgvEngine pins which engine a run plays from argv[0] and the EnvEngine override.
func TestArgvEngine(t *testing.T) {
	for _, row := range []struct {
		argv0, override, want, wantErr string
	}{
		{"claude", "", "claude", ""},
		{"/journey/bin/codex", "claude", "codex", ""},
		{"/home/tester/.local/share/claude/versions/2.1.270", "", "claude", ""},
		{"/home/tester/.local/share/claude/versions/2.1.270", "opencode", "opencode", ""},
		{"/journey/bin/mock-engine", "codex", "codex", ""},
		{
			"/opt/tools/2.1.238", "", "",
			`argv[0] "2.1.238" names no engine (claude|codex|opencode) and MOCK_ENGINE_ENGINE="" selects none`,
		},
		{
			"/opt/tools/versions/2.1.238", "", "",
			`argv[0] "2.1.238" names no engine (claude|codex|opencode) and MOCK_ENGINE_ENGINE="" selects none`,
		},
		{
			"/home/tester/.local/share/claude/versions/latest", "", "",
			`argv[0] "latest" names no engine (claude|codex|opencode) and MOCK_ENGINE_ENGINE="" selects none`,
		},
		{
			"/journey/bin/mock-engine", "", "",
			`argv[0] "mock-engine" names no engine (claude|codex|opencode) and MOCK_ENGINE_ENGINE="" selects none`,
		},
		{
			"/journey/bin/mock-engine", "gemini", "",
			`argv[0] "mock-engine" names no engine (claude|codex|opencode) and MOCK_ENGINE_ENGINE="gemini" selects none`,
		},
	} {
		got, err := argvEngine(row.argv0, row.override)
		gotErr := ""
		if err != nil {
			gotErr = err.Error()
		}
		if got != row.want || gotErr != row.wantErr {
			t.Errorf("argvEngine(%q, %q) = %q, %q; want %q, %q", row.argv0, row.override, got, gotErr, row.want,
				row.wantErr)
		}
	}
}
