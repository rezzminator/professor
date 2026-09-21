package doctor

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvestpy"
)

func TestPrintUnnamedHarvestChecksSkipsNamedAndHealthy(t *testing.T) {
	report := harvestpy.CheckReport{
		Checks: map[string]harvestpy.CheckStatus{
			"interpreter":      {OK: true},                          // named — never printed here
			"digest_integrity": {OK: true},                          // unnamed but healthy — never printed
			"current_pointer":  {Error: "pointer resolves outside"}, // unnamed and failed — must print
		},
	}
	var output strings.Builder
	warnings := printUnnamedHarvestChecks(&output, report, harvestNamedDoctorChecks)
	if warnings != 1 {
		t.Fatalf("warnings=%d, want 1", warnings)
	}
	got := output.String()
	if !strings.Contains(got, "check=current_pointer") || !strings.Contains(got, "pointer resolves outside") {
		t.Fatalf("output=%q, want the failed unnamed check named", got)
	}
	if strings.Contains(got, "interpreter") || strings.Contains(got, "digest_integrity") {
		t.Fatalf("output=%q, printed a named or healthy check", got)
	}
}

func TestPrintUnnamedHarvestChecksNoneFailedIsSilent(t *testing.T) {
	report := harvestpy.CheckReport{
		Checks: map[string]harvestpy.CheckStatus{
			"interpreter":      {OK: true},
			"digest_integrity": {OK: true},
		},
	}
	var output strings.Builder
	warnings := printUnnamedHarvestChecks(&output, report, harvestNamedDoctorChecks)
	if warnings != 0 || output.Len() != 0 {
		t.Fatalf("warnings=%d output=%q, want silent when nothing failed", warnings, output.String())
	}
}
