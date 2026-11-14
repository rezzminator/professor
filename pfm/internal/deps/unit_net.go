package deps

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// unitNetArmed is on in an armed unit test binary (testjail's unit net): from then on no process
// starts and no host tool is looked up, by any path through this package.
var unitNetArmed atomic.Bool

// unitNetFixes ends every refusal with the two ways a test gets past it.
const unitNetFixes = "a unit test starts no process: fake it at the caller's runner seam (deps.FakeRunner)," +
	" or move the test to a _jail_test.go file"

// ArmUnitNet turns the refusal on for the rest of this process. Only testjail.Run's armed path
// calls it; production never does, so its starts and lookups run.
func ArmUnitNet() { unitNetArmed.Store(true) }

// unitNetServed maps a stand-in's path to the runner that plays it in process in an armed binary.
var unitNetServed sync.Map

// ServeUnitNet makes RealRunner answer every start of path from runner, in process, in an armed
// unit binary instead of refusing it, and Resolve of a registry row whose command is path answer
// path; unarmed, the registration is inert. Only testjail calls it, for a stand-in it writes for the
// whole binary (the default engine-sync) or a host tool every unit test must see empty (the macOS
// keychain); a test fakes anything else at its caller's runner seam.
func ServeUnitNet(path string, runner Runner) { unitNetServed.Store(path, runner) }

// unitNetServedCommand is the command path of name's registry row when ServeUnitNet serves it in an
// armed binary: Resolve answers it instead of refusing the lookup.
func unitNetServedCommand(name string) (string, bool) {
	if !unitNetArmed.Load() {
		return "", false
	}
	for index := range fixedCommands {
		entry := &fixedCommands[index]
		if entry.Name != name || entry.Command == "" {
			continue
		}
		if _, served := unitNetServed.Load(entry.Command); served {
			return entry.Command, true
		}
	}
	return "", false
}

// unitNetServedRunner is the runner ServeUnitNet registered for argv0 in an armed binary.
func unitNetServedRunner(argv0 string) (Runner, bool) {
	if !unitNetArmed.Load() {
		return nil, false
	}
	served, ok := unitNetServed.Load(argv0)
	if !ok {
		return nil, false
	}
	runner, ok := served.(Runner)
	return runner, ok
}

// RefuseUnitNetStart is the refusal of argv in an armed unit binary, a path argv[0] included; nil
// anywhere else. RealRunner calls it before every start; a raw start site outside deps that a unit
// test reaches calls it too (tmux.Command sets it as the command's Err).
func RefuseUnitNetStart(argv []string) error {
	if !unitNetArmed.Load() {
		return nil
	}
	return fmt.Errorf("UNIT-NET REFUSED start: %s — %s", strings.Join(argv, " "), unitNetFixes)
}

// refuseUnitNetLookup is Resolve's refusal of a bare name in an armed unit binary, wrapping
// exec.ErrNotFound so a caller's "tool absent" branch runs as on a machine without the tool; nil
// anywhere else. A path (it holds '/') walks no PATH: Resolve only checks the file the caller
// named, a test's own stub included, and the proof judges that read like any other.
func refuseUnitNetLookup(name string) error {
	if !unitNetArmed.Load() || strings.Contains(name, "/") {
		return nil
	}
	return fmt.Errorf("UNIT-NET REFUSED lookup: %s (%w) — %s", name, exec.ErrNotFound, unitNetFixes)
}
