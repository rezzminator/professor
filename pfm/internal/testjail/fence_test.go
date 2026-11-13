package testjail

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestFenceArgsDropHostPathFlags(t *testing.T) {
	got := fenceArgs([]string{
		"-test.run=^TestX$", "-test.testlogfile=/tmp/go-build1/testlog.txt", "-test.v=test2json",
		"-test.gocoverdir=/tmp/op/cover", "--test.coverprofile=c.out", "-test.timeout=10m0s", "-test.count=1",
	})
	want := []string{"-test.run=^TestX$", "-test.v=test2json", "-test.timeout=10m0s", "-test.count=1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fenceArgs = %q, want %q", got, want)
	}
}

// TestGateAdmitsOnlyTheFence: a suite runs only where PFM_DEV_FENCE=1 meets the container marker,
// or on a GitHub-hosted runner. A host shell exporting PFM_DEV_FENCE (and PFM_STATE_DB) once ran a
// suite against the live pfm.db: the variable alone is refused, so Run re-runs the package in the
// fence and the exported store is never opened here.
func TestGateAdmitsOnlyTheFence(t *testing.T) {
	// This binary only runs where the guard let it in.
	if !runsHere(unitBinary, unitNetArmed, paths.OSEnv{}, fenceMarker) {
		t.Fatal("a test ran where its guard refuses it: the guard failed")
	}
	present := filepath.Join(t.TempDir(), "dockerenv")
	if err := os.WriteFile(present, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(t.TempDir(), "dockerenv")
	hosted := map[string]string{"GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "github-hosted"}
	cases := []struct {
		name   string
		env    map[string]string
		marker string
		want   bool
	}{
		{"the fence: the variable inside a container", map[string]string{"PFM_DEV_FENCE": "1"}, present, true},
		{
			"a host shell exporting the fence variable and a store path",
			map[string]string{"PFM_DEV_FENCE": "1", "PFM_STATE_DB": filepath.Join(absent, "pfm.db")},
			absent, false,
		},
		{"a container without the variable", map[string]string{}, present, false},
		{"the variable set to another word", map[string]string{"PFM_DEV_FENCE": "true"}, present, false},
		{"a GitHub-hosted runner", hosted, absent, true},
		{
			"a self-hosted runner",
			map[string]string{"GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "self-hosted"},
			absent, false,
		},
		{
			"a runner environment outside GitHub Actions",
			map[string]string{"RUNNER_ENVIRONMENT": "github-hosted"},
			absent,
			false,
		},
	}
	for _, tc := range cases {
		if got := admitted(&paths.MapEnv{Values: tc.env}, tc.marker); got != tc.want {
			t.Errorf("%s: admitted(%v, %s) = %v, want %v", tc.name, tc.env, tc.marker, got, tc.want)
		}
	}
}

// TestGuardRunsOnlyAnArmedUnitBinaryOffTheFence: a host with no fence marker and no hosted runner runs
// a unit binary once the unit net is armed, and nothing else; the fence runs every binary.
func TestGuardRunsOnlyAnArmedUnitBinaryOffTheFence(t *testing.T) {
	fence := filepath.Join(t.TempDir(), "dockerenv")
	if err := os.WriteFile(fence, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(t.TempDir(), "dockerenv")
	inFence := map[string]string{"PFM_DEV_FENCE": "1"}
	cases := []struct {
		name        string
		unit, armed bool
		env         map[string]string
		marker      string
		want        bool
	}{
		{"an armed unit binary on a host", true, true, map[string]string{}, host, true},
		{"an unarmed unit binary on a host", true, false, map[string]string{}, host, false},
		{"a jail binary on a host, the net armed", false, true, map[string]string{}, host, false},
		{"a jail binary on a host", false, false, map[string]string{}, host, false},
		{"a jail binary in the fence", false, false, inFence, fence, true},
		{"an unarmed unit binary in the fence", true, false, inFence, fence, true},
	}
	for _, tc := range cases {
		if got := runsHere(tc.unit, tc.armed, &paths.MapEnv{Values: tc.env}, tc.marker); got != tc.want {
			t.Errorf(
				"%s: runsHere(unit=%v, armed=%v, %v, %s) = %v, want %v",
				tc.name,
				tc.unit,
				tc.armed,
				tc.env,
				tc.marker,
				got,
				tc.want,
			)
		}
	}
}
