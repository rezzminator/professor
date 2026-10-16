package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/agentrole"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

func TestChatNewHarnessPromptLaunchesOnTheFileAndRecordsItForReload(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)
	harness := filepath.Join(jail.root, "work", "alt.md")
	if err := os.WriteFile(harness, []byte("ALT PROMPT"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(spawn.TestFreshSocketEnv, "cc-harness")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new", "--engine", "cc", "--name", "alt",
		"--cwd", filepath.Join(jail.root, "work"), "--harness-prompt", harness,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat new exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if argv := jail.await(
		t,
		"cc-argv",
		"--system-prompt-file",
	); !strings.Contains(
		argv,
		"--system-prompt-file "+harness,
	) {
		t.Fatalf("claude argv = %q, want --system-prompt-file %s", argv, harness)
	}
	recorded, found, err := agentrole.ReadHarnessPromptRecord(filepath.Join(jail.root, "sid"), "cc-harness", "")
	if err != nil || !found || recorded != harness {
		t.Fatalf("harness record = %q found %v error %v", recorded, found, err)
	}
	// N4: `chat new --name` stores the exact name for the Herdr reporter.
	if got := branchTmuxOutput(
		t, filepath.Join(jail.tmuxDir, "cc-harness"), "show-options", "-v", "-t", "cc-harness", "@pfm_chat_name",
	); got != "alt" {
		t.Fatalf("@pfm_chat_name = %q, want the --name value alt", got)
	}
}

func TestChatNewHarnessPromptRefusalsHappenBeforeSpawnAndWriteNothing(t *testing.T) {
	root := jailTest(t)
	workDir := filepath.Join(root, "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(root, "alt.md")
	if err := os.WriteFile(harness, []byte("ALT"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name, engine, file, want string
	}{
		// The jail rosters no Codex or OpenCode account, so account resolution
		// refuses those engines first; the engine gate itself is pinned by
		// TestLoadHarnessPromptForGatesTheEngineAndPassesAnEmptyPath.
		{name: "codex", engine: "codex", file: harness, want: "pfm chat new: "},
		{name: "opencode", engine: "opencode", file: harness, want: "pfm chat new: "},
		{name: "missing file", engine: "claude", file: filepath.Join(root, "gone.md"), want: "no such file"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{
				"chat", "new", "--engine", testCase.engine, "--name", "alt",
				"--cwd", workDir, "--harness-prompt", testCase.file,
			}, &stdout, &stderr)
			if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), testCase.want) {
				t.Fatalf("run() exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			for _, directory := range []string{os.Getenv("PFM_SID_DIR"), os.Getenv("PFM_TMUX_DIR")} {
				entries, err := os.ReadDir(directory)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "harness-prompt-") || strings.HasPrefix(entry.Name(), "cc-") ||
						strings.HasPrefix(entry.Name(), "cx-") {
						t.Fatalf("refused launch left %s in %s", entry.Name(), directory)
					}
				}
			}
		})
	}
}

// A professor-policy chat that starts without the fleet prompt says so on the
// starter's stderr, with the cause, and still starts; with the prompt staged it
// says nothing.
func TestChatNewWarnsWhenTheFleetPromptCannotReachTheChat(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	for _, entry := range []struct {
		name   string
		stage  bool
		binary string // claude.binary in the config; one not on disk refuses the spawn
		want   string
		// attach: --attach; terminal: stdin and stderr are a terminal; wait: held for Enter.
		attach, terminal, wait bool
	}{
		{"clone not recorded", false, "", "no source repository recorded", false, true, false},
		{"prompt staged", true, "", "", false, true, false},
		{"clone not recorded, spawn refused", false, "/nonexistent/claude", "", false, true, false},
		{"clone not recorded, attached on a terminal", false, "", "no source repository recorded", true, true, true},
		{"clone not recorded, attached off a terminal", false, "", "no source repository recorded", true, false, false},
		{"prompt staged, attached on a terminal", true, "", "", true, true, false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			jail := newRunJail(t)
			defer jail.killSockets(t)
			home := filepath.Join(jail.root, "home")
			claude := `{"systemPrompt":"professor"}`
			if entry.binary != "" {
				claude = fmt.Sprintf(`{"systemPrompt":"professor","binary":%q}`, entry.binary)
			}
			if err := os.WriteFile(
				filepath.Join(home, "pfm.config.json"),
				[]byte(`{"version":2,"claude":`+claude+`}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			if entry.stage {
				composed := paths.ComposedHarnessPromptIn(jail.root, pfmengine.Claude)
				if err := os.MkdirAll(filepath.Dir(composed), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(composed, []byte("fleet prompt\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := paths.WriteSourceRepoMarker(home, jail.root); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(paths.SourceRepoPath(home)); err != nil {
				t.Fatal(err)
			}
			t.Setenv(spawn.TestFreshSocketEnv, "cc-warn")
			var stdout, stderr bytes.Buffer
			input := strings.NewReader("\n")
			t.Cleanup(launchnotice.Replace(input, entry.terminal))
			arguments := []string{
				"chat", "new", "--engine", "cc", "--name", "warn",
				"--cwd", filepath.Join(jail.root, "work"),
			}
			if entry.attach {
				arguments = append(arguments, "--attach")
			}
			code := run(arguments, &stdout, &stderr)
			if (code != 0) != (entry.binary != "") {
				t.Fatalf("chat new exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if waited := input.Len() == 0; waited != entry.wait {
				t.Fatalf("read Enter = %t, want %t: stderr %q", waited, entry.wait, stderr.String())
			}
			if entry.want == "" {
				if strings.Contains(stderr.String(), "WITHOUT") {
					t.Fatalf("chat new stderr=%q, want no prompt warning", stderr.String())
				}
				return
			}
			if strings.Count(stderr.String(), "pfm chat new: Claude started WITHOUT its system prompt file") != 1 ||
				!strings.Contains(stderr.String(), entry.want) {
				t.Fatalf("chat new stderr=%q, want the prompt warning once, naming %q", stderr.String(), entry.want)
			}
		})
	}
}
