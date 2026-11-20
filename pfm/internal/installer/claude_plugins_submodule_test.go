package installer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninitialisedSubmodule(t *testing.T) {
	const registered = "[submodule \"buddy\"]\n\tpath = plugins/buddy\n\turl = https://example.invalid/buddy.git\n"
	for _, test := range []struct {
		name       string
		gitmodules string
		gitEntry   bool
		want       bool
	}{
		{"registered and empty", registered, false, true},
		{"registered and initialised", registered, true, false},
		{"registered under another name", "[submodule \"other\"]\n\tpath = plugins/other\n", false, false},
		{"registered with a quoted path", "[submodule \"buddy\"]\r\n\tpath = \"plugins/buddy\"\r\n", false, true},
		{"no gitmodules file", "", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			clone := t.TempDir()
			if err := os.MkdirAll(filepath.Join(clone, "plugins", "buddy"), 0o755); err != nil {
				t.Fatal(err)
			}
			if test.gitmodules != "" {
				writeFixture(t, filepath.Join(clone, ".gitmodules"), test.gitmodules)
			}
			if test.gitEntry {
				writeFixture(t, filepath.Join(clone, "plugins", "buddy", ".git"), "gitdir: ../../.git/modules/buddy\n")
			}
			if got := uninitialisedSubmodule(clone, "buddy"); got != test.want {
				t.Fatalf("uninitialisedSubmodule(%s) = %v, want %v", clone, got, test.want)
			}
		})
	}
}
