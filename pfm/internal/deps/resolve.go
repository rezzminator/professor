package deps

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// resolveCache memoizes Resolve's successful lookups, keyed by binary name
// and the $PATH they were resolved under. Every tmux capture-pane, every
// spawned codex app-server, and every other exec that runs through
// deps.Executable paid for a fresh Registry() build (a slice allocation plus
// a walk of every fixed and engine-configured entry) and a fresh
// exec.LookPath PATH walk on EVERY call — cheap once, ruinous at the cadence
// a parked picker's Codex idle-identity poll and Codex limits sampler drive
// it (2026-09-08 measurement, devbox). Keying on $PATH rather than name alone
// keeps this invisible to tests that vary PATH per case: a changed PATH is a
// cache miss, not a stale hit.
var (
	resolveCacheMu sync.Mutex
	resolveCache   = map[string]string{}
)

func resolveCacheKey(name string) string {
	return name + "\x00" + paths.OSEnv{}.Get("PATH")
}

// resolveCached returns a memoized resolution for name, invalidating it if
// the file it names no longer exists — a binary can move or be uninstalled
// mid-process, and a cache must never outlive the thing it names.
func resolveCached(name string) (string, bool) {
	resolveCacheMu.Lock()
	defer resolveCacheMu.Unlock()
	key := resolveCacheKey(name)
	path, ok := resolveCache[key]
	if !ok {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		delete(resolveCache, key)
		return "", false
	}
	return path, true
}

func rememberResolved(name, path string) {
	resolveCacheMu.Lock()
	defer resolveCacheMu.Unlock()
	resolveCache[resolveCacheKey(name)] = path
}

// Resolve is the only production seam that obtains an executable path.
// Config-owned names are permitted even though source-literal names are held
// to the registry by the source guard.
func Resolve(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("dependency command is empty")
	}
	if err := refuseUnitNetLookup(name); err != nil {
		if gateErr := platformGate(name); gateErr != nil {
			return "", gateErr
		}
		return "", err
	}
	if cached, ok := resolveCached(name); ok {
		return cached, nil
	}
	if err := platformGate(name); err != nil {
		return "", err
	}
	command := name
	entries := Registry(Options{Home: ".", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	for index := range entries {
		entry := entries[index]
		if entry.Name == name || entry.Command == name {
			command = entry.Command
			break
		}
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return path, err
	}
	rememberResolved(name, path)
	return path, nil
}

// platformGate is the one platform gate: it refuses name when a registry row
// names it and is gated to platforms other than this one. Only the fixed rows
// carry Platforms, so it reads them without building the registry. Resolve
// and RealRunner's bare argv[0] both pass through it.
func platformGate(name string) error {
	for index := range fixedCommands {
		entry := &fixedCommands[index]
		if entry.Name != name && (entry.Command == "" || entry.Command != name) {
			continue
		}
		if !entry.AppliesTo(runtime.GOOS) {
			return fmt.Errorf("dependency %s is not supported on %s", entry.Name, runtime.GOOS)
		}
	}
	return nil
}

// Executable resolves name through Resolve and, when the lookup fails, hands
// back the bare name, so a missing binary surfaces only later as exec.Cmd's
// not-found error. A caller that can report a missing binary calls Resolve
// and returns its error. tmux/tmux.go keeps the fallback on purpose:
// tmux.CouldNotRun classifies exec.Cmd's own not-found error as "could not
// look".
func Executable(name string) string {
	path, err := Resolve(name)
	if err != nil {
		return name
	}
	return path
}

// gateArgv0 refuses argv in an armed unit binary (unit_net.go), a path
// argv[0] included, and otherwise a bare argv[0] the registry gates to
// another platform, before anything execs, with Resolve's own text. A path
// (it holds '/') is the caller's explicit choice, a test's stub included, and
// is not platform-gated.
func gateArgv0(argv []string) error {
	if err := RefuseUnitNetStart(argv); err != nil {
		return err
	}
	if strings.Contains(argv[0], "/") {
		return nil
	}
	return platformGate(argv[0])
}
