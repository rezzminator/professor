package installer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepointPreservesOperatorAtRenameBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.Symlink("old", path); err != nil {
		t.Fatal(err)
	}
	previous := accountLinkRename
	t.Cleanup(func() { accountLinkRename = previous })
	accountLinkRename = func(from, to string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("operator bytes"), 0o600); err != nil {
			return err
		}
		return previous(from, to)
	}
	err := (&engine{}).repointAccountLink("new", path, "old")
	if err == nil {
		t.Fatal("operator replacement at rename boundary was silently replaced")
	}
	assertContent(t, path, "operator bytes")
}

func TestRepointReportsDisplacedEntryOnRestoreConflictAndFailure(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore-conflict", true: "move-failure"}[partial], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "CLAUDE.md")
			if err := os.Symlink("old", path); err != nil {
				t.Fatal(err)
			}
			previous := accountLinkRename
			t.Cleanup(func() { accountLinkRename = previous })
			displaced := ""
			accountLinkRename = func(from, to string) error {
				if err := os.Remove(path); err != nil {
					return err
				}
				writeFixture(t, path, "displaced operator bytes")
				if err := previous(from, to); err != nil {
					return err
				}
				displaced = to
				if partial {
					return errors.New("interrupted move")
				}
				writeFixture(t, path, "newer operator bytes")
				return nil
			}
			err := (&engine{}).repointAccountLink("new", path, "old")
			if err == nil || !strings.Contains(err.Error(), displaced) {
				t.Fatalf("displaced recovery location not reported: %v", err)
			}
			assertContent(t, displaced, "displaced operator bytes")
			if !partial {
				assertContent(t, path, "newer operator bytes")
			}
		})
	}
}
