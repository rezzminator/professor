//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// Both native manager names are intercepted before the host PATH. The Darwin
// fixture models loaded-but-idle jobs in this private HOME, never launchd.
func stageSchedulerFixtures(t *testing.T, home string) {
	t.Helper()
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
  case "$label" in com.professor.pfm.name-sync|com.professor.pfm.mcp) ;; *) exit 64 ;; esac
  if [ "$1" = bootout ]; then rm -f "$state/$label"; exit 0; fi
  [ -f "$state/$label" ] || exit 113
  printf 'state = not running\nlast exit code = 0\n'
  ;;
 *) exit 64 ;;
esac
`,
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(home, ".local", "bin", name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}
