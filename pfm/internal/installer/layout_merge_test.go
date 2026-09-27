package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayoutSessionMergeAndRollback(t *testing.T) {
	env := layoutFixture(t)
	account := filepath.Join(env.Config.Accounts[1].ConfigDir, "file-history")
	store := filepath.Join(env.Home, ".claude", "file-history")
	if err := os.Remove(account); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, filepath.Join(account, "absent", "checkpoint"), "new")
	layoutWrite(t, filepath.Join(account, "same", "unique"), "account")
	layoutWrite(t, filepath.Join(account, "same", "identical"), "equal")
	layoutWrite(t, filepath.Join(account, "same", "different"), "account-copy")
	layoutWrite(t, filepath.Join(store, "same", "identical"), "equal")
	layoutWrite(t, filepath.Join(store, "same", "different"), "store-copy")
	journal := &Journal{env: env}
	finding := LayoutFinding{Row: "session-store", Verdict: VerdictMerge, Path: account}
	conflicts, err := mergeLayoutSession(journal, finding, []string{finding.Path, store}, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || !strings.Contains(conflicts[0], "same/different") {
		t.Fatalf("conflicts=%v", conflicts)
	}
	for path, want := range map[string]string{
		filepath.Join(store, "absent", "checkpoint"): "new",
		filepath.Join(store, "same", "unique"):       "account",
		filepath.Join(store, "same", "different"):    "store-copy",
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("%s=%q err=%v want=%q", path, got, err, want)
		}
	}
	link, err := os.Readlink(account)
	if err != nil || link != store {
		t.Fatalf("account link=%q err=%v want=%q", link, err, store)
	}
	parked := filepath.Join(journal.dir, "backup", "conflicts", "same", "different")
	if got, err := os.ReadFile(parked); err != nil || string(got) != "account-copy" {
		t.Fatalf("parked=%q err=%v", got, err)
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.dir), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(
		filepath.Join(account, "same", "different"),
	); err != nil ||
		string(got) != "account-copy" {
		t.Fatalf("account conflict restore=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(store, "same", "different")); err != nil || string(got) != "store-copy" {
		t.Fatalf("store conflict restore=%q err=%v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(store, "absent")); !os.IsNotExist(err) {
		t.Fatalf("new store child survived rollback: %v", err)
	}
}
