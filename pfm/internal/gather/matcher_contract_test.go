package gather_test

import (
	"testing"

	claudeengine "github.com/rezzminator/professor/pfm/internal/engine/claude"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

func TestClaudeMatcherAndAgentScanUseOneCommandRule(t *testing.T) {
	matcher := claudeengine.Matcher{}
	for _, argv := range [][]string{
		{"claude"},
		{"/opt/claude/versions/2.5.11"},
		{"2.5.11"},
		{"vim"},
	} {
		want := matcher.IsCommand(argv)
		if got := gather.IsClaudeCommand(argv); got != want {
			t.Errorf("argv=%q gather=%t matcher=%t", argv, got, want)
		}
	}
}
