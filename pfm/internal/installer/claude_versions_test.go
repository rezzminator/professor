package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"hostops/pfm/internal/gather"
)

// fakeVersionsProcFS serves a hand-built process table with real
// device+inode image identity — the ProcImage extension ProbeLiveClaudeVersions
// needs to tell a live build apart from every unused one — plus a Cmdline
// map, since candidate scoping reads argv[0] before Image is ever called.
type fakeVersionsProcFS struct {
	cmdlines map[int][]string
	images   map[int]gather.FileID
	// imageErrs overrides images for a pid that must report an Image error
	// instead of an identity, e.g. to prove a non-candidate pid's error is
	// never even reached, or that a candidate's error refuses the prune.
	imageErrs map[int]error
}

func (fake fakeVersionsProcFS) PIDs() ([]int, error) {
	pids := make([]int, 0, len(fake.cmdlines))
	for pid := range fake.cmdlines {
		pids = append(pids, pid)
	}
	return pids, nil
}

func (fake fakeVersionsProcFS) Cmdline(pid int) ([]string, error) {
	argv, ok := fake.cmdlines[pid]
	if !ok {
		return nil, fmt.Errorf("no such pid %d", pid)
	}
	return argv, nil
}
func (fakeVersionsProcFS) Environ(int) (map[string]string, error) { return nil, nil }
func (fakeVersionsProcFS) FDLinks(int) ([]gather.FDLink, error)   { return nil, nil }
func (fakeVersionsProcFS) Stat(int) (gather.ProcStat, error)      { return gather.ProcStat{}, nil }

func (fake fakeVersionsProcFS) Image(pid int) (gather.FileID, error) {
	if err, ok := fake.imageErrs[pid]; ok {
		return gather.FileID{}, err
	}
	id, ok := fake.images[pid]
	if !ok {
		return gather.FileID{}, fmt.Errorf("no such pid %d", pid)
	}
	return id, nil
}

// noImageProcFS satisfies gather.ProcFS but deliberately not the optional
// gather.ProcImage extension — the shape a process table takes on a
// platform, or in a fixture, that cannot answer "which file is pid N
// executing". ProbeLiveClaudeVersions must treat that as a probe failure,
// not as "nothing live".
type noImageProcFS struct{}

func (noImageProcFS) PIDs() ([]int, error)                   { return []int{4242}, nil }
func (noImageProcFS) Cmdline(int) ([]string, error)          { return []string{"claude"}, nil }
func (noImageProcFS) Environ(int) (map[string]string, error) { return nil, nil }
func (noImageProcFS) FDLinks(int) ([]gather.FDLink, error)   { return nil, nil }
func (noImageProcFS) Stat(int) (gather.ProcStat, error)      { return gather.ProcStat{}, nil }

// alwaysAliveSignal answers every kill(pid, 0) probe as "still running" —
// the shape a candidate's genuine Image failure takes, as opposed to the
// pid simply having exited mid-scan.
func alwaysAliveSignal(int, syscall.Signal) error { return nil }

// goneSignal answers every kill(pid, 0) probe as ESRCH — the pid exited
// mid-scan, which ProbeLiveClaudeVersions must skip rather than refuse.
func goneSignal(int, syscall.Signal) error { return syscall.ESRCH }

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestInspectClaudeVersionsMarksLiveNewestConfiguredAndUnparsed(t *testing.T) {
	home := t.TempDir()
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	newest := filepath.Join(versions, "2.1.270")
	live := filepath.Join(versions, "2.1.263")
	configured := filepath.Join(versions, "2.1.250")
	unparsed := filepath.Join(versions, "canary-build")
	for _, path := range []string{newest, live, configured, unparsed} {
		writeExecutable(t, path)
	}

	liveID, err := gather.FileIDOf(live)
	if err != nil {
		t.Fatal(err)
	}
	procs := fakeVersionsProcFS{
		cmdlines: map[int][]string{4242: {live}},
		images:   map[int]gather.FileID{4242: liveID},
	}

	report, err := InspectClaudeVersions(home, configured)
	if err != nil {
		t.Fatal(err)
	}
	if report.LiveProbeErr != nil {
		t.Fatalf("InspectClaudeVersions alone set a live-probe result: %v", report.LiveProbeErr)
	}
	if report.Live != nil {
		t.Fatalf("InspectClaudeVersions alone populated Live: %v", report.Live)
	}
	report = ProbeLiveClaudeVersions(report, procs, alwaysAliveSignal)
	if report.LiveProbeErr != nil {
		t.Fatalf("unexpected live probe error: %v", report.LiveProbeErr)
	}
	if report.Newest == nil || report.Newest.Path != newest {
		t.Fatalf("newest=%#v, want %s", report.Newest, newest)
	}
	if reason := report.Protected[newest]; reason != "newest" {
		t.Fatalf("newest reason=%q, want %q", reason, "newest")
	}
	if reason := report.Protected[live]; reason != "live (pids 4242)" {
		t.Fatalf("live reason=%q, want %q", reason, "live (pids 4242)")
	}
	if pids := report.Live[live]; len(pids) != 1 || pids[0] != 4242 {
		t.Fatalf("live pids=%v, want [4242]", pids)
	}
	if reason := report.Protected[configured]; reason != "configured claude.binary" {
		t.Fatalf("configured reason=%q, want %q", reason, "configured claude.binary")
	}
	if reason := report.Protected[unparsed]; reason != "unparsed name" {
		t.Fatalf("unparsed reason=%q, want %q", reason, "unparsed name")
	}
	for _, version := range report.Versions {
		if version.Path == unparsed && version.VersionOK {
			t.Fatalf("unparsed name %q was read as a valid version", unparsed)
		}
	}
}

func TestPlanClaudeVersionPruneRefusesEverythingWhenTheImageProbeFails(t *testing.T) {
	home := t.TempDir()
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(versions, "1.0.0")
	second := filepath.Join(versions, "2.0.0")
	writeExecutable(t, first)
	writeExecutable(t, second)

	report, err := InspectClaudeVersions(home, "")
	if err != nil {
		t.Fatal(err)
	}
	report = ProbeLiveClaudeVersions(report, noImageProcFS{}, alwaysAliveSignal)
	if report.LiveProbeErr == nil {
		t.Fatal("expected a live-probe error from a process table with no image identity")
	}

	remove, kept := PlanClaudeVersionPrune(report, 2)
	if len(remove) != 0 {
		t.Fatalf("remove=%#v, want none when the probe failed", remove)
	}
	if len(kept) != 2 {
		t.Fatalf("kept=%v, want both versions named", kept)
	}
	for path, reason := range kept {
		if reason != "probe failed" {
			t.Fatalf("kept[%s]=%q, want %q", path, reason, "probe failed")
		}
	}
}

// TestProbeLiveClaudeVersionsSkipsNonCandidatePidsEvenWhenTheirImageErrors
// is DEFECT 2's core regression: on a real host, most pids are not Claude at
// all, and an ordinary permission error reading a stranger's process image
// (EACCES on Linux, an empty lsof answer on macOS) must never poison the
// whole scan. A non-candidate pid's Image is never even called — its argv[0]
// names something else entirely.
func TestProbeLiveClaudeVersionsSkipsNonCandidatePidsEvenWhenTheirImageErrors(t *testing.T) {
	home := t.TempDir()
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(versions, "2.1.263")
	writeExecutable(t, live)
	liveID, err := gather.FileIDOf(live)
	if err != nil {
		t.Fatal(err)
	}

	report, err := InspectClaudeVersions(home, "")
	if err != nil {
		t.Fatal(err)
	}
	procs := fakeVersionsProcFS{
		cmdlines: map[int][]string{
			// A stranger's process, not Claude at all — never a candidate.
			// Its Image would error if called at all; it must not be.
			999: {"/usr/bin/vim", "notes.txt"},
			// The Claude build actually running.
			4242: {live},
		},
		images: map[int]gather.FileID{4242: liveID},
		imageErrs: map[int]error{
			999: fmt.Errorf("EACCES: not this process's owner"),
		},
	}

	report = ProbeLiveClaudeVersions(report, procs, alwaysAliveSignal)
	if report.LiveProbeErr != nil {
		t.Fatalf("a non-candidate pid's Image error poisoned the whole probe: %v", report.LiveProbeErr)
	}
	if pids := report.Live[live]; len(pids) != 1 || pids[0] != 4242 {
		t.Fatalf("live pids=%v, want [4242] — live detection still works for the Claude pid", pids)
	}
}

// TestProbeLiveClaudeVersionsRefusesWhenACandidatePidsImageErrorsAndItIsStillAlive
// is DEFECT 2's other half: a Claude-shaped pid whose Image call genuinely
// fails, while the pid is confirmed still running (kill(pid,0) succeeds),
// must still refuse the whole prune — a live build pfm could not identify is
// the unsafe direction to guess "unused" about.
func TestProbeLiveClaudeVersionsRefusesWhenACandidatePidsImageErrorsAndItIsStillAlive(t *testing.T) {
	home := t.TempDir()
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	build := filepath.Join(versions, "2.1.263")
	writeExecutable(t, build)

	report, err := InspectClaudeVersions(home, "")
	if err != nil {
		t.Fatal(err)
	}
	procs := fakeVersionsProcFS{
		cmdlines:  map[int][]string{4242: {build}},
		imageErrs: map[int]error{4242: fmt.Errorf("image read failed")},
	}

	report = ProbeLiveClaudeVersions(report, procs, alwaysAliveSignal)
	if report.LiveProbeErr == nil {
		t.Fatal("a live candidate's Image error did not refuse the prune")
	}
	remove, kept := PlanClaudeVersionPrune(report, 2)
	if len(remove) != 0 {
		t.Fatalf("remove=%#v, want none when a live candidate could not be identified", remove)
	}
	if kept[build] != "probe failed" {
		t.Fatalf("kept[%s]=%q, want %q", build, kept[build], "probe failed")
	}
}

// TestProbeLiveClaudeVersionsSkipsACandidateWhoseImageFailsBecauseThePidIsGone
// pins the ESRCH carve-out precedent internal/stale.Find also relies on: a
// candidate pid that exited mid-scan is skipped, not refused.
func TestProbeLiveClaudeVersionsSkipsACandidateWhoseImageFailsBecauseThePidIsGone(t *testing.T) {
	home := t.TempDir()
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	build := filepath.Join(versions, "2.1.263")
	writeExecutable(t, build)

	report, err := InspectClaudeVersions(home, "")
	if err != nil {
		t.Fatal(err)
	}
	procs := fakeVersionsProcFS{
		cmdlines:  map[int][]string{4242: {build}},
		imageErrs: map[int]error{4242: fmt.Errorf("image read failed")},
	}

	report = ProbeLiveClaudeVersions(report, procs, goneSignal)
	if report.LiveProbeErr != nil {
		t.Fatalf("a candidate pid that already exited refused the prune: %v", report.LiveProbeErr)
	}
	if len(report.Live) != 0 {
		t.Fatalf("live=%v, want none — the exited pid was never live", report.Live)
	}
}

func TestPlanClaudeVersionPruneKeepsNewestTwoAndRemovesTheRest(t *testing.T) {
	home := t.TempDir()
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{
		"2.1.270": "keep",
		"2.1.269": "keep",
		"2.1.260": "remove",
		"2.1.250": "remove",
	}
	for name := range paths {
		writeExecutable(t, filepath.Join(versions, name))
	}
	report, err := InspectClaudeVersions(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if report.LiveProbeErr != nil {
		t.Fatalf("unexpected live probe error: %v", report.LiveProbeErr)
	}
	remove, kept := PlanClaudeVersionPrune(report, 2)
	if len(remove) != 2 {
		t.Fatalf("remove=%#v, want 2 versions removed", remove)
	}
	for _, version := range remove {
		if paths[filepath.Base(version.Path)] != "remove" {
			t.Fatalf("removed %s, want it kept", version.Path)
		}
	}
	for name, want := range paths {
		path := filepath.Join(versions, name)
		_, isKept := kept[path]
		if want == "keep" && !isKept {
			t.Fatalf("kept=%v missing %s", kept, path)
		}
		if want == "remove" && isKept {
			t.Fatalf("kept=%v wrongly protects %s", kept, path)
		}
	}
	// REGRESSION for issue #24 F7: only the actual newest build (report.Versions
	// sorted newest-first, so "2.1.270" here) is labelled "newest" — the
	// second-newest kept build must carry a distinct, honest label, never a
	// second "newest" claim about a build that is not.
	newestPath := filepath.Join(versions, "2.1.270")
	secondPath := filepath.Join(versions, "2.1.269")
	if got := kept[newestPath]; got != "newest" {
		t.Fatalf("kept[%s]=%q, want %q", newestPath, got, "newest")
	}
	if got := kept[secondPath]; got == "newest" {
		t.Fatalf("kept[%s]=%q, want a label distinct from the actual newest build", secondPath, got)
	}
}

// TestInspectClaudeVersionsRefusesWhenIdentifyingTheConfiguredBinaryFailsForAReasonOtherThanAbsence
// is the MINOR fix: a not-exist error for the configured claude.binary is
// genuine absence and must not block enumeration, but any other error (for
// example a permission-denied stat) means pfm could not identify what that
// path even is, and removing a build it failed to identify is the unsafe
// direction — InspectClaudeVersions must refuse outright, not silently treat
// the path as unconfigured.
func TestInspectClaudeVersionsRefusesWhenIdentifyingTheConfiguredBinaryFailsForAReasonOtherThanAbsence(t *testing.T) {
	home := t.TempDir()
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(versions, "2.1.270"))

	// A path whose PARENT component is a regular file, not a directory:
	// os.Stat fails with ENOTDIR — a real error distinct from "not exist",
	// and one that even root cannot bypass (unlike a permission bit),
	// so the test is reliable in the fenced container too.
	notADirectory := filepath.Join(home, "not-a-directory")
	writeExecutable(t, notADirectory)
	configured := filepath.Join(notADirectory, "claude")

	if _, err := InspectClaudeVersions(home, configured); err == nil {
		t.Fatal("InspectClaudeVersions silently ignored a non-absence error identifying the configured binary")
	}
}
