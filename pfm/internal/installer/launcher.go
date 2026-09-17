package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"hostops/pfm/internal/atomicfile"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
)

type LauncherState string

const (
	LauncherOK        LauncherState = "ok"
	LauncherMissing   LauncherState = "missing"
	LauncherDisplaced LauncherState = "displaced"
)

type ClaudeLauncherStatus struct {
	State  LauncherState
	Target string
}

// ErrClaudeBinaryNotFound reports that no executable Claude binary exists in
// the configured location, the native versions directory, or PATH.
var ErrClaudeBinaryNotFound = errors.New("claude binary not found")

func managedClaudeLauncher(home string) string {
	return filepath.Join(
		home,
		".local",
		"share",
		"pfm",
		"install",
		"bin",
		pfmengine.MustLookup(pfmengine.Claude).Binary,
	)
}

func canonicalClaudeLauncher(home string) string {
	return filepath.Join(home, ".local", "bin", pfmengine.MustLookup(pfmengine.Claude).Binary)
}

func claudeLauncherStatePath(home string) string {
	return filepath.Join(home, ".local", "share", "pfm", "install", "launcher.state")
}

// ResolveClaudeBinary selects the real Claude executable behind pfm's managed
// launcher. A configured executable wins, followed by the newest native
// version and then each PATH component in order. The canonical and managed
// launchers, including physical aliases of the managed file, are never
// returned because doing so would recurse back into pfm.
func ResolveClaudeBinary(home, configuredBinary, pathEnv string) (string, error) {
	managed := managedClaudeLauncher(home)
	canonical := canonicalClaudeLauncher(home)

	if filepath.IsAbs(configuredBinary) {
		eligible, err := eligibleClaudeBinary(configuredBinary, canonical, managed)
		if err != nil {
			return "", fmt.Errorf("inspect configured Claude binary %s: %w", configuredBinary, err)
		}
		if eligible {
			return configuredBinary, nil
		}
	}

	report, err := InspectClaudeVersions(home, configuredBinary)
	if err != nil {
		return "", fmt.Errorf("inspect Claude versions: %w", err)
	}
	if report.Newest != nil {
		return report.Newest.Path, nil
	}

	for _, directory := range strings.Split(pathEnv, string(os.PathListSeparator)) {
		if directory == "" {
			directory = "."
		}
		candidate, err := filepath.Abs(filepath.Join(directory, pfmengine.MustLookup(pfmengine.Claude).Binary))
		if err != nil {
			return "", fmt.Errorf("resolve Claude PATH candidate in %s: %w", directory, err)
		}
		eligible, err := eligibleClaudeBinary(candidate, canonical, managed)
		if err != nil {
			return "", fmt.Errorf("inspect Claude PATH candidate %s: %w", candidate, err)
		}
		if eligible {
			return candidate, nil
		}
	}
	return "", ErrClaudeBinaryNotFound
}

func eligibleClaudeBinary(candidate, canonical, managed string) (bool, error) {
	candidate = filepath.Clean(candidate)
	if candidate == filepath.Clean(canonical) || candidate == filepath.Clean(managed) {
		return false, nil
	}
	candidateInfo, err := os.Stat(candidate)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !candidateInfo.Mode().IsRegular() || candidateInfo.Mode().Perm()&0o111 == 0 {
		return false, nil
	}
	managedInfo, err := os.Stat(managed)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, nil
		}
		return false, fmt.Errorf("inspect managed Claude launcher %s: %w", managed, err)
	}
	return !os.SameFile(candidateInfo, managedInfo), nil
}

func InspectClaudeLauncher(home string) (ClaudeLauncherStatus, error) {
	canonical := canonicalClaudeLauncher(home)
	info, err := os.Lstat(canonical)
	if errors.Is(err, fs.ErrNotExist) {
		return ClaudeLauncherStatus{State: LauncherMissing}, nil
	}
	if err != nil {
		return ClaudeLauncherStatus{}, fmt.Errorf("inspect canonical Claude launcher: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return ClaudeLauncherStatus{State: LauncherDisplaced, Target: canonical}, nil
	}
	target, err := os.Readlink(canonical)
	if err != nil {
		return ClaudeLauncherStatus{}, fmt.Errorf("read canonical Claude launcher: %w", err)
	}
	if filepath.Clean(target) == filepath.Clean(managedClaudeLauncher(home)) {
		managedInfo, statErr := os.Stat(target)
		if statErr != nil {
			return ClaudeLauncherStatus{}, fmt.Errorf("inspect managed Claude launcher: %w", statErr)
		}
		if !managedInfo.Mode().IsRegular() || managedInfo.Mode().Perm()&0o111 == 0 {
			return ClaudeLauncherStatus{}, fmt.Errorf("managed Claude launcher is not executable: %s", target)
		}
		return ClaudeLauncherStatus{State: LauncherOK, Target: target}, nil
	}
	return ClaudeLauncherStatus{State: LauncherDisplaced, Target: target}, nil
}

// RepairClaudeLauncher is the fast SessionStart repair path. A correct link
// costs one lstat and readlink; a displaced native symlink is recorded before
// it is atomically replaced.
func RepairClaudeLauncher(home string) (repaired bool, returnErr error) {
	status, err := InspectClaudeLauncher(home)
	if err != nil {
		return false, err
	}
	if status.State == LauncherOK {
		return false, nil
	}
	managed := managedClaudeLauncher(home)
	info, err := os.Stat(managed)
	if err != nil {
		return false, fmt.Errorf("inspect managed Claude launcher: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return false, fmt.Errorf("managed Claude launcher is not executable: %s", managed)
	}
	canonical := canonicalClaudeLauncher(home)
	if status.State == LauncherDisplaced {
		current, statErr := os.Lstat(canonical)
		if statErr != nil {
			return false, fmt.Errorf("inspect displaced Claude launcher: %w", statErr)
		}
		if current.Mode()&os.ModeSymlink == 0 {
			return false, fmt.Errorf("refuse to replace non-symlink Claude binary: %s", canonical)
		}
		if err := atomicfile.Write(claudeLauncherStatePath(home), []byte(status.Target+"\n"), 0o600); err != nil {
			return false, fmt.Errorf("record displaced Claude target: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		return false, fmt.Errorf("create canonical binary directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(canonical), ".claude-launcher-")
	if err != nil {
		return false, fmt.Errorf("reserve Claude launcher link: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return false, err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return false, err
	}
	defer func() {
		if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove Claude launcher staging %s: %w", temporaryPath, err))
		}
	}()
	if err := os.Symlink(managed, temporaryPath); err != nil {
		return false, fmt.Errorf("create managed Claude launcher link: %w", err)
	}
	if err := os.Rename(temporaryPath, canonical); err != nil {
		return false, fmt.Errorf("publish managed Claude launcher link: %w", err)
	}
	return true, nil
}

func (installer *engine) wireClaudeLauncher() error {
	status, err := InspectClaudeLauncher(installer.options.Home)
	if err != nil {
		return err
	}
	if status.State == LauncherOK {
		installer.ok(canonicalClaudeLauncher(installer.options.Home))
		return nil
	}
	description := "link " + canonicalClaudeLauncher(
		installer.options.Home,
	) + " -> " + managedClaudeLauncher(
		installer.options.Home,
	)
	return installer.change(description, func() error {
		_, err := RepairClaudeLauncher(installer.options.Home)
		return err
	})
}

// pruneClaudeVersions is wireClaudeLauncher's sibling: since pfm's launcher
// disables Claude Code's own version cleanup (it never symlinks the
// canonical binary straight into versions/), pfm owns retention instead.
// Preview mode prints the plan without touching a file — the same preview
// that pfm install's own dry run gives every other change; apply mode
// removes exactly the planned set. A live-process probe failure removes
// nothing and names why, rather than guess a version is unused.
func (installer *engine) pruneClaudeVersions() error {
	home := installer.options.Home
	report, err := InspectClaudeVersions(home, installer.options.ClaudeBinary)
	if err != nil {
		return fmt.Errorf("inspect Claude versions: %w", err)
	}
	if len(report.Versions) == 0 {
		return nil
	}
	procs := gather.NewProcFS(installer.options.ProcRoot)
	report = ProbeLiveClaudeVersions(report, procs, syscall.Kill)
	if report.LiveProbeErr != nil {
		installer.say("  skip    claude versions: retention not applied — %v", report.LiveProbeErr)
		return nil
	}
	remove, kept := PlanClaudeVersionPrune(report, ClaudeVersionKeepCount)
	for _, version := range remove {
		description := fmt.Sprintf(
			"remove %s (%s, %s)",
			version.Path,
			filepath.Base(version.Path),
			FormatClaudeVersionBytes(version.Bytes),
		)
		if err := installer.change(description, func() error {
			return os.Remove(version.Path)
		}); err != nil {
			return fmt.Errorf("prune Claude version %s: %w", version.Path, err)
		}
	}
	keptPaths := make([]string, 0, len(kept))
	for path := range kept {
		keptPaths = append(keptPaths, path)
	}
	sort.Strings(keptPaths)
	for _, path := range keptPaths {
		installer.ok(fmt.Sprintf("keep %s (%s)", path, kept[path]))
	}
	return nil
}

func (installer *engine) unwireClaudeLauncher() error {
	home := installer.options.Home
	status, err := InspectClaudeLauncher(home)
	if err != nil {
		return err
	}
	if status.State != LauncherOK {
		installer.skip(canonicalClaudeLauncher(home) + " is not the installed launcher")
		return nil
	}
	statePath := claudeLauncherStatePath(home)
	content, readErr := os.ReadFile(statePath)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return fmt.Errorf("read displaced Claude target: %w", readErr)
	}
	displaced := strings.TrimSpace(string(content))
	description := "remove " + canonicalClaudeLauncher(home)
	if displaced != "" {
		description = "restore " + canonicalClaudeLauncher(home) + " -> " + displaced
	}
	return installer.change(description, func() error {
		if err := os.Remove(canonicalClaudeLauncher(home)); err != nil {
			return err
		}
		if displaced != "" {
			if err := os.Symlink(displaced, canonicalClaudeLauncher(home)); err != nil {
				return err
			}
		}
		if err := os.Remove(statePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	})
}

// ClaudeAbsent reports whether path is pfm's own Claude launcher AND its last
// run exited 127 — the managed launch contract for "no real Claude binary
// resolved". Any other exit code, or a path that is not pfm's launcher, is a
// real failure, never absence: doctor's dep and harness-prompt rows both decide
// "Claude is absent" through this one check.
func ClaudeAbsent(home, path string, exitCode int) bool {
	if exitCode != 127 {
		return false
	}
	clean := filepath.Clean(path)
	if clean == filepath.Clean(canonicalClaudeLauncher(home)) || clean == filepath.Clean(managedClaudeLauncher(home)) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(clean)
	return err == nil && filepath.Clean(resolved) == filepath.Clean(managedClaudeLauncher(home))
}
