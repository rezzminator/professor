package testjail

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// envEngineSyncBin is enginesync.EnvBin; a literal, as testjail cannot import
// the packages it jails.
const envEngineSyncBin = "ENGINE_SYNC_BIN"

// defaultEngineSync answers every call the way a healthy engine-sync with
// nothing to do does: no events, no rows, no problems, each command's PASS
// line. No pfm test runs the real binary (engine-sync's own gate proves it);
// a test that needs a reply of its own installs FakeEngineSync.
const defaultEngineSync = `#!/bin/sh
json=0
machine=0
for arg; do
  case "$arg" in
  --json) json=1 ;;
  --machine) machine=1 ;;
  esac
done
case "$1" in
version) echo 'engine-sync 0.0.0-testjail'; exit 0 ;;
global) [ "$json" = 1 ] && echo '{"ok":true,"events":[],"error":""}'; exit 0 ;;
doctor)
  if [ "$machine" = 1 ]; then
    [ "$json" = 1 ] && echo '{"ok":true,"rows":[]}'
    exit 0
  fi ;;
esac
if [ "$json" = 1 ]; then
  echo '{"ok":true,"problems":[],"rebuildable":[],"warnings":[],"wrote":0,"unchanged":0,"deleted":0,"error":""}'
  exit 0
fi
case "$1 $2" in
"codex build") echo 'CODEX BUILD PASS' ;;
"codex check") echo 'CODEX CHECK PASS' ;;
"codex agents") echo 'CODEX AGENTS PASS' ;;
"opencode build") echo 'OPENCODE BUILD PASS wrote=0 unchanged=0 deleted=0' ;;
"opencode check") echo 'OPENCODE CHECK PASS' ;;
"opencode doctor") echo 'OPENCODE DOCTOR PASS' ;;
*) echo "engine-sync (testjail): unknown command: $*" >&2; exit 2 ;;
esac
exit 0
`

// installDefaultEngineSync writes defaultEngineSync into dir and points
// ENGINE_SYNC_BIN at it for the whole package process, replacing any
// ambient build (a fence's real binary) so every jailed install and doctor
// runs against the same healthy fake.
func installDefaultEngineSync(dir string) error {
	binary := filepath.Join(dir, "engine-sync")
	if err := WriteExecutable(binary, []byte(defaultEngineSync), 0o755); err != nil {
		return fmt.Errorf("write the default engine-sync fake %s: %w", binary, err)
	}
	if err := os.Setenv(envEngineSyncBin, binary); err != nil {
		return fmt.Errorf("set %s to %s: %w", envEngineSyncBin, binary, err)
	}
	defaultEngineSyncBin = binary
	return nil
}

// FakeEngineSync points ENGINE_SYNC_BIN, for this test, at an engine-sync
// stand-in that appends each call's argv as one line to the returned record,
// writes its HOME to record+".home", prints reply on stdout and a usage line
// on stderr, and exits with code. The test stays serial (t.Setenv).
func FakeEngineSync(t *testing.T, reply string, code int) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "engine-sync")
	record := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + record + "'\nprintf '%s\\n' \"$HOME\" > '" + record +
		".home'\nprintf '%s\n' \"$ENGINE_SYNC_WORKBENCH_MARKER\" > '" + record + ".marker'\nhome=\"$HOME\"; prev=\"\"\nfor arg; do [ \"$prev\" != --home ] || home=\"$arg\"; prev=\"$arg\"; done\nconfig=\"${XDG_CONFIG_HOME:-$home/.config}/engine-sync/config.json\"\nif [ -f \"$config\" ]; then cat \"$config\" > '" + record + ".config'; fi\nif [ -f \"${config%/*}/targets.json\" ]; then cat \"${config%/*}/targets.json\" > '" + record + ".targets'; fi\ncat <<'REPLY'\n" + reply + "\nREPLY\nprintf 'usage: engine-sync\\n' >&2\nexit " +
		strconv.Itoa(code) + "\n"
	if err := WriteExecutable(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write the engine-sync fake %s: %v", binary, err)
	}
	t.Setenv(envEngineSyncBin, binary)
	return record
}

// EngineSyncCalls returns the argv lines a FakeEngineSync recorded, in call
// order; a fake that never ran is a fatal error, never an empty list.
func EngineSyncCalls(t *testing.T, record string) []string {
	t.Helper()
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("engine-sync never ran (%s): %v", record, err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// CleanMirrorReply is engine-sync's --json answer to a mirror build that
// found nothing wrong; FailedMirrorReply one it could not run.
const (
	CleanMirrorReply = `{"ok":true,"problems":[],"rebuildable":[],"warnings":[],"wrote":1,"unchanged":0,` +
		`"deleted":0,"error":""}`
	FailedMirrorReply = `{"ok":false,"problems":[],"rebuildable":[],"warnings":[],"wrote":0,"unchanged":0,` +
		`"deleted":0,"error":"parse .mcp.json: unexpected end of JSON input"}`
)

// MirrorBuilt reports whether a FakeEngineSync record holds a build of
// engine's mirror of dir. A record not written yet is false, so a test can
// ask it at the moment a launch spawns; an unreadable record is fatal.
func MirrorBuilt(t *testing.T, record, engine, dir string) bool {
	t.Helper()
	raw, err := os.ReadFile(record)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatalf("read the engine-sync record %s: %v", record, err)
	}
	prefix := engine + " build " + dir + " "
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// defaultEngineSyncBin is the default fake installDefaultEngineSync wrote,
// set once before any test runs.
var defaultEngineSyncBin string

// EngineSyncBin is the jail's default engine-sync fake. A test that injects
// its own environment map carries it as ENGINE_SYNC_BIN, the variable the
// jail set in the process environment.
func EngineSyncBin() string { return defaultEngineSyncBin }
