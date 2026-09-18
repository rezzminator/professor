package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/paths"
)

// runtimeWithLog builds a runtime whose activity log is path.
func runtimeWithLog(path string) config.Runtime {
	runtime := config.Runtime{Config: config.Config{Log: config.DefaultLog()}}
	runtime.Paths.LogFile = path
	return runtime
}

func TestPrintActivityLogDoctorNamesThePathAndSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pfm.jsonl")
	if err := os.WriteFile(path, []byte("{\"msg\":\"x\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	printActivityLogDoctor(&stdout, runtimeWithLog(path))
	row := stdout.String()
	if !strings.Contains(row, "doctor: log path="+path) || !strings.Contains(row, "bytes=12") {
		t.Fatalf("row = %q, want the path and its size", row)
	}
	if !strings.Contains(row, "level=build") {
		t.Fatalf("row = %q, want the effective level named", row)
	}
}

// An unwritten log is ABSENT; a log that could not be stat'd is unavailable.
// One row may never render both the same way.
func TestPrintActivityLogDoctorSeparatesAbsenceFromAFailedLook(t *testing.T) {
	var absent bytes.Buffer
	printActivityLogDoctor(&absent, runtimeWithLog(filepath.Join(t.TempDir(), "pfm.jsonl")))
	if !strings.Contains(absent.String(), "state=absent") || !strings.Contains(absent.String(), "bytes=0") {
		t.Fatalf("absent row = %q, want state=absent bytes=0", absent.String())
	}

	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var unreadable bytes.Buffer
	printActivityLogDoctor(&unreadable, runtimeWithLog(filepath.Join(blocked, "pfm.jsonl")))
	if !strings.Contains(unreadable.String(), "state="+StateUnavailable) {
		t.Fatalf("unreadable row = %q, want state=%s", unreadable.String(), StateUnavailable)
	}
	if strings.Contains(unreadable.String(), "state=absent") {
		t.Fatal("a failed look rendered as absence")
	}
}

// TestPrintLogLevelDoctorNamesEveryComponentAndItsSource: one row per
// registered component with the level in force and where it came from —
// build, config or env — over (config.Log, env) alone, no host read.
func TestPrintLogLevelDoctorNamesEveryComponentAndItsSource(t *testing.T) {
	var stdout bytes.Buffer
	log := config.Log{Level: "warn", Components: map[string]string{"mcp": "debug"}}
	env := &paths.MapEnv{Values: map[string]string{paths.EnvLogComponents: "db=off"}}
	printLogLevelDoctor(&stdout, log, "1.2.3-alpha", env)
	rows := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(rows) != len(obs.Components) {
		t.Fatalf("rows = %d, want one per component:\n%s", len(rows), stdout.String())
	}
	for _, want := range []string{
		"doctor: log comp=mcp level=debug source=config",
		"doctor: log comp=db level=off source=env",
		"doctor: log comp=tmux level=warn source=config",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("rows lack %q:\n%s", want, stdout.String())
		}
	}
	if !strings.HasPrefix(rows[0], "doctor: log comp=cli ") {
		t.Fatalf("first row = %q, want the registry order", rows[0])
	}

	stdout.Reset()
	printLogLevelDoctor(&stdout, config.Log{}, "1.2.3-alpha", &paths.MapEnv{})
	if !strings.Contains(stdout.String(), "doctor: log comp=cli level=debug source=build") {
		t.Fatalf("an alpha build with no config level must report debug from build:\n%s", stdout.String())
	}

	// A refused override is a row too — a doctor that hides the reason a
	// setting did not take is a doctor that lies about the level in force.
	stdout.Reset()
	refused := &paths.MapEnv{Values: map[string]string{paths.EnvLogLevel: "chatty"}}
	printLogLevelDoctor(&stdout, config.Log{}, "1.2.3", refused)
	if !strings.Contains(stdout.String(), "doctor: log control refused: "+paths.EnvLogLevel) ||
		!strings.Contains(stdout.String(), "comp=cli level=info source=build") {
		t.Fatalf("a refused override was not reported with what stands:\n%s", stdout.String())
	}
}

// TestPrintActivityLogDoctorIncludesTheComponentRows: the existing row keeps
// the path and size, and the per-component rows follow it.
func TestPrintActivityLogDoctorIncludesTheComponentRows(t *testing.T) {
	var stdout bytes.Buffer
	runtime := runtimeWithLog(filepath.Join(t.TempDir(), "pfm.jsonl"))
	runtime.Config.Log.Level = "error"
	runtime.Config.Log.KeepDays = 7
	printActivityLogDoctor(&stdout, runtime)
	if !strings.Contains(stdout.String(), "keep_days=7") {
		t.Fatalf("row lacks the retention:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "doctor: log comp=installer level=error source=config") {
		t.Fatalf("component rows missing:\n%s", stdout.String())
	}
}
