package installer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

func TestSubagentStatusLineStripKeepsCustomCommand(t *testing.T) {
	home := t.TempDir()
	ours := claudelaunch.SubagentStatusLineCommand(home)
	for _, testCase := range []struct {
		command string
		removed bool
	}{
		{ours, true}, {"operator-rows", false},
	} {
		raw := []byte(fmt.Sprintf(`{"subagentStatusLine":{"command":%q}}`, testCase.command))
		updated, removed, err := stripAccountSettings(raw, home, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(removed) != 0; got != testCase.removed {
			t.Fatalf("removed=%v want=%v", removed, testCase.removed)
		}
		if strings.Contains(string(updated), testCase.command) == testCase.removed {
			t.Fatalf("updated=%s", updated)
		}
	}
}
