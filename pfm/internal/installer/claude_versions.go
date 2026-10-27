package installer

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/semver"
)

// ClaudeVersionKeepCount is the retention window PlanClaudeVersionPrune uses
// by default: the newest parsed version plus one, since the second newest
// protects an in-flight `claude update` rollback.
const ClaudeVersionKeepCount = 2

// ClaudeVersion is the shared read-only native build observation.
type ClaudeVersion = gather.ClaudeVersion

// ClaudeVersionsReport is the shared structural native build report.
type ClaudeVersionsReport = gather.ClaudeVersionsReport

var claudeVersionsDir = gather.ClaudeVersionsDir

// InspectClaudeVersions is the shared structural inspector's compatibility facade.
var InspectClaudeVersions = gather.InspectClaudeVersions

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
