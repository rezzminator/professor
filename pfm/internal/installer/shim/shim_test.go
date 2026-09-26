// Package shim is a TEST HOME, not a source directory: it holds no shim of its
// own. Tests always read the installer asset embedded into the pfm binary.
package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// jailedZshCommand builds the zsh invocation these tests run their scripts in.
//
// Two things make it a JAIL rather than merely "zsh with a different HOME".
// `-f` skips every user rc file, and ZDOTDIR is dropped from the environment.
// Both are needed: zsh resolves .zshenv through $ZDOTDIR, which defaults to
// $HOME but is INHERITED when it is already set, so overriding HOME alone
// leaves the developer's OWN .zshenv sourced into the test while $HOME points
// at the temp dir. An rc line that is fine on the real home but fails under
// the temp one — a `. "$HOME/.cargo/env"` that is not there — then fails the
// test for a reason that has nothing to do with the shim being tested, and the
// failure names the developer's dotfile rather than anything this package owns.
func jailedZshCommand(zsh, script, home string, extraEnv ...string) *exec.Cmd {
	command := exec.Command(zsh, "-f", "-c", script)
	environment := make([]string, 0, len(os.Environ())+1+len(extraEnv))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "ZDOTDIR=") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "HOME="+home)
	environment = append(environment, extraEnv...)
	command.Env = environment
	return command
}

func TestShimSyntaxAndResource(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	shimPath := embeddedShimPath(t)
	if output, err := exec.Command(zsh, "-n", shimPath).CombinedOutput(); err != nil {
		t.Fatalf("zsh -n: %v: %s", err, output)
	}

	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeShimFile(t, filepath.Join(binDir, "pfm"), "#!/bin/sh\nprintf 'pfm=%s\\n' \"$*\"\n")
	script := "source " + quoteZsh(shimPath) + "\n" +
		"source " + quoteZsh(shimPath) + "\n" +
		`"$HOME/.local/bin/pfm" --version` + "\n"
	command := jailedZshCommand(zsh, script, home)
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "pfm=--version\n" {
		t.Fatalf("source shim twice: err=%v output=%q", err, output)
	}
}

func TestShimCanBeResourcedWithForeignReadOnlyPFMBinParameter(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeShimFile(t, filepath.Join(binDir, "pfm"), "#!/bin/sh\nprintf 'shim-ok\\n'\n")
	shimPath := embeddedShimPath(t)
	script := `typeset -gr _PFM_BIN=/foreign/read-only/value
source ` + quoteZsh(shimPath) + `
source ` + quoteZsh(shimPath) + `
"$HOME/.local/bin/pfm"
`
	command := jailedZshCommand(zsh, script, home)
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "shim-ok\n" {
		t.Fatalf("resource shim with foreign read-only parameter: err=%v output=%q", err, output)
	}
}

// A Codex fleet chat owns a bare terminal. Replacing that shell with the tmux
// client makes terminal teardown structural: when the harness closes the
// server, no parent shell remains to reveal an accidental prompt.
func TestBareCodexLaunchExecsTheTerminalOwner(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is not installed")
	}
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fake-bin")
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeShimFile(t, filepath.Join(home, ".local", "bin", "pfm"), "#!/bin/sh\nprintf '1\\n'\n")
	writeShimFile(t, filepath.Join(fakeBin, "tmux"), `#!/bin/sh
case " $* " in *" attach "*) printf '%s\n' "$$" > "$SHIM_TMUX_PID" ;; esac
`)
	shellPID := filepath.Join(home, "shell.pid")
	tmuxPID := filepath.Join(home, "tmux.pid")
	driver := filepath.Join(home, "driver.zsh")
	writeShimFile(t, driver,
		"source "+quoteZsh(embeddedShimPath(t))+"\n"+
			"print -r -- \"$$\" > \"$SHIM_SHELL_PID\"\n"+
			"cx\n",
	)
	command := testjail.PTYCommand(zsh, "-fi", driver)
	command.Env = append(
		os.Environ(),
		"HOME="+home,
		"PATH="+fakeBin+":"+os.Getenv("PATH"),
		"TMUX=",
		"SHIM_SHELL_PID="+shellPID,
		"SHIM_TMUX_PID="+tmuxPID,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("drive interactive shim: %v: %s", err, output)
	}
	want := strings.TrimSpace(readShimFile(t, shellPID))
	got := strings.TrimSpace(readShimFile(t, tmuxPID))
	if got != want {
		t.Fatalf("tmux pid=%s, shell pid=%s: Codex launch forked and left an outer terminal shell", got, want)
	}
}

func TestShimAutoOpenDefersDisarmsAndMapsLegacyValuesToPicker(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	shimPath := embeddedShimPath(t)
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fake-bin")
	for _, directory := range []string{filepath.Join(home, ".local", "bin"), fakeBin} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeShimFile(t, filepath.Join(home, ".local", "bin", "pfm"), `#!/bin/sh
if [ "$#" -eq 0 ]; then printf 'picker\n' >> "$SHIM_AUTO_LOG"; fi
if [ "$1" = internal ] && [ "$2" = chat-server ]; then shift 2; printf 'create %s\n' "$*" >> "$SHIM_AUTO_LOG"; fi
`)
	writeShimFile(t, filepath.Join(fakeBin, "tmux"), `#!/bin/sh
printf 'launch %s\n' "$*" >> "$SHIM_AUTO_LOG"
`)
	writeShimFile(t, filepath.Join(fakeBin, "rm"), `#!/bin/sh
printf 'EVALUATED %s\n' "$*" >> "$SHIM_AUTO_LOG"
`)

	cases := []struct {
		name    string
		environ []string
		want    []string
		absent  []string
	}{
		{name: "managed profile", environ: []string{"PFM_AUTO_OPEN=pfm"}, want: []string{"picker"}},
		{name: "legacy managed profile", environ: []string{"CC_AUTO_OPEN=pfm"}, want: []string{"picker"}},
		{name: "legacy truthy", environ: []string{"CC_AUTO_OPEN=1"}, want: []string{"picker"}},
		{
			name:    "retired cc",
			environ: []string{"CC_AUTO_OPEN=cc"},
			want:    []string{"picker"},
			absent:  []string{"launch "},
		},
		{
			name:    "retired cc2",
			environ: []string{"CC_AUTO_OPEN=cc2"},
			want:    []string{"picker"},
			absent:  []string{"launch "},
		},
		{name: "retired VS Code spelling", environ: []string{"VSCODE_AUTO_CC=1"}, want: []string{"picker"}},
		{
			name:    "Codex survives",
			environ: []string{"PFM_AUTO_OPEN=cx"},
			want:    []string{"create cx-", "codex", "launch "},
		},
		{
			name:    "unknown is never evaluated",
			environ: []string{"PFM_AUTO_OPEN=rm -rf /"},
			want:    []string{"picker"},
			absent:  []string{"EVALUATED"},
		},
		{name: "unset", absent: []string{"picker", "create ", "launch "}},
		{
			name:    "app launched from a chat",
			environ: []string{"PFM_AUTO_OPEN=pfm", "CLAUDECODE=1", "TMUX=/private/tmp/chat,1,0"},
			want:    []string{"picker"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "log")
			writeShimFile(t, log, "")
			got := runAutoOpenShell(t, zsh, shimPath, home, fakeBin, log, testCase.environ,
				"print -r -- one\nprint -r -- two\nprint -r -- three\n")
			for _, wanted := range testCase.want {
				if !strings.Contains(got, wanted) {
					t.Fatalf("auto-open log %q lacks %q", got, wanted)
				}
			}
			for _, unwanted := range testCase.absent {
				if strings.Contains(got, unwanted) {
					t.Fatalf("auto-open log %q contains %q", got, unwanted)
				}
			}
			if pickers, launches := strings.Count(
				got,
				"picker\n",
			), strings.Count(
				got,
				"create ",
			); pickers > 1 ||
				launches > 1 {
				t.Fatalf("auto-open fired more than once (pickers=%d launches=%d): %q", pickers, launches, got)
			}
		})
	}

	log := filepath.Join(home, "inherit-log")
	writeShimFile(t, log, "")
	inherited := runAutoOpenShell(t, zsh, shimPath, home, fakeBin, log,
		[]string{"PFM_AUTO_OPEN=pfm", "CC_AUTO_OPEN=1", "VSCODE_AUTO_CC=1"},
		`print -r -- "leaked=${PFM_AUTO_OPEN-no}${CC_AUTO_OPEN-no}${VSCODE_AUTO_CC-no}" >> "$SHIM_AUTO_LOG"`+"\n")
	if !strings.Contains(inherited, "leaked=nonono") {
		t.Fatalf("auto-open left its variables in the environment: %q", inherited)
	}

	// An app launched from inside a chat hands its terminals the chat's identity; the shell
	// clears exactly the keys the VS Code profile nulls, so the picker never sees a chat's tmux.
	log = filepath.Join(home, "chat-env-log")
	writeShimFile(t, log, "")
	cleared := runAutoOpenShell(
		t,
		zsh,
		shimPath,
		home,
		fakeBin,
		log,
		[]string{
			"PFM_AUTO_OPEN=pfm",
			"CLAUDECODE=1",
			"CLAUDE_CODE_SESSION_ID=s",
			"CLAUDE_CODE_CHILD_SESSION=1",
			"TMUX=/private/tmp/chat,1,0",
			"TMUX_PANE=%0",
		},
		`print -r -- "chat=${CLAUDECODE-no}${CLAUDE_CODE_SESSION_ID-no}${CLAUDE_CODE_CHILD_SESSION-no}${TMUX-no}${TMUX_PANE-no}" >> "$SHIM_AUTO_LOG"`+"\n",
	)
	if !strings.Contains(cleared, "chat=nonononono") {
		t.Fatalf("auto-open kept the chat environment its app inherited: %q", cleared)
	}
}

// TestShimAutoOpenClearsAnInheritedChatEnvAndSaysSoOnAnInteractiveTTY pins
// the VS-Code-relaunched-from-a-chat case: PFM_AUTO_OPEN set AND CLAUDECODE
// set AND a real tty on stderr (via testjail.PTYCommand, the only way this repo
// drives [[ -t 2 ]] true) must print the named stderr line. A script run under
// `zsh -fi` never draws a prompt, so the precmd hook cannot fire here; the
// "app launched from a chat" case of
// TestShimAutoOpenDefersDisarmsAndMapsLegacyValuesToPicker pins that the
// picker still opens — the old skip left every new terminal at a dead prompt.
func TestShimAutoOpenClearsAnInheritedChatEnvAndSaysSoOnAnInteractiveTTY(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is not installed")
	}
	shimPath := embeddedShimPath(t)
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeShimFile(t, filepath.Join(binDir, "pfm"), `#!/bin/sh
printf 'PICKER-LAUNCHED\n'
`)
	driver := filepath.Join(home, "driver.zsh")
	writeShimFile(t, driver, "source "+quoteZsh(shimPath)+"\n")

	command := testjail.PTYCommand(zsh, "-fi", driver)
	command.Env = append(os.Environ(),
		"HOME="+home,
		"PATH="+binDir+":/usr/bin:/bin",
		"TERM=dumb",
		"PFM_AUTO_OPEN=pfm",
		"CLAUDECODE=1",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run auto-open shell on a pty: %v: %s", err, output)
	}
	got := string(output)
	want := "pfm: cleared a chat environment (CLAUDECODE=1) this terminal inherited from its app — relaunch the app outside any chat to stop it"
	if !strings.Contains(got, want) {
		t.Fatalf("interactive tty with inherited CLAUDECODE did not report the clear:\n%q", got)
	}
}

func runAutoOpenShell(t *testing.T, zsh, shimPath, home, fakeBin, log string, environ []string, body string) string {
	t.Helper()
	command := exec.Command(zsh, "-f", "-i", "+m")
	command.Stdin = strings.NewReader("source " + quoteZsh(shimPath) + "\n" + body)
	command.Env = append([]string{
		"HOME=" + home,
		"PATH=" + fakeBin + ":/usr/bin:/bin",
		"TERM=dumb",
		"SHIM_AUTO_LOG=" + log,
	}, environ...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run auto-open shell: %v: %s", err, output)
	}
	return readShimFile(t, log)
}

func writeShimFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

func readShimFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func embeddedShimPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "assets", "shim", "pfm.zsh")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("embedded shim asset %s: %v", path, err)
	}
	return path
}

func quoteZsh(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
