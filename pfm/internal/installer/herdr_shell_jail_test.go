package installer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHerdrShellPickerReturnsToShell(t *testing.T) {
	installer, _, _ := herdrShellInstaller(t, "", true)
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Fatal(err)
	}
	installer.options.Env = &paths.MapEnv{Values: map[string]string{"SHELL": zsh}}
	if err := installer.wireHerdrShell(false); err != nil {
		t.Fatal(err)
	}
	home := installer.options.Home
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeScript(t, bin, "pfm", "#!/bin/sh\nif [ $# = 0 ]; then printf 'picker\\n' >> \"$HOME/result\"; fi\n")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	const ready = "herdr-ready> "
	rc := "source '" + filepath.Join(cwd, "assets", "shim", "pfm.zsh") + "'\nPS1='" + ready + "'\n"
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(rc), 0o600); err != nil {
		t.Fatal(err)
	}
	// The system zshrc's compinit is the host's, not the shim's: where an fpath
	// directory is writable by others (GitHub's hosted runner), its
	// insecure-directories question would hold the first prompt.
	if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte("skip_global_compinit=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := paths.HerdrShellWrapper(home)
	command, err := testjail.PTYCommand("env", "SHELL="+wrapper, wrapper)
	if err != nil {
		t.Fatal(err)
	}
	command.Env = append(
		os.Environ(),
		"HOME="+home,
		"ZDOTDIR="+home,
		"SHELL=/bin/sh",
		"PFM_HERDR_SHELL=",
		"HERDR_ENV=0",
	)
	// Typed at the first prompt, as a person types: input queued before the
	// shell starts is not delivered alike by util-linux and BSD script.
	stdin, typed, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := typed.Close(); err != nil {
			t.Errorf("close typed input: %v", err)
		}
	})
	screen := &lockedBuffer{}
	command.Stdin, command.Stdout, command.Stderr = stdin, screen, screen
	if err := command.Start(); err != nil {
		t.Fatalf("start interactive wrapper: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("close the child's end of typed input: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	deadline := time.After(15 * time.Second)
	for !strings.Contains(screen.String(), ready) {
		select {
		case err := <-exited:
			t.Fatalf("interactive wrapper ended before its first prompt: %v: %s", err, screen.String())
		case <-deadline:
			_ = command.Process.Kill()
			t.Fatalf("interactive wrapper never drew prompt %q: %s", ready, screen.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if _, err := typed.WriteString("print -r -- plain:${PFM_AUTO_OPEN-unset} >> \"$HOME/result\"\nexit\n"); err != nil {
		t.Fatalf("type into interactive wrapper: %v", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("interactive wrapper: %v: %s", err, screen.String())
		}
	case <-deadline:
		_ = command.Process.Kill()
		t.Fatalf("interactive wrapper never exited: %s", screen.String())
	}
	result, err := os.ReadFile(filepath.Join(home, "result"))
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != "picker\nplain:unset\n" {
		t.Fatalf("picker did not return to disarmed shell: %s", result)
	}
}

func TestHerdrShellWrapper(t *testing.T) {
	raw, err := readAsset("shim/pfm-herdr-shell.sh")
	if err != nil {
		t.Fatalf("wrapper asset: %v", err)
	}
	home := t.TempDir()
	shell := writeScript(
		t,
		home,
		"zsh",
		"#!/bin/sh\nprintf 'auto=%s shell=%s args=' \"${PFM_AUTO_OPEN-unset}\" \"$SHELL\"\nprintf '<%s>' \"$@\"\nprintf '\\n'\n",
	)
	wrapper := filepath.Join(home, "pfm-herdr-shell")
	rendered := strings.NewReplacer("__PFM_REAL_SHELL__", "'"+shell+"'", "__PFM_WRAPPER__", "'"+wrapper+"'").
		Replace(string(raw))
	if err := testjail.WriteExecutable(wrapper, []byte(rendered), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	pfm := writeScript(t, bin, "pfm", "#!/bin/sh\nexit 0\n")
	for _, tc := range []struct {
		name                        string
		tty, login, nested, missing bool
		args                        []string
		want                        string
		pane, socket                string
		otherShell                  bool
	}{
		{name: "interactive", tty: true, want: "auto=pfm"},
		{name: "login", tty: true, login: true, want: "args=<-l>"},
		{name: "noninteractive", want: "auto=unset"},
		{name: "command", tty: true, args: []string{"-lc", "echo literal words"}, want: "auto=unset"},
		{name: "script", tty: true, args: []string{"script.sh"}, want: "auto=unset"},
		{name: "nested", tty: true, nested: true, want: "auto=unset"},
		{name: "new pane inherits old guard", tty: true, nested: true, pane: "pane-new", want: "auto=pfm"},
		{name: "other server reuses pane", tty: true, nested: true, socket: "new.sock", want: "auto=pfm"},
		{name: "other shell", tty: true, otherShell: true, want: "auto=unset"},
		{name: "missing", tty: true, missing: true, want: "auto=unset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.missing {
				if err := os.Remove(pfm); err != nil {
					t.Fatal(err)
				}
			}
			realShell := shell
			if tc.otherShell {
				content, readErr := os.ReadFile(shell)
				if readErr != nil {
					t.Fatal(readErr)
				}
				realShell = writeScript(t, home, "bash", string(content))
			}
			if tc.login {
				realShell = wrapper
			}
			cmd := exec.Command("env", append([]string{"SHELL=" + realShell, wrapper}, tc.args...)...)
			if tc.tty {
				var err error
				cmd, err = testjail.PTYCommand(append([]string{"env", "SHELL=" + realShell, wrapper}, tc.args...)...)
				if err != nil {
					t.Fatal(err)
				}
			}
			cmd.Env = append(
				os.Environ(),
				"HOME="+home,
				"SHELL=/bin/sh",
				"PFM_AUTO_OPEN=pfm",
				"PFM_HERDR_SHELL=",
				"HERDR_SOCKET_PATH="+tc.socket,
				"HERDR_PANE_ID="+tc.pane,
			)
			if tc.nested {
				cmd.Env = append(cmd.Env, "PFM_HERDR_SHELL=:1")
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("wrapper: %v: %s", err, out)
			}
			wantShell := realShell
			if tc.login {
				wantShell = shell
			}
			if !strings.Contains(string(out), tc.want) || !strings.Contains(string(out), "shell="+wantShell) {
				t.Fatalf("output=%s, want %s and real SHELL", out, tc.want)
			}
			if len(tc.args) > 0 && !strings.Contains(string(out), "<"+tc.args[len(tc.args)-1]+">") {
				t.Fatalf("argv lost: %s", out)
			}
		})
	}
}

// The wrapper runs for real: its fallback to /bin/sh for a vanished captured shell is the subject.
func TestHerdrShellMissingCapturedShellFallsBack(t *testing.T) {
	installer, _, _ := herdrShellInstaller(t, "", true)
	shell := writeScript(t, t.TempDir(), "zsh", "#!/bin/sh\nexit 0\n")
	runner := &deps.FakeRunner{}
	runner.ScriptLookPath("herdr", "/fixture/herdr", nil)
	scriptLoginShell(runner, shell, nil)
	installer.options.ProcessRunner = runner
	if err := installer.wireHerdrShell(false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(shell); err != nil {
		t.Fatal(err)
	}
	wrapper := paths.HerdrShellWrapper(installer.options.Home)
	cmd := exec.Command(wrapper, "-c", "printf 'shell=%s auto=%s' \"$SHELL\" \"${PFM_AUTO_OPEN-unset}\"")
	cmd.Env = append(os.Environ(), "SHELL="+wrapper, "PFM_AUTO_OPEN=pfm")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "not executable; using /bin/sh") ||
		!strings.Contains(string(out), "shell=/bin/sh auto=unset") {
		t.Fatalf("missing shell did not leave a usable fallback: %s err=%v", out, err)
	}
}
