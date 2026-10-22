// Package shim is a TEST HOME, not a source directory: it holds no shim of its
// own. Tests always read the installer asset embedded into the pfm binary.
package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestCxHandsArgumentsToCodexLaunch(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeShimFile(t, filepath.Join(binDir, "pfm"), "#!/bin/sh\nexit 0\n")
	script := "source " + quoteZsh(embeddedShimPath(t)) + "\n" +
		"_cx_server() { print -r -- \"$3\" }\n" +
		"_pfm_selfswitch() { return 0 }\n" +
		"cx --resume 'literal prompt'\n"
	// cx() only runs its terminal dance under `[[ -o interactive ]]` (F11), so this drives it
	// with `-i` even without a tty: real interactivity is covered separately by
	// TestBareCodexLaunchExecsTheTerminalOwner over a PTY.
	command := jailedZshCommand(zsh, script, home)
	command.Args = append([]string{command.Args[0], "-i"}, command.Args[1:]...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("cx: %v: %s", err, output)
	}
	want := `"` + filepath.Join(home, ".local", "bin", "pfm") + `" internal codex-launch --resume literal\ prompt`
	if strings.TrimSpace(string(output)) != want {
		t.Fatalf("run=%q, want %q", output, want)
	}
}

// TestClaudeCodexCxSkipTerminalOwnershipWithoutHelpers pins F11: an agent tool
// shell replays a snapshot of the shim that keeps claude()/codex()/cx() but
// drops every `_`-prefixed helper, and is never interactive. All three must
// still run their underlying command and return its exit status cleanly —
// no "command not found" from a terminal-ownership call the guard should
// have skipped.
func TestClaudeCodexCxSkipTerminalOwnershipWithoutHelpers(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	fakeBin := filepath.Join(home, "fakebin")
	for _, directory := range []string{binDir, fakeBin} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeShimFile(t, filepath.Join(binDir, "pfm"), "#!/bin/sh\nexit 0\n")
	writeShimFile(t, filepath.Join(binDir, "claude"), "#!/bin/sh\nexit 7\n")
	writeShimFile(t, filepath.Join(fakeBin, "codex"), "#!/bin/sh\nexit 9\n")
	script := "source " + quoteZsh(embeddedShimPath(t)) + "\n" +
		// Simulate the snapshot: every `_`-prefixed helper is gone, but the
		// callable functions survive.
		"unfunction -m '_*'\n" +
		"claude; print -r -- \"claude=$?\"\n" +
		"codex; print -r -- \"codex=$?\"\n" +
		"cx --resume x; print -r -- \"cx=$?\"\n" +
		"pfm; print -r -- \"pfm=$?\"\n"
	command := jailedZshCommand(
		zsh, script, home,
		"PATH="+binDir+string(os.PathListSeparator)+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("claude/codex/cx without helpers: %v: %s", err, output)
	}
	got := string(output)
	for _, want := range []string{"claude=7", "codex=0", "cx: needs an interactive terminal", "cx=1", "pfm=0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output=%q, want %q", got, want)
		}
	}
	if strings.Contains(got, "not found") || strings.Contains(got, "command not found") {
		t.Fatalf("a terminal-ownership helper was called although undefined: %q", got)
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

// TestPfmOwnTerminalClosesOwnedAndPreservesOthers pins `_pfm_own_terminal`
// itself (TESTPLAN.md's J row), not just a caller that happens to reach it:
// an owned bare terminal is closed (the process exits, nothing after it
// runs), while a shell that does not own its terminal — a script, a nested
// chat pane — is preserved: the function returns the exit status instead of
// ending the shell.
func TestPfmOwnTerminalClosesOwnedAndPreservesOthers(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	shimPath := embeddedShimPath(t)

	t.Run("closes an owned bare terminal", func(t *testing.T) {
		if _, err := exec.LookPath("script"); err != nil {
			t.Skip("script is not installed")
		}
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeShimFile(t, filepath.Join(home, ".local", "bin", "pfm"), "#!/bin/sh\nexit 0\n")
		driver := filepath.Join(home, "driver.zsh")
		writeShimFile(t, driver,
			"source "+quoteZsh(shimPath)+"\n"+
				"_pfm_own_terminal 0\n"+
				"print -r -- UNREACHABLE\n",
		)
		command := testjail.PTYCommand(zsh, "-fi", driver)
		command.Env = append(os.Environ(), "HOME="+home, "TMUX=", "TERM=dumb")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("owned terminal: %v: %s", err, output)
		}
		if strings.Contains(string(output), "UNREACHABLE") {
			t.Fatalf("an owned terminal kept running past _pfm_own_terminal: %q", output)
		}
	})

	t.Run("preserves a script or nested-chat shell", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeShimFile(t, filepath.Join(home, ".local", "bin", "pfm"), "#!/bin/sh\nexit 0\n")
		script := "source " + quoteZsh(shimPath) + "\n" +
			"_pfm_own_terminal 3\n" +
			"print -r -- \"after=$?\"\n"
		output, err := jailedZshCommand(zsh, script, home).CombinedOutput()
		if err != nil {
			t.Fatalf("non-owning shell: %v: %s", err, output)
		}
		if !strings.Contains(string(output), "after=3") {
			t.Fatalf("a non-owning shell was not preserved: %q", output)
		}
	})
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
	if err := testjail.WriteExecutable(path, []byte(content), 0o700); err != nil {
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

// TestPickerTypedIntoABareTerminalHandsItToTheChat pins the picker's terminal
// ownership: `pfm` typed into a bare terminal runs the action the picker chose
// and then ENDS the shell, so a chat's /exit (or a detach) closes the terminal
// instead of dropping to a prompt that reads the terminal's late colour-query
// replies as typed input. Esc (no action line) and a failed pfm keep the
// shell. The fake pfm behaves as action.Dispatch does: it execs the attach
// itself when stdout is a terminal and prints the one-line action otherwise.
func TestPickerTypedIntoABareTerminalHandsItToTheChat(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is not installed")
	}
	cases := []struct {
		name      string
		pfm       string
		wantShell bool
		wantTmux  bool
	}{
		{
			name: "a chosen chat ends the shell when the attach returns",
			pfm: `#!/bin/sh
if [ "$#" -ne 0 ]; then exit 64; fi
if [ -t 1 ]; then TMUX= exec tmux -L cc-1-2-3 attach -t cc-1-2-3; fi
printf '%s\n' 'TMUX= tmux -L cc-1-2-3 attach -t cc-1-2-3'
`,
			wantShell: false, wantTmux: true,
		},
		{name: "Esc returns to the prompt", pfm: "#!/bin/sh\nexit 0\n", wantShell: true},
		{name: "a failed picker returns to the prompt", pfm: "#!/bin/sh\nexit 1\n", wantShell: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			fakeBin := filepath.Join(home, "fake-bin")
			for _, directory := range []string{filepath.Join(home, ".local", "bin"), fakeBin} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			writeShimFile(t, filepath.Join(home, ".local", "bin", "pfm"), testCase.pfm)
			tmuxLog := filepath.Join(home, "tmux.log")
			writeShimFile(t, filepath.Join(fakeBin, "tmux"), `#!/bin/sh
printf 'tmux %s TMUX=[%s]\n' "$*" "$TMUX" >> "$SHIM_TMUX_LOG"
`)
			driver := filepath.Join(home, "driver.zsh")
			writeShimFile(t, driver,
				"source "+quoteZsh(embeddedShimPath(t))+"\n"+
					"pfm\n"+
					"print -r -- SHELL-CAME-BACK\n",
			)
			command := testjail.PTYCommand(zsh, "-fi", driver)
			command.Env = append(
				os.Environ(),
				"HOME="+home,
				"PATH="+filepath.Join(home, ".local", "bin")+":"+fakeBin+":"+os.Getenv("PATH"),
				"TMUX=",
				"TERM=dumb",
				"SHIM_TMUX_LOG="+tmuxLog,
			)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("drive interactive picker: %v: %s", err, output)
			}
			tmuxCalls, readErr := os.ReadFile(tmuxLog)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatalf("read tmux log: %v", readErr)
			}
			attach := "tmux -L cc-1-2-3 attach -t cc-1-2-3 TMUX=[]"
			if got := strings.Contains(string(tmuxCalls), attach); got != testCase.wantTmux {
				t.Fatalf(
					"attach ran=%v, want %v: tmux log %q, output %q",
					got, testCase.wantTmux, tmuxCalls, output,
				)
			}
			if got := strings.Contains(string(output), "SHELL-CAME-BACK"); got != testCase.wantShell {
				t.Fatalf(
					"shell survived=%v, want %v: the picker's terminal stayed at a prompt after its chat ended: %q",
					got, testCase.wantShell, output,
				)
			}
		})
	}
}

// fakeShellReporterPFM logs every `pfm internal herdr-shell` call, one argv
// element per bracket, the shell's own pid as [PID] and the shell clock after
// --at as [AT] when it is one.
const fakeShellReporterPFM = `#!/bin/sh
[ "$1 $2" = "internal herdr-shell" ] || exit 0
shift 2
line= previous=
for arg; do
  if [ "$arg" = "$FAKE_SHELL_PID" ]; then arg=PID; fi
  if [ "$previous" = --at ] && printf '%s' "$arg" | grep -Eq '^[0-9]+\.[0-9]+$'; then arg=AT; fi
  line="$line[$arg]"
  previous=$arg
done
printf '%s\n' "$line" >> "$FAKE_PFM_LOG"
`

// A plain shell in a Herdr pane hands a command that ran at least the
// threshold to pfm when it finishes: the command, its exit status and its
// duration, under the shell's pid; the next command releases it. A pfm chat's
// tmux pane, an opt-out, a short command and a shell outside Herdr hand
// nothing.
func TestHerdrShellReporterHandsAFinishedLongCommandToPFM(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	const reported = "[done][--pid][PID][--at][AT][--command][make test][--exit][3][--seconds][0]\n" +
		"[release][--pid][PID][--at][AT]\n"
	// state is the shell's own record, read synchronously before the next
	// command: whether the hook is installed and whether the finished command
	// was handed over, so a case that hands nothing needs no wait.
	for name, test := range map[string]struct {
		environ []string
		want    string
		state   string
		// exits ends the shell after the report instead of starting a next
		// command: a nested zsh left by Ctrl-D runs no preexec.
		exits bool
	}{
		"long command reports done, next command releases": {want: reported, state: "hooked=1 reported=1"},
		"long command reports done, the shell's exit releases": {
			want: reported, state: "hooked=1 reported=1", exits: true,
		},
		"short command reports nothing": {
			environ: []string{"PFM_HERDR_LONG_COMMAND_SECS=100"}, state: "hooked=1 reported=",
		},
		"a threshold that is no number is the default": {
			environ: []string{"PFM_HERDR_LONG_COMMAND_SECS=junk"}, state: "hooked=1 reported=",
		},
		"opt-out reports nothing": {environ: []string{"PFM_HERDR_SHELL_REPORT=0"}, state: "hooked=1 reported="},
		"a pfm chat's tmux pane reports nothing": {
			environ: []string{"TMUX=/tmp/tmux-1/cc-1,1,0"}, state: "hooked=0",
		},
		"outside Herdr reports nothing": {environ: []string{"HERDR_ENV="}, state: "hooked=0"},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			binDir := filepath.Join(home, ".local", "bin")
			if err := os.MkdirAll(binDir, 0o700); err != nil {
				t.Fatal(err)
			}
			writeShimFile(t, filepath.Join(binDir, "pfm"), fakeShellReporterPFM)
			log := filepath.Join(home, "pfm.log")
			// The driver plays one long command, waits for its report (handed
			// over in the background), then starts the next command or exits.
			script := "source " + quoteZsh(embeddedShimPath(t)) + "\n" +
				"export FAKE_SHELL_PID=$$\n" +
				"state=\"hooked=$+functions[_pfm_herdr_precmd]\"\n" +
				"if (( $+functions[_pfm_herdr_precmd] )); then\n" +
				"  _pfm_herdr_preexec 'make test'; (exit 3); _pfm_herdr_precmd\n" +
				"  state+=\" reported=$_pfm_herdr_reported\"\n" +
				"  [[ -z $_pfm_herdr_reported ]] || for i in {1..50}; do [[ -s $FAKE_PFM_LOG ]] && break; sleep 0.1; done\n" +
				"fi\n" +
				"print -r -- \"STATE[$state]\"\n"
			if !test.exits {
				script += "(( ! $+functions[_pfm_herdr_preexec] )) || _pfm_herdr_preexec 'ls'\n"
			}
			environ := append([]string{
				"HERDR_ENV=1", "TMUX=", "PFM_HERDR_LONG_COMMAND_SECS=0", "PFM_HERDR_SHELL_REPORT=",
				"FAKE_PFM_LOG=" + log,
			}, test.environ...)
			command := jailedZshCommand(zsh, script, home, environ...)
			command.Args = append([]string{command.Args[0], "-i"}, command.Args[1:]...)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v: %s", err, output)
			}
			if !strings.Contains(string(output), "STATE["+test.state+"]") {
				t.Fatalf("shell state: %s\nwant STATE[%s]", output, test.state)
			}
			got := ""
			for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
				content, _ := os.ReadFile(log)
				got = string(content)
				if got == test.want || time.Now().After(deadline) {
					break
				}
			}
			if got != test.want {
				t.Fatalf("pfm herdr-shell calls:\n%s\nwant:\n%s", got, test.want)
			}
		})
	}
}

// Typed Codex launches use the same config/env door as cx, never a raw fallback.
func TestCodexShimUsesManagedLaunch(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeShimFile(
		t,
		filepath.Join(bin, "pfm"),
		"#!/bin/sh\n[ \"$1 $2\" = 'internal codex-launch' ] || exit 64\nshift 2\nprintf 'argv='\nfor arg in \"$@\"; do printf '%s|' \"$arg\"; done\nprintf '\\n'\n",
	)
	command := jailedZshCommand(zsh, "source "+quoteZsh(embeddedShimPath(t))+"\ncodex resume --last\n", home)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("managed launch %v: %s", err, output)
	}
	if strings.TrimSpace(string(output)) != "argv=resume|--last|" {
		t.Fatalf("managed launch args: %s", output)
	}
}
