package installer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const loginDefaultTestBegin = "# BEGIN pfm claude-config-dir — installer-owned"

// loginDefaultHome is a roster host whose primary is account 2, so the login
// default proves it names the primary. It returns that dir. No claude runs.
func loginDefaultHome(t *testing.T) (home, account string, roster func(*Options)) {
	t.Helper()
	home = t.TempDir()
	t.Setenv(paths.EnvHome, home)
	first := filepath.Join(home, ".cc", "1")
	account = filepath.Join(home, ".cc", "2")
	roster = func(options *Options) {
		options.ClaudeAccounts = []pfmconfig.Account{{ID: 1, ConfigDir: first}, {ID: 2, ConfigDir: account}}
		options.ClaudeRosterHost = true
		options.PrimaryConfigDir = account
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
	if block != loginDefaultShellBlock(account) {
		t.Fatalf("profile block = %q, want primary dir %s", block, account)
	}
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

func TestLoginDefaultUsesPrimaryWithoutAccountOne(t *testing.T) {
	home, second, roster := loginDefaultHome(t)
	primary := filepath.Join(home, ".cc", "3")
	runSkillInstall(t, home, ModeApply, roster, func(options *Options) {
		options.ClaudeAccounts = []pfmconfig.Account{{ID: 2, ConfigDir: second}, {ID: 3, ConfigDir: primary}}
		options.PrimaryConfigDir = primary
	})
	profile, zshenv, environment := loginDefaultFiles(home)
	for _, path := range []string{profile, zshenv} {
		if got := readSkillFile(t, path); got != loginDefaultShellBlock(primary) {
			t.Fatalf("%s = %q, want primary block", path, got)
		}
	}
	if got := readSkillFile(t, environment); got != loginDefaultEnvironment(primary) {
		t.Fatalf("environment.d = %q, want primary default", got)
	}
}

func TestLoginDefaultRejectsUnsafePrimary(t *testing.T) {
	for _, mode := range []Mode{ModeDryRun, ModeApply} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			home, _, roster := loginDefaultHome(t)
			dir := filepath.Join(home, ".cc", "my dir")
			var output strings.Builder
			options := Options{
				MCPConfigPath: testConfigPath(t), Mode: mode, Home: home,
				Stdout: &output, Runner: &fakeRunner{},
			}
			roster(&options)
			options.ClaudeAccounts = []pfmconfig.Account{{ID: 2, ConfigDir: dir}}
			options.PrimaryConfigDir = dir
			_, err := Run(context.Background(), options)
			want := fmt.Sprintf("login default: account 2 dir %q carries a space, quote, backslash, $ or backtick", dir)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want %q", err, want)
			}
			if mode == ModeDryRun && !strings.Contains(output.String(), "  FAIL    "+want+"\n") {
				t.Errorf("missing failure line %q:\n%s", want, output.String())
			}
			profile, zshenv, environment := loginDefaultFiles(home)
			for _, path := range []string{profile, zshenv, environment} {
				requireNoPath(t, path, "unsafe primary writes no login default")
			}
		})
	}
}

func TestLoginDefaultRemovesDefaultsWithoutRosterOrPrimary(t *testing.T) {
	cases := []struct {
		name, reason string
		primaryLost  bool
		explicit     bool
	}{
		{name: "roster dropped", reason: "no Claude account roster"},
		{name: "primary lost", reason: "no primary Claude account", primaryLost: true},
		{name: "explicit without roster", reason: "no Claude account roster", explicit: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			home, _, roster := loginDefaultHome(t)
			profile, zshenv, environment := loginDefaultFiles(home)
			const own = "export EDITOR=vi\n"
			writeFixture(t, profile, own)
			runSkillInstall(t, home, ModeApply, roster)
			output := runSkillInstall(t, home, ModeApply, roster, func(options *Options) {
				options.PrimaryConfigDir = ""
				if test.primaryLost {
					// The plugin step checks its fallback before the login step.
					options.ConfigDir = options.ClaudeAccounts[1].ConfigDir
				}
				if !test.primaryLost {
					options.ClaudeAccounts = nil
					options.ClaudeRosterHost = false
				}
				if test.explicit {
					options.ConfigDir = filepath.Join(home, "target")
					if err := os.Mkdir(options.ConfigDir, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			})
			if want := "  skip    login default: " + test.reason + "\n"; !strings.Contains(output, want) {
				t.Errorf("missing %q:\n%s", want, output)
			}
			if got := readSkillFile(t, profile); got != own {
				t.Errorf("profile = %q, want %q", got, own)
			}
			requireNoPath(t, zshenv, "lost roster or primary removes its block")
			requireNoPath(t, environment, "lost roster or primary removes environment.d")
		})
	}
}

func TestLoginDefaultFreshHomeWithoutRoster(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	output := runSkillInstall(t, home, ModeApply)
	if want := "  skip    login default: no Claude account roster\n"; !strings.Contains(output, want) {
		t.Fatalf("missing %q:\n%s", want, output)
	}
	profile, zshenv, environment := loginDefaultFiles(home)
	for _, path := range []string{profile, zshenv, environment} {
		requireNoPath(t, path, "no roster creates no login default")
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, "  change  ") && strings.Contains(line, path) {
				t.Errorf("no default was written, but output reports %q", line)
			}
		}
	}
}

func TestLoginDefaultExplicitConfigDirPreservesDefaults(t *testing.T) {
	home, _, roster := loginDefaultHome(t)
	runSkillInstall(t, home, ModeApply, roster)
	profile, zshenv, environment := loginDefaultFiles(home)
	before := map[string]string{}
	for _, path := range []string{profile, zshenv, environment} {
		before[path] = readSkillFile(t, path)
	}
	target := filepath.Join(home, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	output := runSkillInstall(t, home, ModeApply, roster, func(options *Options) {
		options.ClaudeAccounts = nil
		options.PrimaryConfigDir = ""
		options.ConfigDir = target
	})
	if want := "  skip    login default: a --config-dir install leaves it as it is\n"; !strings.Contains(output, want) {
		t.Errorf("missing %q:\n%s", want, output)
	}
	for path, content := range before {
		if got := readSkillFile(t, path); got != content {
			t.Errorf("explicit install changed %s to %q", path, got)
		}
	}
}

func TestLoginDefaultSymlinkedStartupFiles(t *testing.T) {
	cases := []struct {
		name      string
		mode      Mode
		block     bool
		missing   bool
		directory bool
	}{
		{name: "install", mode: ModeApply},
		{name: "install current block", mode: ModeApply, block: true},
		{name: "uninstall block", mode: ModeUninstall, block: true},
		{name: "uninstall without block", mode: ModeUninstall},
		{name: "install missing target", mode: ModeApply, missing: true},
		{name: "uninstall missing target", mode: ModeUninstall, missing: true},
		{name: "install unreadable target", mode: ModeApply, directory: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			home, primary, roster := loginDefaultHome(t)
			profile, zshenv, _ := loginDefaultFiles(home)
			target := filepath.Join(home, "dotfiles", "zshenv")
			content := "export A=1\n"
			if test.block {
				content = loginDefaultShellBlock(primary)
			}
			if test.directory {
				if err := os.MkdirAll(target, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if !test.missing {
				writeFixture(t, target, content)
			}
			if err := os.Symlink(target, zshenv); err != nil {
				t.Fatal(err)
			}
			output := runSkillInstall(t, home, test.mode, roster)
			if got, err := os.Readlink(zshenv); err != nil || got != target {
				t.Errorf("startup link = %q, %v, want %s", got, err, target)
			}
			if test.missing {
				requireNoPath(t, target, "install preserves the missing symlink target")
			} else if test.directory {
				if info, err := os.Stat(target); err != nil || !info.IsDir() {
					t.Errorf("target directory replaced: %v", err)
				}
			} else if got := readSkillFile(t, target); got != content {
				t.Errorf("symlink target = %q, want %q", got, content)
			}
			if !test.missing {
				entries, err := os.ReadDir(filepath.Dir(target))
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 1 || entries[0].Name() != "zshenv" {
					t.Errorf("install wrote beside target: %v", entries)
				}
			}
			prefix := "  skip    login default: " + zshenv + " links to " + target + "; pfm does not write through a link — "
			want := ""
			switch {
			case test.mode == ModeApply && test.block:
				want = "  ok      " + zshenv + " login default\n"
			case test.mode == ModeApply:
				want = prefix + "add this block to " + target + " by hand:\n" + loginDefaultShellBlock(primary)
			case test.block:
				want = prefix + "remove the block between \"" + loginDefaultFenceBegin + "\" and \"" +
					loginDefaultFenceEnd + "\" from " + target + " by hand\n"
			}
			if want != "" && !strings.Contains(output, want) {
				t.Errorf("missing %q:\n%s", want, output)
			}
			if (test.mode == ModeUninstall && !test.block) || (test.mode == ModeApply && test.block) {
				if strings.Contains(output, prefix) {
					t.Errorf("unexpected skip for startup link:\n%s", output)
				}
			}
			if test.mode == ModeApply {
				if got := readSkillFile(t, profile); got != loginDefaultShellBlock(primary) {
					t.Errorf("regular profile = %q, want primary block", got)
				}
			}
		})
	}
}

func TestLoginDefaultSymlinkedDamagedFence(t *testing.T) {
	for _, mode := range []Mode{ModeApply, ModeUninstall} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			home, _, roster := loginDefaultHome(t)
			_, zshenv, _ := loginDefaultFiles(home)
			target := filepath.Join(home, "dotfiles", "zshenv")
			damaged := loginDefaultFenceBegin + "\nexport A=1\n"
			writeFixture(t, target, damaged)
			if err := os.Symlink(target, zshenv); err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			options := Options{
				MCPConfigPath: testConfigPath(t), Mode: mode, Home: home,
				Stdout: &output, Runner: &fakeRunner{},
			}
			roster(&options)
			_, err := Run(context.Background(), options)
			want := "login default: " + zshenv + ": a pfm claude-config-dir begin marker with no end; remove it by hand"
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want %q", err, want)
			}
			if got := readSkillFile(t, target); got != damaged {
				t.Errorf("damaged link target = %q, want %q", got, damaged)
			}
		})
	}
}

func TestLoginDefaultRemovalFailures(t *testing.T) {
	for _, primaryLost := range []bool{false, true} {
		t.Run(fmt.Sprint(primaryLost), func(t *testing.T) {
			home, _, roster := loginDefaultHome(t)
			runSkillInstall(t, home, ModeApply, roster)
			_, zshenv, environment := loginDefaultFiles(home)
			for _, path := range []string{zshenv, environment} {
				if err := os.Rename(path, path+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var output strings.Builder
			options := Options{
				MCPConfigPath: testConfigPath(t), Mode: ModeDryRun, Home: home,
				Stdout: &output, Runner: &fakeRunner{},
			}
			roster(&options)
			options.PrimaryConfigDir = ""
			reason := "no primary Claude account"
			if !primaryLost {
				options.ClaudeAccounts = nil
				options.ClaudeRosterHost = false
				reason = "no Claude account roster"
			}
			_, err := Run(context.Background(), options)
			if err == nil {
				t.Errorf("err = nil, want both removal failures")
			}
			if want := "  skip    login default: " + reason + "\n"; !strings.Contains(output.String(), want) {
				t.Errorf("missing %q:\n%s", want, output.String())
			}
			for _, path := range []string{zshenv, environment} {
				want := "login default: read " + path + ": read " + path + ": is a directory"
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want joined failure %q", err, want)
				}
				if !strings.Contains(output.String(), "  FAIL    "+want+"\n") {
					t.Errorf("missing failure %q:\n%s", want, output.String())
				}
			}
		})
	}
}

func TestLoginDefaultRemovalWriteFailures(t *testing.T) {
	for _, primaryLost := range []bool{false, true} {
		for _, shell := range []bool{false, true} {
			t.Run(fmt.Sprintf("primary lost=%t/shell=%t", primaryLost, shell), func(t *testing.T) {
				home, primary, roster := loginDefaultHome(t)
				profile, zshenv, environment := loginDefaultFiles(home)
				const own = "export EDITOR=vi\n"
				writeFixture(t, profile, own)
				runSkillInstall(t, home, ModeApply, roster)
				path := environment
				match := "  change  remove " + path
				cause := "remove " + path
				if shell {
					path = zshenv
					match = "  change  rewrite " + path + " (backup preserved)"
					cause = "back up " + path + " to " + path + ".pre-professor-19700101-000000"
				}
				writer := &storeMutationWriter{match: match, mutate: func() {
					if err := os.Rename(path, path+".saved"); err != nil {
						t.Fatal(err)
					}
					writeFixture(t, filepath.Join(path, "kept"), "kept\n")
				}}
				options := Options{
					MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home,
					Stdout: writer, Runner: &fakeRunner{}, Now: func() time.Time { return time.Unix(0, 0).UTC() },
				}
				roster(&options)
				options.PrimaryConfigDir = ""
				if primaryLost {
					options.ConfigDir = primary
				} else {
					options.ClaudeAccounts = nil
					options.ClaudeRosterHost = false
				}
				_, err := Run(context.Background(), options)
				if err == nil || !strings.Contains(err.Error(), cause) {
					t.Errorf("err = %v, want removal failure %q", err, cause)
				}
				want := "  FAIL    login default: " + cause + ": remove " + path + ": directory not empty\n"
				if shell {
					want = "  FAIL    login default: " + cause + ": read " + path + ": is a directory\n"
				}
				if !strings.Contains(writer.output.String(), want) {
					t.Errorf("missing failure %q:\n%s", want, writer.output.String())
				}
				if shell {
					requireNoPath(t, environment, "shell removal failure still removes environment.d")
				} else {
					requireNoPath(t, zshenv, "environment removal failure keeps the shell removal")
				}
				if got := readSkillFile(t, profile); got != own {
					t.Errorf("profile = %q, want %q", got, own)
				}
			})
		}
	}
}

func TestSpliceLoginDefaultMarkerErrors(t *testing.T) {
	for _, test := range []struct{ name, content, want string }{
		{
			name: "two ends", content: loginDefaultFenceBegin + "\n" + loginDefaultFenceEnd + "\n" + loginDefaultFenceEnd + "\n",
			want: "two pfm claude-config-dir end markers; remove one by hand",
		},
		{
			name: "end without begin", content: loginDefaultFenceEnd + "\n",
			want: "a pfm claude-config-dir end marker with no begin before it; remove it by hand",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := spliceLoginDefaultBlock(test.content, "")
			if err == nil || err.Error() != test.want {
				t.Errorf("splice error = %v, want %q", err, test.want)
			}
			home, _, roster := loginDefaultHome(t)
			_, zshenv, _ := loginDefaultFiles(home)
			writeFixture(t, zshenv, test.content)
			var output strings.Builder
			options := Options{
				MCPConfigPath: testConfigPath(t), Mode: ModeDryRun, Home: home,
				Stdout: &output, Runner: &fakeRunner{},
			}
			roster(&options)
			_, err = Run(context.Background(), options)
			want := "login default: " + zshenv + ": " + test.want
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("install error = %v, want %q", err, want)
			}
			if !strings.Contains(output.String(), "  FAIL    "+want+"\n") {
				t.Errorf("missing failure %q:\n%s", want, output.String())
			}
			if got := readSkillFile(t, zshenv); got != test.content {
				t.Errorf("damaged fence = %q, want %q", got, test.content)
			}
		})
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
