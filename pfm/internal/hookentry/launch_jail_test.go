//go:build jail

package hookentry

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestLaunchWarnsWhenTheFleetPromptCannotReachTheChat(t *testing.T) {
	const want = "Claude started WITHOUT its system prompt file"
	for _, test := range []struct {
		name    string
		policy  string
		stage   bool
		refused bool // the chat's tmux server cannot be created: no chat starts
		warning bool
		// terminal: stdin and stderr are a terminal; wait: the launcher holds for Enter.
		terminal, wait bool
		// cancel: the starter presses Ctrl-C at the Enter prompt; the launch is
		// abandoned and the server it already made is killed, not orphaned.
		cancel bool
	}{
		{"professor policy with no source clone", config.SystemPromptProfessor, false, false, true, true, true, false},
		{"professor policy with no source clone off a terminal", config.SystemPromptProfessor, false, false, true, false, false, false},
		{"professor policy with the composed prompt staged", config.SystemPromptProfessor, true, false, false, true, false, false},
		{"production policy needs no prompt", config.SystemPromptProduction, false, false, false, true, false, false},
		{"professor policy with no source clone, server refused", config.SystemPromptProfessor, false, true, false, true, false, false},
		{"professor policy with no source clone, Ctrl-C at the prompt", config.SystemPromptProfessor, false, false, true, true, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := testjail.ShortRoot(t)
			testjail.TmuxServer(t)
			dir := filepath.Join(root, "acme", "src")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := atomicfile.Write(
				filepath.Join(root, "acme", ".professor", "baseline.json"), []byte("{}"), 0o600,
			); err != nil {
				t.Fatal(err)
			}
			if test.stage {
				if err := paths.WriteSourceRepoMarker(root, t.TempDir()); err != nil {
					t.Fatal(err)
				}
				promptPath, err := action.ProfessorPromptPath(root)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(promptPath, []byte("professor prompt\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			binary, argvPath := filepath.Join(root, "claude"), filepath.Join(root, "argv")
			if err := testjail.WriteExecutable(
				binary,
				[]byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+cmdparse.Quote(argvPath)+"\n"),
				0o700,
			); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("TMUX", "")
			// The caller lookup scans an empty tmux dir of its own, never the fence's shared one.
			t.Setenv(paths.EnvTmuxDir, t.TempDir())
			t.Setenv(paths.EnvHome, root)
			t.Setenv(paths.EnvStateDB, filepath.Join(root, "state", "pfm.db"))
			runtime := config.Runtime{
				Config: config.Config{Claude: config.ClaudePrefs{SystemPrompt: test.policy}},
				Paths: paths.Values{
					Home:    root,
					StateDB: filepath.Join(root, "state", "pfm.db"),
					SIDDir:  filepath.Join(root, "sid"),
					TmuxDir: filepath.Join(root, "tmux"),
				},
			}
			if test.refused {
				if err := os.WriteFile(runtime.Paths.TmuxDir, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout bytes.Buffer
			var stderr lockedBuffer
			enter := strings.NewReader("\n")
			var input io.Reader = enter
			var finishRead func()
			if test.cancel {
				t.Setenv(spawn.TestFreshSocketEnv, "cc-cancel")
				// The test process outlives the interrupt whatever the launcher does.
				guard := make(chan os.Signal, 1)
				signal.Notify(guard, os.Interrupt)
				t.Cleanup(func() { signal.Stop(guard) })
				released := make(chan struct{})
				finished := make(chan struct{})
				finishRead = func() { close(released); <-finished }
				input = interruptingReader{released: released, finished: finished}
			}
			t.Cleanup(launchnotice.Replace(input, test.terminal))
			if finishRead != nil {
				t.Cleanup(finishRead)
			}
			code := Launch(
				[]string{"--real", binary, "--cwd", dir, "--"}, &stdout, &stderr, runtime, &paths.MapEnv{},
			)
			if test.cancel {
				if code != 130 {
					t.Fatalf("launch cancelled at the prompt = %d, want 130: %s", code, stderr.String())
				}
				socket := filepath.Join(runtime.Paths.TmuxDir, "cc-cancel")
				if exec.Command("tmux", "-S", socket, "has-session").Run() == nil {
					_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
					t.Fatalf("the cancelled launch left its tmux server %s running", socket)
				}
			} else if waited := enter.Len() == 0; waited != test.wait {
				t.Fatalf("read Enter = %t, want %t: %q", waited, test.wait, stderr.String())
			} else if (code != 0) != test.refused {
				t.Fatalf("launch = %d, want refused=%t: %s", code, test.refused, stderr.String())
			}
			if got := strings.Contains(stderr.String(), want); got != test.warning {
				t.Fatalf("stderr warns=%t, want %t: %q", got, test.warning, stderr.String())
			}
		})
	}
}

// Jail: the engine command runs in a real tmux session and writes the argv the test reads.
func TestLaunchWorkbench(t *testing.T) {
	for _, scenario := range []string{"new", "resume", "explicit", "invalid", "unknown effort", "disabled", "disabled session id", "disabled resume", "outside", "version", "mcp", "unreadable primary"} {
		t.Run(scenario, func(t *testing.T) {
			root := testjail.ShortRoot(t)
			testjail.TmuxServer(t)
			dir := filepath.Join(root, "acme", "docs", "scribe")
			prompt := filepath.Join(dir, ".professor", "scribe.md")
			manifest := `{"prompt":"scribe.md","effort":"xhigh"}`
			args := []string{}
			wantPrompt, wantEffort, wantError, wantWarning := prompt, "xhigh", "", ""
			switch scenario {
			case "resume":
				args = []string{"--resume", "44444444-4444-4444-8444-444444444444"}
			case "explicit":
				args = []string{"--effort", "low", "--system-prompt-file", "/work/alt.md"}
				wantPrompt, wantEffort = "/work/alt.md", "low"
			case "invalid":
				// A persona that cannot resolve launches the chat without it, said.
				manifest = `{"prompt":""}`
				wantPrompt, wantEffort = "", ""
				wantWarning = "pfm internal launch: workbench persona not applied (" + paths.WorkbenchManifest(dir) +
					`: "prompt" is required); launching without it` + "\n"
			case "unknown effort":
				manifest = `{"prompt":"scribe.md","effort":"turbo"}`
				wantEffort = ""
				wantWarning = "pfm internal launch: workbench " + dir + `: unknown Claude effort "turbo" ` +
					"(want low, medium, high, xhigh or max); launching the persona on the default effort\n"
			case "disabled", "disabled session id", "disabled resume":
				manifest = `{"prompt":"scribe.md","engines":["codex"],"effort":"xhigh"}`
				if scenario == "disabled session id" {
					// --session-id names a NEW session: a disabled engine is refused.
					args = []string{"--session-id", "55555555-5555-4555-8555-555555555555"}
				}
				if scenario != "disabled resume" {
					wantError = "pfm internal launch: workbench " + dir + ` does not enable claude: add "claude" to "engines" in ` + paths.WorkbenchManifest(
						dir,
					) + "\n"
				} else {
					args = []string{"--continue"}
					wantPrompt, wantEffort = "", ""
				}
			case "outside":
				wantPrompt, wantEffort = "", ""
			case "unreadable primary":
				if err := atomicfile.Write(
					filepath.Join(root, ".claude-primary"),
					[]byte("garbage"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				wantWarning = "pfm: primary account unreadable (read primary account: parse primary account mirror: " +
					`strconv.Atoi: parsing "garbage": invalid syntax); launching on account 1` + "\n"
			case "version", "mcp":
				manifest = `{"prompt":""}`
				args = []string{"--version"}
				if scenario == "mcp" {
					args = []string{"mcp", "list"}
				}
			}
			for path, body := range map[string]string{
				filepath.Join(root, "acme", ".professor", "baseline.json"): "{}",
				paths.WorkbenchManifest(dir):                               manifest, prompt: "You are scribe.",
			} {
				if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "outside" {
				dir = filepath.Join(root, "acme", "src")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			binary, argvPath := filepath.Join(root, "claude"), filepath.Join(root, "argv")
			if err := testjail.WriteExecutable(
				binary,
				[]byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+cmdparse.Quote(argvPath)+"\n"),
				0o700,
			); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("TMUX", "")
			// The caller lookup scans an empty tmux dir of its own, never the fence's shared one.
			t.Setenv(paths.EnvTmuxDir, t.TempDir())
			t.Setenv(paths.EnvHome, root)
			t.Setenv(paths.EnvStateDB, filepath.Join(root, "state", "pfm.db"))
			runtime := config.Runtime{
				Config: config.Config{},
				Paths: paths.Values{
					Home:    root,
					StateDB: filepath.Join(root, "state", "pfm.db"),
					SIDDir:  filepath.Join(root, "sid"),
					TmuxDir: filepath.Join(root, "tmux"),
				},
			}
			var stderr, stdout bytes.Buffer
			previous := LaunchExec
			t.Cleanup(func() { LaunchExec = previous })
			LaunchExec = func(path string, argv, _ []string) error {
				if scenario != "version" && scenario != "mcp" {
					t.Fatal("refusal reached exec")
				}
				if path != binary || !reflect.DeepEqual(argv, append([]string{binary}, args...)) {
					t.Fatalf("passthrough = %s %q", path, argv)
				}
				return nil
			}
			// A non-terminal launcher runs the real tmux boundary and records the engine argv.
			code := Launch(
				append([]string{"--real", binary, "--cwd", dir, "--"}, args...),
				&stdout,
				&stderr,
				runtime,
				&paths.MapEnv{},
			)
			if wantError != "" {
				if code != 1 || stderr.String() != wantError {
					t.Fatalf("refusal = %d %q, want 1 %q", code, stderr.String(), wantError)
				}
				return
			}
			if code != 0 {
				t.Fatalf("launch = %d: %s", code, stderr.String())
			}
			if wantWarning != "" && stderr.String() != wantWarning {
				t.Fatalf("launch stderr = %q, want %q", stderr.String(), wantWarning)
			}
			if scenario == "version" || scenario == "mcp" {
				return
			}
			raw, err := os.ReadFile(argvPath)
			if err != nil {
				t.Fatal(err)
			}
			argv := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
			parsed, err := claudelaunch.Parse(append([]string{binary}, argv...))
			if err != nil {
				t.Fatal(err)
			}
			if parsed.PromptFile != wantPrompt || parsed.Effort != wantEffort {
				t.Fatalf(
					"persona = %q %q, want %q %q; argv %q",
					parsed.PromptFile,
					parsed.Effort,
					wantPrompt,
					wantEffort,
					argv,
				)
			}
			if scenario == "explicit" {
				for _, flag := range []string{"--effort", "--system-prompt-file"} {
					count := 0
					for _, arg := range argv {
						if arg == flag {
							count++
						}
					}
					if count != 1 {
						t.Errorf("%s count = %d", flag, count)
					}
				}
			}
		})
	}
}

// Jail: the subject is a real tmux server exiting before the done waiter connects.
func TestLaunchReadsCompletedStatusAfterServerExit(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	for _, exitStatus := range []int{0, 7} {
		t.Run(strconv.Itoa(exitStatus), func(t *testing.T) {
			root := testjail.ShortRoot(t)
			binDir := filepath.Join(root, "bin")
			if err := os.MkdirAll(binDir, 0o700); err != nil {
				t.Fatal(err)
			}
			// Force the done waiter to connect only after the last pane exited.
			// The signal cannot survive that server, but the recorded status must.
			wrapper := "#!/bin/sh\n" +
				"if [ \"$3\" = wait-for ] && [ \"$4\" != -S ]; then\n" +
				"  case \"$4\" in pfm-launch-done-*)\n" +
				"    n=0; while " + cmdparse.Quote(realTmux) + " -S \"$2\" has-session 2>/dev/null; do\n" +
				"      n=$((n + 1)); [ \"$n\" -lt 500 ] || exit 99; sleep 0.01\n" +
				"    done;; esac\nfi\nexec " + cmdparse.Quote(realTmux) + " \"$@\"\n"
			if err := testjail.WriteExecutable(filepath.Join(binDir, "tmux"), []byte(wrapper), 0o700); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(binDir, "claude")
			if err := testjail.WriteExecutable(
				binary,
				[]byte("#!/bin/sh\nexit "+strconv.Itoa(exitStatus)+"\n"),
				0o700,
			); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("TMUX", "")
			// The caller lookup scans an empty tmux dir of its own, never the fence's shared one.
			t.Setenv(paths.EnvTmuxDir, t.TempDir())
			t.Setenv(paths.EnvHome, root)
			t.Setenv(paths.EnvStateDB, filepath.Join(root, "state", "pfm.db"))
			machine := config.Runtime{
				Config: config.Config{Claude: config.ClaudePrefs{SystemPrompt: config.SystemPromptProduction}},
				Paths: paths.Values{
					Home: root, StateDB: filepath.Join(root, "state", "pfm.db"),
					SIDDir: filepath.Join(root, "sid"), TmuxDir: filepath.Join(root, "tmux"),
				},
			}
			var stdout, stderr bytes.Buffer
			if got := Launch(
				[]string{"--real", binary, "--cwd", root, "--"},
				&stdout,
				&stderr,
				machine,
				&paths.MapEnv{},
			); got != exitStatus {
				t.Fatalf("completed launch = %d, want %d: %s", got, exitStatus, stderr.String())
			}
		})
	}
}
