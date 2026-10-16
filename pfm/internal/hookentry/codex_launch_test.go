package hookentry

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A Codex run that is not the interactive chat of a pane — every subcommand
// that is no session (login, app-server, ...) and the non-interactive turns
// exec and review, which fire the Codex hooks — loses the four Herdr names and
// the tmux pane names, so no reporter it fires finds the parent's pane. An
// interactive launch, a resume included, keeps its whole environment.
func TestCodexLaunchDropsThePaneOfAChildThatIsNoInteractiveChat(t *testing.T) {
	root := testjail.ShortRoot(t)
	binDir := filepath.Join(root, "bin")
	binary := filepath.Join(binDir, "codex")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PATH", binDir)
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/h.sock")
	t.Setenv("HERDR_BIN_PATH", "/usr/local/bin/herdr")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("TMUX_PANE", "%3")
	t.Setenv("PFM_KEEP", "kept")
	previous := LaunchExec
	t.Cleanup(func() { LaunchExec = previous })
	paneNames := []string{"HERDR_ENV", "HERDR_PANE_ID", "HERDR_SOCKET_PATH", "HERDR_BIN_PATH", "TMUX", "TMUX_PANE"}
	for _, test := range []struct {
		name     string
		args     []string
		stripped bool
	}{
		{"interactive", nil, false},
		{"interactive with a prompt", []string{"fix the build"}, false},
		{"interactive resume", []string{"resume", "--last"}, false},
		{"interactive fork", []string{"fork", "--last"}, false},
		{"interactive in a directory named like a subcommand", []string{"--cd", "sandbox"}, false},
		{"interactive on a model named like a subcommand", []string{"-m", "review", "fix it"}, false},
		{"exec after options", []string{"-c", "model=o3", "exec", "say hi"}, true},
		{"app-server", []string{"app-server"}, true},
		{"login", []string{"login"}, true},
		{"mcp", []string{"mcp", "list"}, true},
		{"version", []string{"--version"}, true},
		{"help flag", []string{"-h"}, true},
		{"exec", []string{"exec", "say hi"}, true},
		{"exec resume", []string{"exec", "resume", "--last"}, true},
		{"review", []string{"review", "--uncommitted"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gotEnv []string
			LaunchExec = func(_ string, _, environment []string) error {
				gotEnv = append([]string(nil), environment...)
				return nil
			}
			runtime := config.Runtime{Paths: paths.Values{
				Home:    root,
				StateDB: filepath.Join(root, "state", "pfm.db"),
				SIDDir:  filepath.Join(root, "sid"),
				TmuxDir: filepath.Join(root, "tmux"),
			}}
			var stderr bytes.Buffer
			if code := CodexLaunch(test.args, &stderr, runtime); code != 0 {
				t.Fatalf("CodexLaunch code=%d stderr=%q", code, stderr.String())
			}
			if gotEnv == nil {
				t.Fatalf("CodexLaunch never reached exec")
			}
			names := make([]string, 0, len(gotEnv))
			for _, entry := range gotEnv {
				name, _, _ := strings.Cut(entry, "=")
				names = append(names, name)
			}
			for _, name := range paneNames {
				if present := slices.Contains(names, name); present == test.stripped {
					t.Errorf("%s present=%t in the exec env, want %t", name, present, !test.stripped)
				}
			}
			if !slices.Contains(names, "PFM_KEEP") {
				t.Errorf("an unrelated variable left the exec env: %q", gotEnv)
			}
		})
	}
}
