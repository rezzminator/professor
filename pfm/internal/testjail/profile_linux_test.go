package testjail

import (
	"fmt"
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
		{
			"pressure directory missing",
			missingDir,
			"map[] psi: open {root}/001/pressure/cpu: no such file or directory",
		},
		{
			"one resource file missing",
			pressureDir(t, map[string]string{"cpu": psiSome, "io": psiSome}),
			"map[] psi: open {root}/002/memory: no such file or directory",
		},
		{
			"a short line",
			pressureDir(t, map[string]string{"cpu": "some avg10=0.00\n", "io": psiSome, "memory": psiSome}),
			"map[] psi {root}/003/cpu: malformed line \"some avg10=0.00\"",
		},
		{"a total that is no number", pressureDir(t, map[string]string{
			"cpu": "some avg10=0.00 avg60=0.00 avg300=0.00 total=many\n", "io": psiSome, "memory": psiSome,
		}), "map[] psi {root}/004/cpu: strconv.ParseInt: parsing \"many\": invalid syntax"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readPSI(tc.dir)
			if got := strings.ReplaceAll(
				fmt.Sprint(got, " ", err),
				filepath.Dir(filepath.Dir(missingDir)),
				"{root}",
			); got != tc.want {
				t.Errorf("readPSI = %q, want %q", got, tc.want)
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
	psi, present := summary["psi"]
	if got, want := rooted(
		t,
		fmt.Sprintf("%#v %t %v", psi, present, summary["psi_error"]),
	), "<nil> true psi: open {root}/001/pressure/cpu: no such file or directory"; got != want {
		t.Fatalf("psi and psi_error = %q, want %q", got, want)
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

func TestParseProcessStart(t *testing.T) {
	const kernel = "cpu  1 2 3\nbtime 1763000000\nprocesses 9\n"
	stat := func(comm string) string {
		return "4242 (" + comm + ") S 1 4242 4242 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 8 0 123456 1000 200"
	}
	boot := time.Unix(1763000000, 0)
	cases := []struct {
		name, stat, kernel string
		want               time.Time
		err                string
	}{
		{"a plain command name", stat("go"), kernel, boot.Add(1234560 * time.Millisecond), ""},
		{
			"a command name holding spaces and parentheses",
			stat("go (test) x"),
			kernel,
			boot.Add(1234560 * time.Millisecond),
			"",
		},
		{"no command name", "4242 go S 1", kernel, time.Time{}, `stat "4242 go S 1": no command name in parentheses`},
		{
			"a short line",
			"4242 (go) S 1 2",
			kernel,
			time.Time{},
			`stat "4242 (go) S 1 2": 3 fields after the command name, want starttime at the 20th`,
		},
		{"no btime", stat("go"), "cpu  1 2 3\n", time.Time{}, "/proc/stat: no btime line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseProcessStart(tc.stat, tc.kernel)
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if !got.Equal(tc.want) || gotErr != tc.err {
				t.Fatalf("parseProcessStart = %v, %q; want %v, %q", got, gotErr, tc.want, tc.err)
			}
		})
	}
}
