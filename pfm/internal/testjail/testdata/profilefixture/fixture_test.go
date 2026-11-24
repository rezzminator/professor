// Package profilefixture is the deliberately broken package that proves
// testjail's profiler: PFM_PROFILE_FIXTURE=fail|hang|slow picks the failure.
// Slow blocks until its driver releases it, by opening and closing the FIFO
// PFM_PROFILE_FIXTURE_RELEASE names, or until it is killed.
// It lives under testdata, so ./... never runs it; name it explicitly.
package profilefixture

import (
	"io"
	"os"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

// blocked parks a goroutine on a lock nobody releases, so the dump has a
// named culprit to show.
func blocked(mu *sync.Mutex) { mu.Lock() }

func TestFixture(t *testing.T) {
	var mu sync.Mutex
	mu.Lock()
	go blocked(&mu)
	switch os.Getenv("PFM_PROFILE_FIXTURE") {
	case "fail":
		busy()
		t.Fatal("deliberate failure")
	case "hang":
		select {}
	case "slow":
		release := os.Getenv("PFM_PROFILE_FIXTURE_RELEASE")
		if release == "" {
			select {}
		}
		fifo, err := os.Open(release)
		if err != nil {
			t.Fatalf("open the release FIFO %s: %v", release, err)
		}
		defer fifo.Close()
		if _, err := io.Copy(io.Discard, fifo); err != nil {
			t.Fatalf("read the release FIFO %s to its close: %v", release, err)
		}
	}
}

// busy is CPU work for the profile to sample: a fixed count of steps, never a
// stretch of wall time.
func busy() {
	sum := uint64(0)
	for n := range uint64(200_000_000) {
		sum = sum*31 + n
	}
	busySink = sum
}

// busySink keeps busy's loop from being optimised away.
var busySink uint64
