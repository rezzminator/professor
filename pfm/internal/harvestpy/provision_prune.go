package harvestpy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// pruneRemoveAll removes one superseded environment; a test swaps it to fail.
var pruneRemoveAll = os.RemoveAll

// pruneHostGOOS is the OS whose process table the prune reads; a test pins it.
var pruneHostGOOS = runtime.GOOS

// PrunedEnvironment is one superseded environment a prune removed.
type PrunedEnvironment struct {
	Path  string
	Bytes int64
}

// KeptEnvironment is one superseded environment a prune kept, and why.
type KeptEnvironment struct {
	Path, Reason string
}

// PruneFailure is one failed look or removal.
type PruneFailure struct {
	Path string
	Err  error
}

// PruneReport is one prune pass over a platform's environment root.
type PruneReport struct {
	Removed []PrunedEnvironment
	Kept    []KeptEnvironment
	Failed  []PruneFailure
}

func (report *PruneReport) remove(path string, bytes int64) {
	if err := pruneRemoveAll(path); err != nil {
		report.Failed = append(report.Failed, PruneFailure{Path: path, Err: err})
		return
	}
	report.Removed = append(report.Removed, PrunedEnvironment{Path: path, Bytes: bytes})
}

// PruneEnvironments prunes root's environments for platform under the
// provisioning lock, so a provision in flight never loses the env it is
// building. A host with no environment root is left without one.
func PruneEnvironments(ctx context.Context, root string, platform Platform, procRoot string) PruneReport {
	var report PruneReport
	if platform.GOOS == "" {
		platform.GOOS, platform.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	envRoot := filepath.Join(root, "env", platform.String())
	if _, err := os.Lstat(envRoot); errors.Is(err, fs.ErrNotExist) {
		return report
	} else if err != nil {
		report.Failed = append(report.Failed, PruneFailure{Path: envRoot, Err: err})
		return report
	}
	release, err := lockProvisionRoot(ctx, envRoot)
	if err != nil {
		report.Failed = append(report.Failed, PruneFailure{Path: envRoot, Err: err})
		return report
	}
	report = pruneEnvironmentRoot(envRoot, procRoot, pruneHostGOOS)
	if err := release(); err != nil {
		report.Failed = append(report.Failed, PruneFailure{Path: envRoot, Err: err})
	}
	return report
}

// pruneEnvironmentRoot removes, with the provisioning lock held, every env dir
// current does not name and no live process runs from, plus a crash's
// leftover current-pointer staging link. A running sidecar needs its env:
// its venv's pyvenv.cfg home= names the env's own python/bin, so its stdlib
// and site-packages load from there for as long as it runs. Liveness is a
// Linux process's /proc/{pid}/exe resolving under the env; where it cannot be
// read every superseded env is kept and the reason says so.
func pruneEnvironmentRoot(envRoot, procRoot, goos string) PruneReport {
	var report PruneReport
	current := filepath.Join(envRoot, "current")
	target, err := os.Readlink(current)
	if errors.Is(err, fs.ErrNotExist) {
		return report
	}
	if err != nil {
		report.Failed = append(report.Failed, PruneFailure{Path: current, Err: err})
		return report
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(envRoot, target)
	}
	keep := filepath.Clean(target)
	entries, err := os.ReadDir(envRoot)
	if err != nil {
		report.Failed = append(report.Failed, PruneFailure{Path: envRoot, Err: err})
		return report
	}
	executables, liveErr := liveExecutables(procRoot, goos)
	for _, entry := range entries {
		path := filepath.Join(envRoot, entry.Name())
		switch {
		case strings.HasPrefix(entry.Name(), ".current-") && entry.Type()&fs.ModeSymlink != 0:
			report.remove(path, 0)
		case !entry.IsDir() || path == keep:
		case liveErr != nil:
			report.Kept = append(
				report.Kept,
				KeptEnvironment{Path: path, Reason: "liveness unreadable: " + liveErr.Error()},
			)
		default:
			if pid := runningFrom(executables, path); pid != "" {
				report.Kept = append(report.Kept, KeptEnvironment{Path: path, Reason: "pid " + pid + " runs from it"})
				continue
			}
			report.remove(path, measureTree(path).EnvironmentBytes)
		}
	}
	return report
}

// liveExecutables maps each readable process to its executable. A process
// gone mid-read, a kernel thread and another user's process are skipped:
// envs are 0700, so another user cannot run from one.
func liveExecutables(procRoot, goos string) (map[string]string, error) {
	if goos != "linux" {
		return nil, fmt.Errorf("%s has no /proc to show a process's executable", goos)
	}
	if procRoot == "" {
		procRoot = "/proc"
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, fmt.Errorf("read processes: %w", err)
	}
	executables := map[string]string{}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		exe, err := os.Readlink(filepath.Join(procRoot, entry.Name(), "exe"))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) ||
			errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EPERM) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read process %s executable: %w", entry.Name(), err)
		}
		executables[entry.Name()] = strings.TrimSuffix(exe, " (deleted)")
	}
	return executables, nil
}

// runningFrom names a process whose executable lies under env, or "".
func runningFrom(executables map[string]string, env string) string {
	physical, err := filepath.EvalSymlinks(env)
	if err != nil {
		physical = env
	}
	for pid, exe := range executables {
		for _, root := range []string{env, physical} {
			if exe == root || strings.HasPrefix(exe, root+string(filepath.Separator)) {
				return pid
			}
		}
	}
	return ""
}
