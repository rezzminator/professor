package installer

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

type failingInstallerClock struct{ clock.Clock }

func (failingInstallerClock) Sleep(context.Context, time.Duration) error {
	return errors.New("fixture clock cancelled")
}

func TestNormalizedInstallerSleepErrorIsReportedWithoutPanic(t *testing.T) {
	var output bytes.Buffer
	options, err := normalizeInstallerOptions(Options{
		Home:   t.TempDir(),
		Clock:  failingInstallerClock{Clock: clock.Real},
		Stdout: &output,
	})
	if err != nil {
		t.Fatalf("normalizeInstallerOptions() error = %v", err)
	}

	installer := &engine{options: options}
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		installer.pause(time.Second)
	}()
	if panicked {
		t.Fatal("normalized installer sleep must report a clock error, not panic")
	}
	if !strings.Contains(output.String(), "installer: launchd retry sleep failed: fixture clock cancelled") {
		t.Fatalf("output=%q, want the clock error", output.String())
	}
}
