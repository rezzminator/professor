//go:build jail

package agentlauncher

// The staged launcher is a shell script and is the subject, so it runs for real: it must exec the pfm
// with the arguments unchanged and keep its own pid.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestLauncherRunsThePfmWithItsArgumentsAndPid proves the script does what the
// agents rely on: argv passes through unchanged and the pfm replaces the shell.
func TestLauncherRunsThePfmWithItsArgumentsAndPid(t *testing.T) {
	t.Parallel()
	home := testjail.ShortRoot(t)
	pfm := ExecTarget(home)
	if err := os.MkdirAll(filepath.Dir(pfm), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(pfm, []byte("#!/bin/sh\nprintf '%s|' \"$$\" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Stage(home); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(Program(home), "internal", "schedule-fire", "two words")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("launcher run: %v", err)
	}
	want := strconv.Itoa(command.Process.Pid) + "|internal|schedule-fire|two words|"
	if got := string(output); got != want {
		t.Fatalf("output %q, want %q: the launcher's own pid, then the arguments unchanged", got, want)
	}
}
