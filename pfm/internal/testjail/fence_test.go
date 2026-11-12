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
	// This binary only runs where the gate let it in.
	if !Disposable() {
		t.Fatal("a test ran outside the fence and off a hosted runner: the gate failed")
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
