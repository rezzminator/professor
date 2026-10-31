package testjail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScrubOperatorPATH(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	otherInstall := filepath.Join(root, "other", ".local", "share", "pfm", "install", "bin")
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherInstall, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	join := func(entries ...string) string { return strings.Join(entries, string(os.PathListSeparator)) }
	homeBin := filepath.Join(home, ".local", "bin")
	for _, test := range []struct {
		name, pathEnv, home, want string
	}{
		{"home bin removed", join("/usr/bin", homeBin, "/bin"), home, join("/usr/bin", "/bin")},
		{"unclean spelling removed", join(home+"/.local/./bin/", "/usr/bin"), home, "/usr/bin"},
		{"symlinked spelling removed", join(filepath.Join(link, ".local", "bin"), "/usr/bin"), home, "/usr/bin"},
		{"install bin under another home removed", join("/usr/bin", otherInstall), home, "/usr/bin"},
		{"empty entries and order kept", join("/b", "", homeBin, "/a", ""), home, join("/b", "", "/a", "")},
		{"no home still strips install bin", join(homeBin, otherInstall, "/usr/bin"), "", join(homeBin, "/usr/bin")},
		{"empty PATH stays empty", "", home, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := scrubOperatorPATH(test.pathEnv, test.home); got != test.want {
				t.Fatalf("scrubOperatorPATH(%q, %q)=%q, want %q", test.pathEnv, test.home, got, test.want)
			}
		})
	}
}
