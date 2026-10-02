package installer

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLiveChatPIDs(t *testing.T) {
	root := t.TempDir()
	account, proc := filepath.Join(root, "account"), filepath.Join(root, "proc")
	for _, pid := range []string{"12", "3"} {
		if err := os.MkdirAll(filepath.Join(proc, pid), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(account, "sessions", pid+".json"), "{}")
	}
	for _, name := range []string{"42.json", "not-a-pid.json", "12.txt"} {
		writeFixture(t, filepath.Join(account, "sessions", name), "{}")
	}
	if err := os.Mkdir(filepath.Join(account, "sessions", "99.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := liveChatPIDs(proc, account)
	if err != nil || !reflect.DeepEqual(got, []string{"12", "3"}) {
		t.Fatalf("live=%v err=%v", got, err)
	}
	pids, err := processIDs(proc)
	if err != nil || !reflect.DeepEqual(pids, got) {
		t.Fatalf("processes=%v err=%v", pids, err)
	}
}

func TestLiveChatPIDsMissingAndUnreadable(t *testing.T) {
	root := t.TempDir()
	if got, err := liveChatPIDs(root, filepath.Join(root, "missing")); err != nil || len(got) != 0 {
		t.Fatalf("missing sessions=%v err=%v", got, err)
	}
	account := filepath.Join(root, "account")
	writeFixture(t, filepath.Join(account, "sessions"), "not a directory")
	if _, err := liveChatPIDs(root, account); err == nil {
		t.Fatal("unreadable sessions read as empty")
	}
	if err := os.Remove(filepath.Join(account, "sessions")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(account, "sessions", "12.json"), "{}")
	if _, err := processIDs(filepath.Join(root, "missing-proc")); err == nil {
		t.Fatal("unreadable process table read as empty")
	}
}
