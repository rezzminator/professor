package doctor

import (
	"fmt"
	"io"
	"sort"

	"hostops/pfm/internal/harvestpy"
)

// harvestNamedDoctorChecks lists the harvestpy check keys that already get
// their own dedicated row in printHarvestPythonDoctorWithRunner (interpreter,
// lock, inventory, live smoke). harvestpy.CheckConversionEnvironment computes
// more checks than that — current_pointer, marker, marker_state, source_hash,
// project_metadata, digest_integrity, current_target, interpreter_build,
// environment_shape, dependency_check — and report.Healthy goes false when
// ANY of them fails. Without printUnnamedHarvestChecks below, one of those
// could fail while every printed row still reads healthy: a coincidence
// detector (L3-F1).
var harvestNamedDoctorChecks = map[string]bool{
	"interpreter":           true,
	"lock_hash":             true,
	"lock_completeness":     true,
	"live_smoke":            true,
	"live_smoke_conversion": true,
}

// printUnnamedHarvestChecks prints one warning row for every check
// harvestpy.CheckConversionEnvironment computed and failed that isn't already
// covered by its own dedicated row above (named). It guarantees the outcome
// L3-F1 asks for: no check the evaluator computes can fail while the doctor
// section stays clean. Returns the number of rows printed so the caller can
// fold them into its own warning tally.
func printUnnamedHarvestChecks(stdout io.Writer, report harvestpy.CheckReport, named map[string]bool) int {
	names := make([]string, 0, len(report.Checks))
	for name := range report.Checks {
		if named[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	warnings := 0
	for _, name := range names {
		status := report.Checks[name]
		if status.OK {
			continue
		}
		warnings++
		fmt.Fprintf(stdout, "doctor: harvestpy check=%s broken error=%s\n", name, status.Error)
	}
	return warnings
}
