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
)

func TestJournalWriteOrRestore(t *testing.T) {
	for _, test := range []struct {
		name       string
		failAction bool
		failUndo   bool
	}{
		{name: "applied"},
		{name: "failed action restored", failAction: true},
		{name: "failed restore stays open", failAction: true, failUndo: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			settings := filepath.Join(home, ".claude", "settings.json")
			plugins := filepath.Join(home, ".claude", "plugins")
			if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
				t.Fatal(err)
			}
			before := []byte("{\"operator\":true}\n")
			if err := os.WriteFile(settings, before, 0o600); err != nil {
				t.Fatal(err)
			}
			journal := NewJournal(context.Background(), LayoutEnv{Home: home})
			failure := errors.New("plugin failed")
			err := journal.WriteOrRestore([]string{settings, plugins}, func() error {
				if len(journal.records) != 2 || journal.records[0].Result != layoutRecordPending ||
					journal.records[1].Result != layoutRecordPending {
					t.Fatalf("action ran before snapshots: %+v", journal.records)
				}
				if err := os.WriteFile(settings, []byte("changed"), 0o600); err != nil {
					return err
				}
				if err := os.MkdirAll(plugins, 0o700); err != nil {
					return err
				}
				if test.failUndo {
					if err := os.Remove(journal.records[0].Backup); err != nil {
						return err
					}
				}
				if test.failAction {
					return failure
				}
				return nil
			})
			if test.failAction {
				if !errors.Is(err, failure) {
					t.Fatalf("error=%v, want action failure", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantResult := layoutRecordApplied
			if test.failAction {
				wantResult = layoutRecordRestored
			}
			if test.failUndo {
				wantResult = layoutRecordUnrestored
				if !strings.Contains(err.Error(), "restore") {
					t.Fatalf("error omitted restore failure: %v", err)
				}
			}
			if journal.records[0].Result != wantResult {
				t.Fatalf("settings result=%q, want %q", journal.records[0].Result, wantResult)
			}
			if test.failAction && !test.failUndo {
				got, readErr := os.ReadFile(settings)
				if readErr != nil || !bytes.Equal(got, before) {
					t.Fatalf("settings=%q error=%v, want %q", got, readErr, before)
				}
				if _, statErr := os.Lstat(plugins); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("plugins after restore: %v", statErr)
				}
				if journal.records[1].Result != layoutRecordRestored {
					t.Fatalf("plugins result=%q", journal.records[1].Result)
				}
			}
		})
	}
}

func TestJournalWriteOrRestoreSnapshotFailureStopsAction(t *testing.T) {
	home := t.TempDir()
	journal := NewJournal(context.Background(), LayoutEnv{Home: home})
	loop := filepath.Join(home, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	called := false
	err := journal.WriteOrRestore(
		[]string{filepath.Join(home, "settings.json"), filepath.Join(loop, "plugins")}, func() error {
			called = true
			return nil
		},
	)
	if err == nil || called {
		t.Fatalf("error=%v called=%t, want snapshot failure before action", err, called)
	}
	if len(journal.records) != 1 || journal.records[0].Result != layoutRecordRestored {
		t.Fatalf("snapshot failure left records %+v", journal.records)
	}
}

func TestJournalWriteOrRestoreFirstSnapshotErrorKeepsJournalEmpty(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(home, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := NewJournal(context.Background(), LayoutEnv{Home: home})
	called := false
	err := journal.WriteOrRestore([]string{filepath.Join(root, "settings.json")}, func() error {
		called = true
		return nil
	})
	if err == nil || strings.Contains(err.Error(), "journal.json") || called || journal.Dir() != "" ||
		len(journal.records) != 0 {
		t.Fatalf("first snapshot error=%v called=%t dir=%q records=%+v", err, called, journal.Dir(), journal.records)
	}
}

func TestJournalWriteOrRestoreRefusesAnotherPendingWriter(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, ".claude", "settings.json")
	journal := NewJournal(context.Background(), LayoutEnv{Home: home})
	if err := journal.before(target); err != nil {
		t.Fatal(err)
	}
	called := false
	err := journal.WriteOrRestore([]string{target}, func() error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "pending") || called ||
		journal.records[0].Result != layoutRecordPending {
		t.Fatalf("pending writer err=%v called=%t records=%+v", err, called, journal.records)
	}
}

// A plugin restore that failed leaves its journal open for rollback even
// after a later ordinary write in the same install marks its own records
// applied and the install seals.
func TestJournalFailedRestoreSurvivesLaterWrite(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	hooks := filepath.Join(home, ".codex", "hooks.json")
	for _, path := range []string{settings, hooks} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	journal := NewJournal(context.Background(), LayoutEnv{Home: home})
	err := journal.WriteOrRestore([]string{settings}, func() error {
		if err := os.Remove(journal.records[0].Backup); err != nil {
			return err
		}
		return errors.New("plugin failed")
	})
	if err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("WriteOrRestore() error=%v, want the failed restore named", err)
	}
	if err := journal.Write([]string{hooks}, func() error {
		return os.WriteFile(hooks, []byte("{\"hooks\":{}}\n"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Seal(io.Discard); err != nil {
		t.Fatal(err)
	}
	journals, err := InstallJournals(home)
	if err != nil || len(journals) != 1 || !journals[0].Pending {
		t.Fatalf("InstallJournals()=%+v err=%v, want the unrestored plugin write still open", journals, err)
	}
}
