package doctor

import (
	"bytes"
	"strings"
	"testing"
)

// TestDoctorProjectUpdatesFlagRunsOnlyTheProjectReport pins 1-a: with
// --project-updates, doctor runs only the project-template report — no other
// health line, no `doctor: failures=`/`doctor: warnings=` summary — and maps
// its exit through the doctor ladder (3 here: no baseline at cwd or above).
func TestDoctorProjectUpdatesFlagRunsOnlyTheProjectReport(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	code := runDoctor([]string{"--project-updates"}, &stdout, &stderr, runtime)
	if code != 3 {
		t.Fatalf("doctor --project-updates code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "doctor:") {
		t.Fatalf("doctor --project-updates ran other checks:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "FAILED — ") {
		t.Fatalf("doctor --project-updates stdout=%q, want the missing-baseline terminal", stdout.String())
	}
}

// TestDoctorProjectUpdatesUsageRejectsMixedFlags pins the usage door: --root
// or --json without --project-updates, --project-updates with --verbose or
// --skip-harvest, and any positional, all exit 2.
func TestDoctorProjectUpdatesUsageRejectsMixedFlags(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	for _, args := range [][]string{
		{"--root", "."},
		{"--json"},
		{"--project-updates", "--verbose"},
		{"--project-updates", "--skip-harvest"},
		{"--project-updates", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runDoctor(args, &stdout, &stderr, runtime); code != 2 {
			t.Fatalf("doctor %v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
		want := "usage: pfm doctor [--verbose] [--skip-harvest] (exit 0 clean, 1 warnings, 3 failures) | " +
			"pfm doctor --project-updates [--root DIR] [--json] (exit 0 clean, 1 review required, 3 report failure); 2 usage error\n"
		if stderr.String() != want {
			t.Fatalf("doctor %v usage=%q, want %q", args, stderr.String(), want)
		}
	}
}
