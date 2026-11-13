package agentlauncher

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

func TestContentExecsThePfmThroughOneSingleQuotedWord(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, pfm, want string }{
		{"plain", "/h/.local/bin/pfm", `exec '/h/.local/bin/pfm' "$@"`},
		{"space", "/h o/.local/bin/pfm", `exec '/h o/.local/bin/pfm' "$@"`},
		{"quote", "/it's/.local/bin/pfm", `exec '/it'"'"'s/.local/bin/pfm' "$@"`},
		{"dollar", "/$HOME/`x`/pfm", "exec '/$HOME/`x`/pfm' \"$@\""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			want := "#!/bin/sh\n" +
				"# Stable launchd entry for pfm's LaunchAgents (written by pfm install, never edited).\n" +
				"# It execs pfm so launchd keeps one pid, while Background Task Management sees\n" +
				"# this unchanging file instead of a pfm binary that is a new identity per build.\n" + test.want + "\n"
			if content := string(Content(test.pfm)); content != want {
				t.Fatalf("launcher = %q, want %q", content, want)
			}
		})
	}
}

func TestEnsureWritesOnceAndLeavesAnEqualLauncherUntouched(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	wrote, err := Ensure(home)
	if err != nil || !wrote {
		t.Fatalf("first Ensure = %v, %v; want it to write", wrote, err)
	}
	first, err := os.Stat(Program(home))
	if err != nil {
		t.Fatal(err)
	}
	if first.Mode().Perm() != Mode {
		t.Fatalf("launcher mode %v, want %v", first.Mode().Perm(), Mode)
	}
	old := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(Program(home), old, old); err != nil {
		t.Fatal(err)
	}
	wrote, err = Ensure(home)
	if err != nil || wrote {
		t.Fatalf("second Ensure = %v, %v; want no write", wrote, err)
	}
	second, err := os.Stat(Program(home))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(first, second) || !second.ModTime().Equal(old) {
		t.Fatalf("an equal launcher was replaced or touched: same inode %v, mtime %v",
			os.SameFile(first, second), second.ModTime())
	}
}

func TestEnsureRepairsADriftedLauncher(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, home string)
	}{
		{"edited", func(t *testing.T, home string) {
			writeFile(t, Program(home), "#!/bin/sh\nexit 0\n", Mode)
		}},
		{"not executable", func(t *testing.T, home string) {
			writeFile(t, Program(home), string(Content(ExecTarget(home))), 0o644)
		}},
		{"a symlink", func(t *testing.T, home string) {
			if err := os.MkdirAll(filepath.Dir(Program(home)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/bin/sh", Program(home)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			test.setup(t, home)
			wrote, err := Ensure(home)
			if err != nil || !wrote {
				t.Fatalf("Ensure = %v, %v; want a repair", wrote, err)
			}
			if current, err := UpToDate(home); err != nil || !current {
				t.Fatalf("UpToDate after repair = %v, %v", current, err)
			}
		})
	}
}

func TestRemoveDeletesTheLauncherAndOnlyItsOwnEmptyDirectory(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		symlink bool
	}{
		{"its own directory", false},
		{"a symlinked pfm directory", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			dir := filepath.Dir(Program(home))
			libexec := filepath.Dir(dir)
			if test.symlink {
				target := filepath.Join(home, "elsewhere")
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(libexec, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, dir); err != nil {
					t.Fatal(err)
				}
			}
			if err := Stage(home); err != nil {
				t.Fatal(err)
			}
			if err := Remove(home); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if _, err := os.Lstat(Program(home)); !os.IsNotExist(err) {
				t.Fatalf("launcher survived Remove: %v", err)
			}
			info, err := os.Lstat(dir)
			if test.symlink != (err == nil && info.Mode()&os.ModeSymlink != 0) {
				t.Fatalf("launcher directory after Remove: %v, %v; want a symlink kept=%v, an own directory gone",
					info, err, test.symlink)
			}
			if _, err := os.Stat(libexec); err != nil {
				t.Fatalf("Remove took the shared libexec directory: %v", err)
			}
			if err := Remove(home); err != nil {
				t.Fatalf("Remove of an absent launcher: %v", err)
			}
		})
	}
}

// writeFile writes a launcher the test only compares and stats: nothing runs it.
func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutableNeverRun(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
