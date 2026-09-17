package installer

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// /reload is a global command like every other: it must reach EVERY configured
// Claude seat's commands/, not only the --config-dir one. A seat whose
// commands/ is a real directory (not a symlink to the primary's) had no
// /reload at all — the demo fence's seat 3 was exactly that.
func TestReloadCommandLinksIntoEverySeat(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, ".claude")
	second := filepath.Join(home, ".cc", "2")
	for _, dir := range []string{primary, second} {
		if err := os.MkdirAll(filepath.Join(dir, "commands"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	installer := &engine{
		options: Options{
			Mode:       ModeApply,
			Home:       home,
			ConfigDir:  primary,
			ConfigDirs: []string{primary, second},
			Stdout:     io.Discard,
		},
		apply:       true,
		managedRoot: filepath.Join(home, "managed"),
	}
	if err := os.MkdirAll(installer.managedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(installer.managedRoot, "reload.command.md"),
		[]byte("# reload\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := installer.wireCommands([]assetFile{{path: "reload.command.md", mode: 0o600}}); err != nil {
		t.Fatalf("wireCommands: %v", err)
	}
	for _, dir := range []string{primary, second} {
		link := filepath.Join(dir, "commands", "reload.md")
		if _, err := os.Lstat(link); err != nil {
			t.Fatalf("seat %s has no /reload: %v", dir, err)
		}
	}
}

func TestReloadCommandLinksIntoTheImplicitSeat(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, ".claude")
	second := filepath.Join(home, ".cc", "2")
	third := filepath.Join(home, ".cc", "3")
	for _, dir := range []string{primary, second, third} {
		if err := os.MkdirAll(filepath.Join(dir, "commands"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	installer := &engine{
		options: Options{
			Mode:       ModeApply,
			Home:       home,
			ConfigDir:  primary,
			ConfigDirs: []string{second, third},
			Stdout:     io.Discard,
		},
		apply:       true,
		managedRoot: filepath.Join(home, "managed"),
	}
	if err := os.MkdirAll(installer.managedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(installer.managedRoot, "reload.command.md"),
		[]byte("# reload\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := installer.wireCommands([]assetFile{{path: "reload.command.md", mode: 0o600}}); err != nil {
		t.Fatalf("wireCommands: %v", err)
	}
	for _, dir := range []string{primary, second, third} {
		link := filepath.Join(dir, "commands", "reload.md")
		if _, err := os.Lstat(link); err != nil {
			t.Fatalf("seat %s has no /reload: %v", dir, err)
		}
	}
}
