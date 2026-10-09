package doctor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// costLedgerRuntime is a runtime over a fresh home whose cost ledger switch is on, the default.
func costLedgerRuntime(t *testing.T) (config.Runtime, string) {
	t.Helper()
	home := t.TempDir()
	machine := config.Defaults(home, nil)
	runtime := config.Runtime{Config: machine}
	runtime.Paths.Home = home
	return runtime, paths.CostSinkLog(home)
}

func writeSinkLog(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPrintCostLedgerDoctorShowsTheSwitchAndTheLedgerDirectory(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		env     map[string]string
		want    string
	}{
		{"default on", true, nil, "doctor: cost ledger state=on source=default dir=%s/.local/state/pfm/cost-ledger\n"},
		{"off", false, nil, "doctor: cost ledger state=off source=default dir=%s/.local/state/pfm/cost-ledger\n"},
		{
			"directory override", true,
			map[string]string{paths.EnvCostLedgerDir: "/ledger-elsewhere"},
			"doctor: cost ledger state=on source=default dir=/ledger-elsewhere\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime, _ := costLedgerRuntime(t)
			runtime.Config.Claude.CostLedger = tc.enabled
			var stdout bytes.Buffer
			warnings := printCostLedgerDoctor(&stdout, runtime, &paths.MapEnv{Values: tc.env})
			want := tc.want
			if strings.Contains(want, "%s") {
				want = fmt.Sprintf(want, runtime.Paths.Home)
			}
			if !strings.HasPrefix(stdout.String(), want) {
				t.Fatalf("output:\n%s\nwant it to start with %q", stdout.String(), want)
			}
			if warnings != 0 {
				t.Fatalf("warnings = %d, want 0 for a switch row", warnings)
			}
		})
	}
}

func TestPrintCostLedgerDoctorShowsTheTailOfTheSinkLog(t *testing.T) {
	runtime, logPath := costLedgerRuntime(t)
	var lines []string
	for i := 1; i <= 8; i++ {
		lines = append(lines, fmt.Sprintf("2026-01-02T03:04:0%d.000Z cost-sink: line %d", i, i))
	}
	writeSinkLog(t, logPath, lines)
	var stdout bytes.Buffer
	warnings := printCostLedgerDoctor(&stdout, runtime, &paths.MapEnv{})
	out := stdout.String()
	if !strings.Contains(out, "doctor: cost sink log path="+logPath+" bytes=") {
		t.Fatalf("no log row naming the path:\n%s", out)
	}
	for i := 4; i <= 8; i++ {
		want := fmt.Sprintf("doctor: cost sink log: 2026-01-02T03:04:0%d.000Z cost-sink: line %d\n", i, i)
		if !strings.Contains(out, want) {
			t.Fatalf("tail lacks line %d:\n%s", i, out)
		}
	}
	if strings.Contains(out, "cost-sink: line 3") {
		t.Fatalf("tail went past its last five lines:\n%s", out)
	}
	if warnings != 0 {
		t.Fatalf("a readable log is not a warning: %d", warnings)
	}
}

// A long log is read from its end: the tail never needs the head, and a line cut by the window
// is dropped rather than shown half.
func TestPrintCostLedgerDoctorReadsOnlyTheEndOfALongLog(t *testing.T) {
	runtime, logPath := costLedgerRuntime(t)
	lines := make([]string, 0, 2000)
	for i := 1; i <= 2000; i++ {
		lines = append(lines, fmt.Sprintf("2026-01-02T03:04:05.000Z cost-launch: armed a sink for pid %04d", i))
	}
	writeSinkLog(t, logPath, lines)
	var stdout bytes.Buffer
	printCostLedgerDoctor(&stdout, runtime, &paths.MapEnv{})
	out := stdout.String()
	if !strings.Contains(out, "armed a sink for pid 2000\n") || !strings.Contains(out, "armed a sink for pid 1996\n") {
		t.Fatalf("tail lacks the last lines:\n%s", out)
	}
	if got := strings.Count(out, "doctor: cost sink log: "); got != 5 {
		t.Fatalf("tail rows = %d, want 5:\n%s", got, out)
	}
}

func TestPrintCostLedgerDoctorSeparatesAnAbsentLogFromAFailedLook(t *testing.T) {
	runtime, logPath := costLedgerRuntime(t)
	var absent bytes.Buffer
	if warnings := printCostLedgerDoctor(&absent, runtime, &paths.MapEnv{}); warnings != 0 {
		t.Fatalf("a log nothing has written is a fresh home, not a warning: %d", warnings)
	}
	if !strings.Contains(absent.String(), "doctor: cost sink log path="+logPath+" state=absent\n") {
		t.Fatalf("absent output:\n%s", absent.String())
	}

	// A directory where the file belongs cannot be read: that is a failed look, never "absent".
	if err := os.MkdirAll(logPath, 0o700); err != nil {
		t.Fatal(err)
	}
	var unreadable bytes.Buffer
	warnings := printCostLedgerDoctor(&unreadable, runtime, &paths.MapEnv{})
	out := unreadable.String()
	if !strings.Contains(out, "doctor: cost sink log path="+logPath+" state="+StateUnavailable+" error=") {
		t.Fatalf("unreadable output:\n%s", out)
	}
	if strings.Contains(out, "state=absent") {
		t.Fatalf("a failed look rendered as absence:\n%s", out)
	}
	if warnings != 1 {
		t.Fatalf("a log doctor could not read counts %d warnings, want 1", warnings)
	}
}

// The row is part of a full doctor run, in the order the checks print.
func TestDoctorRunPrintsTheCostLedgerRows(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	armed := "2026-01-02T03:04:05.000Z cost-launch: armed a sink for pid 7"
	writeSinkLog(t, paths.CostSinkLog(runtime.Paths.Home), []string{armed})
	var stdout, stderr bytes.Buffer
	runDoctor(nil, &stdout, &stderr, runtime)
	for _, want := range []string{
		"doctor: cost ledger state=on",
		"doctor: cost sink log: " + armed + "\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output lacks %q:\n%s", want, stdout.String())
		}
	}
}
