package gather

import (
	"fmt"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// A caller holding a runtime hands gather its paths: the process table, tmux directory, SID directory
// and home come from them, never from the environment, so a runtime whose ProcRoot is an empty table
// never scans the live one. A caller with none still resolves them from the environment.
func TestNewReadsTheCallersPathsElseTheEnvironment(t *testing.T) {
	environmentProc := t.TempDir()
	t.Setenv(paths.EnvProcRoot, environmentProc)
	givenProc := t.TempDir()
	given := paths.Values{ProcRoot: givenProc, TmuxDir: "/given/tmux", SIDDir: "/given/sid", Home: "/given/home"}
	for _, tc := range []struct {
		name  string
		paths *paths.Values
		want  string
	}{
		{"the caller's paths", &given, fmt.Sprintf(
			"proc {Root:%s} tmux {Binary: TmuxDir:/given/tmux TmuxTmpDir:} sid /given/sid home /given/home", givenProc,
		)},
		{"no paths", nil, fmt.Sprintf("proc {Root:%s} tmux dir is the environment's: true", environmentProc)},
	} {
		gatherer, err := New(Dependencies{Paths: tc.paths})
		if err != nil {
			t.Fatalf("%s: New: %v", tc.name, err)
		}
		got := fmt.Sprintf("proc %+v tmux %+v sid %s home %s",
			gatherer.proc, gatherer.tmux, gatherer.paths.SIDDir, gatherer.paths.Home)
		if tc.paths == nil {
			resolved, err := paths.Resolve()
			if err != nil {
				t.Fatalf("resolve the environment's paths: %v", err)
			}
			got = fmt.Sprintf("proc %+v tmux dir is the environment's: %t",
				gatherer.proc, gatherer.tmux == TmuxProbe{TmuxDir: resolved.TmuxDir})
		}
		if got != tc.want {
			t.Errorf("%s: gatherer = %q, want %q", tc.name, got, tc.want)
		}
	}
}
