package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// callmeterPlugin is the plugin whose hooks run a Go binary: its wrapper runs
// {callmeter home}/bin/{plugin.json version}/callmeter and downloads a release
// binary only when that file is missing. A -dev copy therefore needs the binary
// built from the same checkout in place before the copy is refreshed.
const callmeterPlugin = "callmeter"

// callmeterBuildTimeout bounds the callmeter build: a cold module cache
// downloads its dependencies first.
const callmeterBuildTimeout = 10 * time.Minute

// callmeterVersionPattern is a version the wrapper accepts as a path element.
var callmeterVersionPattern = regexp.MustCompile(`^[0-9A-Za-z._+-]+$`)

// callmeterEnvDropped are the variables a callmeter build does not inherit:
// each would build another binary than the wrapper runs on this host.
var callmeterEnvDropped = []string{"GOFLAGS", "GOEXPERIMENT", "GOOS", "GOARCH", "CGO_ENABLED", "GOWORK"}

// callmeterEnvPinned replaces them: a GOFLAGS set in the environment also
// outranks one `go env -w` wrote, and GOWORK=off ignores any go.work above the
// checkout.
var callmeterEnvPinned = []string{"CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=-mod=readonly"}

// ensureCallmeterBinary builds ./cmd/callmeter from the module holding
// target's checkout, stamped with the checkout's plugin.json version, and puts
// it where the wrapper runs it (callmeterBinaryPath). A binary with the same
// bytes is left alone; otherwise the one in place is kept as callmeter.prev,
// the rollback, and the build renamed over it, so a hook never sees a partial
// file. Nothing else under the callmeter home is touched, its database never.
// A dry run builds nothing and says what would be built.
func (installer *engine) ensureCallmeterBinary(ctx context.Context, target *ClaudePluginTarget) error {
	version, err := callmeterCheckoutVersion(target.Checkout)
	if err != nil {
		return err
	}
	binary, err := callmeterBinaryPath(installer.env(), installer.options.Home, version)
	if err != nil {
		return err
	}
	module := filepath.Dir(filepath.Dir(target.Checkout))
	if !installer.apply {
		return installer.change(
			"build callmeter "+version+" from "+module+" and place it at "+binary+" when it differs", nil,
		)
	}
	goBinary, err := installer.processRunner().LookPath("go")
	if err != nil {
		return fmt.Errorf("build callmeter %s from %s: go not found: %w", version, module, err)
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(binary), err)
	}
	// The build lands in a scratch dir beside the binary, so placing it is a rename.
	staging, err := os.MkdirTemp(filepath.Dir(binary), ".callmeter.build-")
	if err != nil {
		return fmt.Errorf("stage the callmeter build: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	built := filepath.Join(staging, "callmeter")
	argv := []string{
		goBinary, "build", "-trimpath", "-buildvcs=false",
		"-ldflags", "-s -w -buildid= -X main.version=" + version,
		"-o", built, "./cmd/callmeter",
	}
	options := deps.RunOptions{Dir: module, Env: callmeterBuildEnvironment()}
	if err := runPluginCommandWithin(ctx, installer.processRunner(), argv, options, callmeterBuildTimeout); err != nil {
		return fmt.Errorf("build callmeter %s in %s: %w", version, module, err)
	}
	fresh, err := os.ReadFile(built)
	if err != nil {
		return fmt.Errorf("read the callmeter build %s: %w", built, err)
	}
	if len(fresh) == 0 {
		return fmt.Errorf("build callmeter %s in %s: the build wrote no binary at %s", version, module, built)
	}
	current, err := os.ReadFile(binary)
	switch {
	case err == nil && bytes.Equal(current, fresh) && callmeterExecutable(binary):
		installer.ok("callmeter binary " + binary + " matches the build of " + module)
		return nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("read %s: %w", binary, err)
	}
	existed := err == nil
	message := "place callmeter " + version + " built from " + module + " at " + binary
	if existed {
		message += ", the binary there kept as " + binary + ".prev"
	}
	return installer.change(message, func() error { return placeCallmeterBinary(built, binary, existed) })
}

// callmeterLockStale is the wrapper's own bound past which a lock is a killed
// run's (libexec/callmeter lock_is_stale).
const callmeterLockStale = 120 * time.Second

// placeCallmeterBinary renames built over binary, executable, holding the
// wrapper's download lock bin/.lock-{version}: no download moves a release
// binary over the build, and no other install places at once. With keepPrev
// the binary in place is first hard-linked to binary.prev through a name in
// built's own scratch dir, so the rollback is whole at every moment.
func placeCallmeterBinary(built, binary string, keepPrev bool) (err error) {
	lock := filepath.Join(filepath.Dir(filepath.Dir(binary)), ".lock-"+filepath.Base(filepath.Dir(binary)))
	if err := takeCallmeterLock(lock); err != nil {
		return fmt.Errorf("place %s: %w — rerun pfm install --yes", binary, err)
	}
	defer func() {
		if removeErr := os.Remove(lock); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("release the callmeter lock %s: %w", lock, removeErr))
		}
	}()
	if err := os.Chmod(built, 0o755); err != nil {
		return fmt.Errorf("make %s executable: %w", built, err)
	}
	if keepPrev {
		staged := filepath.Join(filepath.Dir(built), "callmeter.prev")
		if err := os.Link(binary, staged); err != nil {
			return fmt.Errorf("keep %s as the rollback: %w", binary, err)
		}
		if err := os.Rename(staged, binary+".prev"); err != nil {
			return fmt.Errorf("keep %s as the rollback: %w", binary, err)
		}
	}
	if err := os.Rename(built, binary); err != nil {
		return fmt.Errorf("place %s: %w", binary, err)
	}
	return nil
}

// takeCallmeterLock makes the lock directory, taking over one older than the
// wrapper's stale bound once; a live holder is an error naming it.
func takeCallmeterLock(lock string) error {
	err := os.Mkdir(lock, 0o700)
	if err == nil || !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, statErr := os.Stat(lock)
	if statErr != nil {
		return fmt.Errorf("lock %s: %w", lock, errors.Join(err, statErr))
	}
	if time.Since(info.ModTime()) < callmeterLockStale {
		return fmt.Errorf("lock %s held by a callmeter download or another pfm install", lock)
	}
	if err := os.Remove(lock); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("take over the stale lock %s: %w", lock, err)
	}
	return os.Mkdir(lock, 0o700)
}

// callmeterCheckoutVersion is the version in the checkout's
// .claude-plugin/plugin.json, which the copy's wrapper reads too.
func callmeterCheckoutVersion(checkout string) (string, error) {
	version, err := ClaudePluginCheckoutVersion(checkout)
	if err != nil {
		return "", err
	}
	// "." and ".." pass the wrapper's character set but leave bin/{version}/.
	if !callmeterVersionPattern.MatchString(version) || version == "." || version == ".." {
		return "", fmt.Errorf(
			"%s: version %q is not one the callmeter wrapper runs",
			filepath.Join(checkout, ".claude-plugin", "plugin.json"),
			escapeControl(version),
		)
	}
	return version, nil
}

// ClaudePluginCheckoutVersion is the version in a plugin checkout's
// .claude-plugin/plugin.json, the one its -dev copy is installed at; empty when
// the file names none. A file that cannot be read or parsed returns its error.
func ClaudePluginCheckoutVersion(checkout string) (string, error) {
	path := filepath.Join(checkout, ".claude-plugin", "plugin.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	return manifest.Version, nil
}

// CallmeterBinaryGap is the gap of target when it is the enabled callmeter
// -dev copy and the binary its wrapper runs (callmeterBinaryPath for the
// checkout's plugin.json version) is not an executable file: the wrapper then
// downloads a release binary, which the checksum list of an unreleased version
// refuses, and every hook drops its event. ok is false for every other target
// and for a binary in place.
func CallmeterBinaryGap(env paths.Env, home string, target ClaudePluginTarget) (gap ClaudePluginGap, ok bool) {
	if target.Name != callmeterPlugin || !target.Local || target.InstallOnly {
		return ClaudePluginGap{}, false
	}
	problem := "plugin " + target.ID + " binary "
	version, err := callmeterCheckoutVersion(target.Checkout)
	if err != nil {
		return ClaudePluginGap{Problem: problem + "cannot be checked: " + err.Error(), Fix: ClaudePluginRepair}, true
	}
	binary, err := callmeterBinaryPath(env, home, version)
	if err != nil {
		return ClaudePluginGap{Problem: problem + "cannot be checked: " + err.Error(), Fix: ClaudePluginRepair}, true
	}
	_, err = os.Stat(binary)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		problem += binary + " missing: its hooks would download a release binary, which an unreleased version fails"
	case err != nil:
		problem += binary + " cannot be read: " + err.Error()
	case !callmeterExecutable(binary):
		problem += binary + " is not an executable file"
	default:
		return ClaudePluginGap{}, false
	}
	return ClaudePluginGap{Problem: problem, Fix: ClaudePluginRepair}, true
}

// callmeterBinaryPath is where the callmeter wrapper runs version from: under
// CALLMETER_HOME, else under XDG_STATE_HOME when absolute or home's
// .local/state, plus callmeter — bin/{version}/callmeter.
func callmeterBinaryPath(env paths.Env, home, version string) (string, error) {
	root := env.Get("CALLMETER_HOME")
	if root == "" {
		state := env.Get("XDG_STATE_HOME")
		if !filepath.IsAbs(state) {
			if home == "" {
				return "", errors.New(
					"callmeter home unresolved: CALLMETER_HOME, XDG_STATE_HOME and the home dir are unset",
				)
			}
			state = filepath.Join(home, ".local", "state")
		}
		root = filepath.Join(state, "callmeter")
	}
	return filepath.Join(root, "bin", version, "callmeter"), nil
}

// callmeterBuildEnvironment is the inherited environment without
// callmeterEnvDropped, plus callmeterEnvPinned: the static binary the wrapper runs.
func callmeterBuildEnvironment() []string {
	inherited := os.Environ()
	environment := make([]string, 0, len(inherited)+len(callmeterEnvPinned))
	for _, entry := range inherited {
		name, _, _ := strings.Cut(entry, "=")
		dropped := false
		for _, drop := range callmeterEnvDropped {
			dropped = dropped || name == drop
		}
		if !dropped {
			environment = append(environment, entry)
		}
	}
	return append(environment, callmeterEnvPinned...)
}

// callmeterExecutable reports whether binary is a file the wrapper runs: one
// that is not executable it would replace with a download.
func callmeterExecutable(binary string) bool {
	info, err := os.Stat(binary)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
