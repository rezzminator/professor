package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSymlinkOverDirSwapsInTheLinkAndRemovesTheDirectory(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "skill")
	if err := os.MkdirAll(filepath.Join(target, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "nested", "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SymlinkOverDir(target, source); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(target); err != nil || got != source {
		t.Fatalf("readlink = %q, %v; want %q", got, err, source)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 2 {
		t.Fatalf("scratch left behind: %v, %v", entries, err)
	}
}

func TestSymlinkOverDirRefusedDisplacedEntryRollsBack(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	source := filepath.Join(root, "source")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(target, "user.md")
	if err := os.WriteFile(file, []byte("user edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := SymlinkOverDir(target, source, func(displaced string) error {
		body, err := os.ReadFile(filepath.Join(displaced, "user.md"))
		if err != nil {
			return err
		}
		return fmt.Errorf("refuse displaced entry %q", body)
	})
	if err == nil {
		t.Fatal("refused displaced entry was deleted")
	}
	if body, err := os.ReadFile(file); err != nil || string(body) != "user edit" {
		t.Fatalf("user file=%q %v", body, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("staging entries=%v %v", entries, err)
	}
}

func TestSymlinkOverDirRollbackPreservesRacedOperatorEntry(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	source := filepath.Join(root, "source")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "original"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	var preserved string
	planted := false
	err := replaceDirectoryWithSymlink(target, source, func() {
		planted = true
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("operator"), 0o600); err != nil {
			t.Fatal(err)
		}
	}, func(displaced string) error {
		preserved = displaced
		return fmt.Errorf("operator changed displaced entry")
	})
	if !planted {
		t.Fatal("race seam did not plant the operator file")
	}
	if err == nil {
		t.Fatal("expected displaced-entry conflict")
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "operator" {
		t.Fatalf("raced operator entry=%q %v, want operator", body, err)
	}
	if body, err := os.ReadFile(filepath.Join(preserved, "original")); err != nil || string(body) != "original" {
		t.Fatalf("preserved original=%q %v", body, err)
	}
}
