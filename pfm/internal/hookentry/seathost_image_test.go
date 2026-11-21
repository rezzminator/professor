package hookentry

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/binwatch"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestRefreshSeatHostImage(t *testing.T) {
	for _, tc := range []struct {
		name              string
		install           func(t *testing.T, installed string)
		imageErr, execErr error
		wantExec          bool
		wantStderr        func(installed string) string
	}{
		{name: "replaced binary execs the installed one", install: renameNewBuildOver, wantExec: true},
		{name: "current binary keeps running", install: func(*testing.T, string) {}},
		{
			name: "failed exec keeps running", install: renameNewBuildOver, wantExec: true,
			execErr: errors.New("exec refused"),
			wantStderr: func(installed string) string {
				return "pfm internal seat-host: switch to the installed pfm " + installed +
					", keeping the running one: exec refused\n"
			},
		},
		{
			name: "unlocatable binary keeps running", install: renameNewBuildOver,
			imageErr: errors.New("no executable"),
			wantStderr: func(string) string {
				return "pfm internal seat-host: locate the installed pfm, keeping the running one: no executable\n"
			},
		},
		{
			name: "unreadable installed binary keeps running",
			install: func(t *testing.T, installed string) {
				if err := os.Remove(installed); err != nil {
					t.Fatal(err)
				}
			},
			wantStderr: func(installed string) string {
				return "pfm internal seat-host: identify the installed pfm " + installed +
					", keeping the running one: stat " + installed + ": no such file or directory\n"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installed := filepath.Join(t.TempDir(), "pfm")
			if err := os.WriteFile(installed, []byte("old build"), 0o600); err != nil {
				t.Fatal(err)
			}
			image, err := binwatch.ImageAt(installed)
			if err != nil {
				t.Fatal(err)
			}
			tc.install(t, installed)
			var execed, execEnv []string
			var stderr bytes.Buffer
			environ := []string{"PFM_SEAT_HOST_END=7"}
			refreshSeatHostImage(image, tc.imageErr, func(binary string, argv, env []string) error {
				execed, execEnv = append([]string{binary}, argv...), env
				return tc.execErr
			}, environ, &stderr)
			var want, wantEnv []string
			if tc.wantExec {
				want, wantEnv = append([]string{installed}, os.Args...), environ
			}
			wantStderr := ""
			if tc.wantStderr != nil {
				wantStderr = tc.wantStderr(installed)
			}
			if !slices.Equal(execed, want) || !slices.Equal(execEnv, wantEnv) || stderr.String() != wantStderr {
				t.Fatalf("exec = %q env %q, stderr = %q; want exec %q env %q, stderr %q",
					execed, execEnv, stderr.String(), want, wantEnv, wantStderr)
			}
		})
	}
}

// TestSeatHostEnd pins the engine end a replaced seat-host carries into the
// installed pfm: what seatHostEndEnviron writes, parseSeatHostEnd reads back,
// and a value it never writes is refused.
func TestSeatHostEnd(t *testing.T) {
	t.Setenv(paths.EnvSeatHostEnd, "3")
	for _, tc := range []struct {
		status  int
		faulted bool
		want    string
	}{
		{status: 7, want: "7"},
		{status: 0, want: "0"},
		{status: 255, want: "255"},
		{status: 129, faulted: true, want: "129:faulted"},
	} {
		var ends []string
		for _, entry := range seatHostEndEnviron(tc.status, tc.faulted) {
			if end, found := strings.CutPrefix(entry, paths.EnvSeatHostEnd+"="); found {
				ends = append(ends, end)
			}
		}
		status, faulted, err := parseSeatHostEnd(tc.want)
		if !slices.Equal(ends, []string{tc.want}) || status != tc.status || faulted != tc.faulted || err != nil {
			t.Fatalf("end %d faulted=%t: environ ends %q, parsed %d %t %v; want [%q], %d %t",
				tc.status, tc.faulted, ends, status, faulted, err, tc.want, tc.status, tc.faulted)
		}
	}
	for _, end := range []string{"", "x", ":faulted", "7:other", "-1", "256"} {
		status, faulted, err := parseSeatHostEnd(end)
		want := fmt.Sprintf("engine end %q is no exit status 0-255", end)
		if err == nil || err.Error() != want || status != 0 || faulted {
			t.Fatalf("parseSeatHostEnd(%q) = %d %t %v; want 0 false %q", end, status, faulted, err, want)
		}
	}
}

// renameNewBuildOver installs the way make host-install does: a new file
// renamed over the path.
func renameNewBuildOver(t *testing.T, installed string) {
	t.Helper()
	staged := installed + ".new"
	if err := os.WriteFile(staged, []byte("new build"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, installed); err != nil {
		t.Fatal(err)
	}
}
