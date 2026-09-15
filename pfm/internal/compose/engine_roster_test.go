package compose

import "testing"

func TestNewRowsFollowEngineRosterMatrix(t *testing.T) {
	tests := []struct {
		name         string
		claude       []AccountRoot
		codex        []int
		opencode     []int
		wantKinds    []Kind
		wantAccounts []int
	}{
		{name: "zero zero"},
		{
			name:         "claude only",
			claude:       []AccountRoot{{Account: 2, Path: "/cc/2"}, {Account: 4, Path: "/cc/4"}},
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
			claude:       []AccountRoot{{Account: 2, Path: "/cc/2"}, {Account: 4, Path: "/cc/4"}},
			codex:        []int{7, 9},
			wantKinds:    []Kind{NewClaude, NewCodex},
			wantAccounts: []int{4, 9},
		},
		{
			name:         "all three",
			claude:       []AccountRoot{{Account: 2, Path: "/cc/2"}},
			codex:        []int{7},
			opencode:     []int{1},
			wantKinds:    []Kind{NewClaude, NewCodex, NewOpencode},
			wantAccounts: []int{2, 7, 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := Compose(Input{
				AccountRoots: test.claude,
				Options: Options{
					CurrentDir:          "/work/project",
					PrimaryAccount:      4,
					CodexAccountIDs:     test.codex,
					PrimaryCodexAccount: 9,
					OpencodeAccountIDs:  test.opencode,
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
