package mockengine

import (
	"slices"
	"testing"
)

// TestToolShellArgv pins the argv a Claude tool shell runs a command with under a zsh login shell: a
// fresh, non-interactive zsh sourcing the snapshot, then evaluating the command as one word.
func TestToolShellArgv(t *testing.T) {
	got := toolShellArgv("/usr/bin/zsh", "/c/shell-snapshots/snapshot-zsh-s.sh", "claude --version; cx")
	want := []string{
		"/usr/bin/zsh", "-c", `source "$1" && eval "$2"`, "zsh", "/c/shell-snapshots/snapshot-zsh-s.sh",
		"claude --version; cx",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("toolShellArgv = %q, want %q", got, want)
	}
}
