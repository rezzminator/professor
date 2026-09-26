package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayoutJournalRollbackRestoresPriorBytesAndLinks(t *testing.T) {
	env := layoutFixture(t)
	before, err := os.ReadFile(filepath.Join(env.Home, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	journal := &layoutJournal{env: env}
	finding := LayoutFinding{Row: "zshrc", Verdict: VerdictRepoint, Path: filepath.Join(env.Home, ".zshrc")}
	err = journal.mutate(finding, []string{finding.Path}, func() error {
		return os.WriteFile(finding.Path, []byte("changed\n"), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(env.Home, ".local", "state", "pfm", "shared.db")
	linkFinding := LayoutFinding{Row: "shared-db", Verdict: VerdictCreate, Path: created}
	if err := journal.mutate(linkFinding, []string{created}, func() error {
		return os.Symlink(filepath.Join(env.Home, ".claude"), created)
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(journal.dir, "journal.json"))
	if err != nil || !bytes.Contains(raw, []byte(`"backup"`)) || !bytes.Contains(raw, []byte(`"result": "applied"`)) {
		t.Fatalf("journal=%s err=%v", raw, err)
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.dir), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(finding.Path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("restored zshrc=%q err=%v, want %q", after, err, before)
	}
	if _, err := os.Lstat(created); !os.IsNotExist(err) {
		t.Fatalf("created link survived rollback: %v", err)
	}
	if _, err := os.Lstat(journal.dir); !os.IsNotExist(err) {
		t.Fatalf("journal directory survived rollback: %v", err)
	}
}

func TestLayoutJournalUnknownIDNamesMigrationsDir(t *testing.T) {
	env := layoutFixture(t)
	err := RollbackLayout(context.Background(), env, "20260101T000000Z", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(env.Home, ".local", "state", "pfm", "migrations")) {
		t.Fatalf("unknown journal error=%v", err)
	}
}

func TestLayoutRollbackRefusesDatabaseHolder(t *testing.T) {
	env := layoutFixture(t)
	journal := &layoutJournal{env: env}
	if err := journal.snapshot(layoutRowStateDB, VerdictMove, env.StateDB); err != nil {
		t.Fatal(err)
	}
	fd := filepath.Join(env.ProcRoot, "4242", "fd")
	if err := os.MkdirAll(fd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(env.StateDB, filepath.Join(fd, "3")); err != nil {
		t.Fatal(err)
	}
	err := RollbackLayout(context.Background(), env, filepath.Base(journal.dir), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "held by pid 4242") {
		t.Fatalf("holder rollback error=%v", err)
	}
	if _, err := os.Stat(journal.dir); err != nil {
		t.Fatalf("refused rollback removed its journal: %v", err)
	}
}

func TestLayoutRollbackReportsEveryFailedRecordAndRestoresOthers(t *testing.T) {
	env := layoutFixture(t)
	path := filepath.Join(env.Home, ".zshrc")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	journal := &layoutJournal{env: env}
	if err := journal.mutate(
		LayoutFinding{Row: "zshrc", Verdict: VerdictRepoint, Path: path},
		[]string{path},
		func() error {
			return os.WriteFile(path, []byte("changed"), 0o600)
		},
	); err != nil {
		t.Fatal(err)
	}
	operator := filepath.Join(env.Home, "operator.txt")
	layoutWrite(t, operator, "keep")
	journal.records = append(
		journal.records,
		layoutJournalRecord{Row: "zshrc", Destination: operator, Result: "applied"},
	)
	if err := journal.flush(); err != nil {
		t.Fatal(err)
	}
	err = RollbackLayout(context.Background(), env, filepath.Base(journal.dir), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "record 1 has unsafe path") {
		t.Fatalf("partial rollback error=%v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("healthy record not restored: %q err=%v", after, err)
	}
	if _, err := os.Stat(journal.dir); err != nil {
		t.Fatalf("partial rollback removed recovery journal: %v", err)
	}
	if got, err := os.ReadFile(operator); err != nil || string(got) != "keep" {
		t.Fatalf("rollback touched unlisted operator path: %q err=%v", got, err)
	}
}
