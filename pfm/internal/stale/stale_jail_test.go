//go:build jail

package stale

import (
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
