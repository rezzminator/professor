package hookentry

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
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
