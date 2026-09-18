package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hostops/pfm/internal/deps"
)

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
					Unit: "pfm-mcp.service", Present: true, Enabled: true, Active: true,
				},
			},
			want: []string{
				"doctor: service-manager=systemd unit=pfm-mcp.service present=true enabled=true active=true",
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
			printServiceManagerDoctor(context.Background(), &output, nil)
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

// TestPrintServiceManagerDoctorPresentButUnhealthyNamesTheRemedy proves the
// present-but-not-active/enabled shape carries a start hint rather than
// silently reading as the healthy row.
func TestPrintServiceManagerDoctorPresentButUnhealthyNamesTheRemedy(t *testing.T) {
	ServiceManagerProbeOverride = func(context.Context, deps.Runner) serviceManagerReport {
		return serviceManagerReport{
			Manager: "systemd",
			Present: true,
			Unit: serviceManagerUnitState{
				Unit: "pfm-mcp.service", Present: true, Enabled: true, Active: false,
			},
		}
	}
	defer func() { ServiceManagerProbeOverride = nil }()

	var output strings.Builder
	warnings := printServiceManagerDoctor(context.Background(), &output, nil)
	if warnings == 0 {
		t.Fatalf("present-but-inactive unit reported 0 warnings, output=%q", output.String())
	}
	got := output.String()
	if !strings.Contains(got, "active=false") ||
		!strings.Contains(got, "systemctl --user enable --now pfm-mcp.service") {
		t.Fatalf("output %q missing the inactive facts or its start hint", got)
	}
}
