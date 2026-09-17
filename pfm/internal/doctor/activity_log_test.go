package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
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
