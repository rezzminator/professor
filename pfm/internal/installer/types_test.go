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
	if !strings.Contains(output.String(), "installer: launchd retry sleep failed: fixture clock cancelled") {
		t.Fatalf("output=%q, want the clock error", output.String())
	}
}

func TestExecCommandRunnerAbsentBinaryIsALookupNotARun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, output := range []bool{false, true} {
		t.Run(map[bool]string{false: "Run", true: "Output"}[output], func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			const name = "systemctl"
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

func TestExecCommandRunnerPresentBinaryRecordsItsExit(t *testing.T) {
	for _, output := range []bool{false, true} {
		t.Run(map[bool]string{false: "Run", true: "Output"}[output], func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			var err error
			if output {
				var result []byte
				result, err = (execCommandRunner{}).Output(ctx, "sh", "-c", "printf fixture; exit 7")
				if string(result) != "fixture" {
					t.Fatalf("output=%q", result)
				}
			} else {
				err = (execCommandRunner{}).Run(ctx, "sh", "-c", "exit 7")
			}
			var exit commandExitError
			if !errors.As(err, &exit) || exit.code != 7 {
				t.Fatalf("err=%v", err)
			}
			runs := 0
			for _, record := range recorder.Records() {
				if record.Message == "runner.run" {
					runs++
					code, _ := record.Field(obs.FieldExit)
					if record.Level != "INFO" || code != float64(7) {
						t.Fatalf("record=%+v", record)
					}
				}
			}
			if runs != 1 {
				t.Fatalf("runs=%d: %s", runs, recorder.Raw())
			}
		})
	}
}
