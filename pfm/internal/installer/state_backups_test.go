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

const stateStampLayout = "20060102-150405"

// writeAgedFixture writes a fixture file and sets its modification time.
func writeAgedFixture(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	writeFixture(t, path, "fixture")
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func stateExpiryEngine(home string, now time.Time, output *bytes.Buffer, apply bool) *engine {
	state := filepath.Join(home, ".local", "state", "pfm")
	return &engine{options: Options{
		Home: home, StateDB: filepath.Join(state, "pfm.db"), Stdout: output,
		Env: &paths.MapEnv{HomeDir: home, Values: map[string]string{
			paths.EnvStateDB: filepath.Join(state, "pfm.db"),
			paths.EnvCacheDB: filepath.Join(state, "pfm-cache.db"),
		}},
		Now: func() time.Time { return now },
	}, apply: apply}
}

// TestExpireStateBackups pins the 30-day window per kind: the age is the
// writer's own stamp where the name carries one (an archive moved by rename
// keeps its old mtime, so a backup made yesterday must not read as old), else
// the mtime a copy got; live state, unstamped names and a flight's hand
// archive are never touched.
func TestExpireStateBackups(t *testing.T) {
	now := time.Date(2031, 2, 3, 4, 5, 6, 0, time.Local)
	old, young := now.Add(-31*24*time.Hour), now.Add(-24*time.Hour)
	stamp := func(at time.Time) string { return at.Format(stateStampLayout) }
	utc := func(at time.Time) string { return at.UTC().Format(stateStampLayout) }
	cases := []struct {
		name, entry, file string
		mtime             time.Time
		made              time.Time
		expired           bool
	}{
		{"state db migration backup past its window", "pfm.db.bak-before-v3", "", old, old, true},
		{"cache db migration backup with suffix", "pfm-cache.db.bak-before-v9.1", "", old, old, true},
		{"migration backup inside its window", "pfm.db.bak-before-v4", "", young, young, false},
		{"retired fleet.db backup", "fleet.db.bak-wiped-20260823-001808", "", old, old, true},
		{"retired chat-skills temp dir", "retired-chat-skills.X8Z7oiYZ", "deep-rr/SKILL.md", old, old, true},
		{"archived store entry stamped long ago", "retired-store-entries/.last-update-result.json.pre-professor-" +
			stamp(old), "", young, old, true},
		{"archived store entry stamped yesterday keeps its old mtime", "retired-store-entries/.claude.json" +
			".pre-professor-" + stamp(young) + ".1", "", old, young, false},
		{"retired command backup", "retired-commands/cc-ls.pre-professor-" + stamp(old), "", old, old, true},
		{"doctor stale backup past its window", "stale-backup/" + stamp(old), ".zshrc", young, old, true},
		{"doctor stale backup inside its window", "stale-backup/" + stamp(young) + ".1", ".zshrc", old, young, false},
		{"stray claude state stamped in UTC", "stray-claude-state/" + utc(old), ".claude.json", young, old, true},
		{"stray claude state inside its window", "stray-claude-state/" + utc(young), ".claude.json", old, young, false},
		{"unstamped file in an archive dir", "retired-store-entries/notes.txt", "", old, time.Time{}, false},
		{"the live state db", "pfm.db", "", old, time.Time{}, false},
		{"a flight's hand archive", "account-sync-m7", "journal.json", old, time.Time{}, false},
	}
	for _, apply := range []bool{true, false} {
		t.Run(map[bool]string{true: "apply", false: "dry run"}[apply], func(t *testing.T) {
			home := t.TempDir()
			state := filepath.Join(home, ".local", "state", "pfm")
			for _, tc := range cases {
				writeAgedFixture(t, filepath.Join(state, tc.entry, tc.file), tc.mtime)
				if err := os.Chtimes(filepath.Join(state, tc.entry), tc.mtime, tc.mtime); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			installer := stateExpiryEngine(home, now, &output, apply)
			installer.expireStateBackups()
			verb := map[bool]string{true: "freed", false: "frees"}[apply]
			expired := 0
			for _, tc := range cases {
				entry := filepath.Join(state, tc.entry)
				_, statErr := os.Lstat(filepath.Join(entry, tc.file))
				line := "  change  remove expired backup " + entry + " (made " + tc.made.Format("2006-01-02") +
					", " + verb + " 7 bytes)\n"
				if tc.expired {
					expired++
					if !strings.Contains(output.String(), line) {
						t.Errorf("%s: output misses %q:\n%s", tc.name, line, output.String())
					}
					if apply != errors.Is(statErr, os.ErrNotExist) {
						t.Errorf("%s: removed=%v, want %v", tc.name, statErr != nil, apply)
					}
					continue
				}
				if strings.Contains(output.String(), entry+" ") || statErr != nil {
					t.Errorf("%s: kept entry touched: err=%v output:\n%s", tc.name, statErr, output.String())
				}
			}
			if installer.report.Changed != expired || strings.Count(output.String(), "\n") != expired {
				t.Fatalf("changed=%d lines=%d, want %d:\n%s",
					installer.report.Changed, strings.Count(output.String(), "\n"), expired, output.String())
			}
			if !apply {
				return
			}
			output.Reset()
			installer.expireStateBackups()
			if output.Len() != 0 {
				t.Fatalf("second run printed %q", output.String())
			}
		})
	}
}

// TestExpireStateBackupsFailureWarnsAndKeeps pins the failure door: a failed
// removal is one warn line naming the path and error, counts no change, and
// leaves the entry for the next run while the others still go.
func TestExpireStateBackupsFailureWarnsAndKeeps(t *testing.T) {
	now := time.Date(2031, 2, 3, 4, 5, 6, 0, time.Local)
	old := now.Add(-40 * 24 * time.Hour)
	home := t.TempDir()
	state := filepath.Join(home, ".local", "state", "pfm")
	stuck := filepath.Join(state, "pfm.db.bak-before-v2")
	freed := filepath.Join(state, "pfm.db.bak-before-v3")
	writeAgedFixture(t, stuck, old)
	writeAgedFixture(t, freed, old)
	restore := stateRemoveAll
	stateRemoveAll = func(path string) error {
		if path == stuck {
			return errors.New("permission denied")
		}
		return os.RemoveAll(path)
	}
	t.Cleanup(func() { stateRemoveAll = restore })
	var output bytes.Buffer
	installer := stateExpiryEngine(home, now, &output, true)
	installer.expireStateBackups()
	want := "  warn    " + stuck + ": permission denied\n" +
		"  change  remove expired backup " + freed + " (made " + old.Format("2006-01-02") + ", freed 7 bytes)\n"
	if output.String() != want || installer.report.Changed != 1 {
		t.Fatalf("output=%q changed=%d; want %q changed=1", output.String(), installer.report.Changed, want)
	}
	if got := readFixture(t, stuck); got != "fixture" {
		t.Fatalf("failed removal changed %s: %q", stuck, got)
	}
}

// TestStateBackupsNamesDoctorAsideAndFailedLooks pins the shared registry
// pfm doctor --stale reads: doctor's own move-aside dirs are marked, and an
// unreadable archive dir is an entry carrying its error, never absence.
func TestStateBackupsNamesDoctorAsideAndFailedLooks(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".local", "state", "pfm")
	writeFixture(t, filepath.Join(state, "stale-backup", "20300101-000000", ".zshrc"), "x")
	writeFixture(t, filepath.Join(state, "stray-claude-state", "20300101-000000", ".claude.json"), "x")
	writeFixture(t, filepath.Join(state, "retired-commands"), "a file where a dir belongs")
	backups := StateBackups(home, filepath.Join(state, "pfm.db"), filepath.Join(state, "pfm-cache.db"))
	got := map[string]StateBackup{}
	for _, backup := range backups {
		got[backup.Path] = backup
	}
	for _, aside := range []string{
		filepath.Join(state, "stale-backup", "20300101-000000"),
		filepath.Join(state, "stray-claude-state", "20300101-000000"),
	} {
		if backup, ok := got[aside]; !ok || !backup.DoctorAside || backup.Err != nil {
			t.Errorf("aside %s: %+v ok=%v in %+v", aside, backup, ok, backups)
		}
	}
	failed, ok := got[filepath.Join(state, "retired-commands")]
	if !ok || failed.Err == nil || failed.Check != "retired-archive" {
		t.Fatalf("unreadable archive dir: %+v ok=%v in %+v", failed, ok, backups)
	}
}
