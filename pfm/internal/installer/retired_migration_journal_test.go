package installer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestRetireMigrationJournal pins the retire step's every door: apply removes
// the whole tree and names what it freed, once; a dry run names it and keeps
// it; an absent journal is silent; no sibling is touched. The failure door is
// removeStatePath's, pinned by TestExpireStateBackupsFailureWarnsAndKeeps.
func TestRetireMigrationJournal(t *testing.T) {
	cases := []struct {
		name  string
		apply bool
		seed  bool
		want  func(journal string) string
		gone  bool
	}{
		{"apply removes the tree", true, true, func(journal string) string {
			return "  change  remove retired migration journal " + journal + " (freed 14 bytes)\n"
		}, true},
		{"dry run names it", false, true, func(journal string) string {
			return "  change  remove retired migration journal " + journal + " (frees 14 bytes)\n"
		}, false},
		{"absent journal is silent", true, false, func(string) string { return "" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			journal := RetiredMigrationJournal(home)
			sibling := filepath.Join(filepath.Dir(journal), "pfm.db")
			writeFixture(t, sibling, "live")
			if tc.seed {
				writeFixture(t, filepath.Join(journal, "20260928T100205Z", "journal.json"), "journal")
				writeFixture(t, filepath.Join(journal, "20260928T100205Z", "backup", "settings.json"), "backup")
				writeFixture(t, filepath.Join(journal, "20260929T002504Z", "scope.json"), "s")
			}
			var output bytes.Buffer
			installer := &engine{options: Options{Home: home, Stdout: &output}, apply: tc.apply}
			installer.retireMigrationJournal()
			want := tc.want(journal)
			changed := 0
			if strings.Contains(want, "change") {
				changed = 1
			}
			if output.String() != want || installer.report.Changed != changed {
				t.Fatalf("output=%q changed=%d; want output=%q changed=%d",
					output.String(), installer.report.Changed, want, changed)
			}
			if _, err := os.Lstat(journal); tc.gone != errors.Is(err, os.ErrNotExist) {
				t.Fatalf("journal present=%v after the step, want gone=%v", err == nil, tc.gone)
			}
			if got := readFixture(t, sibling); got != "live" {
				t.Fatalf("sibling state changed: %q", got)
			}
			if !tc.apply {
				return
			}
			output.Reset()
			installer.retireMigrationJournal()
			if output.Len() != 0 {
				t.Fatalf("second run printed %q", output.String())
			}
		})
	}
}

// TestInstallRetiresStateAndAFailedRemovalOnlyWarns runs pfm install over a
// host holding the retired journal and an expired backup whose removal fails:
// the journal goes, the failure is one warn line, and the run still succeeds.
func TestInstallRetiresStateAndAFailedRemovalOnlyWarns(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2031, 2, 3, 4, 5, 6, 0, time.Local)
	state := filepath.Join(home, ".local", "state", "pfm")
	journal := RetiredMigrationJournal(home)
	writeFixture(t, filepath.Join(journal, "20260928T100205Z", "journal.json"), "journal")
	expired := filepath.Join(state, "pfm.db.bak-before-v3")
	writeFixture(t, expired, "backup")
	old := now.Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(expired, old, old); err != nil {
		t.Fatal(err)
	}
	restore := stateRemoveAll
	stateRemoveAll = func(path string) error {
		if path == expired {
			return errors.New("read-only file system")
		}
		return os.RemoveAll(path)
	}
	t.Cleanup(func() { stateRemoveAll = restore })
	sourceRepo := t.TempDir()
	recordFixtureSourceRepo(t, home, sourceRepo)
	configPath := filepath.Join(home, "pfm.config.json")
	writeFixture(t, configPath, `{"version":2}`)
	var output bytes.Buffer
	options := Options{
		Mode: ModeApply, Home: home, SourceRepo: sourceRepo, MCPConfigPath: configPath,
		CodexHomes: []string{}, Runner: &fakeRunner{nameSyncIdle: true},
		Env: &paths.MapEnv{HomeDir: home, Values: map[string]string{
			paths.EnvSIDDir:   filepath.Join(home, "sid"),
			paths.EnvStateDB:  filepath.Join(state, "pfm.db"),
			paths.EnvCacheDB:  filepath.Join(state, "pfm-cache.db"),
			paths.EnvProcRoot: filepath.Join(home, "proc"),
		}},
		Now:    func() time.Time { return now },
		Stdout: &output,
	}
	if _, err := Run(t.Context(), options); err != nil {
		t.Fatalf("install failed on a failed expiry removal: %v\n%s", err, output.String())
	}
	for _, want := range []string{
		"  change  remove retired migration journal " + journal + " (freed 7 bytes)\n",
		"  warn    " + expired + ": read-only file system\n",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("install output misses %q:\n%s", want, output.String())
		}
	}
	if _, err := os.Lstat(journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains: %v", err)
	}
	if got := readFixture(t, expired); got != "backup" {
		t.Fatalf("failed removal changed the backup: %q", got)
	}
}
