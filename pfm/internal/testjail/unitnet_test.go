package testjail

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestUnitNetFailsEveryDoorNamingItsVariable arms the unit net for this test alone and tries each
// door a unit test could still reach unscripted: a home file, a real tool, a tmux socket, a live
// seat. Each read fails, or finds nothing, on a path that names the variable it came through.
func TestUnitNetFailsEveryDoorNamingItsVariable(t *testing.T) {
	dir := ShortRoot(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "11111111-2222-3333-4444-555555555555")
	if err := pointUnitNetDirs(dir, func(name, value string) error { t.Setenv(name, value); return nil }); err != nil {
		t.Fatalf("pointUnitNetDirs(%s) = %v, want nil", dir, err)
	}
	home, err := paths.OSEnv{}.Home()
	if err != nil {
		t.Fatalf("home inside the net: %v", err)
	}
	_, homeErr := os.ReadFile(filepath.Join(home, ".claude.json"))
	_, toolErr := exec.LookPath("git")
	socket := filepath.Join(paths.TmuxDirUnder(paths.OSEnv{}.Get(paths.EnvTmuxTmpDir)), "default")
	_, socketErr := net.Dial("unix", socket)
	text := func(err error) string {
		if err == nil {
			return "<nil>"
		}
		return err.Error()
	}
	uid := strconv.Itoa(os.Getuid())
	cases := []struct{ door, got, want string }{
		{"HOME", home, filepath.Join(dir, "HOME")},
		{
			"home file",
			text(homeErr),
			"open " + filepath.Join(dir, "HOME", ".claude.json") + ": no such file or directory",
		},
		{"real tool", text(toolErr), `exec: "git": executable file not found in $PATH`},
		{
			"tmux socket",
			text(socketErr),
			"dial unix " + filepath.Join(
				dir,
				"TMUX_TMPDIR",
				"tmux-"+uid,
				"default",
			) + ": connect: no such file or directory",
		},
		{"XDG_CONFIG_HOME", os.Getenv("XDG_CONFIG_HOME"), filepath.Join(dir, "XDG_CONFIG_HOME")},
		{"XDG_DATA_HOME", os.Getenv("XDG_DATA_HOME"), filepath.Join(dir, "XDG_DATA_HOME")},
		{"XDG_STATE_HOME", os.Getenv("XDG_STATE_HOME"), filepath.Join(dir, "XDG_STATE_HOME")},
		{"XDG_CACHE_HOME", os.Getenv("XDG_CACHE_HOME"), filepath.Join(dir, "XDG_CACHE_HOME")},
		{"XDG_RUNTIME_DIR", os.Getenv("XDG_RUNTIME_DIR"), filepath.Join(dir, "XDG_RUNTIME_DIR")},
		{"PATH", os.Getenv("PATH"), filepath.Join(dir, "PATH")},
		{"TMUX", os.Getenv("TMUX"), ""},
		{"CLAUDE_CODE_SESSION_ID", os.Getenv("CLAUDE_CODE_SESSION_ID"), ""},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s inside the net = %q, want %q", tc.door, tc.got, tc.want)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "PATH"))
	if err != nil || len(entries) != 0 {
		t.Errorf(
			"the net's PATH directory %s holds %v (read error %v), want it empty",
			filepath.Join(dir, "PATH"),
			entries,
			err,
		)
	}
}

func TestUnitNetTMPDIRMovesIntoTheNetOnlyAboveMacOSSocketBudget(t *testing.T) {
	for _, tc := range []struct {
		bytes int
		want  string
	}{
		{108, "/r/u1"},
		{105, "/r/u1"},
		{104, "/r"},
		{103, "/r"},
	} {
		if got := unitNetTMPDIR("/r", "/r/u1", tc.bytes); got != tc.want {
			t.Errorf("unitNetTMPDIR(/r, /r/u1, %d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}
