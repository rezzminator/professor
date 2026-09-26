package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/semver"
)

// ClaudeVersionKeepCount is the retention window PlanClaudeVersionPrune uses
// by default: the newest parsed version plus one, since the second newest
// protects an in-flight `claude update` rollback.
const ClaudeVersionKeepCount = 2

// ClaudeVersion is one build under ~/.local/share/claude/versions.
type ClaudeVersion struct {
	Path      string
	Version   semver.Version
	VersionOK bool
	Bytes     int64
	ModTime   time.Time
	ID        gather.FileID
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

func claudeVersionsDir(home string) string {
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
	dir := claudeVersionsDir(home)
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
		id, err := gather.FileIDOf(path)
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
		configuredID, err := gather.FileIDOf(configuredBinary)
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
		displacedID, err := gather.FileIDOf(displaced)
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

// Signaler delivers a signal to pid; signal 0 only asks whether pid exists.
// syscall.Kill is the real one; a test's fake plays the kernel — the same
// contract as internal/stale.Signaler (stale.go:45-47), defined again here
// rather than imported so the installer package's dependency surface stays
// its own; the two are not the same concern and should not couple.
type Signaler func(pid int, signal syscall.Signal) error

// ProbeLiveClaudeVersions is InspectClaudeVersions' separate, expensive
// half: cross-referencing the live process table against every version
// already enumerated, so a build a running chat is executing is never read
// as unused. Only `pfm doctor` and pruneClaudeVersions call it — never the
// managed launcher's hot path (see InspectClaudeVersions's doc comment).
//
// Scoping mirrors internal/stale.Find (stale.go:53-89): a pid is a Claude
// candidate only when its argv[0] names the Claude binary, lives inside the
// versions directory, or its own basename parses as a version — every other
// pid, and any pid whose Cmdline could not be read, is skipped BEFORE Image
// is ever called. A real host's process table is full of pids pfm has no
// business probing, and Image forks a subprocess per call on macOS
// (gather.ProcImage, procfs_darwin.go) — probing every pid on the box is
// both wasteful and the direct cause of DEFECT 2: any one other user's
// process (EACCES reading /proc/<pid>/exe, or an empty lsof answer for a
// pid pfm does not own) used to poison the whole scan.
//
// A candidate whose Image call fails because the pid is already gone
// (kill(pid, 0) -> ESRCH) is skipped, not refused — stale.go does the same.
// Any OTHER candidate Image error still refuses the whole prune, naming the
// pid: a live build pfm could not identify is the unsafe direction to guess
// "unused" about.
func ProbeLiveClaudeVersions(report ClaudeVersionsReport, procs gather.ProcFS, signal Signaler) ClaudeVersionsReport {
	if report.Protected == nil {
		report.Protected = map[string]string{}
	}
	report.Live = map[string][]int{}

	imager, ok := procs.(gather.ProcImage)
	if !ok {
		report.LiveProbeErr = fmt.Errorf("process table %T reports no image identity", procs)
		return report
	}
	pids, pidsErr := procs.PIDs()
	if pidsErr != nil {
		report.LiveProbeErr = fmt.Errorf("read process table: %w", pidsErr)
		return report
	}
	dir := report.Dir
	for _, pid := range pids {
		argv, cmdErr := procs.Cmdline(pid)
		if cmdErr != nil || !isClaudeVersionCandidate(argv, dir) {
			continue
		}
		image, imageErr := imager.Image(pid)
		if imageErr != nil {
			if errors.Is(signal(pid, 0), syscall.ESRCH) {
				continue // the pid exited mid-scan — not a probe failure
			}
			if report.LiveProbeErr == nil {
				report.LiveProbeErr = fmt.Errorf("image for pid %d: %w", pid, imageErr)
			}
			continue
		}
		for _, version := range report.Versions {
			if version.ID == image {
				report.Live[version.Path] = append(report.Live[version.Path], pid)
			}
		}
	}
	for path, pids := range report.Live {
		sort.Ints(pids)
		report.Live[path] = pids
		report.Protected[path] = fmt.Sprintf("live (pids %s)", joinPids(pids))
	}
	return report
}

// isClaudeVersionCandidate reports whether argv names a process worth
// paying Image's cost for: its argv[0] is named after the Claude binary,
// lives inside the versions directory itself, or its own basename parses as
// a version. Anything else — another user's editor, a shell, an unrelated
// daemon — is not a Claude Code process at all and is skipped before ever
// touching gather.ProcImage.
func isClaudeVersionCandidate(argv []string, versionsDir string) bool {
	if len(argv) == 0 || argv[0] == "" {
		return false
	}
	base := filepath.Base(argv[0])
	if base == pfmengine.MustLookup(pfmengine.Claude).Binary {
		return true
	}
	if withinDir(argv[0], versionsDir) {
		return true
	}
	_, ok := semver.ParseVersion("v" + base)
	return ok
}

// withinDir reports whether path is versionsDir itself or a descendant of
// it, comparing cleaned paths so "../versions-evil" is never mistaken for a
// match.
func withinDir(path, versionsDir string) bool {
	if versionsDir == "" {
		return false
	}
	cleanDir := filepath.Clean(versionsDir)
	cleanPath := filepath.Clean(path)
	if cleanPath == cleanDir {
		return true
	}
	relative, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// readClaudeLauncherState reads the displaced-native-target recorded by
// RepairClaudeLauncher, or "" when none was ever recorded — the same file
// unwireClaudeLauncher reads to restore a pre-pfm launcher on uninstall.
func readClaudeLauncherState(home string) string {
	content, err := os.ReadFile(claudeLauncherStatePath(home))
	if err != nil {
		return ""
	}
	return string(content)
}

func joinPids(pids []int) string {
	parts := make([]string, len(pids))
	for index, pid := range pids {
		parts[index] = fmt.Sprintf("%d", pid)
	}
	return strings.Join(parts, ",")
}

// FormatClaudeVersionBytes renders a byte count the way the versions doctor
// row and the prune preview both print it — the one shared implementation
// so the two surfaces can never drift on what "1.3G" means.
func FormatClaudeVersionBytes(count int64) string {
	const (
		kilobyte = 1024
		megabyte = kilobyte * 1024
		gigabyte = megabyte * 1024
	)
	switch {
	case count >= gigabyte:
		return fmt.Sprintf("%.1fG", float64(count)/float64(gigabyte))
	case count >= megabyte:
		return fmt.Sprintf("%.0fM", float64(count)/float64(megabyte))
	case count >= kilobyte:
		return fmt.Sprintf("%.0fK", float64(count)/float64(kilobyte))
	default:
		return fmt.Sprintf("%dB", count)
	}
}

// PlanClaudeVersionPrune decides which versions InspectClaudeVersions found
// are safe to remove: everything outside the newest `keep` parsed versions
// and not otherwise protected. A live-process probe failure refuses
// everything, naming each kept version "probe failed" rather than guessing
// any of them are unused.
func PlanClaudeVersionPrune(report ClaudeVersionsReport, keep int) (remove []ClaudeVersion, kept map[string]string) {
	kept = make(map[string]string, len(report.Versions))
	for path, reason := range report.Protected {
		kept[path] = reason
	}
	if report.LiveProbeErr != nil {
		kept = make(map[string]string, len(report.Versions))
		for _, version := range report.Versions {
			kept[version.Path] = "probe failed"
		}
		return nil, kept
	}
	parsedSeen := 0
	for _, version := range report.Versions {
		if !version.VersionOK {
			continue // already protected as "unparsed name"
		}
		parsedSeen++
		if parsedSeen <= keep {
			if _, already := kept[version.Path]; !already {
				// Only the FIRST parsed build inside the keep window is
				// actually the newest (report.Versions is sorted newest
				// first) — every other build the window keeps is a real,
				// distinct build, not a second "newest" (issue #24 F7).
				if parsedSeen == 1 {
					kept[version.Path] = "newest"
				} else {
					kept[version.Path] = "within keep window"
				}
			}
			continue
		}
		if _, protected := kept[version.Path]; protected {
			continue
		}
		remove = append(remove, version)
	}
	return remove, kept
}
