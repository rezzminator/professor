package testjail

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// unitNetArmed is the unit net's one switch. On, a unit binary (tier_unit.go) runs on any host,
// inside the net Run lays before its first test: umask 022, the allowlisted environment, TZ=UTC, the
// net directories, and deps' refusal of every process start and host-tool lookup. Off, it would
// re-run in the fence as a jail binary does (fence.go).
const unitNetArmed = true

// UnitNetArmed reports whether this binary runs inside the armed unit net: a unit binary
// (tier_unit.go) with the switch on. A TestMain that would start a process before Run (a shared
// binary build) asks it first: an armed unit binary starts none.
func UnitNetArmed() bool { return unitBinary && unitNetArmed }

// NotOnPath is the error text a lookup of the bare name prints when it walks a PATH that holds no
// such program: the unit net's refusal in an armed unit binary (deps refuseUnitNetLookup), exec's
// own not-found anywhere else, where the jail binary walks the PATH the test set. A unit file runs
// in both binaries, so its expected text names the lookup through here.
func NotOnPath(name string) string {
	if UnitNetArmed() {
		return "UNIT-NET REFUSED lookup: " + name + " (executable file not found in $PATH) — a unit test starts no" +
			" process: fake it at the caller's runner seam (deps.FakeRunner), or move the test to a _jail_test.go file"
	}
	return fmt.Sprintf("exec: %q: executable file not found in $PATH", name)
}

// unitNetDirs point, each, at an empty directory of its own name inside the net: a test that still
// reads a home file, resolves a tool through PATH or dials a tmux socket fails on a path that names
// the variable it came through, and never reaches the machine's own.
var unitNetDirs = []string{
	"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
	paths.EnvTmuxTmpDir, "PATH", "PFM_SOURCE_REPO",
}

// unitNetKeep is every name of the starting environment an armed unit binary keeps, each with the
// unit-tier reader it is kept for; armUnitProcess removes every other name before Run sets its own.
var unitNetKeep = map[string]string{
	paths.EnvTestArtifactDir:     "the step profiler writes the binary's timing artifacts there (profile.go)",
	paths.EnvTestProfile:         "turns the step profiler on or off for this binary (profile.go)",
	paths.EnvTestDeadlineEpoch:   "the profiler's deadline, the gate's step budget (profile.go)",
	"PFM_STRESS":                 "runs the stress batteries (compose, store, transcript stress_test.go)",
	"PFM_STRESS_STRICT":          "holds a stress battery to its time bound (mcpserv, picker, ui, store)",
	"PFM_PROFILE_FIXTURE":        "picks the profiler fixture's deliberate behaviour, set by its jail driver (testdata/profilefixture)",
	"PFM_PROFILE_FIXTURE_SLOW_S": "the profiler fixture's sleep, set by its jail driver (testdata/profilefixture)",
}

// unitNetZone is the time zone of every armed unit binary, the fence's: a test formatting a local
// time reads the same on any host.
const unitNetZone = "UTC"

// armUnitProcess is the armed unit binary's first act, before Run reads anything: umask 022, as a
// test of a protected directory's mode must not read the caller's group-writable 002; the starting
// environment emptied to unitNetKeep; TZ and time.Local at unitNetZone; and deps' refusal of every
// process start and host-tool lookup armed. Any other binary keeps all of it.
func armUnitProcess(armed bool) error {
	if !armed {
		return nil
	}
	syscall.Umask(0o022)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, keep := unitNetKeep[name]; keep || name == "" {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("clear %s from the unit net's environment: %w", name, err)
		}
	}
	if err := os.Setenv("TZ", unitNetZone); err != nil {
		return fmt.Errorf("set TZ to %s: %w", unitNetZone, err)
	}
	time.Local = time.UTC
	deps.ArmUnitNet()
	serveUnitNetKeychain()
	return nil
}

// unitNetKeychainMissing is what security answers for an item the keychain lacks:
// errSecItemNotFound's exit status and its stderr line.
var unitNetKeychainMissing = deps.RunResult{
	ExitCode: 44,
	Stderr:   []byte("security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.\n"),
}

// unitNetKeychain is the keychain every armed unit binary reads: empty. A credential read finds no
// item, as on a host never logged in, and never the host's own login keychain.
type unitNetKeychain struct{}

// serveUnitNetKeychain serves the registry's keychain command from unitNetKeychain for the whole
// armed binary (deps.ServeUnitNet), so its lookup resolves and every run answers in process.
func serveUnitNetKeychain() {
	registry := deps.Registry()
	for index := range registry {
		if registry[index].Name == deps.KeychainCommand && registry[index].Command != "" {
			deps.ServeUnitNet(registry[index].Command, unitNetKeychain{})
		}
	}
}

func (unitNetKeychain) Run(_ context.Context, argv []string, _ deps.RunOptions) (deps.RunResult, error) {
	if len(argv) > 1 && argv[1] == "find-generic-password" {
		return unitNetKeychainMissing, nil
	}
	err := fmt.Errorf("testjail: the unit net's keychain plays only find-generic-password, not %q: "+
		"fake it at the caller's runner seam", argv)
	return deps.RunResult{ExitCode: 1}, err
}

func (unitNetKeychain) Start(_ context.Context, argv []string, _ deps.StartOptions) (deps.Process, error) {
	return nil, fmt.Errorf("testjail: the unit net's keychain plays Run only, not Start of %q", argv)
}

func (unitNetKeychain) LookPath(name string) (string, error) {
	return "", fmt.Errorf("testjail: the unit net's keychain looks up no tool (%s): %w", name, exec.ErrNotFound)
}

// ambientIdentity is the live seat a test process could inherit from the shell that started it: a
// tmux pane, a chat socket, a host Claude or Codex session, a systemd service (INVOCATION_ID, which
// a hosted CI runner's job inherits and which routes every chat spawn through systemd-run). The
// fence has none of them; a test that needs one sets it with t.Setenv. Literal names: testjail
// cannot import internal/resolve (import cycle).
var ambientIdentity = []string{
	"TMUX", "TMUX_PANE", "CHAT_CALL_SOCKET", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "INVOCATION_ID",
}

// clearAmbientIdentity empties every ambientIdentity variable through setenv.
func clearAmbientIdentity(setenv func(name, value string) error) error {
	for _, name := range ambientIdentity {
		if err := setenv(name, ""); err != nil {
			return fmt.Errorf("clear %s: %w", name, err)
		}
	}
	return nil
}

// pointUnitNetDirs points every unitNetDirs variable at a directory of its name under dir and clears the
// ambient identity, through setenv.
func pointUnitNetDirs(dir string, setenv func(name, value string) error) error {
	for _, name := range unitNetDirs {
		path := filepath.Join(dir, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			return fmt.Errorf("create the unit net's %s directory %s: %w", name, path, err)
		}
		if err := setenv(name, path); err != nil {
			return fmt.Errorf("point %s at %s: %w", name, path, err)
		}
	}
	return clearAmbientIdentity(setenv)
}

// unitNetSocketPathBytes is this platform's unix socket path budget, sun_path's length: 108 on Linux,
// 104 on macOS.
const unitNetSocketPathBytes = len(syscall.RawSockaddrUnix{}.Path)

// unitNetTMPDIR is the TMPDIR an armed unit binary's tests run under: the net's own dir where a socket
// path holds more than macOS's 104 bytes, else root as Run set it. The net's directory spends a dozen of
// the bytes, and macOS's 104 are exactly the budget the longest test names under t.TempDir need (Run
// sets TMPDIR to the canonical temp root, no wrapper, for that reason).
func unitNetTMPDIR(root, dir string, socketPathBytes int) string {
	if socketPathBytes > 104 {
		return dir
	}
	return root
}

// unitNet lays the net under root for an armed unit binary: pointUnitNetDirs's directories live in a
// directory of the binary's own (a one-letter prefix), which TMPDIR becomes where unitNetTMPDIR says.
// It returns the root the rest of Run builds under and the net's removal; any other binary keeps root
// and removes nothing.
func unitNet(root string) (string, func(), error) {
	if !unitBinary || !unitNetArmed {
		return root, func() {}, nil
	}
	dir, err := os.MkdirTemp(root, "u")
	if err != nil {
		return root, func() {}, fmt.Errorf("create the unit net under %s: %w", root, err)
	}
	remove := func() {
		if err := os.RemoveAll(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			warnSetup("remove the unit net %s: %v", dir, err)
		}
	}
	tmp := unitNetTMPDIR(root, dir, unitNetSocketPathBytes)
	if err := os.Setenv("TMPDIR", tmp); err != nil {
		return dir, remove, fmt.Errorf("set TMPDIR to %s: %w", tmp, err)
	}
	return dir, remove, pointUnitNetDirs(dir, os.Setenv)
}
