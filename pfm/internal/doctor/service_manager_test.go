package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// mcpEnabledRuntime is a runtime whose config serves one MCP server, so the
// service-manager row probes instead of reporting disabled-in-config.
func mcpEnabledRuntime() config.Runtime {
	return config.Runtime{Config: config.Config{MCPServers: map[string]config.MCPServer{"professor": {Enabled: true}}}}
}

// TestPrintServiceManagerDoctorReportsThreeStates watches the three states
// the row must never collapse: manager present with healthy units, no
// service manager on this host (an expected container/unmanaged-host
// answer, never a failure), and "could not ask" carrying the probe's own
// error text (an error must never render as absence). It drives
// printServiceManagerDoctor through ServiceManagerProbeOverride so the
// assertions hold on every platform this suite runs on, independent of
// which systemd/launchd probe the build itself compiles.
func TestPrintServiceManagerDoctorReportsThreeStates(t *testing.T) {
	probeErr := errors.New("dial unix /run/user/1000/bus: connect: no such file or directory")
	cases := []struct {
		name    string
		report  serviceManagerReport
		want    []string
		exclude []string
	}{
		{
			name: "present and healthy",
			report: serviceManagerReport{
				Manager: "systemd",
				Present: true,
				Unit: serviceManagerUnitState{
					Unit: "pfm-mcp.service", Present: true, Enabled: true, Active: true, State: "active",
				},
			},
			want: []string{
				"doctor: service-manager=systemd unit=pfm-mcp.service present=true enabled=true active=active\n",
			},
			exclude: []string{"could_not_ask", "state=unavailable"},
		},
		{
			name: "no service manager on this host",
			report: serviceManagerReport{
				Manager: "systemd",
				Present: false,
				Unit:    serviceManagerUnitState{Unit: "pfm-mcp.service"},
			},
			want: []string{
				"doctor: service-manager=none manager=systemd unit=pfm-mcp.service state=unavailable",
				"no systemd user manager on this host",
			},
			exclude: []string{"could_not_ask", "error="},
		},
		{
			name: "could not ask",
			report: serviceManagerReport{
				Manager: "systemd",
				Present: true,
				Unit: serviceManagerUnitState{
					Unit: "pfm-mcp.service", Err: probeErr,
				},
			},
			want: []string{
				"doctor: service-manager=systemd unit=pfm-mcp.service could_not_ask",
				"error=" + probeErr.Error(),
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ServiceManagerProbeOverride = func(context.Context, deps.Runner) serviceManagerReport {
				return testCase.report
			}
			defer func() { ServiceManagerProbeOverride = nil }()

			var output strings.Builder
			printServiceManagerDoctor(context.Background(), &output, nil, mcpEnabledRuntime())
			got := output.String()
			for _, want := range testCase.want {
				if !strings.Contains(got, want) {
					t.Fatalf("output %q missing %q", got, want)
				}
			}
			for _, forbidden := range testCase.exclude {
				if strings.Contains(got, forbidden) {
					t.Fatalf("output %q must not contain %q for state %q", got, forbidden, testCase.name)
				}
			}
		})
	}
}

// TestPrintServiceManagerDoctorPresentButUnhealthyNamesTheRemedy proves each
// unhealthy shape names the manager's own state word and the one command that
// clears it: an unstaged unit is cleared by the installer, a staged one that is
// not enabled or not active by the manager's start command.
func TestPrintServiceManagerDoctorPresentButUnhealthyNamesTheRemedy(t *testing.T) {
	cases := []struct {
		name   string
		report serviceManagerReport
		want   []string
	}{
		{
			name: "systemd unit activating",
			report: serviceManagerReport{Manager: "systemd", Present: true, Unit: serviceManagerUnitState{
				Unit: "pfm-mcp.service", Present: true, Enabled: true, State: "activating",
			}},
			want: []string{
				"present=true enabled=true active=activating",
				"systemctl --user enable --now pfm-mcp.service",
			},
		},
		{
			name: "systemd unit never staged",
			report: serviceManagerReport{Manager: "systemd", Present: true, Unit: serviceManagerUnitState{
				Unit: "pfm-mcp.service", State: "inactive",
			}},
			want: []string{"present=false enabled=false active=inactive", "— run pfm install --yes"},
		},
		{
			name: "launchd label not running",
			report: serviceManagerReport{Manager: "launchd", Present: true, Unit: serviceManagerUnitState{
				Unit: "com.professor.pfm.mcp", Present: true, Enabled: true, State: "not running",
			}},
			want: []string{"active=not running", "launchctl kickstart -k gui/$(id -u)/com.professor.pfm.mcp"},
		},
		{
			name: "launchd label unknown",
			report: serviceManagerReport{Manager: "launchd", Present: true, Unit: serviceManagerUnitState{
				Unit: "com.professor.pfm.mcp",
			}},
			want: []string{"present=false", "— run pfm install --yes"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ServiceManagerProbeOverride = func(context.Context, deps.Runner) serviceManagerReport {
				return testCase.report
			}
			defer func() { ServiceManagerProbeOverride = nil }()

			var output strings.Builder
			warnings := printServiceManagerDoctor(context.Background(), &output, nil, mcpEnabledRuntime())
			if warnings != 1 {
				t.Fatalf("unhealthy unit reported %d warnings, want 1, output=%q", warnings, output.String())
			}
			got := output.String()
			for _, want := range testCase.want {
				if !strings.Contains(got, want) {
					t.Fatalf("output %q missing %q", got, want)
				}
			}
			if strings.Contains(got, "active=false") || strings.Contains(got, "active=true") {
				t.Fatalf("output %q renders the manager's state as a boolean", got)
			}
		})
	}
}

// TestPrintServiceManagerDoctorDisabledInConfigNeverProbes: with every MCP
// server disabled there is no daemon to supervise — the row says so, asks the
// manager nothing and warns nothing.
func TestPrintServiceManagerDoctorDisabledInConfigNeverProbes(t *testing.T) {
	ServiceManagerProbeOverride = func(context.Context, deps.Runner) serviceManagerReport {
		t.Fatal("a disabled MCP config must not probe the service manager")
		return serviceManagerReport{}
	}
	defer func() { ServiceManagerProbeOverride = nil }()

	runtime := config.Runtime{
		Config: config.Config{MCPServers: map[string]config.MCPServer{"professor": {Enabled: false}}},
	}
	var output strings.Builder
	if warnings := printServiceManagerDoctor(context.Background(), &output, nil, runtime); warnings != 0 {
		t.Fatalf("disabled-in-config reported %d warnings, output=%q", warnings, output.String())
	}
	manager, unit := serviceManagerIdentity()
	want := "doctor: service-manager=" + manager + " unit=" + unit + " disabled-in-config\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}
