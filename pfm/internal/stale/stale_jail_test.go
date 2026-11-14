//go:build jail

package stale

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/hostfixture"
)

// TestFindSkipsAnExitedPfmProcessInsteadOfReportingItUnreadable is
// hostfixture case 9 (StaleArtifacts): a pid that exited between the pid
// listing and the image read must be SKIPPED, not folded into
// scan.Unreadable — that column exists for a process still alive whose
// image genuinely could not be read, and a real ESRCH from a dead pid is
// evidence of neither.
func TestFindSkipsAnExitedPfmProcessInsteadOfReportingItUnreadable(t *testing.T) {
	// hostfixture.StaleArtifacts uses t.Setenv internally (testjail.Fleet),
	// which testing.T refuses to combine with t.Parallel.
	artifacts := hostfixture.StaleArtifacts(t)
	fixture := newFixture(t)
	fixture.process(artifacts.StalePID, "", "pfm", "mcp", "serve")
	scan, err := Find(fixture.table(), fixture.binary, gather.ProbeProcess)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Unreadable) != 0 {
		t.Fatalf("scan.Unreadable = %v, want the exited pid skipped, not reported unreadable", scan.Unreadable)
	}
}

func TestHoldCompatibleProxyOwnsDescriptorLifetime(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	handle, err := HoldCompatibleProxy(home)
	if err != nil {
		t.Fatal(err)
	}
	marker := compatibleProxyMarker(home)
	target, err := compatibleProxyMarkerTarget(home)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode = %o, want 600", info.Mode().Perm())
	}
	proc := gather.NewProcFS(filepath.Join(t.TempDir(), "missing-proc-root"))
	links, err := proc.FDLinks(os.Getpid())
	if err != nil || !slices.ContainsFunc(links, func(link gather.FDLink) bool { return link.Target == target }) {
		t.Fatalf("marker descriptor is not live: links=%v err=%v", links, err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	links, err = proc.FDLinks(os.Getpid())
	if err != nil || slices.ContainsFunc(links, func(link gather.FDLink) bool { return link.Target == target }) {
		t.Fatalf("marker descriptor after close: links=%v err=%v, want absent", links, err)
	}
}
