package installer

// The staged claude shim is a real shell script: it is the subject, so it runs for real.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestClaudeLauncherShimDefaultsConfigDir runs the staged shim with a fake
// pfm that prints the environment it got: an unset CLAUDE_CONFIG_DIR takes the
// login default pfm install wrote, with its sentinel, so the real Claude never
// writes ~/.claude.json; a set value and a host without the default pass through.
func TestClaudeLauncherShimDefaultsConfigDir(t *testing.T) {
	t.Parallel()
	body, err := readAsset("bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, set, want string
		file            bool
	}{
		{"unset", "", "dir=/fixture/acct/1 sentinel=/fixture/acct/1", true},
		{"set", "/fixture/chosen", "dir=/fixture/chosen sentinel=", true},
		{"no-default", "", "dir= sentinel=", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			shim := filepath.Join(home, "shim", "claude")
			for _, dir := range []string{filepath.Dir(shim), filepath.Join(home, ".local", "bin")} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := testjail.WriteExecutable(shim, body, 0o755); err != nil {
				t.Fatal(err)
			}
			fake := "#!/bin/sh\nprintf 'dir=%s sentinel=%s args=%s' \"${CLAUDE_CONFIG_DIR:-}\" \"${PFM_CLAUDE_CONFIG_DIR_DEFAULT:-}\" \"$*\"\n"
			if err := testjail.WriteExecutable(
				filepath.Join(home, ".local", "bin", "pfm"),
				[]byte(fake),
				0o755,
			); err != nil {
				t.Fatal(err)
			}
			if test.file {
				path := loginDefaultEnvironmentPath(home)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(loginDefaultEnvironment("/fixture/acct/1")), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command(shim, "-p", "hi")
			command.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
			if test.set != "" {
				command.Env = append(command.Env, "CLAUDE_CONFIG_DIR="+test.set)
			}
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("shim: %v: %s", err, output)
			}
			if want := test.want + " args=internal claude-launch -p hi"; string(output) != want {
				t.Fatalf("shim env=%q, want %q", output, want)
			}
		})
	}
}
