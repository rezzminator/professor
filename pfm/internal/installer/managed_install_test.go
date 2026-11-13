package installer

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestInstallManagedArgsWriteSpacedDestination runs this kernel's argv with
// its real tools (no sudo) into a destination holding a space and a missing
// parent: GNU install -D on Linux, mkdir -p then BSD-portable install on macOS.
func TestInstallManagedArgsWriteSpacedDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.json")
	want := "{\"cleanupPeriodDays\":36500}\n"
	if err := os.WriteFile(source, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "Application Support", "ClaudeCode", "managed-settings.d", "pfm.json")
	for _, args := range managedInstallArgs(source, destination) {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%q: %v\n%s", args, err, out)
		}
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != want || info.Mode().Perm() != 0o644 {
		t.Fatalf("installed %q mode %v (err %v), want %q mode 0644", got, info.Mode().Perm(), err, want)
	}
}
