package installer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

func herdrShellInstaller(t *testing.T, original string, present bool) (*engine, string, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, ".config", "herdr", "config.toml")
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &deps.FakeRunner{}
	scriptLoginShell(runner, "/bin/zsh", nil)
	if present {
		runner.ScriptLookPath("herdr", "/fixture/herdr", nil)
	} else {
		runner.ScriptLookPath("herdr", "", exec.ErrNotFound)
	}
	output := &bytes.Buffer{}
	installer := &engine{
		options: Options{
			Home:          home,
			Env:           &paths.MapEnv{Values: map[string]string{"SHELL": "/bin/zsh"}},
			Stdout:        output,
			ProcessRunner: runner,
		},
		apply:       true,
		stamp:       "test",
		managedRoot: managedRootForHome(home),
	}
	return installer, config, output
}

// Both platform lookups use the injected process runner, never the host DB.
func scriptLoginShell(runner *deps.FakeRunner, shell string, err error) {
	uid := strconv.Itoa(os.Getuid())
	runner.Script([]string{"getent", "passwd", uid}, deps.RunResult{
		Stdout: []byte("fixture:x:" + uid + ":20:Fixture:/fixture:" + shell + "\n"),
	}, err)
	runner.Script([]string{"dscl", "/Search", "-search", "/Users", "UniqueID", uid}, deps.RunResult{
		Stdout: []byte("fixture\t\tUniqueID = (\n    " + uid + "\n)\n"),
	}, err)
	runner.Script([]string{"dscl", "/Search", "-read", path.Join("/Users", "fixture"), "UserShell"}, deps.RunResult{
		Stdout: []byte("UserShell: " + shell + "\n"),
	}, err)
}

func TestHerdrShellUsesLoginShell(t *testing.T) {
	installer, _, _ := herdrShellInstaller(t, "", true)
	installer.options.Env = &paths.MapEnv{Values: map[string]string{"SHELL": "/opt/homebrew/bin/bash"}}
	if err := installer.wireHerdrShell(false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(paths.HerdrShellWrapper(installer.options.Home))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/herdr-login-shell.golden")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(line, "pfm_real_shell=") {
			if line+"\n" != string(want) {
				t.Fatalf("captured shell = %q, want %q", line, strings.TrimSpace(string(want)))
			}
			return
		}
	}
	t.Fatal("wrapper has no captured real shell")
}

func TestHerdrShellInspectionDetectsAndRepairsExecutableDrift(t *testing.T) {
	for _, corrupt := range []string{"#!/bin/sh\nexit 1\n", "append", "replace"} {
		t.Run(corrupt, func(t *testing.T) {
			installer, config, _ := herdrShellInstaller(t, "", true)
			if err := installer.wireHerdrShell(false); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			wrapper := paths.HerdrShellWrapper(installer.options.Home)
			raw, err := os.ReadFile(wrapper)
			if err != nil {
				t.Fatal(err)
			}
			broken := corrupt
			switch corrupt {
			case "append":
				broken = string(raw) + "# stale extra content\n"
			case "replace":
				broken = strings.Replace(string(raw), "unset PFM_AUTO_OPEN", "#unset PFM_AUTO_OPEN", 1)
			}
			if err := testjail.WriteExecutableNeverRun(wrapper, []byte(broken), 0o755); err != nil {
				t.Fatal(err)
			}
			status, err := InspectHerdrShell(installer.options.Home, installer.env())
			if err != nil || !status.Owned || status.Wired {
				t.Errorf("corrupt executable wrapper reported healthy: status=%+v error=%v", status, err)
			}
			if err := installer.wireHerdrShell(false); err != nil {
				t.Fatal(err)
			}
			status, err = InspectHerdrShell(installer.options.Home, installer.env())
			if err != nil || !status.Owned || !status.Wired {
				t.Fatalf("repaired wrapper not healthy: status=%+v error=%v", status, err)
			}
			after, err := os.ReadFile(config)
			if err != nil || !bytes.Equal(after, before) {
				t.Fatalf("health repair rewrote config: %s error=%v", after, err)
			}
		})
	}
}

func TestHerdrShellRefreshesOwnedWrapper(t *testing.T) {
	installer, config, _ := herdrShellInstaller(t, "", true)
	if err := installer.wireHerdrShell(false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	wrapper := paths.HerdrShellWrapper(installer.options.Home)
	raw, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(string(raw), "pfm_real_shell='/bin/zsh'", "pfm_real_shell='/bin/sh'", 1)
	if stale == string(raw) {
		t.Fatal("fixture has no captured shell to replace")
	}
	if err := testjail.WriteExecutableNeverRun(wrapper, []byte(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	installer.options.Env = &paths.MapEnv{Values: map[string]string{"SHELL": "/bin/sh"}}
	if err := installer.wireHerdrShell(false); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(wrapper)
	if err != nil || !bytes.Equal(after, raw) {
		t.Fatalf("owned wrapper not refreshed: %s; error=%v", after, err)
	}
	after, err = os.ReadFile(config)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("refresh changed config: %s; error=%v", after, err)
	}
}

func TestHerdrShellInstall(t *testing.T) {
	for _, original := range []string{
		"",
		"# mine\n[terminal]\nshell_mode = \"non_login\"\n[keys]\nnew_tab = \"prefix+t\"\n",
		"[theme]\nname = \"dark\"\n",
		"[terminal]\r\nshell_mode = 'non_login'\r\n",
		"note = '''\n[terminal]\n'''\n[terminal]\nshell_mode = 'non_login'\n",
		"note = '''\n[terminal]\n'''\n",
	} {
		t.Run(original, func(t *testing.T) {
			installer, config, _ := herdrShellInstaller(t, original, true)
			if err := installer.wireHerdrShell(false); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			var parsed struct {
				Terminal struct {
					DefaultShell string `toml:"default_shell"`
				}
			}
			if _, err := toml.Decode(string(raw), &parsed); err != nil {
				t.Fatal(err)
			}
			wrapper := filepath.Join(installer.managedRoot, "bin", "pfm-herdr-shell")
			if parsed.Terminal.DefaultShell != wrapper {
				t.Fatalf("default_shell = %q, want %q; config=%s", parsed.Terminal.DefaultShell, wrapper, raw)
			}
			before, err := os.Stat(config)
			if err != nil {
				t.Fatal(err)
			}
			changed := installer.report.Changed
			if err := installer.wireHerdrShell(false); err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(config)
			if err != nil {
				t.Fatal(err)
			}
			if installer.report.Changed != changed || !os.SameFile(before, after) {
				t.Fatal("second install rewrote wiring")
			}
			if err := installer.wireHerdrShell(true); err != nil {
				t.Fatal(err)
			}
			raw, err = os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "default_shell") {
				t.Fatalf("uninstall left wiring: %s", raw)
			}
			if _, err := os.Stat(wrapper); !os.IsNotExist(err) {
				t.Fatalf("uninstall wrapper: %v", err)
			}
		})
	}
}

func TestHerdrShellUserOwnedAndAbsent(t *testing.T) {
	for _, present := range []bool{true, false} {
		original := "[terminal]\ndefault_shell = '/bin/fish'\n[keys]\nnew_tab = 'prefix+t'\n"
		installer, config, output := herdrShellInstaller(t, original, present)
		if err := installer.wireHerdrShell(false); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != original {
			t.Fatalf("changed personal config: %s", raw)
		}
		want := "user-owned default_shell=\"/bin/fish\""
		if !present {
			want = "herdr absent"
		}
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing skip %q: %s", want, output)
		}
		// A user-owned default_shell may exec pfm's wrapper as its own inner shell, so the
		// wrapper is staged even though the config stays the user's; with Herdr absent nothing is.
		wrapper := filepath.Join(installer.managedRoot, "bin", "pfm-herdr-shell")
		staged, err := os.ReadFile(wrapper)
		if !present {
			if !os.IsNotExist(err) {
				t.Fatalf("Herdr absent staged %s: %v", wrapper, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("user-owned default_shell left no wrapper at %s: %v", wrapper, err)
		}
		for _, line := range []string{"pfm_wrapper='" + wrapper + "'\n", "pfm_real_shell='/bin/zsh'\n"} {
			if !strings.Contains(string(staged), line) {
				t.Fatalf("wrapper %s lacks %q:\n%s", wrapper, line, staged)
			}
		}
	}
}

func TestHerdrShellRefusesDamagedConfigAndPreservesEditedOwnership(t *testing.T) {
	installer, _, _ := herdrShellInstaller(t, "[terminal", true)
	if err := installer.wireHerdrShell(false); err == nil {
		t.Fatal("malformed config treated as absence")
	}
	installer, _, output := herdrShellInstaller(t, "[terminal]\ndefault_shell = ''\n", true)
	if err := installer.wireHerdrShell(false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `user-owned default_shell=""`) {
		t.Fatalf("empty user setting not owned: %s", output)
	}
	installer, config, _ := herdrShellInstaller(t, "", true)
	if err := installer.wireHerdrShell(false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), paths.HerdrShellWrapper(installer.options.Home), "/bin/fish", 1)
	if err := os.WriteFile(config, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installer.wireHerdrShell(true); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != edited {
		t.Fatalf("uninstall overwrote edited ownership: %s", after)
	}
	if err := installer.writeHerdrShellConfig(config, nil, []byte("replacement"), false); err == nil {
		t.Fatal("concurrent user edit was not refused")
	}
}

func TestHerdrShellPreservesSymlinkAndMode(t *testing.T) {
	original := "[terminal]\nshell_mode = 'non_login'\n"
	installer, config, _ := herdrShellInstaller(t, original, true)
	target := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Rename(config, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, config); err != nil {
		t.Fatal(err)
	}
	for _, uninstall := range []bool{false, true} {
		if err := installer.wireHerdrShell(uninstall); err != nil {
			t.Fatal(err)
		}
		link, err := os.Readlink(config)
		if err != nil || link != target {
			t.Fatalf("config symlink lost: target=%q err=%v", link, err)
		}
		info, err := os.Stat(target)
		if err != nil || info.Mode().Perm() != 0o640 {
			t.Fatalf("target mode lost: info=%v err=%v", info, err)
		}
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != original {
		t.Fatalf("target not restored: %s err=%v", raw, err)
	}
	backup, err := os.ReadFile(target + ".pre-professor-test")
	if err != nil || string(backup) != original {
		t.Fatalf("original backup not preserved: %s err=%v", backup, err)
	}
}

func TestHerdrShellMarkersInsideStringAreNotOwnership(t *testing.T) {
	installer, config, _ := herdrShellInstaller(t, "", true)
	wrapper := paths.HerdrShellWrapper(installer.options.Home)
	block := herdrShellOpen + fmt.Sprintf("default_shell = %q\n", wrapper) + herdrShellClose
	original := "note = '''\n" + block + "'''\n[terminal]\ndefault_shell = " + fmt.Sprintf("%q\n", wrapper)
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := InspectHerdrShell(installer.options.Home, installer.env())
	if err != nil || status.Owned {
		t.Fatalf("literal marker claimed user key: %+v err=%v", status, err)
	}
	for _, uninstall := range []bool{false, true} {
		if err := installer.wireHerdrShell(uninstall); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(config)
	if err != nil || string(raw) != original {
		t.Fatalf("literal marker or user key changed: %s err=%v", raw, err)
	}
}

func TestHerdrShellHoldsThemeLockWhileWriting(t *testing.T) {
	installer, _, _ := herdrShellInstaller(t, "", true)
	checked := false
	installer.options.Stdout = &storeMutationWriter{match: "wire herdr shell", mutate: func() {
		checked = true
		lock := filepath.Join(themesync.DefaultStateDir(installer.options.Home), "pfm-theme.lock")
		file, err := os.OpenFile(lock, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		}()
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !errors.Is(err, syscall.EWOULDBLOCK) {
			t.Fatalf("theme writer is not excluded during config publication: %v", err)
		}
	}}
	if err := installer.wireHerdrShell(false); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("config publication was not observed")
	}
	// The same lock is released on return, so theme sync can proceed.
	release, err := themesync.LockState(installer.options.Home)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestHerdrShellLoginLookupFallback(t *testing.T) {
	for _, name := range []string{
		"executable", "unset", "missing", "nonexecutable", "directory", "relative", "wrapper", "wrapper symlink", "invalid login",
	} {
		t.Run(name, func(t *testing.T) {
			installer, _, output := herdrShellInstaller(t, "", true)
			wrapper := paths.HerdrShellWrapper(installer.options.Home)
			fallback := ""
			want := "/bin/sh"
			login, lookupErr := "/bin/zsh", errors.New("account database unavailable")
			switch name {
			case "executable":
				fallback = filepath.Join(t.TempDir(), "bash")
				writeExecutable(t, fallback)
				want = fallback
			case "missing":
				fallback = filepath.Join(t.TempDir(), "gone")
			case "nonexecutable":
				fallback = filepath.Join(t.TempDir(), "shell")
				if err := os.WriteFile(fallback, []byte("not executable"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				fallback = t.TempDir()
			case "relative":
				fallback = "bash"
			case "wrapper", "wrapper symlink":
				if err := os.MkdirAll(filepath.Dir(wrapper), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := testjail.WriteExecutableNeverRun(wrapper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				fallback = wrapper
				if name == "wrapper symlink" {
					fallback = filepath.Join(t.TempDir(), "shell")
					if err := os.Symlink(wrapper, fallback); err != nil {
						t.Fatal(err)
					}
				}
			case "invalid login":
				login, lookupErr = filepath.Join(t.TempDir(), "gone"), nil
			}
			runner := &deps.FakeRunner{}
			runner.ScriptLookPath("herdr", "/fixture/herdr", nil)
			scriptLoginShell(runner, login, lookupErr)
			installer.options.ProcessRunner = runner
			installer.options.Env = &paths.MapEnv{Values: map[string]string{"SHELL": fallback}}
			if err := installer.wireHerdrShell(false); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(wrapper)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), "pfm_real_shell='"+want+"'\n") {
				t.Fatalf("wrong fallback, want %q: %s", want, raw)
			}
			if !strings.Contains(output.String(), "  warn    herdr shell: login shell lookup:") ||
				!strings.Contains(output.String(), "; using "+want) {
				t.Fatalf("lookup failure not visible: %s", output)
			}
			if lookupErr != nil && !strings.Contains(output.String(), lookupErr.Error()) {
				t.Fatalf("lookup error lost: %s", output)
			}
		})
	}
}

// lockedBuffer collects a running command's output for a concurrent reader.
type lockedBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (output *lockedBuffer) Write(p []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.data.Write(p)
}

func (output *lockedBuffer) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.data.String()
}
