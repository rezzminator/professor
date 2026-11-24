//go:build jail && darwin

package stats

import (
	"fmt"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestSamplerSampleResourcesUsesDarwinHostCounters(t *testing.T) {
	sampler := &Sampler{}
	first, err := sampler.SampleResources(nil)
	if err != nil {
		t.Fatalf("first Darwin resource sample: %v", err)
	}
	if first.Header.MemoryBytes == 0 {
		t.Fatalf("first Darwin resource sample has no host memory: %#v", first.Header)
	}
	// Darwin's host total is summed from ps per-process lifetime CPU, so an
	// interval in which more CPU exits than accrues carries no honest delta and
	// SampleResources deliberately leaves CPUValid false (see stats.go's own
	// comment at the deltaIdle guard). Under `go test ./...` short-lived test
	// processes churn constantly, so ONE 25ms interval is a coin flip: this test
	// passed on an idle box and failed under full-suite load while the sampler
	// behaved exactly as documented. Sampling until a valid interval appears
	// keeps the assertion strict — a sampler that never reports a delta still
	// fails — while removing the load sensitivity.
	var second Snapshot
	testwait.Until(t, "a Darwin resource sample to report a CPU delta", func() (bool, string) {
		second, err = sampler.SampleResources(nil)
		if err != nil {
			return true, err.Error()
		}
		return !second.Ready || second.Header.CPUValid, fmt.Sprintf("%#v", second.Header)
	})
	if err != nil {
		t.Fatalf("later Darwin resource sample: %v", err)
	}
	if !second.Ready {
		t.Fatalf("later Darwin resource sample not ready: %#v", second)
	}
}
