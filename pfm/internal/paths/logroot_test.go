package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogRootFollowsTheRecordedClone: pfm's logs live in the recorded clone's
// tmp/logs/pfm, created there; a jailed home, a home with no clone on record,
// a clone that is gone and a tmp folder that cannot be created log under the
// home's state folder, the last two saying why. A jail whose marker names a
// writable clone never writes into it.
func TestLogRootFollowsTheRecordedClone(t *testing.T) {
	cases := []struct {
		name     string
		jailed   bool
		marker   bool
		gone     bool
		tmpFile  bool
		inClone  bool
		fallback string
	}{
		{name: "the recorded clone", marker: true, inClone: true},
		{name: "no clone on record"},
		{name: "a jailed home never reaches its recorded clone", jailed: true, marker: true},
		{
			name: "a recorded clone that is gone", marker: true, gone: true,
			fallback: "log root: inspect recorded source repository {clone}: recorded source repository is unusable: " +
				"stat {clone}: no such file or directory",
		},
		{
			name: "a tmp folder that cannot be created", marker: true, tmpFile: true,
			fallback: "log root: create {clone}/tmp/logs/pfm: mkdir {clone}/tmp: not a directory",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, clone := t.TempDir(), filepath.Join(t.TempDir(), "professor")
			if err := os.Mkdir(clone, 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.marker {
				if err := WriteSourceRepoMarker(home, clone); err != nil {
					t.Fatal(err)
				}
			}
			if tc.tmpFile {
				if err := os.WriteFile(filepath.Join(clone, "tmp"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.gone {
				if err := os.Remove(clone); err != nil {
					t.Fatal(err)
				}
			}
			env := &MapEnv{}
			if tc.jailed {
				env.Values = map[string]string{EnvHome: home}
			}
			place := LogRoot(env, home)
			want := filepath.Join(home, ".local", "state", "pfm", "log")
			if tc.inClone {
				want = filepath.Join(clone, "tmp", "logs", "pfm")
			}
			if place.Dir != want {
				t.Fatalf("LogRoot().Dir = %q, want %q", place.Dir, want)
			}
			gotFallback := ""
			if place.Fallback != nil {
				gotFallback = place.Fallback.Error()
			}
			if wantFallback := strings.ReplaceAll(tc.fallback, "{clone}", clone); gotFallback != wantFallback {
				t.Fatalf("LogRoot().Fallback = %q, want %q", gotFallback, wantFallback)
			}
			_, err := os.Stat(filepath.Join(clone, "tmp", "logs"))
			if tc.inClone != (err == nil) || !tc.inClone && !tc.tmpFile && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("clone log folder stat err = %v, want it present only when the log lives there", err)
			}
			if got, want := place.Activity(), filepath.Join(want, "pfm.jsonl"); got != want {
				t.Fatalf("Activity() = %q, want %q", got, want)
			}
			if got, want := place.CostSink(), filepath.Join(want, "cost-sink.log"); got != want {
				t.Fatalf("CostSink() = %q, want %q", got, want)
			}
			if got, want := place.SubagentStatuslineTrace(), filepath.Join(
				want,
				"subagent-statusline.jsonl",
			); got != want {
				t.Fatalf("SubagentStatuslineTrace() = %q, want %q", got, want)
			}
			if got, want := place.Restart(), filepath.Join(want, "restart"); got != want {
				t.Fatalf("Restart() = %q, want %q", got, want)
			}
		})
	}
}
