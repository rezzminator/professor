//go:build linux

package doctor

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// TestProbeServiceManagerLinuxNoSystemctlOnPath watches the "no service
// manager on this host" state: systemctl absent from PATH is a clean,
// expected answer (report.Present=false), never a probe error.
func TestProbeServiceManagerLinuxNoSystemctlOnPath(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("systemctl", "", exec.ErrNotFound)

	report := probeServiceManager(context.Background(), fake)
	if report.Present {
		t.Fatalf("systemctl absent from PATH must report Present=false, got %+v", report)
	}
	if report.Manager != "systemd" {
		t.Fatalf("manager = %q, want systemd", report.Manager)
	}
	if report.Unit.Unit != "pfm-mcp.service" {
		t.Fatalf("unit = %q, want pfm-mcp.service", report.Unit.Unit)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("no systemctl run should have been attempted, got %v", calls)
	}
}

// TestProbeServiceManagerLinuxHealthy watches the "present and healthy"
// state: is-enabled and is-active both exit 0.
func TestProbeServiceManagerLinuxHealthy(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("systemctl", "/usr/bin/systemctl", nil)
	fake.Script(
		[]string{"systemctl", "--user", "is-enabled", "--quiet", "pfm-mcp.service"},
		deps.RunResult{ExitCode: 0}, nil,
	)
	fake.Script(
		[]string{"systemctl", "--user", "is-active", "--quiet", "pfm-mcp.service"},
		deps.RunResult{ExitCode: 0}, nil,
	)

	report := probeServiceManager(context.Background(), fake)
	if !report.Present || !report.Unit.Present || !report.Unit.Enabled || !report.Unit.Active {
		t.Fatalf("expected a fully healthy report, got %+v", report)
	}
	if report.Unit.Err != nil {
		t.Fatalf("healthy unit must not carry a probe error, got %v", report.Unit.Err)
	}
}

// TestProbeServiceManagerLinuxInactiveUnit watches a present manager whose
// unit answers a nonzero exit — a real, decoded answer, never "could not
// ask".
func TestProbeServiceManagerLinuxInactiveUnit(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("systemctl", "/usr/bin/systemctl", nil)
	fake.Script(
		[]string{"systemctl", "--user", "is-enabled", "--quiet", "pfm-mcp.service"},
		deps.RunResult{ExitCode: 0}, nil,
	)
	fake.Script(
		[]string{"systemctl", "--user", "is-active", "--quiet", "pfm-mcp.service"},
		deps.RunResult{ExitCode: 3}, nil,
	)

	report := probeServiceManager(context.Background(), fake)
	if !report.Present || !report.Unit.Present || !report.Unit.Enabled || report.Unit.Active {
		t.Fatalf("expected enabled but inactive, got %+v", report)
	}
	if report.Unit.Err != nil {
		t.Fatalf("a decoded nonzero exit must not be read as a probe error, got %v", report.Unit.Err)
	}
}

// TestProbeServiceManagerLinuxCouldNotAsk watches the third state: systemctl
// resolves on PATH but the Run call itself fails (never started, a
// non-ExitError failure) — reported through Unit.Err, never silently as
// "not active".
func TestProbeServiceManagerLinuxCouldNotAsk(t *testing.T) {
	probeErr := errors.New("permission denied")
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("systemctl", "/usr/bin/systemctl", nil)
	fake.Script(
		[]string{"systemctl", "--user", "is-enabled", "--quiet", "pfm-mcp.service"},
		deps.RunResult{}, probeErr,
	)

	report := probeServiceManager(context.Background(), fake)
	if !report.Present {
		t.Fatalf("systemctl resolved on PATH; report.Present must stay true, got %+v", report)
	}
	if report.Unit.Err == nil {
		t.Fatalf("a Run failure must surface as Unit.Err, got %+v", report.Unit)
	}
	if report.Unit.Active || report.Unit.Enabled {
		t.Fatalf("a could-not-ask probe must not report enabled/active facts, got %+v", report.Unit)
	}
}
