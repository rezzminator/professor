//go:build jail

package tmux

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestExecRefusesTheRealTmuxInATestProcess: with no runner a test process
// gets the refusal for the real tmux — tmux on PATH, the registry's
// resolution, or a symlink to it — never for a tmux script a test wrote under
// the temporary directory, one exec cannot find, or while a test holds the
// TmuxServer allowance; the refusal is a tmux that never started, recorded
// as one.
func TestExecRefusesTheRealTmuxInATestProcess(t *testing.T) {
	realTmux, err := exec.LookPath(Binary)
	if err != nil {
		t.Fatalf("the real tmux this test refuses is not on PATH: %v", err)
	}
	written := filepath.Join(t.TempDir(), Binary)
	if err := testjail.WriteExecutable(written, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), Binary)
	if err := os.Symlink(realTmux, linked); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		argv0   string
		allowed bool
		refused bool
	}{
		{"tmux on PATH", Binary, false, true},
		{"the registry's tmux", deps.Executable(Binary), false, true},
		{"a symlink to the real tmux", linked, false, true},
		{"a script a test wrote", written, false, false},
		{"a path exec cannot find", "/opt/test-fixture/tmux", false, false},
		{"a test holding the allowance", Binary, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.allowed {
				t.Cleanup(deps.AllowInTest(Binary))
			}
			if got := errors.Is(refuseInTest(tc.argv0), ErrRealServerInTest); got != tc.refused {
				t.Fatalf("refuseInTest(%q) refused = %v, want %v", tc.argv0, got, tc.refused)
			}
		})
	}
	ctx, recorder := obs.Test(t)
	err = Exec(ctx, "", "/tmp/s/sock", "list-panes").Run()
	if !errors.Is(err, ErrRealServerInTest) || !CouldNotRun(err) {
		t.Fatalf("Exec(registered tmux).Run() = %v, want the refusal, a tmux that never started", err)
	}
	if record := recorder.Records(); len(record) != 1 || record[0].Level != "ERROR" {
		t.Fatalf("records = %s, want one ERROR record", recorder.Raw())
	}
}
