package compose

import "testing"

func TestNewRowsFollowEngineRosterMatrix(t *testing.T) {
	tests := []struct {
		name         string
		claude       []ClaudeSeat
		codex        []int
		openCode     []int
		wantKinds    []Kind
		wantAccounts []int
	}{
		{name: "zero zero"},
		{
			name:         "claude only",
			claude:       []ClaudeSeat{{Account: 2, ConfigDir: "/cc/2"}, {Account: 4, ConfigDir: "/cc/4"}},
			wantKinds:    []Kind{NewClaude},
			wantAccounts: []int{4},
		},
		{
			name:         "codex only",
			codex:        []int{7, 9},
			wantKinds:    []Kind{NewCodex},
			wantAccounts: []int{9},
		},
		{
			name:         "both",
			claude:       []ClaudeSeat{{Account: 2, ConfigDir: "/cc/2"}, {Account: 4, ConfigDir: "/cc/4"}},
			codex:        []int{7, 9},
			wantKinds:    []Kind{NewClaude, NewCodex},
			wantAccounts: []int{4, 9},
		},
		{
			name:         "all three",
			claude:       []ClaudeSeat{{Account: 2, ConfigDir: "/cc/2"}},
			codex:        []int{7},
			openCode:     []int{1},
			wantKinds:    []Kind{NewClaude, NewCodex, NewOpenCode},
			wantAccounts: []int{2, 7, 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := Compose(Input{
				ClaudeSeats: test.claude,
				Options: Options{
					CurrentDir:          "/work/project",
					PrimaryAccount:      4,
					CodexAccountIDs:     test.codex,
					PrimaryCodexAccount: 9,
					OpenCodeAccountIDs:  test.openCode,
				},
			})
			if len(output.Rows) != len(test.wantKinds) {
				t.Fatalf("new rows = %#v, want kinds %v", output.Rows, test.wantKinds)
			}
			for index, row := range output.Rows {
				if row.Kind != test.wantKinds[index] || row.Account != test.wantAccounts[index] {
					t.Fatalf(
						"row %d = kind %s account %d, want %s/%d",
						index,
						row.Kind,
						row.Account,
						test.wantKinds[index],
						test.wantAccounts[index],
					)
				}
			}
		})
	}
}
