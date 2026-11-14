//go:build !jail && !e2e && !journey

package deps_test

import (
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// TestUnitNetEnvironmentIsTheGoldenList: in an armed unit binary, after testjail.Run's setup, the
// umask is 022, TZ is UTC, and the environment holds exactly the names the unit net and the jail
// set, plus whichever of the seven allowlisted names the caller passed and the profiler's own
// parent marker, set only while the profiler runs (PFM_TEST_PROFILE=0 turns it off); nothing else
// of the caller's environment reaches a test.
func TestUnitNetEnvironmentIsTheGoldenList(t *testing.T) {
	optional := []string{
		"PFM_PROFILE_FIXTURE", "PFM_PROFILE_FIXTURE_SLOW_S", "PFM_STRESS", "PFM_STRESS_STRICT",
		"PFM_TEST_ARTIFACT_DIR", "PFM_TEST_DEADLINE_EPOCH", "PFM_TEST_PROFILE", "PFM_TEST_PROFILE_PARENT",
	}
	want := []string{
		"CHAT_CALL_SOCKET",
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDE_CONFIG_DIR",
		"CODEX_THREAD_ID",
		"ENGINE_SYNC_BIN",
		"GIT_CONFIG_COUNT",
		"GIT_CONFIG_GLOBAL",
		"GIT_CONFIG_KEY_0",
		"GIT_CONFIG_NOSYSTEM",
		"GIT_CONFIG_VALUE_0",
		"HOME",
		"INVOCATION_ID",
		"PATH",
		"PFM_CONFIG",
		"PFM_HOME",
		"PFM_MANAGED_SETTINGS_DIR",
		"PFM_PRICES_OFFLINE",
		"PFM_SID_DIR",
		"PFM_SKILL_SOURCES_OFFLINE",
		"PFM_SOURCE_REPO",
		"PFM_TESTJAIL",
		"PFM_THEMES_OFFLINE",
		"PFM_UPDATE_CHECK_OFFLINE",
		"PFM_USAGE_OFFLINE",
		"TMPDIR",
		"TMUX",
		"TMUX_PANE",
		"TMUX_TMPDIR",
		"TZ",
		"XDG_CACHE_HOME",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"XDG_RUNTIME_DIR",
		"XDG_STATE_HOME",
	}
	var got []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !slices.Contains(optional, name) {
			got = append(got, name)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("environment names after the unit net's setup =\n%q\nwant\n%q", got, want)
	}
	if zone := os.Getenv("TZ"); zone != "UTC" {
		t.Errorf("TZ = %q, want UTC", zone)
	}
	mask := syscall.Umask(0) // read back at once: the mask is the process's, no test here changes it
	syscall.Umask(mask)
	if mask != 0o022 {
		t.Errorf("umask = %#o, want 022 whatever the caller's", mask)
	}
}
