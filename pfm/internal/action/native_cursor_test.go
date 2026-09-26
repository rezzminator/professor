package action

import (
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

const nativeCursorAssignment = "CLAUDE_CODE_NATIVE_CURSOR=1"

func TestLauncherRunNativeCursorFollowsConfig(t *testing.T) {
	for _, want := range []bool{false, true} {
		run, err := LauncherRun("/bin/claude", nil, "", "/home/test", pfmconfig.ClaudePrefs{NativeCursor: want})
		if err != nil {
			t.Fatalf("LauncherRun(nativeCursor=%v) error = %v", want, err)
		}
		if got := strings.Contains(run, " "+nativeCursorAssignment); got != want {
			t.Fatalf("nativeCursor=%v: assignment present=%v in %q", want, got, run)
		}
	}
}

func TestClaudeSpawnEnvironmentNativeCursorFollowsConfig(t *testing.T) {
	for _, want := range []bool{false, true} {
		spawn := ClaudeSpawn{
			Purpose: PurposeInteractive,
			Home:    "/home/test",
			Machine: pfmconfig.Config{Claude: pfmconfig.ClaudePrefs{NativeCursor: want}},
			binary:  "/bin/claude",
		}
		environment := spawn.Environment([]string{"PATH=/bin"})
		if got := slices.Contains(environment, nativeCursorAssignment); got != want {
			t.Fatalf("nativeCursor=%v: assignment present=%v in %v", want, got, environment)
		}
	}
}
