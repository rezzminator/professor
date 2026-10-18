package reload

import (
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// One matcher names the account a recipe runs on, for native reload's default
// account and the open door's offer alike: exactly one configured account may
// own the directory, and an unknown, relative, ambiguous or rosterless case is
// unknown, never a guess.
func TestRecipeAccount(t *testing.T) {
	machine := pfmconfig.Config{
		Accounts: []pfmconfig.Account{
			{ID: 1, ConfigDir: "/fixture/cc/1"},
			{ID: 2, ConfigDir: "/fixture/cc/2/"},
			{ID: 3, ConfigDir: "/fixture/cc/dup"},
			{ID: 4, ConfigDir: "/fixture/cc/dup"},
		},
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: "/fixture/cx/1"}, {ID: 2, Home: "/fixture/cx/2"}},
	}
	for _, test := range []struct {
		name   string
		engine pfmengine.ID
		env    []string
		want   int
		found  bool
	}{
		{"claude directory", pfmengine.Claude, []string{"CLAUDE_CONFIG_DIR=/fixture/cc/1"}, 1, true},
		{"claude directory is cleaned", pfmengine.Claude, []string{"CLAUDE_CONFIG_DIR=/fixture/cc/2"}, 2, true},
		{
			"last assignment wins", pfmengine.Claude,
			[]string{"CLAUDE_CONFIG_DIR=/fixture/cc/1", "CLAUDE_CONFIG_DIR=/fixture/cc/2"},
			2, true,
		},
		{"codex home", pfmengine.Codex, []string{"CODEX_HOME=/fixture/cx/2"}, 2, true},
		{"codex ignores the claude directory", pfmengine.Codex, []string{"CLAUDE_CONFIG_DIR=/fixture/cc/1"}, 0, false},
		{"claude ignores the codex home", pfmengine.Claude, []string{"CODEX_HOME=/fixture/cx/1"}, 0, false},
		{"no assignment", pfmengine.Claude, []string{"A=b"}, 0, false},
		{"relative directory", pfmengine.Claude, []string{"CLAUDE_CONFIG_DIR=cc/1"}, 0, false},
		{"unconfigured directory", pfmengine.Claude, []string{"CLAUDE_CONFIG_DIR=/fixture/cc/9"}, 0, false},
		{"two accounts own the directory", pfmengine.Claude, []string{"CLAUDE_CONFIG_DIR=/fixture/cc/dup"}, 0, false},
		{"engine without a roster", pfmengine.OpenCode, []string{"CLAUDE_CONFIG_DIR=/fixture/cc/1"}, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, found := RecipeAccount(machine, test.engine, test.env)
			if got != test.want || found != test.found {
				t.Fatalf(
					"RecipeAccount(%s, %v) = %d, %t; want %d, %t",
					test.engine,
					test.env,
					got,
					found,
					test.want,
					test.found,
				)
			}
		})
	}
}
