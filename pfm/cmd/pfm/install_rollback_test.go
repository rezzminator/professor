package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestInstallRollbackUsesJournalAndRejectsMixedFlags(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(_ context.Context, _ installer.Options) (installer.Report, error) {
		t.Fatal("rollback entered ordinary installer")
		return installer.Report{}, nil
	}
	home := t.TempDir()
	runtime := commandRuntime{
		Paths:  paths.Values{Home: home},
		Config: pfmconfig.Config{Path: filepath.Join(home, "pfm.config.json")},
	}
	id := "20260102T030405Z"
	dir := filepath.Join(home, ".local", "state", "pfm", "migrations", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.json"), []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "scope.json"),
		[]byte(`{"accounts":[],"codexHomes":[],"stateDb":"","cacheDb":""}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--rollback", id, "--yes"}, {"--rollback", ""}} {
		var stdout, stderr bytes.Buffer
		if code := runInstall(args, &stdout, &stderr, runtime); code != 2 {
			t.Errorf("args=%v code=%d stderr=%s, want usage 2", args, code, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--rollback", id}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("rollback code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "rolled-back")); err != nil {
		t.Fatalf("rolled-back journal is not kept and marked: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runInstall([]string{"--rollback", id}, &stdout, &stderr, runtime); code != 1 ||
		!strings.Contains(stderr.String(), "refused: already rolled back at ") {
		t.Fatalf("second rollback code=%d stderr=%s", code, stderr.String())
	}
	stderr.Reset()
	if code := runInstall([]string{"--rollback", "20260102T030406Z"}, &stdout, &stderr, runtime); code != 2 ||
		!strings.Contains(stderr.String(), filepath.Join(home, ".local", "state", "pfm", "migrations")) {
		t.Fatalf("unknown rollback code=%d stderr=%s", code, stderr.String())
	}
}

func TestInstallRollbackRefusesJournalWithoutScope(t *testing.T) {
	home := t.TempDir()
	id := "20260102T030405Z"
	dir := filepath.Join(home, ".local", "state", "pfm", "migrations", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.json"), []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{
		Paths:  paths.Values{Home: home},
		Config: pfmconfig.Config{Path: filepath.Join(home, "pfm.config.json")},
	}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--rollback", id}, &stdout, &stderr, runtime); code != 1 ||
		!strings.Contains(stderr.String(), "rollback "+id+" refused: journal "+dir+" has no scope.json") {
		t.Fatalf("rollback code=%d stderr=%q", code, stderr.String())
	}
}

func TestInstallRollbackForceOverwritesDrift(t *testing.T) {
	home := t.TempDir()
	written := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(written, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := installer.NewJournal(context.Background(), installer.LayoutEnv{Home: home})
	if err := journal.Write([]string{written}, func() error {
		return os.WriteFile(written, []byte("installed\n"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(written, []byte("newer work after the install\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := filepath.Base(journal.Dir())
	runtime := commandRuntime{Paths: paths.Values{Home: home, ProcRoot: t.TempDir()}}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--rollback", id}, &stdout, &stderr, runtime); code != 1 ||
		!strings.Contains(stderr.String(), "pfm install: rollback: rollback "+id+" refused: drift at "+written) {
		t.Fatalf("drifted rollback code=%d stderr=%q", code, stderr.String())
	}
	if got, err := os.ReadFile(written); err != nil || string(got) != "newer work after the install\n" {
		t.Fatalf("refused rollback touched %s: %q err=%v", written, got, err)
	}
	stderr.Reset()
	if code := runInstall([]string{"--rollback", id, "--force"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("forced rollback code=%d stderr=%q", code, stderr.String())
	}
	if got, err := os.ReadFile(written); err != nil || string(got) != "before\n" {
		t.Fatalf("forced rollback left %s=%q err=%v", written, got, err)
	}
}

func TestInstallRollbackAcceptsTheStateDBItMigrated(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(context.Context, installer.Options) (installer.Report, error) { return installer.Report{}, nil }
	home := t.TempDir()
	statePath := filepath.Join(home, ".local", "state", "pfm", "pfm.db")
	cachePath := filepath.Join(home, ".local", "state", "pfm", "pfm-cache.db")
	procRoot := t.TempDir()
	// systemctl answers ActiveState as a real one does for a unit not loaded.
	binDir, _ := writeManagerFakes(t, "case \"$*\" in *ActiveState*) echo inactive ;; esac\nexit 0", "exit 0")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", home)
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_STATE_DB", statePath)
	t.Setenv("PFM_CACHE_DB", cachePath)
	t.Setenv("PFM_PROC_ROOT", procRoot)
	legacy := paths.LegacyStateDB(home)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := sqlitedb.OpenStore(context.Background(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Exec(
		`PRAGMA user_version=1; CREATE TABLE swap_event(id INTEGER PRIMARY KEY); INSERT INTO swap_event VALUES(1)`,
	); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	legacyBytes, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{
		Paths:  paths.Values{Home: home, StateDB: statePath, CacheDB: cachePath, ProcRoot: procRoot},
		Config: pfmconfig.Config{Path: filepath.Join(home, "pfm.config.json")},
	}
	var stdout, stderr bytes.Buffer
	runInstall([]string{"--yes", "--skip-harvest"}, &stdout, &stderr, runtime)
	var id string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if dir, ok := strings.CutPrefix(line, "install journal: "); ok {
			id = filepath.Base(dir)
		}
	}
	if id == "" || !strings.Contains(stdout.String(), "  ok      layout state-db "+statePath) {
		t.Fatalf("install did not move and journal the state db: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	stderr.Reset()
	if code := runInstall([]string{"--rollback", id}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("rollback after the install's own migration code=%d stderr=%q", code, stderr.String())
	}
	if got, err := os.ReadFile(legacy); err != nil || !bytes.Equal(got, legacyBytes) {
		t.Fatalf("legacy state db not restored: %d bytes err=%v", len(got), err)
	}
}
