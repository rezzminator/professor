// Package profilefixture is the deliberately broken package that proves
// testjail's profiler: PFM_PROFILE_FIXTURE=fail|hang|slow picks the failure,
// and PFM_PROFILE_FIXTURE_SLOW_S the whole seconds slow sleeps (default 3).
// It lives under testdata, so ./... never runs it; name it explicitly.
package profilefixture

import (
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

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
		busy(200 * time.Millisecond)
		t.Fatal("deliberate failure")
	case "hang":
		select {}
	case "slow":
		seconds := 3
		if value := os.Getenv("PFM_PROFILE_FIXTURE_SLOW_S"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 0 {
				t.Fatalf("PFM_PROFILE_FIXTURE_SLOW_S=%q: want whole seconds", value)
			}
			seconds = parsed
		}
		time.Sleep(time.Duration(seconds) * time.Second)
	}
}

func busy(d time.Duration) {
	end := time.Now().Add(d)
	n := 0
	for time.Now().Before(end) {
		n++
	}
	_ = n
}
