//go:build linux

package harvestpy

import "testing"

// TestPhysicalMemoryBytesReadsTheHost: the pool's default bound reads the
// host's RAM; a probe that answers 0 on a real kernel would silently cap every
// pool at the unknown-memory bound.
func TestPhysicalMemoryBytesReadsTheHost(t *testing.T) {
	t.Parallel()
	if got := physicalMemoryBytes(); got < 256<<20 {
		t.Fatalf("physicalMemoryBytes() = %d, want the host's RAM", got)
	}
}
