package installer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const loginDefaultTestBegin = "# BEGIN pfm claude-config-dir — installer-owned"

// loginDefaultHome is a roster host whose primary is account 2, so the login
// default proves it names account 1 and not the primary. No claude runs.
func loginDefaultHome(t *testing.T) (home, account string, roster func(*Options)) {
	t.Helper()
	home = t.TempDir()
	t.Setenv(paths.EnvHome, home)
	account = filepath.Join(home, ".cc", "1")
	second := filepath.Join(home, ".cc", "2")
	roster = func(options *Options) {
		options.ClaudeAccounts = []pfmconfig.Account{{ID: 1, ConfigDir: account}, {ID: 2, ConfigDir: second}}
		options.PrimaryConfigDir = second
		options.ClaudeBinary = "no-such-claude"
		options.ProcessRunner = &pluginRunner{}
	}
	return home, account, roster
}

func loginDefaultFiles(home string) (profile, zshenv, environment string) {
	return filepath.Join(home, ".profile"),
		filepath.Join(home, ".zshenv"),
		filepath.Join(home, ".config", "environment.d", "pfm-claude-config-dir.conf")
}

func TestInstallWritesTheLoginDefaultOnceAndUninstallRemovesIt(t *testing.T) {
	home, account, roster := loginDefaultHome(t)
	profile, zshenv, environment := loginDefaultFiles(home)
	const own = "export EDITOR=vi\n"
	writeFixture(t, profile, own)

	preview := runSkillInstall(t, home, ModeDryRun, roster)
	for _, path := range []string{zshenv, environment} {
		requireNoPath(t, path, "preview")
		if !strings.Contains(preview, "  change  create "+path+"\n") {
			t.Fatalf("preview names no create of %s:\n%s", path, preview)
		}
	}
	if !strings.Contains(preview, "  change  rewrite "+profile+" (backup preserved)\n") {
		t.Fatalf("preview names no rewrite of %s:\n%s", profile, preview)
	}
	if got := readSkillFile(t, profile); got != own {
		t.Fatalf("preview wrote %s: %q", profile, got)
	}

	runSkillInstall(t, home, ModeApply, roster)
	got := readSkillFile(t, profile)
	if !strings.HasPrefix(got, own+loginDefaultTestBegin+"\n") {
		t.Fatalf("%s lost its own content or the block: %q", profile, got)
	}
	block := strings.TrimPrefix(got, own)
	if readSkillFile(t, zshenv) != block {
		t.Fatalf("%s is not the block alone: %q", zshenv, readSkillFile(t, zshenv))
	}
	if !strings.HasSuffix(block, "# END pfm claude-config-dir — installer-owned\n") {
		t.Fatalf("block has no end marker: %q", block)
	}
	lines := strings.Split(readSkillFile(t, environment), "\n")
	for _, want := range []string{
		claudeConfigDirEnv + "=${" + claudeConfigDirEnv + ":-" + account + "}",
		claudelaunch.ConfigDirDefaultEnv + "=" + account,
	} {
		if !containsLine(lines, want) {
			t.Fatalf("%s lacks %q: %q", environment, want, lines)
		}
	}

	before := map[string]string{}
	for _, path := range []string{profile, zshenv, environment} {
		before[path] = readSkillFile(t, path)
	}
	second := runSkillInstall(t, home, ModeApply, roster)
	for path, content := range before {
		if readSkillFile(t, path) != content {
			t.Fatalf("second install changed %s", path)
		}
		for _, line := range strings.Split(second, "\n") {
			if strings.HasPrefix(line, "  change  ") && strings.Contains(line, path) {
				t.Fatalf("second install reports %q", line)
			}
		}
	}

	runSkillInstall(t, home, ModeUninstall, roster)
	if got := readSkillFile(t, profile); got != own {
		t.Fatalf("uninstall left %s as %q, want %q", profile, got, own)
	}
	requireNoPath(t, zshenv, "uninstall removes the block it created")
	requireNoPath(t, environment, "uninstall removes the environment.d file")
}

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

// A block with a begin marker and no end is not pfm's to guess at: install
// fails naming the file and leaves every byte of it in place.
func TestInstallRefusesADamagedLoginDefaultFence(t *testing.T) {
	home, _, roster := loginDefaultHome(t)
	_, zshenv, _ := loginDefaultFiles(home)
	damaged := "export A=1\n" + loginDefaultTestBegin + "\nexport B=2\n"
	writeFixture(t, zshenv, damaged)
	var output strings.Builder
	options := Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
	}
	roster(&options)
	_, err := Run(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), zshenv) {
		t.Fatalf("err=%v, want a failure naming %s\n%s", err, zshenv, output.String())
	}
	if got := readSkillFile(t, zshenv); got != damaged {
		t.Fatalf("%s rewritten: %q", zshenv, got)
	}
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}
