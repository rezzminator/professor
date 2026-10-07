//go:build e2e

package e2e

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// Both native manager names are intercepted before the host PATH. The Darwin
// fixture models loaded-but-idle jobs in this private HOME, never launchd.
func stageSchedulerFixtures(t *testing.T, home string) {
	t.Helper()
	if err := writeSchedulerFixtures(home); err != nil {
		t.Fatal(err)
	}
}

func writeSchedulerFixtures(home string) error {
	scripts := map[string]string{
		"systemctl": `#!/bin/sh
[ "${PFM_E2E_HOME-}" = "$HOME" ] || exit 64
printf 'systemctl %s\n' "$*" >> "$HOME/scheduler-calls"
if [ "${1-}" = --version ]; then printf 'systemd 253\n'; exit 0; fi
case "$*" in
  *is-active*) exit 3 ;;
  *show-environment*) exit 1 ;;
esac
exit 1
`,
		"launchctl": `#!/bin/sh
[ "${PFM_E2E_HOME-}" = "$HOME" ] || exit 64
printf 'launchctl %s\n' "$*" >> "$HOME/scheduler-calls"
state="$HOME/fixture-launchd"
mkdir -p "$state"
case "${1-}" in
 bootstrap)
  case "${3-}" in "$HOME"/Library/LaunchAgents/com.professor.pfm.*.plist) ;; *) exit 64 ;; esac
  label="${3##*/}"; label="${label%.plist}"
  : > "$state/$label"
  ;;
 print|bootout)
  label="${2##*/}"
  case "$label" in com.professor.pfm.name-sync|com.professor.pfm.mcp|com.professor.pfm.reminder) ;; *) exit 64 ;; esac
  if [ "$1" = bootout ]; then rm -f "$state/$label"; exit 0; fi
  [ -f "$state/$label" ] || exit 113
  printf 'state = not running\nlast exit code = 0\n'
  ;;
 *) exit 64 ;;
esac
`,
	}
	for name, body := range scripts {
		if err := testjail.WriteExecutable(
			filepath.Join(home, ".local", "bin", name),
			[]byte(body),
			0o700,
		); err != nil {
			return fmt.Errorf("write scheduler fixture %s: %w", name, err)
		}
	}
	return nil
}

func TestSchedulerFixturesRunWhileOtherGoroutinesFork(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	dir := t.TempDir()
	defer startForkLoad(t)()
	deadline := time.Now().Add(time.Second)
	for index := 0; index < 500 && time.Now().Before(deadline); index++ {
		home := filepath.Join(dir, fmt.Sprintf("home-%d", index))
		bin := filepath.Join(home, ".local", "bin")
		if err := os.MkdirAll(bin, 0o700); err != nil {
			t.Fatalf("iteration %d: %v", index, err)
		}
		if err := writeSchedulerFixtures(home); err != nil {
			t.Fatalf("iteration %d: %v", index, err)
		}
		env := append(os.Environ(), "HOME="+home, "PFM_E2E_HOME="+home)
		systemctl := exec.Command(filepath.Join(bin, "systemctl"), "--version")
		systemctl.Env = env
		output, err := systemctl.CombinedOutput()
		if err != nil || string(output) != "systemd 253\n" {
			t.Fatalf("iteration %d: systemctl --version beside forking goroutines: %v %q", index, err, output)
		}
		launchctl := exec.Command(filepath.Join(bin, "launchctl"))
		launchctl.Env = env
		output, err = launchctl.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 64 {
			t.Fatalf("iteration %d: launchctl with no argument must reach its own exit 64: %v %q", index, err, output)
		}
	}
}

func TestWriteSchedulerFixturesNamesTheStubThatFailedToWrite(t *testing.T) {
	t.Parallel()
	err := writeSchedulerFixtures(t.TempDir())
	if err == nil {
		t.Fatal("writeSchedulerFixtures into a home without .local/bin returned nil")
	}
	if got := err.Error(); !strings.HasPrefix(got, "write scheduler fixture ") || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error %q does not name the fixture and wrap the write error", got)
	}
}
