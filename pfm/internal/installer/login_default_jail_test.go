//go:build jail

package installer

// The login block is sourced by a real sh: the environment it leaves is the subject.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

// The block runs in every zsh and login shell, pfm's own panes included: a
// CLAUDE_CONFIG_DIR already set, even to another account, must survive it.
func TestLoginDefaultBlockLeavesAPresetConfigDirAlone(t *testing.T) {
	home, account, roster := loginDefaultHome(t)
	profile, zshenv, _ := loginDefaultFiles(home)
	runSkillInstall(t, home, ModeApply, roster)
	sentinel := claudelaunch.ConfigDirDefaultEnv
	script := `. "$1" && sh -c 'printf "%s|%s" "${` + claudeConfigDirEnv + `-unset}" "${` + sentinel + `-unset}"'`
	cases := []struct {
		name, preset, want string
		set                bool
	}{
		{name: "unset", want: account + "|" + account},
		{name: "empty", set: true, want: account + "|" + account},
		{name: "another account", preset: "/elsewhere/.cc/2", set: true, want: "/elsewhere/.cc/2|unset"},
	}
	for _, file := range []string{profile, zshenv} {
		for _, test := range cases {
			t.Run(filepath.Base(file)+"/"+test.name, func(t *testing.T) {
				command := exec.CommandContext(context.Background(), "sh", "-c", script, "sh", file)
				command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
				if test.set {
					command.Env = append(command.Env, claudeConfigDirEnv+"="+test.preset)
				}
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("sh %s: %v\n%s", file, err, output)
				}
				if string(output) != test.want {
					t.Fatalf("got %q want %q", output, test.want)
				}
			})
		}
	}
}
