package gather

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/semver"
)

// ErrClaudeBinaryNotFound reports that no executable Claude binary exists in
// the configured location, the native versions directory, or PATH.
var ErrClaudeBinaryNotFound = errors.New("claude binary not found")

// ErrConfiguredClaudeBinaryNotFound reports that claude.binary names no
// executable in its absolute location or the caller-supplied PATH.
var ErrConfiguredClaudeBinaryNotFound = errors.New("configured Claude binary not found")

var readClaudeShim = os.ReadFile

func ManagedClaudeLauncher(home string) string {
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

func CanonicalClaudeLauncher(home string) string {
	return filepath.Join(home, ".local", "bin", pfmengine.MustLookup(pfmengine.Claude).Binary)
}

func ClaudeLauncherStatePath(home string) string {
	return filepath.Join(home, ".local", "share", "pfm", "install", "launcher.state")
}

// ResolveClaudeBinary selects the real Claude executable behind pfm's managed
// launcher. A configured executable wins and a missing configured command
// fails explicitly; otherwise the newest native version wins, followed by each
// PATH component in order. The canonical and managed launchers, including
// physical aliases of the managed file, are never returned because doing so
// would recurse back into pfm. Any pfm launcher shim, whatever home owns it, is
// never returned either: a shim of another home, or a copy of the asset, execs
// back into pfm just the same.
func ResolveClaudeBinary(home, configuredBinary, pathEnv string) (string, error) {
	managed := ManagedClaudeLauncher(home)
	canonical := CanonicalClaudeLauncher(home)

	if filepath.IsAbs(configuredBinary) {
		eligible, err := eligibleClaudeBinary(configuredBinary, canonical, managed)
		if err != nil {
			return "", fmt.Errorf("inspect configured Claude binary %s: %w", configuredBinary, err)
		}
		if eligible {
			return configuredBinary, nil
		}
	} else if configuredBinary != "" && configuredBinary != pfmengine.MustLookup(pfmengine.Claude).Binary {
		for _, directory := range strings.Split(pathEnv, string(os.PathListSeparator)) {
			if directory == "" {
				directory = "."
			}
			candidate, err := filepath.Abs(filepath.Join(directory, configuredBinary))
			if err != nil {
				return "", fmt.Errorf("resolve configured Claude binary %s in %s: %w", configuredBinary, directory, err)
			}
			eligible, err := eligibleClaudeBinary(candidate, canonical, managed)
			if err != nil {
				return "", fmt.Errorf("inspect configured Claude binary %s: %w", candidate, err)
			}
			if eligible {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("%w: %s", ErrConfiguredClaudeBinaryNotFound, configuredBinary)
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
	if isPfmClaudeShim(candidate) {
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

const (
	// claudeShimMaxBytes caps what isPfmClaudeShim reads: the launcher shim is
	// two lines, while the native Claude binary and the npm cli.js are orders
	// of magnitude larger and are never opened.
	claudeShimMaxBytes = 4096
	// claudeShimMarker is the subcommand the shim execs into pfm with.
	claudeShimMarker = "internal claude-launch"
)

// isPfmClaudeShim reports whether path is a pfm Claude launcher shim, whichever
// home owns it: it resolves to a managed launcher path under any home, or it is
// a small script that execs pfm's claude-launch subcommand.
// A candidate that stats but cannot be read counts as a shim: the loop guard fails closed.
func isPfmClaudeShim(path string) bool {
	if resolved, err := filepath.EvalSymlinks(path); err == nil &&
		strings.HasSuffix(resolved, string(filepath.Separator)+ManagedClaudeLauncher("")) {
		return true
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > claudeShimMaxBytes {
		return false
	}
	body, err := readClaudeShim(path)
	if err != nil {
		return true
	}
	return strings.HasPrefix(string(body), "#!") && strings.Contains(string(body), claudeShimMarker)
}

// ClaudeVersion is one build under ~/.local/share/claude/versions.
type ClaudeVersion struct {
	Path      string
	Version   semver.Version
	VersionOK bool
	Bytes     int64
	ModTime   time.Time
	ID        FileID
}

// ClaudeVersionsReport is the native version directory's picture: every
// build found (newest first), which build is newest, and why every
// structurally protected build is kept. InspectClaudeVersions alone never
// populates Live or LiveProbeErr — those are ProbeLiveClaudeVersions' job,
// a separate, deliberately optional call (see both doc comments).
type ClaudeVersionsReport struct {
	Dir      string
	Versions []ClaudeVersion
	Newest   *ClaudeVersion
	// Live maps a version's path to the live pids executing it. Populated
	// only by ProbeLiveClaudeVersions.
	Live map[string][]int
	// LiveProbeErr is set when the process table, or any one candidate
	// process's image, could not be read. A probe that could not run never
	// renders as "nothing found": PlanClaudeVersionPrune refuses every
	// version rather than risk removing one a chat is running right now.
	// Populated only by ProbeLiveClaudeVersions.
	LiveProbeErr error
	// Protected maps a version's path to the reason it is never removed:
	// "newest", "live (pids …)", "configured claude.binary", "displaced
	// native target", or "unparsed name".
	Protected map[string]string
}

func ClaudeVersionsDir(home string) string {
	return filepath.Join(home, ".local", "share", pfmengine.MustLookup(pfmengine.Claude).LongName, "versions")
}

// InspectClaudeVersions enumerates every regular, executable file under
// ~/.local/share/claude/versions, orders them newest first by parsed
// semantic version (an unparsed name sorts last and is never chosen as
// newest), and marks the structural protections: newest, unparsed name, the
// configured `claude.binary`, and a displaced native target.
//
// It deliberately touches NO process table. ResolveClaudeBinary — the managed
// launcher's hot path, run on every single `claude` launch — calls only this
// half; probing every live pid for its executing image (a fork of `lsof` per
// pid on macOS, see gather.ProcImage) is expensive and useless there, since
// resolution only wants the newest build's path. The live-process half is
// ProbeLiveClaudeVersions, a separate call only `pfm doctor` and
// pruneClaudeVersions (the destructive path) make.
func InspectClaudeVersions(home, configuredBinary string) (ClaudeVersionsReport, error) {
	dir := ClaudeVersionsDir(home)
	report := ClaudeVersionsReport{Dir: dir, Protected: map[string]string{}}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return report, nil
		}
		return ClaudeVersionsReport{}, fmt.Errorf("read Claude versions directory %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return ClaudeVersionsReport{}, fmt.Errorf("inspect Claude version %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		version, ok := semver.ParseVersion("v" + entry.Name())
		id, err := FileIDOf(path)
		if err != nil {
			return ClaudeVersionsReport{}, fmt.Errorf("identify Claude version %s: %w", path, err)
		}
		report.Versions = append(report.Versions, ClaudeVersion{
			Path: path, Version: version, VersionOK: ok, Bytes: info.Size(), ModTime: info.ModTime(), ID: id,
		})
	}

	newer := func(a, b ClaudeVersion) bool {
		if a.VersionOK != b.VersionOK {
			return a.VersionOK
		}
		if a.VersionOK && a.Version != b.Version {
			return b.Version.Less(a.Version)
		}
		return a.ModTime.After(b.ModTime)
	}
	sort.Slice(report.Versions, func(i, j int) bool { return newer(report.Versions[i], report.Versions[j]) })

	if len(report.Versions) > 0 && report.Versions[0].VersionOK {
		newest := report.Versions[0]
		report.Newest = &newest
		report.Protected[newest.Path] = "newest"
	}
	for _, version := range report.Versions {
		if !version.VersionOK {
			report.Protected[version.Path] = "unparsed name"
		}
	}
	// A not-exist error is genuine absence — nothing is configured or
	// displaced there, never a reason to refuse the prune. Any OTHER error
	// (permission denied, a path that is not a regular file, …) means pfm
	// could not identify what that path even IS, and removing a build it
	// failed to identify is the unsafe direction: refuse outright rather
	// than silently treat "could not look" as "not protected".
	if configuredBinary != "" {
		configuredID, err := FileIDOf(configuredBinary)
		switch {
		case err == nil:
			for _, version := range report.Versions {
				if version.ID == configuredID {
					report.Protected[version.Path] = "configured claude.binary"
				}
			}
		case os.IsNotExist(err):
			// nothing configured there
		default:
			return ClaudeVersionsReport{}, fmt.Errorf("identify configured Claude binary %s: %w", configuredBinary, err)
		}
	}
	if displaced := strings.TrimSpace(readClaudeLauncherState(home)); displaced != "" {
		displacedID, err := FileIDOf(displaced)
		switch {
		case err == nil:
			for _, version := range report.Versions {
				if version.ID == displacedID {
					report.Protected[version.Path] = "displaced native target"
				}
			}
		case os.IsNotExist(err):
			// the recorded displaced target no longer exists
		default:
			return ClaudeVersionsReport{}, fmt.Errorf("identify displaced Claude target %s: %w", displaced, err)
		}
	}

	return report, nil
}

// readClaudeLauncherState reads the displaced-native-target recorded by
// RepairClaudeLauncher, or "" when none was ever recorded — the same file
// unwireClaudeLauncher reads to restore a pre-pfm launcher on uninstall.
func readClaudeLauncherState(home string) string {
	content, err := os.ReadFile(ClaudeLauncherStatePath(home))
	if err != nil {
		return ""
	}
	return string(content)
}
