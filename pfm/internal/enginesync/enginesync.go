// Package enginesync runs the engine-sync binary, the single writer of every
// Codex and OpenCode artifact compiled from Claude sources. pfm never imports
// engine-sync's packages: every pfm door that once compiled such an artifact
// execs the binary through this package, per the caller interface
// plugins/engine-sync/docs/interface.md (binary resolution, exit codes, the
// --json shapes).
package enginesync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	// EnvBin names a dev or fence build that, when set, replaces the stable
	// path: it must be an executable regular file, else Resolve fails naming
	// it. It is never linked anywhere.
	EnvBin = "ENGINE_SYNC_BIN"
	// EnvHome overrides {engine-sync home} when absolute.
	EnvHome          = "ENGINE_SYNC_HOME"
	envXDGDataHome   = "XDG_DATA_HOME"
	envXDGConfigHome = "XDG_CONFIG_HOME"
	binaryName       = "engine-sync"
)

// Runner execs engine-sync. Its zero value runs the real process table under
// the real environment and $HOME.
type Runner struct {
	// Env resolves the binary's environment variables; nil is the process's.
	Env paths.Env
	// Exec runs the binary; nil is deps.RealRunner.
	Exec deps.Runner
	// Home is the home {engine-sync home} defaults under and every call
	// passes as --home; "" is Env's home.
	Home string
	// Capabilities are the host's capability names (HostCapabilities): a
	// Runner that sets them passes one --capability flag each on every call
	// that renders or checks agents; one that leaves them unset (the workbench
	// mirror) leaves engine-sync to the capabilities in the global file's targets.
	Capabilities []string
}

// UnavailableError is the binary not executable at $ENGINE_SYNC_BIN when it is
// set, else at the stable path. Its text is the interface's: every caller shows it as a
// failure — a failed install step, a failing doctor row, exit 1 — and never
// as a skip, except a binary-only install with nothing to wire and an uninstall
// preview, which names the engine-sync-owned entries left behind. The uninstall
// apply still tears down pfm-owned state and returns the unavailable failure.
type UnavailableError struct {
	Path  string
	Cause error
}

func (err *UnavailableError) Error() string {
	return fmt.Sprintf("engine-sync binary unavailable: %s: %v", err.Path, err.Cause)
}

func (err *UnavailableError) Unwrap() error { return err.Cause }

func (runner Runner) env() paths.Env {
	if runner.Env != nil {
		return runner.Env
	}
	return paths.OSEnv{}
}

func (runner Runner) exec() deps.Runner {
	if runner.Exec != nil {
		return runner.Exec
	}
	return deps.RealRunner{}
}

// home is the home the stable path defaults under and every call's --home.
func (runner Runner) home() (string, error) {
	if runner.Home != "" {
		return runner.Home, nil
	}
	home, err := runner.env().Home()
	if err != nil {
		return "", fmt.Errorf("resolve the engine-sync home: %w", err)
	}
	return home, nil
}

// DataHome is {engine-sync home}: $ENGINE_SYNC_HOME when absolute, else
// $XDG_DATA_HOME/engine-sync when absolute, else {home}/.local/share/engine-sync.
func DataHome(env paths.Env, home string) string {
	if dir := env.Get(EnvHome); filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	if dir := env.Get(envXDGDataHome); filepath.IsAbs(dir) {
		return filepath.Join(dir, binaryName)
	}
	return filepath.Join(home, ".local", "share", binaryName)
}

// GlobalConfigPath is {home}/.claude/engine-sync.json, the one machine file
// engine-sync reads: Professor's policy and, under "targets", the roster
// `pfm install` ran `engine-sync global` over.
func GlobalConfigPath(home string) string {
	return filepath.Join(home, ".claude", "engine-sync.json")
}

// OldConfigDirs are the directories engine-sync once read its machine config
// from and now refuses to start beside: {home}/.config/engine-sync and, when
// $XDG_CONFIG_HOME is absolute, $XDG_CONFIG_HOME/engine-sync. The first
// entry is the default one; the second appears only when it differs.
func OldConfigDirs(env paths.Env, home string) []string {
	dirs := []string{filepath.Join(home, ".config", binaryName)}
	if dir := env.Get(envXDGConfigHome); filepath.IsAbs(dir) {
		if xdg := filepath.Join(dir, binaryName); xdg != dirs[0] {
			dirs = append(dirs, xdg)
		}
	}
	return dirs
}

// StablePath is {engine-sync home}/bin/engine-sync, the link the plugin
// wrapper moves forward to a cached version.
func StablePath(env paths.Env, home string) string {
	return filepath.Join(DataHome(env, home), "bin", binaryName)
}

// Resolve returns the binary to run: $ENGINE_SYNC_BIN when set, else the
// stable path. Each must name an executable regular file; a set override that
// does not is an *UnavailableError naming the override path, never silently
// passed over for the stable binary.
func (runner Runner) Resolve() (string, error) {
	home, err := runner.home()
	if err != nil {
		return "", err
	}
	env := runner.env()
	if bin := env.Get(EnvBin); bin != "" {
		if err := executableFile(bin); err != nil {
			return "", &UnavailableError{Path: bin, Cause: fmt.Errorf("%s=%s: %w", EnvBin, bin, err)}
		}
		return bin, nil
	}
	stable := StablePath(env, home)
	if err := executableFile(stable); err != nil {
		return "", &UnavailableError{Path: stable, Cause: err}
	}
	return stable, nil
}

// executableFile reports why path is not an executable regular file; a link
// is followed, so the stable link to a cached version qualifies.
func executableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file (%s)", info.Mode().Type())
	}
	if info.Mode().Perm()&0o111 == 0 {
		return errors.New("not executable")
	}
	return nil
}

// run execs the resolved binary with args. A nonzero exit is the binary
// answering, never an error here; an unavailable binary or one that could not
// start is.
func (runner Runner) run(ctx context.Context, args []string) (deps.RunResult, error) {
	binary, err := runner.Resolve()
	if err != nil {
		obs.Logger(ctx).Error("engine-sync resolve", obs.FieldCmd, strings.Join(args, " "), obs.FieldErr, err)
		return deps.RunResult{ExitCode: -1}, err
	}
	result, err := runner.exec().Run(ctx, append([]string{binary}, args...), deps.RunOptions{Env: os.Environ()})
	if err != nil {
		wrapped := fmt.Errorf("run %s %s: %w", binary, strings.Join(args, " "), err)
		obs.Logger(ctx).Error("engine-sync run", obs.FieldCmd, strings.Join(args, " "), obs.FieldErr, wrapped)
		return result, wrapped
	}
	return result, nil
}
