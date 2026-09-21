//go:build darwin

package doctor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func launchdPrintArgv(label string) []string {
	return []string{"launchctl", "print", "gui/" + strconv.Itoa(os.Getuid()) + "/" + label}
}

// TestProbeServiceManagerDarwinNoLaunchctlOnPath watches the "no service
// manager on this host" state: launchctl absent from PATH is a clean,
// expected answer (report.Present=false), never a probe error.
func TestProbeServiceManagerDarwinNoLaunchctlOnPath(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("launchctl", "", exec.ErrNotFound)

	report := probeServiceManager(context.Background(), fake)
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

// TestProbeServiceManagerDarwinRunning watches the "present and healthy"
// state: launchctl print exits 0 and its output carries "state = running".
func TestProbeServiceManagerDarwinRunning(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("launchctl", "/bin/launchctl", nil)
	fake.Script(
		launchdPrintArgv("com.professor.pfm.mcp"),
		deps.RunResult{ExitCode: 0, Stdout: []byte("PID = 1234\nstate = running\n")}, nil,
	)

	report := probeServiceManager(context.Background(), fake)
	if !report.Present || !report.Unit.Present || !report.Unit.Enabled || !report.Unit.Active {
		t.Fatalf("expected a fully healthy report, got %+v", report)
	}
}

// TestProbeServiceManagerDarwinNotRunningSubstringTrap watches the exact bug
// installer.launchAgentRunning's own comment documents: "state = not
// running" contains the substring "running" and must not read as active.
func TestProbeServiceManagerDarwinNotRunningSubstringTrap(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("launchctl", "/bin/launchctl", nil)
	fake.Script(
		launchdPrintArgv("com.professor.pfm.mcp"),
		deps.RunResult{ExitCode: 0, Stdout: []byte("PID = 1234\nstate = not running\n")}, nil,
	)

	report := probeServiceManager(context.Background(), fake)
	if report.Unit.Active {
		t.Fatalf("'state = not running' must not be read as active, got %+v", report.Unit)
	}
	if !report.Unit.Present || !report.Unit.Enabled {
		t.Fatalf("a known, loaded label must still report present/enabled, got %+v", report.Unit)
	}
}

// TestProbeServiceManagerDarwinLabelUnknown watches launchd not knowing the
// label at all (nonzero exit): nothing staged/loaded — present=false, not
// an error.
func TestProbeServiceManagerDarwinLabelUnknown(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("launchctl", "/bin/launchctl", nil)
	fake.Script(
		launchdPrintArgv("com.professor.pfm.mcp"),
		deps.RunResult{ExitCode: 3}, nil,
	)

	report := probeServiceManager(context.Background(), fake)
	if report.Unit.Present {
		t.Fatalf("an unknown label must report Present=false, got %+v", report.Unit)
	}
	if report.Unit.Err != nil {
		t.Fatalf("an unknown label is not a probe error, got %v", report.Unit.Err)
	}
}

// TestProbeServiceManagerDarwinCouldNotAsk watches the third state: the Run
// call itself fails — reported through Unit.Err, never silently as absence.
func TestProbeServiceManagerDarwinCouldNotAsk(t *testing.T) {
	probeErr := errors.New("launchctl: no such process")
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("launchctl", "/bin/launchctl", nil)
	fake.Script(launchdPrintArgv("com.professor.pfm.mcp"), deps.RunResult{}, probeErr)

	report := probeServiceManager(context.Background(), fake)
	if !report.Present {
		t.Fatalf("launchctl resolved on PATH; report.Present must stay true, got %+v", report)
	}
	if report.Unit.Err == nil {
		t.Fatalf("a Run failure must surface as Unit.Err, got %+v", report.Unit)
	}
}
