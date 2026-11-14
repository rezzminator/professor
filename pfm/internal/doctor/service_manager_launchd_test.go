package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func launchdPrintArgv(label string) []string {
	return []string{"launchctl", "print", "gui/" + strconv.Itoa(os.Getuid()) + "/" + label}
}

// TestProbeServiceManagerLaunchdNoLaunchctlOnPath watches the "no service
// manager on this host" state: launchctl absent from PATH is a clean,
// expected answer (report.Present=false), never a probe error.
func TestProbeServiceManagerLaunchdNoLaunchctlOnPath(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("launchctl", "", exec.ErrNotFound)

	report := launchdServiceManager.service(context.Background(), fake)
	if report.Present {
		t.Fatalf("launchctl absent from PATH must report Present=false, got %+v", report)
	}
	if report.Manager != "launchd" {
		t.Fatalf("manager = %q, want launchd", report.Manager)
	}
	if report.Unit.Unit != "com.professor.pfm.mcp" {
		t.Fatalf("unit = %q, want com.professor.pfm.mcp", report.Unit.Unit)
	}
}

// TestProbeServiceManagerLaunchdRunning watches the "present and healthy"
// state: launchctl print exits 0 and its output carries "state = running".
func TestProbeServiceManagerLaunchdRunning(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("launchctl", "/bin/launchctl", nil)
	fake.Script(
		launchdPrintArgv("com.professor.pfm.mcp"),
		deps.RunResult{ExitCode: 0, Stdout: []byte("PID = 1234\nstate = running\n")}, nil,
	)

	report := launchdServiceManager.service(context.Background(), fake)
	if !report.Present || !report.Unit.Present || !report.Unit.Enabled || !report.Unit.Active {
		t.Fatalf("expected a fully healthy report, got %+v", report)
	}
	if report.Unit.State != "running" {
		t.Fatalf("state = %q, want launchd's own word running", report.Unit.State)
	}
}

// TestProbeServiceManagerLaunchdNotRunningSubstringTrap watches the exact bug
// installer.launchAgentRunning's own comment documents: "state = not
// running" contains the substring "running" and must not read as active.
func TestProbeServiceManagerLaunchdNotRunningSubstringTrap(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("launchctl", "/bin/launchctl", nil)
	fake.Script(
		launchdPrintArgv("com.professor.pfm.mcp"),
		deps.RunResult{ExitCode: 0, Stdout: []byte("PID = 1234\nstate = not running\n")}, nil,
	)

	report := launchdServiceManager.service(context.Background(), fake)
	if report.Unit.Active {
		t.Fatalf("'state = not running' must not be read as active, got %+v", report.Unit)
	}
	if !report.Unit.Present || !report.Unit.Enabled {
		t.Fatalf("a known, loaded label must still report present/enabled, got %+v", report.Unit)
	}
}

// TestProbeServiceManagerLaunchdLabelUnknown watches launchd not knowing the
// label at all — exit 113, or stderr naming "Could not find service" (the
// answer `launchctl print` gives for an unloaded label): nothing staged or
// loaded, present=false, never an error.
func TestProbeServiceManagerLaunchdLabelUnknown(t *testing.T) {
	for _, result := range []deps.RunResult{
		{ExitCode: 113, Stderr: []byte("Bad request.\n")},
		{ExitCode: 1, Stderr: []byte("Could not find service \"com.professor.pfm.mcp\" in domain for user gui: 501\n")},
	} {
		fake := &deps.FakeRunner{}
		fake.ScriptLookPath("launchctl", "/bin/launchctl", nil)
		fake.Script(launchdPrintArgv("com.professor.pfm.mcp"), result, nil)

		report := launchdServiceManager.service(context.Background(), fake)
		if report.Unit.Present {
			t.Fatalf("an unknown label must report Present=false, got %+v", report.Unit)
		}
		if report.Unit.Err != nil {
			t.Fatalf("an unknown label is not a probe error, got %v", report.Unit.Err)
		}
	}
}

// TestProbeServiceManagerLaunchdOtherFailureCouldNotAsk: a non-zero exit that
// is not not-found — the gui/<uid> domain missing (exit 112) — means launchd
// was not asked about the label at all, so it is could-not-ask with the exit
// code and stderr, never present=false.
func TestProbeServiceManagerLaunchdOtherFailureCouldNotAsk(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("launchctl", "/bin/launchctl", nil)
	fake.Script(
		launchdPrintArgv("com.professor.pfm.mcp"),
		deps.RunResult{ExitCode: 112, Stderr: []byte("Bad request.\nCould not find domain for user gui: 501\n")}, nil,
	)

	report := launchdServiceManager.service(context.Background(), fake)
	if got, want := fmt.Sprint(
		report.Unit.Err,
	), "launchctl print gui/"+strconv.Itoa(os.Getuid())+"/com.professor.pfm.mcp exit=112 stderr=\"Bad request.\\nCould not find domain for user gui: 501\""; got != want {
		t.Errorf("could-not-ask error = %q, want %q", got, want)
	}
}

// TestProbeServiceManagerLaunchdTimeoutCouldNotAsk: a killed probe reports a
// nil Run error, so the expired probe context is could-not-ask naming the
// deadline.
func TestProbeServiceManagerLaunchdTimeoutCouldNotAsk(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("launchctl", "/bin/launchctl", nil)
	fake.Script(launchdPrintArgv("com.professor.pfm.mcp"), deps.RunResult{ExitCode: -1}, nil)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	report := launchdServiceManager.service(ctx, fake)
	if report.Unit.Err == nil {
		t.Fatalf("an expired probe must be could-not-ask, got %+v", report.Unit)
	}
	if report.Unit.Err.Error() != "launchctl print gui/"+strconv.Itoa(
		os.Getuid(),
	)+"/com.professor.pfm.mcp did not answer within the 5s deadline: context deadline exceeded" {
		t.Fatalf("error %q does not name the %s deadline", report.Unit.Err, deps.ProbeTimeout)
	}
}

// TestProbeServiceManagerLaunchdCouldNotAsk watches the third state: the Run
// call itself fails — reported through Unit.Err, never silently as absence.
func TestProbeServiceManagerLaunchdCouldNotAsk(t *testing.T) {
	probeErr := errors.New("launchctl: no such process")
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("launchctl", "/bin/launchctl", nil)
	fake.Script(launchdPrintArgv("com.professor.pfm.mcp"), deps.RunResult{}, probeErr)

	report := launchdServiceManager.service(context.Background(), fake)
	if !report.Present {
		t.Fatalf("launchctl resolved on PATH; report.Present must stay true, got %+v", report)
	}
	if report.Unit.Err == nil {
		t.Fatalf("a Run failure must surface as Unit.Err, got %+v", report.Unit)
	}
}
