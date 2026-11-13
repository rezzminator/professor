package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// runtimeWithLog builds a runtime over home, a home with no clone on record:
// its log root is the home's state folder.
func runtimeWithLog(home string) config.Runtime {
	runtime := config.Runtime{Config: config.Config{Log: config.DefaultLog()}}
	runtime.Paths.Home = home
	return runtime
}

func TestPrintActivityLogDoctorNamesThePathAndSize(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".local", "state", "pfm", "log", "pfm.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"msg\":\"x\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	printActivityLogDoctor(&stdout, runtimeWithLog(home), &paths.MapEnv{})
	row := strings.SplitN(stdout.String(), "\n", 2)[0]
	if want := "doctor: log path=" + path + " bytes=12 level=build segment_mb=8 keep_mb=80"; row != want {
		t.Fatalf("row = %q, want %q", row, want)
	}
}

// A recorded clone the log cannot live in is a warning row naming why, and
// the row above it names the state folder the log fell back to.
func TestPrintActivityLogDoctorWarnsOfAFallbackRoot(t *testing.T) {
	home, clone := t.TempDir(), filepath.Join(t.TempDir(), "professor")
	if err := os.Mkdir(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(clone); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	warnings := printActivityLogDoctor(&stdout, runtimeWithLog(home), &paths.MapEnv{})
	rows := strings.Split(stdout.String(), "\n")
	state := filepath.Join(home, ".local", "state", "pfm", "log")
	want := "doctor: log WARN root=" + state + ": log root: inspect recorded source repository " + clone +
		": recorded source repository is unusable: stat " + clone + ": no such file or directory"
	if warnings != 1 || rows[1] != want {
		t.Fatalf("warnings = %d, row = %q; want 1 and %q", warnings, rows[1], want)
	}
}

// An unwritten log is ABSENT; a log that could not be stat'd is unavailable.
// One row may never render both the same way.
func TestPrintActivityLogDoctorSeparatesAbsenceFromAFailedLook(t *testing.T) {
	var absent bytes.Buffer
	if warnings := printActivityLogDoctor(&absent, runtimeWithLog(t.TempDir()), &paths.MapEnv{}); warnings != 0 {
		t.Fatalf("an unwritten log is a fresh home, not a warning: %d", warnings)
	}
	if !strings.Contains(absent.String(), "state=absent") || !strings.Contains(absent.String(), "bytes=0") {
		t.Fatalf("absent row = %q, want state=absent bytes=0", absent.String())
	}

	blocked := t.TempDir()
	logDir := filepath.Join(blocked, ".local", "state", "pfm", "log")
	if err := os.MkdirAll(filepath.Dir(logDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var unreadable bytes.Buffer
	if warnings := printActivityLogDoctor(&unreadable, runtimeWithLog(blocked), &paths.MapEnv{}); warnings != 0 {
		t.Fatalf("an unreadable log is reported, not tallied: %d", warnings)
	}
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
	if rows[0] != "doctor: log comp=cli level=warn source=config" {
		t.Fatalf("first row = %q, want the registry order", rows[0])
	}

	stdout.Reset()
	printLogLevelDoctor(&stdout, config.Log{}, "1.2.3-alpha", &paths.MapEnv{})
	if stdout.String() != `doctor: log comp=cli level=debug source=build
doctor: log comp=mcp level=debug source=build
doctor: log comp=http.in level=debug source=build
doctor: log comp=http.out level=debug source=build
doctor: log comp=runner level=debug source=build
doctor: log comp=tmux level=debug source=build
doctor: log comp=hooks level=debug source=build
doctor: log comp=db level=debug source=build
doctor: log comp=installer level=debug source=build
doctor: log comp=state level=debug source=build
` {
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
	dir := t.TempDir()
	runtime := runtimeWithLog(dir)
	runtime.Config.Log.Level = "error"
	runtime.Config.Log.KeepFiles = 3
	printActivityLogDoctor(&stdout, runtime, &paths.MapEnv{})
	if got, want := rooted(
		stdout.String(),
		dir,
	), "doctor: log path={root}/.local/state/pfm/log/pfm.jsonl bytes=0 state=absent level=error segment_mb=8 keep_mb=24\ndoctor: log comp=cli level=error source=config\ndoctor: log comp=mcp level=error source=config\ndoctor: log comp=http.in level=error source=config\ndoctor: log comp=http.out level=error source=config\ndoctor: log comp=runner level=error source=config\ndoctor: log comp=tmux level=error source=config\ndoctor: log comp=hooks level=error source=config\ndoctor: log comp=db level=error source=config\ndoctor: log comp=installer level=error source=config\ndoctor: log comp=state level=error source=config\n"; got != want {
		t.Errorf("activity log rows = %q, want %q", got, want)
	}
}

// TestPrintActivityLogDoctorCountsARefusedOverride: an override obs refused
// leaves the operator believing a level is in force that is not — the row is
// printed and counted as one warning, read from the env the caller injects.
func TestPrintActivityLogDoctorCountsARefusedOverride(t *testing.T) {
	var stdout bytes.Buffer
	refused := &paths.MapEnv{Values: map[string]string{paths.EnvLogLevel: "chatty"}}
	warnings := printActivityLogDoctor(&stdout, runtimeWithLog(t.TempDir()), refused)
	if warnings != 1 {
		t.Fatalf("a refused override counted %d warnings, want 1:\n%s", warnings, stdout.String())
	}
	if !strings.Contains(stdout.String(), "doctor: log control refused: "+paths.EnvLogLevel) {
		t.Fatalf("the refused row is missing:\n%s", stdout.String())
	}
}
