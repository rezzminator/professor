package testjail

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// pressureDir writes a pressure directory in the kernel's format: cpu has a
// "some" line, io and memory carry "some" and "full".
func pressureDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const (
	psiSome = "some avg10=0.00 avg60=0.01 avg300=0.02 total=1500000\n"
	psiFull = "full avg10=0.00 avg60=0.00 avg300=0.00 total=250000\n"
)

func TestReadPSIParsesTheStallTotalsInSeconds(t *testing.T) {
	dir := pressureDir(t, map[string]string{"cpu": psiSome, "io": psiSome + psiFull, "memory": psiSome + psiFull})
	got, err := readPSI(dir)
	if err != nil {
		t.Fatalf("readPSI: %v", err)
	}
	want := map[string]float64{
		"cpu_some": 1.5, "io_some": 1.5, "io_full": 0.25, "memory_some": 1.5, "memory_full": 0.25,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readPSI = %v, want %v", got, want)
	}
}

func TestReadPSIUnreadableIsAnErrorNeverZero(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "pressure")
	cases := []struct {
		name string
		dir  string
		want string
	}{
		{"pressure directory missing", missingDir, missingDir},
		{"one resource file missing", pressureDir(t, map[string]string{"cpu": psiSome, "io": psiSome}), "memory"},
		{
			"a short line",
			pressureDir(t, map[string]string{"cpu": "some avg10=0.00\n", "io": psiSome, "memory": psiSome}),
			"malformed line",
		},
		{"a total that is no number", pressureDir(t, map[string]string{
			"cpu": "some avg10=0.00 avg60=0.00 avg300=0.00 total=many\n", "io": psiSome, "memory": psiSome,
		}), "many"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readPSI(tc.dir)
			if err == nil || got != nil {
				t.Fatalf("readPSI = %v, %v, want an error and no values", got, err)
			}
			if !strings.HasPrefix(err.Error(), "psi") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name psi and %q", err, tc.want)
			}
		})
	}
}

func TestSummaryPSIUnreadableIsNullWithItsReasonOnLinux(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "pressure")
	psi0, psiErr := readPSI(missingDir)
	p := &profiler{pkg: "x", start: time.Now(), bundles: []string{}, psi0: psi0, psiErr: psiErr}
	p.mu.Lock()
	summary := p.summary(0, "exit")
	p.mu.Unlock()
	if got, ok := summary["psi"]; !ok || got != nil {
		t.Fatalf("psi = %#v (present %v), want null", got, ok)
	}
	if reason, _ := summary["psi_error"].(string); !strings.Contains(reason, missingDir) {
		t.Fatalf("psi_error = %q, want the missing directory named", reason)
	}
	text := diagnose("", "exit", summary, "")
	if want := "VM pressure while this process ran: not measured (" + summary["psi_error"].(string) + ")"; !strings.Contains(
		text,
		want,
	) {
		t.Fatalf("diagnosis lacks %q:\n%s", want, text)
	}
}

func TestRunDelayReadsThisProcessOnLinux(t *testing.T) {
	got, err := runDelaySeconds()
	if err != nil || got < 0 {
		t.Fatalf("runDelaySeconds = %v, %v, want a non-negative reading of this process", got, err)
	}
}

func TestMaxRSSIsKilobytesOnLinux(t *testing.T) {
	if got := maxRSSKB(31800); got != 31800 {
		t.Fatalf("maxRSSKB(31800) = %d, want it unchanged: Linux reports kilobytes", got)
	}
}
