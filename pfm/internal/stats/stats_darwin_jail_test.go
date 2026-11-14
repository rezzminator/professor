//go:build jail && darwin

package stats

import (
	"testing"
	"time"
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
	deadline := time.Now().Add(5 * time.Second)
	var second Snapshot
	for {
		time.Sleep(25 * time.Millisecond)
		second, err = sampler.SampleResources(nil)
		if err != nil {
			t.Fatalf("later Darwin resource sample: %v", err)
		}
		if !second.Ready {
			t.Fatalf("later Darwin resource sample not ready: %#v", second)
		}
		if second.Header.CPUValid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"no Darwin resource sample reported a CPU delta within 5s: %#v",
				second.Header,
			)
		}
	}
}
