//go:build linux

package doctor

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

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

var systemdShowArgv = []string{
	"systemctl", "--user", "show", "--property=LoadState,UnitFileState,ActiveState", "pfm-mcp.service",
}

// TestProbeServiceManagerLinuxReadsOneShowAnswer drives the one
// `systemctl --user show` the probe asks through every answer it must read:
// a completed show is an answer (present/enabled/active and the ActiveState
// word), while a non-zero exit, unparsable output or a Run error is "could not
// ask" carrying the exit code and stderr — never folded into "not active".
func TestProbeServiceManagerLinuxReadsOneShowAnswer(t *testing.T) {
	cases := []struct {
		name    string
		result  deps.RunResult
		runErr  error
		want    serviceManagerUnitState
		wantErr []string
	}{
		{
			name:   "healthy",
			result: deps.RunResult{Stdout: []byte("LoadState=loaded\nUnitFileState=enabled\nActiveState=active\n")},
			want:   serviceManagerUnitState{Present: true, Enabled: true, Active: true, State: "active"},
		},
		{
			name: "activating",
			result: deps.RunResult{
				Stdout: []byte("LoadState=loaded\nUnitFileState=enabled-runtime\nActiveState=activating\n"),
			},
			want: serviceManagerUnitState{Present: true, Enabled: true, State: "activating"},
		},
		{
			name:   "never staged",
			result: deps.RunResult{Stdout: []byte("LoadState=not-found\nUnitFileState=\nActiveState=inactive\n")},
			want:   serviceManagerUnitState{State: "inactive"},
		},
		{
			name:   "staged, disabled, inactive",
			result: deps.RunResult{Stdout: []byte("LoadState=loaded\nUnitFileState=disabled\nActiveState=inactive\n")},
			want:   serviceManagerUnitState{Present: true, State: "inactive"},
		},
		{
			name: "dead user bus",
			result: deps.RunResult{
				ExitCode: 1, Stderr: []byte("Failed to connect to bus: No medium found\n"),
			},
			wantErr: []string{"exit=1", "Failed to connect to bus: No medium found"},
		},
		{
			name:    "unparsable answer",
			result:  deps.RunResult{Stdout: []byte("garbage\n")},
			wantErr: []string{"LoadState"},
		},
		{
			name:    "run error",
			runErr:  errors.New("permission denied"),
			wantErr: []string{"permission denied"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := &deps.FakeRunner{}
			fake.ScriptLookPath("systemctl", "/usr/bin/systemctl", nil)
			fake.Script(systemdShowArgv, testCase.result, testCase.runErr)

			report := probeServiceManager(context.Background(), fake)
			if !report.Present {
				t.Fatalf("systemctl resolved on PATH; report.Present must be true, got %+v", report)
			}
			got := report.Unit
			if testCase.wantErr != nil {
				if got.Err == nil {
					t.Fatalf("want could-not-ask, got %+v", got)
				}
				for _, want := range testCase.wantErr {
					if !strings.Contains(got.Err.Error(), want) {
						t.Fatalf("error %q missing %q", got.Err, want)
					}
				}
				if got.Present || got.Enabled || got.Active {
					t.Fatalf("a could-not-ask probe must not report facts, got %+v", got)
				}
				return
			}
			if got.Err != nil {
				t.Fatalf("an answer must not carry a probe error, got %v", got.Err)
			}
			testCase.want.Unit = "pfm-mcp.service"
			if got != testCase.want {
				t.Fatalf("state = %+v, want %+v", got, testCase.want)
			}
		})
	}
}

// TestProbeServiceManagerLinuxTimeoutCouldNotAsk: a probe killed by its
// deadline reports a nil Run error (RealRunner folds the kill into an exit
// code), so the expired probe context itself must read as could-not-ask,
// naming the deadline.
func TestProbeServiceManagerLinuxTimeoutCouldNotAsk(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("systemctl", "/usr/bin/systemctl", nil)
	fake.Script(systemdShowArgv, deps.RunResult{ExitCode: -1}, nil)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	report := probeServiceManager(ctx, fake)
	if report.Unit.Err == nil {
		t.Fatalf("an expired probe must be could-not-ask, got %+v", report.Unit)
	}
	if !strings.Contains(report.Unit.Err.Error(), deps.ProbeTimeout.String()) {
		t.Fatalf("error %q does not name the %s deadline", report.Unit.Err, deps.ProbeTimeout)
	}
}
