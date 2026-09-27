package installer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

func TestJournalSealPrunesOldSealedJournals(t *testing.T) {
	env := layoutFixture(t)
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	ids := map[int]string{}
	for _, age := range []int{40, 30, 20, 16} {
		id := now.AddDate(0, 0, -age).Format("20060102T150405Z")
		ids[age] = id
		body := `[{"result":"applied"}]`
		if age == 40 {
			body = `[{"result":"applied"},{"result":"restored"}]`
		}
		layoutWrite(t, filepath.Join(root, id, "journal.json"), body)
	}
	journal := NewJournal(context.Background(), env)
	journal.clock = clock.NewFake(now)
	if err := journal.Write([]string{filepath.Join(env.Home, ".zshrc")}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := journal.Seal(&output); err != nil {
		t.Fatal(err)
	}
	for _, age := range []int{40, 30, 20, 16} {
		_, err := os.Stat(filepath.Join(root, ids[age]))
		wantRemoved := age >= 30
		if os.IsNotExist(err) != wantRemoved {
			t.Errorf("age %d exists=%t, want removed=%t: %v", age, err == nil, wantRemoved, err)
		}
		if wantRemoved && !strings.Contains(output.String(), "  prune   install journal "+ids[age]+" (") {
			t.Errorf("age %d missing prune line: %q", age, output.String())
		}
	}
	if _, err := os.Stat(journal.Dir()); err != nil {
		t.Fatalf("current journal pruned: %v", err)
	}
}

func TestJournalSealKeepsYoungPendingAndUnreadableJournals(t *testing.T) {
	env := layoutFixture(t)
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	ids := []string{}
	for age := 1; age <= 5; age++ {
		id := now.AddDate(0, 0, -age).Format("20060102T150405Z")
		ids = append(ids, id)
		layoutWrite(t, filepath.Join(root, id, "journal.json"), `[{"result":"applied"}]`)
	}
	for age, body := range map[int]string{40: `[{"result":"pending"}]`, 41: `{`} {
		id := now.AddDate(0, 0, -age).Format("20060102T150405Z")
		ids = append(ids, id)
		layoutWrite(t, filepath.Join(root, id, "journal.json"), body)
	}
	id := now.AddDate(0, 0, -42).Format("20060102T150405Z")
	ids = append(ids, id)
	if err := os.MkdirAll(filepath.Join(root, id), 0o700); err != nil {
		t.Fatal(err)
	}
	pruneInstallJournals(env.Home, "", now, io.Discard)
	for _, id := range ids {
		if _, err := os.Stat(filepath.Join(root, id)); err != nil {
			t.Errorf("journal %s was pruned: %v", id, err)
		}
	}
}

func TestJournalSealWithoutRecordsDoesNotPrune(t *testing.T) {
	env := layoutFixture(t)
	root := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	ids := []string{"20200101T000000Z", "20200102T000000Z", "20200103T000000Z", "20200104T000000Z"}
	for _, id := range ids {
		layoutWrite(t, filepath.Join(root, id, "journal.json"), `[]`)
	}
	for _, journal := range []*Journal{{env: env}, {env: env, dryRun: true}} {
		if err := journal.Seal(io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		if _, err := os.Stat(filepath.Join(root, id)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJournalSealWarnsWhenPruneFails(t *testing.T) {
	env := layoutFixture(t)
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	failedID := now.AddDate(0, 0, -40).Format("20060102T150405Z")
	for _, age := range []int{40, 30, 20, 16} {
		id := now.AddDate(0, 0, -age).Format("20060102T150405Z")
		layoutWrite(t, filepath.Join(root, id, "journal.json"), `[]`)
	}
	previous := removeInstallJournal
	removeInstallJournal = func(path string) error {
		if filepath.Base(path) == failedID {
			return errors.New("permission denied")
		}
		return previous(path)
	}
	t.Cleanup(func() { removeInstallJournal = previous })
	journal := NewJournal(context.Background(), env)
	journal.clock = clock.NewFake(now)
	if err := journal.Write([]string{filepath.Join(env.Home, ".zshrc")}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := journal.Seal(&output); err != nil {
		t.Fatalf("prune failure failed seal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, failedID)); err != nil ||
		!strings.Contains(output.String(), "  warn    install journal "+failedID+" not pruned: permission denied") {
		t.Fatalf("failed prune removed journal or missed warning: err=%v output=%q", err, output.String())
	}
}

func TestRollbackRemovesTheMigrationBackupsTheInstallCreated(t *testing.T) {
	env := layoutFixture(t)
	env.StateDB = filepath.Join(env.Home, ".local", "state", "pfm", "pfm.db")
	env.CacheDB = filepath.Join(env.Home, ".local", "state", "pfm", "pfm-cache.db")
	layoutWrite(t, env.StateDB, "moved\n")
	journal := NewJournal(context.Background(), env)
	if err := journal.Write([]string{env.StateDB}, func() error {
		return os.WriteFile(env.StateDB, []byte("migrated\n"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	before, err := journal.MigrationBackups()
	if err != nil {
		t.Fatal(err)
	}
	stateBackup := env.StateDB + ".bak-before-v2"
	cacheBackup := env.CacheDB + ".bak-before-v10"
	layoutWrite(t, stateBackup, "moved\n")
	layoutWrite(t, cacheBackup, "cache\n")
	if err := journal.JournalMigrationBackups(before); err != nil {
		t.Fatal(err)
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.Dir()), false, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{stateBackup, cacheBackup} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("rollback left the install's migration backup %s: %v", path, err)
		}
	}
	if got := readFixture(t, env.StateDB); got != "moved\n" {
		t.Fatalf("state database = %q, want the pre-install content", got)
	}
}

func TestJournalMigrationBackupsLeavesABackupThatPredatesTheInstall(t *testing.T) {
	env := layoutFixture(t)
	env.StateDB = filepath.Join(env.Home, ".local", "state", "pfm", "pfm.db")
	env.CacheDB = filepath.Join(env.Home, ".local", "state", "pfm", "pfm-cache.db")
	layoutWrite(t, env.StateDB, "moved\n")
	operator := env.StateDB + ".bak-before-v1"
	layoutWrite(t, operator, "an older backup\n")
	journal := NewJournal(context.Background(), env)
	if err := journal.Write([]string{env.StateDB}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	before, err := journal.MigrationBackups()
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.JournalMigrationBackups(before); err != nil {
		t.Fatal(err)
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.Dir()), false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, operator); got != "an older backup\n" {
		t.Fatalf("rollback touched a backup that predates the install: %q", got)
	}
}
