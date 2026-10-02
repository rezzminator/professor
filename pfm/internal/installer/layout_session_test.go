package installer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestLayoutSessionStoreTarget(t *testing.T) {
	for _, link := range []string{"present", "absent"} {
		for _, state := range []string{"missing", "file", "unreadable", "directory"} {
			t.Run(link+"/"+state, func(t *testing.T) {
				env := layoutFixture(t)
				path := filepath.Join(env.Config.Accounts[1].ConfigDir, "projects")
				store := filepath.Join(env.Home, ".claude", "projects")
				if link == "absent" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				if state != "directory" {
					if err := os.Remove(store); err != nil {
						t.Fatal(err)
					}
				}
				switch state {
				case "file":
					layoutWrite(t, store, "operator data")
				case "unreadable":
					if err := os.Symlink(store, store); err != nil {
						t.Fatal(err)
					}
				}
				finding := layoutFinding(t, classifySessionStore(env), "session-store", path)
				if state == "unreadable" {
					if !errors.Is(finding.Err, syscall.ELOOP) || !strings.Contains(finding.Err.Error(), store) {
						t.Fatalf("store stat error lost: %+v", finding)
					}
					return
				}
				want := VerdictCreate
				if state == "file" {
					want = VerdictRefuse
				} else if state == "directory" && link == "present" {
					want = VerdictOK
				}
				if finding.Err != nil || finding.Verdict != want {
					t.Fatalf("finding=%+v, want %s", finding, want)
				}
				if state == "file" && finding.Detail != "store entry is not a directory" {
					t.Fatalf("file detail=%q", finding.Detail)
				}
				if link == "present" && finding.Source != store {
					t.Fatalf("source=%q, want %q", finding.Source, store)
				}
				if link == "absent" && finding.Source != "" {
					t.Fatalf("absent link source=%q", finding.Source)
				}
			})
		}
	}
}

func TestLayoutApplySessionStore(t *testing.T) {
	for _, scenario := range []string{"seats-only", "dangling"} {
		t.Run(scenario, func(t *testing.T) {
			env := layoutFixture(t)
			env.Config.Accounts[0] = pfmconfig.Account{ID: 1, ConfigDir: filepath.Join(env.Home, ".cc", "1")}
			for _, entry := range SessionPaths {
				store := filepath.Join(env.Home, ".claude", entry)
				if err := os.Remove(store); err != nil {
					t.Fatal(err)
				}
				for _, account := range env.Config.Accounts {
					link := filepath.Join(account.ConfigDir, entry)
					if account.ID == 1 && scenario == "dangling" {
						if err := os.MkdirAll(account.ConfigDir, 0o700); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(store, link); err != nil {
							t.Fatal(err)
						}
					}
					if account.ID == 2 && scenario == "seats-only" {
						if err := os.Remove(link); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			beforeLinks := map[string]os.FileInfo{}
			if scenario == "dangling" {
				for _, account := range env.Config.Accounts {
					for _, entry := range SessionPaths {
						link := filepath.Join(account.ConfigDir, entry)
						info, err := os.Lstat(link)
						if err != nil {
							t.Fatal(err)
						}
						beforeLinks[link] = info
					}
				}
			}
			var output bytes.Buffer
			dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
			if err != nil || dir == "" {
				t.Fatalf("apply journal=%q err=%v output=%s", dir, err, output.String())
			}
			for _, entry := range SessionPaths {
				store := filepath.Join(env.Home, ".claude", entry)
				info, err := os.Stat(store)
				if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
					t.Fatalf("store %s info=%v err=%v", store, info, err)
				}
				for _, account := range env.Config.Accounts {
					link := filepath.Join(account.ConfigDir, entry)
					target, err := filepath.EvalSymlinks(link)
					if err != nil || target != store {
						t.Fatalf("link %s target=%q err=%v", link, target, err)
					}
					if before, exists := beforeLinks[link]; exists {
						after, err := os.Lstat(link)
						if err != nil || !os.SameFile(before, after) {
							t.Fatalf("dangling link replaced: %s err=%v", link, err)
						}
					}
				}
			}
			for _, finding := range ClassifyLayout(env) {
				if finding.Err != nil || finding.Verdict != VerdictOK {
					t.Errorf("remaining finding %+v", finding)
				}
			}
			var second bytes.Buffer
			again, err := ApplyLayout(context.Background(), env, nil, true, &second)
			if err != nil || again != "" || !strings.Contains(second.String(), "layout: nothing to do") {
				t.Fatalf("second journal=%q err=%v output=%s", again, err, second.String())
			}
			if err := RollbackLayout(
				context.Background(),
				env,
				filepath.Base(dir),
				false,
				&bytes.Buffer{},
			); err != nil {
				t.Fatal(err)
			}
			for _, entry := range SessionPaths {
				store := filepath.Join(env.Home, ".claude", entry)
				if _, err := os.Lstat(store); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("rollback store %s err=%v", store, err)
				}
				for _, account := range env.Config.Accounts {
					link := filepath.Join(account.ConfigDir, entry)
					if scenario == "dangling" {
						if target, err := os.Readlink(link); err != nil || target != store {
							t.Fatalf("rollback link %s target=%q err=%v", link, target, err)
						}
					} else if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("rollback link %s err=%v", link, err)
					}
				}
			}
		})
	}
}

func TestLayoutApplySessionStoreFileRefuses(t *testing.T) {
	env := layoutFixture(t)
	store := filepath.Join(env.Home, ".claude", "projects")
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, store, "operator data")
	var output bytes.Buffer
	_, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err == nil || !strings.Contains(err.Error(), "store entry is not a directory") {
		t.Fatalf("apply err=%v output=%s", err, output.String())
	}
	if data, err := os.ReadFile(store); err != nil || string(data) != "operator data" {
		t.Fatalf("store changed: data=%q err=%v", data, err)
	}
}

func TestLayoutSessionStoreCreateError(t *testing.T) {
	env := layoutFixture(t)
	store := filepath.Join(env.Home, ".claude", "projects")
	link := filepath.Join(env.Config.Accounts[1].ConfigDir, "projects")
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, store, "operator data")
	finding := LayoutFinding{Row: layoutRowSessionStore, Verdict: VerdictCreate, Path: link}
	err := applyLayoutRow(context.Background(), NewJournal(context.Background(), env), finding, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), store) {
		t.Fatalf("store-creation err=%v, want store path %s", err, store)
	}
	if data, err := os.ReadFile(store); err != nil || string(data) != "operator data" {
		t.Fatalf("store changed: data=%q err=%v", data, err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed store creation changed link: %v", err)
	}
}

func TestLayoutSessionStoreRepointCreatesStore(t *testing.T) {
	env := layoutFixture(t)
	store := filepath.Join(env.Home, ".claude", "projects")
	link := filepath.Join(env.Config.Accounts[1].ConfigDir, "projects")
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(env.Home, "old-projects")
	if err := os.Symlink(old, link); err != nil {
		t.Fatal(err)
	}
	journal := NewJournal(context.Background(), env)
	finding := LayoutFinding{Row: layoutRowSessionStore, Verdict: VerdictRepoint, Path: link, Source: old}
	if err := applyLayoutRow(context.Background(), journal, finding, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(link); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("repoint info=%v err=%v", info, err)
	}
	if err := RollbackLayout(
		context.Background(),
		env,
		filepath.Base(journal.dir),
		false,
		&bytes.Buffer{},
	); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != old {
		t.Fatalf("rollback target=%q err=%v", target, err)
	}
	if _, err := os.Lstat(store); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback store err=%v", err)
	}
}

func createdSessionStoreJournal(t *testing.T) (LayoutEnv, string, string, string, string) {
	t.Helper()
	env := layoutFixture(t)
	store := filepath.Join(env.Home, ".claude", "projects")
	link := filepath.Join(env.Config.Accounts[1].ConfigDir, "projects")
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(env.Home, "old-projects")
	if err := os.Symlink(old, link); err != nil {
		t.Fatal(err)
	}
	journal := NewJournal(context.Background(), env)
	finding := LayoutFinding{Row: layoutRowSessionStore, Verdict: VerdictRepoint, Path: link, Source: old}
	if err := applyLayoutRow(context.Background(), journal, finding, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	return env, filepath.Base(journal.dir), store, link, old
}

func TestLayoutRollbackKeepsACreatedSessionStoreThatHoldsData(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "forced"}[force], func(t *testing.T) {
			env, id, store, link, old := createdSessionStoreJournal(t)
			transcript := filepath.Join(store, "session.jsonl")
			layoutWrite(t, transcript, "chat data")
			var output bytes.Buffer
			if err := RollbackLayout(context.Background(), env, id, force, &output); err != nil {
				t.Fatalf("rollback: %v; output=%s", err, output.String())
			}
			if data, err := os.ReadFile(transcript); err != nil || string(data) != "chat data" {
				t.Fatalf("transcript=%q err=%v", data, err)
			}
			if want := "  keep    session store " + store + " holds data written since the install; left in place\n"; !strings.Contains(
				output.String(),
				want,
			) {
				t.Fatalf("output=%q, want %q", output.String(), want)
			}
			if target, err := os.Readlink(link); err != nil || target != old {
				t.Fatalf("seat link target=%q err=%v", target, err)
			}
		})
	}
}

func TestLayoutRollbackKeepsACreatedStoreReplacedByAFile(t *testing.T) {
	env, id, store, link, old := createdSessionStoreJournal(t)
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, store, "replacement")
	var output bytes.Buffer
	if err := RollbackLayout(context.Background(), env, id, false, &output); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(store); err != nil || string(data) != "replacement" {
		t.Fatalf("replacement=%q err=%v", data, err)
	}
	if !strings.Contains(
		output.String(),
		"  keep    session store "+store+" holds data written since the install; left in place\n",
	) {
		t.Fatalf("output=%q", output.String())
	}
	if target, err := os.Readlink(link); err != nil || target != old {
		t.Fatalf("seat link target=%q err=%v", target, err)
	}
}

// Two accounts merged in one install: the first merge creates the store, the
// second journals it again with a backup of what the first made. The store is
// still the install's own, so a transcript written since is kept, with or
// without --force, and each account's own entry comes back.
func TestLayoutRollbackKeepsAStoreTwoMergesCreated(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "forced"}[force], func(t *testing.T) {
			env := layoutFixture(t)
			env.Config.Accounts[0] = pfmconfig.Account{ID: 1, ConfigDir: filepath.Join(env.Home, ".cc", "1")}
			store := filepath.Join(env.Home, ".claude", "projects")
			if err := os.Remove(store); err != nil {
				t.Fatal(err)
			}
			journal := NewJournal(context.Background(), env)
			accounts := []string{}
			for index, account := range env.Config.Accounts {
				entry := filepath.Join(account.ConfigDir, "projects")
				if err := os.RemoveAll(entry); err != nil {
					t.Fatal(err)
				}
				layoutWrite(t, filepath.Join(entry, "p", "old-"+string(rune('a'+index))+".jsonl"), "old")
				accounts = append(accounts, entry)
			}
			for _, entry := range accounts {
				finding := LayoutFinding{Row: layoutRowSessionStore, Verdict: VerdictMerge, Path: entry}
				if err := applyLayoutRow(context.Background(), journal, finding, &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
			}
			transcript := filepath.Join(store, "p", "new.jsonl")
			layoutWrite(t, transcript, "chat data")
			var output bytes.Buffer
			if err := RollbackLayout(
				context.Background(),
				env,
				filepath.Base(journal.dir),
				force,
				&output,
			); err != nil {
				t.Fatalf("rollback: %v; output=%s", err, output.String())
			}
			if data, err := os.ReadFile(transcript); err != nil || string(data) != "chat data" {
				t.Fatalf("transcript=%q err=%v output=%s", data, err, output.String())
			}
			if got := strings.Count(output.String(), "  keep    session store "+store+" "); got != 1 {
				t.Fatalf("keep lines=%d output=%q", got, output.String())
			}
			for index, entry := range accounts {
				if info, err := os.Lstat(entry); err != nil || !info.IsDir() {
					t.Fatalf("account entry %s info=%v err=%v", entry, info, err)
				}
				if _, err := os.Stat(filepath.Join(entry, "p", "old-"+string(rune('a'+index))+".jsonl")); err != nil {
					t.Fatalf("account entry %s lost its own transcript: %v", entry, err)
				}
			}
		})
	}
}

func TestLayoutRollbackUnreadableCreatedStoreFails(t *testing.T) {
	env, id, store, _, _ := createdSessionStoreJournal(t)
	layoutStoreReadDir = func(string) ([]os.DirEntry, error) { return nil, syscall.EACCES }
	t.Cleanup(func() { layoutStoreReadDir = os.ReadDir })
	err := RollbackLayout(context.Background(), env, id, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), store) || !errors.Is(err, syscall.EACCES) {
		t.Fatalf("rollback err=%v", err)
	}
	if _, err := os.Stat(store); err != nil {
		t.Fatalf("store removed: %v", err)
	}
	marker := filepath.Join(env.Home, ".local", "state", "pfm", "migrations", id, layoutRolledBackMarker)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback marker err=%v", err)
	}
}

// A Create or Repoint changes only the seat's link when the shared store
// already exists: journaling the store too would copy every transcript other
// seats hold into the journal and charge it to the space preflight.
func TestLayoutSnapshotPathsJournalsTheSessionStoreOnlyWhenTheRowCreatesIt(t *testing.T) {
	env := layoutFixture(t)
	store := filepath.Join(env.Home, ".claude", "projects")
	link := filepath.Join(env.Config.Accounts[1].ConfigDir, "projects")
	layoutWrite(t, filepath.Join(store, "-work-atlas", "held.jsonl"), "another seat's transcript")
	for _, verdict := range []LayoutVerdict{VerdictCreate, VerdictRepoint} {
		got, err := layoutSnapshotPaths(env, LayoutFinding{Row: layoutRowSessionStore, Verdict: verdict, Path: link})
		if err != nil || len(got) != 1 || got[0] != link {
			t.Fatalf("%s over an existing store journals %q (err %v), want only the link %s", verdict, got, err, link)
		}
	}
	if err := os.RemoveAll(store); err != nil {
		t.Fatal(err)
	}
	got, err := layoutSnapshotPaths(env, LayoutFinding{Row: layoutRowSessionStore, Verdict: VerdictCreate, Path: link})
	if err != nil || len(got) != 2 || got[0] != link || got[1] != store {
		t.Fatalf("create of an absent store journals %q (err %v), want the link and the store it makes", got, err)
	}
}
