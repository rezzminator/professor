package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestChatNewUnknownFlagUsageNamesAgentRole(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "new", "--no-such-flag"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run() exit=%d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined: -no-such-flag") ||
		!strings.Contains(stderr.String(), "--agent-role ROLE") {
		t.Fatalf("run() stderr=%q", stderr.String())
	}
}

// TestChatNewUnknownRoleExitsWithoutSpawning is the CLI-level seam for
// --agent-role: pfm chat new --agent-role <unknown> resolves the role BEFORE
// action.HeadlessRun / spawn.Run ever runs, so an unregistered role must
// exit 2, write the resolution error to stderr, print nothing to stdout, and
// leave the jailed tmux socket directory EMPTY — the closest observable
// proof this harness offers that nothing was spawned, since the CLI has no
// spawn-call counter to assert zero invocations against directly.
func TestChatNewUnknownRoleExitsWithoutSpawning(t *testing.T) {
	root := jailTest(t)
	workDir := filepath.Join(root, "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	runtime.Config.Claude.SystemPrompt = pfmconfig.SystemPromptProfessor

	var stdout, stderr bytes.Buffer
	code := runRun(context.Background(), []string{
		"--engine", "cc",
		"--name", "worker",
		"--cwd", workDir,
		"--agent-role", "ghost-role",
		"do the thing",
	}, &stdout, &stderr, runtime, paths.OSEnv{}, nil)

	if code != 2 {
		t.Fatalf("run() exit=%d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("run() stdout=%q, want nothing printed before a role resolution failure", stdout.String())
	}
	if !strings.Contains(stderr.String(), `no cc role "ghost-role" found`) {
		t.Fatalf("run() stderr=%q, want the agent role resolution error", stderr.String())
	}

	tmuxDir := os.Getenv("PFM_TMUX_DIR")
	entries, err := os.ReadDir(tmuxDir)
	if err != nil {
		t.Fatalf("read jailed tmux dir %s: %v", tmuxDir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("jailed tmux dir %s has %d entries, want 0 — a socket here means something spawned "+
			"despite the unresolved role", tmuxDir, len(entries))
	}
}

func TestChatNewAgentRoleRefusalsHappenBeforeResolutionOrSpawn(t *testing.T) {
	root := jailTest(t)
	workDir := filepath.Join(root, "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		engine string
		want   string
	}{
		{
			name: "opencode", engine: "opencode",
			want: "pfm chat new: --agent-role is not supported for opencode: native launch policy does not compose registered agent roles",
		},
		{
			name: "Claude wrong policy", engine: "claude",
			want: "pfm chat new: --agent-role needs claude.systemPrompt=professor (current: production): the role prompt is composed onto the staged fleet prompt, which that policy does not stage",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{
				"chat", "new", "--engine", testCase.engine, "--name", "worker",
				"--cwd", workDir, "--agent-role", "ghost-role", "caller prompt",
			}, &stdout, &stderr)
			if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), testCase.want) {
				t.Fatalf("run() exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			entries, err := os.ReadDir(os.Getenv("PFM_SID_DIR"))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "agent-role-") || strings.HasPrefix(entry.Name(), "role-") {
					t.Fatalf("refused launch wrote role state %s", entry.Name())
				}
			}
		})
	}
}
