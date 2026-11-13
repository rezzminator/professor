package testjail

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestEnterJailDirMovesOnlyARunInJailPackage: Run leaves a package in its own directory, RunInJail
// moves it into the jail directory, and a jail directory it cannot enter fails the setup.
func TestEnterJailDirMovesOnlyARunInJailPackage(t *testing.T) {
	jail := t.TempDir()
	start, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		inJail bool
		home   string
		ok     bool
		wd     string
	}{
		{"run stays in the package directory", false, jail, true, start},
		{"run in jail enters the jail directory", true, jail, true, jail},
		{"an absent jail directory fails the setup", true, filepath.Join(jail, "absent"), false, start},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Chdir(start)
			t.Setenv(paths.EnvHome, testCase.home)
			before := startInJail
			startInJail = testCase.inJail
			t.Cleanup(func() { startInJail = before })
			ok := enterJailDir()
			wd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			want, err := filepath.EvalSymlinks(testCase.wd)
			if err != nil {
				t.Fatal(err)
			}
			if ok != testCase.ok || wd != want {
				t.Fatalf("enterJailDir() = %t in %s, want %t in %s", ok, wd, testCase.ok, want)
			}
		})
	}
}
