package testjail

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// envEngineSyncBin is enginesync.EnvBin; a literal, as testjail cannot import
// the packages it jails.
const envEngineSyncBin = "ENGINE_SYNC_BIN"

// The default engine-sync's replies: a healthy engine-sync with nothing to do answers no events,
// no rows, no problems, and each mirror command's PASS line. The script and its in-process player
// (defaultEngineSyncPlayer) read them from here.
const (
	defaultEngineSyncVersion    = "engine-sync 9.0.0-testjail"
	defaultEngineSyncGlobalJSON = `{"ok":true,"events":[],"error":""}`
	defaultEngineSyncDoctorJSON = `{"ok":true,"rows":[]}`
	defaultEngineSyncReportJSON = `{"ok":true,"problems":[],"rebuildable":[],"warnings":[],"wrote":0,"unchanged":0,"deleted":0,"error":""}`
)

// defaultEngineSyncPasses is each mirror command's PASS line.
var defaultEngineSyncPasses = []struct{ command, line string }{
	{"codex build", "CODEX BUILD PASS"},
	{"codex check", "CODEX CHECK PASS"},
	{"codex agents", "CODEX AGENTS PASS"},
	{"opencode build", "OPENCODE BUILD PASS wrote=0 unchanged=0 deleted=0"},
	{"opencode check", "OPENCODE CHECK PASS"},
	{"opencode doctor", "OPENCODE DOCTOR PASS"},
}

// defaultEngineSync answers every call the way a healthy engine-sync with
// nothing to do does: no events, no rows, no problems, each command's PASS
// line. No pfm test runs the real binary (engine-sync's own gate proves it);
// a test that needs a reply of its own installs FakeEngineSync.
var defaultEngineSync = func() string {
	var passes strings.Builder
	for _, pass := range defaultEngineSyncPasses {
		fmt.Fprintf(&passes, "\"%s\") echo '%s' ;;\n", pass.command, pass.line)
	}
	return `#!/bin/sh
json=0
machine=0
for arg; do
  case "$arg" in
  --json) json=1 ;;
  --machine) machine=1 ;;
  esac
done
case "$1" in
version) echo '` + defaultEngineSyncVersion + `'; exit 0 ;;
global) [ "$json" = 1 ] && echo '` + defaultEngineSyncGlobalJSON + `'; exit 0 ;;
doctor)
  if [ "$machine" = 1 ]; then
    [ "$json" = 1 ] && echo '` + defaultEngineSyncDoctorJSON + `'
    exit 0
  fi ;;
esac
if [ "$json" = 1 ]; then
  echo '` + defaultEngineSyncReportJSON + `'
  exit 0
fi
case "$1 $2" in
` + passes.String() + `*) echo "engine-sync (testjail): unknown command: $*" >&2; exit 2 ;;
esac
exit 0
`
}()

// defaultEngineSyncPlayer plays defaultEngineSync in process, the same reply for the same argv:
// an armed unit binary refuses to start the script, so testjail serves its path from here
// (deps.ServeUnitNet). It answers Run only; Start and LookPath fail naming the gap.
type defaultEngineSyncPlayer struct{}

func (defaultEngineSyncPlayer) Run(_ context.Context, argv []string, _ deps.RunOptions) (deps.RunResult, error) {
	args := argv[1:]
	json, machine := slices.Contains(args, "--json"), slices.Contains(args, "--machine")
	word := func(index int) string {
		if index < len(args) {
			return args[index]
		}
		return ""
	}
	reply := func(line string) (deps.RunResult, error) { return deps.RunResult{Stdout: []byte(line + "\n")}, nil }
	switch {
	case word(0) == engineSyncVersion:
		return reply(defaultEngineSyncVersion)
	case word(0) == "global" && json:
		return reply(defaultEngineSyncGlobalJSON)
	case word(0) == "global":
		return deps.RunResult{}, nil
	case word(0) == "doctor" && machine && json:
		return reply(defaultEngineSyncDoctorJSON)
	case word(0) == "doctor" && machine:
		return deps.RunResult{}, nil
	case json:
		return reply(defaultEngineSyncReportJSON)
	}
	for _, pass := range defaultEngineSyncPasses {
		if word(0)+" "+word(1) == pass.command {
			return reply(pass.line)
		}
	}
	stderr := "engine-sync (testjail): unknown command: " + strings.Join(args, " ") + "\n"
	return deps.RunResult{Stderr: []byte(stderr), ExitCode: 2}, nil
}

func (defaultEngineSyncPlayer) Start(_ context.Context, argv []string, _ deps.StartOptions) (deps.Process, error) {
	return nil, fmt.Errorf("testjail: the default engine-sync plays Run only in the unit net, not Start of %q: "+
		"fake it at the caller's runner seam (FakeEngineSyncRunner)", argv)
}

func (defaultEngineSyncPlayer) LookPath(name string) (string, error) {
	return "", fmt.Errorf("testjail: the default engine-sync player looks up no tool (%s): %w", name, exec.ErrNotFound)
}

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
	deps.ServeUnitNet(binary, defaultEngineSyncPlayer{}) // takes effect in an armed unit binary only
	return nil
}

// FakeEngineSync points ENGINE_SYNC_BIN, for this test, at an engine-sync
// stand-in that appends each call's argv as one line to the returned record,
// writes its HOME to record+".home", copies the global file
// {home}/.claude/engine-sync.json (home: --home, else HOME) to record+".config", prints reply on stdout and a usage line
// on stderr, and exits with code. The test stays serial (t.Setenv).
func FakeEngineSync(t *testing.T, reply string, code int) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "engine-sync")
	record := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'engine-sync 9.0.0-testjail'; exit 0; fi\nprintf '%s\\n' \"$*\" >> '" + record + "'\nprintf '%s\\n' \"$HOME\" > '" + record +
		".home'\nprintf '%s\n' \"$ENGINE_SYNC_WORKBENCH_MARKER\" > '" + record + ".marker'\nprintf '%s\n' \"$ENGINE_SYNC_DISABLE\" > '" + record + ".disable'\nhome=\"$HOME\"; prev=\"\"\nfor arg; do [ \"$prev\" != --home ] || home=\"$arg\"; prev=\"$arg\"; done\nconfig=\"$home/.claude/engine-sync.json\"\nif [ -f \"$config\" ]; then cat \"$config\" > '" + record + ".config'; fi\ncat <<'REPLY'\n" + reply + "\nREPLY\nprintf 'usage: engine-sync\\n' >&2\nexit " +
		strconv.Itoa(
			code,
		) + "\n"
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
