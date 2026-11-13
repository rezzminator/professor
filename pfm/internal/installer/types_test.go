package installer

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

type failingInstallerClock struct{ clock.Clock }

func (failingInstallerClock) Sleep(context.Context, time.Duration) error {
	return errors.New("fixture clock cancelled")
}

func TestNormalizedInstallerSleepErrorIsReportedWithoutPanic(t *testing.T) {
	t.Parallel()
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
	if got, want := output.String(), "installer: launchd retry sleep failed: fixture clock cancelled\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestExecCommandRunnerAbsentBinaryIsALookupNotARun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, output := range []bool{false, true} {
		t.Run(map[bool]string{false: "Run", true: "Output"}[output], func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			// Absent on every platform and in no dependency registry row, so
			// no platform gate answers before the lookup this test pins.
			const name = "pfm-absent-command"
			var err error
			if output {
				_, err = (execCommandRunner{}).Output(ctx, name)
			} else {
				err = (execCommandRunner{}).Run(ctx, name)
			}
			if !errors.Is(err, exec.ErrNotFound) || !strings.Contains(err.Error(), name) {
				t.Fatalf("err=%v", err)
			}
			records := recorder.Records()
			if len(records) != 1 || records[0].Message != "runner.lookpath" || records[0].Level != "WARN" {
				t.Fatalf("records: %s", recorder.Raw())
			}
		})
	}
}
