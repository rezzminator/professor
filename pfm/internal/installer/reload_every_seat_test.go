package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// /reload is linked once in the store commands registry.
func TestReloadCommandLinksIntoTheStore(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	primary := filepath.Join(home, ".claude")
	for _, dir := range []string{primary} {
		if err := os.MkdirAll(filepath.Join(dir, "commands"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	installer := &engine{
		options: Options{
			Mode:      ModeApply,
			Home:      home,
			ConfigDir: primary,
			Stdout:    &output,
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
	for _, dir := range []string{primary} {
		link := filepath.Join(dir, "commands", "reload.md")
		if _, err := os.Lstat(link); err != nil {
			t.Fatalf("seat %s has no /reload: %v", dir, err)
		}
	}
	want := "commands -> " + filepath.Join(primary, "commands") + "\n  change  link " +
		filepath.Join(primary, "commands", "reload.md") + " -> " +
		filepath.Join(installer.managedRoot, "reload.command.md") + "\n\n"
	if output.String() != want {
		t.Fatalf("transcript=%q, want %q", output.String(), want)
	}
}
