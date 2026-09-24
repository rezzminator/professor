package hookentry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	gitGuardDenied   = `"permissionDecision":"deny"`
	gitGuardExecutor = "flights-smart-executor"
)

func gitGuardPayload(t *testing.T, tool, command, cwd, agentType string) string {
	t.Helper()
	payload := map[string]any{"tool_name": tool, "tool_input": map[string]any{"command": command}}
	if cwd != "" {
		payload["cwd"] = cwd
	}
	if agentType != "" {
		payload["agent_type"] = agentType
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(raw)
}

func runGitGuard(t *testing.T, payload string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := GitGuard(strings.NewReader(payload), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestGitGuardAllowsGitterEverything(t *testing.T) {
	for _, command := range []string{"git commit -m x", "git worktree add ../x"} {
		t.Run(command, func(t *testing.T) {
			code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, "/tmp", "gitter"))
			if code != 0 || stdout != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q, want allowed with no output", code, stdout, stderr)
			}
		})
	}
}

func TestGitGuardMainChatWorktreeAddNamesTheRightWay(t *testing.T) {
	withScript := t.TempDir()
	for _, dir := range []string{
		filepath.Join(withScript, ".git"), filepath.Join(withScript, ".claude", "scripts"), filepath.Join(withScript, "pfm"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(withScript, ".claude", "scripts", "worktree.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	without := t.TempDir()
	if err := os.WriteFile(filepath.Join(without, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, cwd, want, absent string }{
		{
			name: "repo ships worktree.sh, cwd is a subdirectory", cwd: filepath.Join(withScript, "pfm"),
			want: "gitter creates and removes worktrees with " + script +
				" create|remove|prune — the only right way here",
			absent: "Phase SETUP",
		},
		{
			name: "repo without worktree.sh", cwd: without,
			want: "gitter creates and removes worktrees (Phase SETUP)", absent: "worktree.sh create",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := gitGuardPayload(t, "Bash", "git worktree add -b b .worktrees/b HEAD", test.cwd, "")
			code, stdout, stderr := runGitGuard(t, payload)
			if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
				t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
			}
			reason := gitGuardDenyReason(t, stdout)
			for _, want := range []string{
				"git worktree add -b b .worktrees/b HEAD",
				`Only gitter writes this: spawn Agent(subagent_type: "gitter") with the repo path and the exact change`,
				test.want,
			} {
				if !strings.Contains(reason, want) {
					t.Fatalf("reason=%q, want it to contain %q", reason, want)
				}
			}
			if strings.Contains(reason, test.absent) {
				t.Fatalf("reason=%q, want it without %q", reason, test.absent)
			}
		})
	}
}

func gitGuardDenyReason(t *testing.T, stdout string) string {
	t.Helper()
	var response struct {
		HookSpecificOutput struct {
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("decode deny %q: %v", stdout, err)
	}
	return response.HookSpecificOutput.PermissionDecisionReason
}

func TestGitGuardBlocksSharedStateWritesForAnExecutor(t *testing.T) {
	cwd := t.TempDir()
	for _, command := range []string{
		"git stash",
		"git stash push",
		"git stash save wip",
		"git stash drop",
		"git add -N .",
		"git rm --cached f",
		"git reset --hard",
		"git reset HEAD~1",
		"git reset -- a.py",
		"git checkout -b x",
		"git checkout main",
		"git switch main",
		"git push",
		"git fetch",
		"git pull",
		"git config --global --add safe.directory '*'",
		"git config user.name x",
		"git branch -D x",
		"git branch feature",
		"git tag v1",
		"git tag -d v1",
		"git clean -fd",
		"git checkout -- .",
		"git restore .",
		"git restore --staged a.py",
		"git apply --index p.diff",
		"git symbolic-ref HEAD refs/heads/x",
		"git remote add o u",
		"git submodule update --init",
		"git notes add -m x",
		"git gc",
		"cd /r && git commit -am x",
		`bash -c "git merge x"`,
		"timeout 60 git rebase main",
		"git -C /r worktree remove /r/.worktrees/x",
		"git --no-pager -c core.pager=cat cherry-pick abc",
		"bash .claude/scripts/worktree.sh create x",
		".claude/scripts/worktree.sh prune",
	} {
		t.Run(command, func(t *testing.T) {
			code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, cwd, gitGuardExecutor))
			if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
				t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
			}
		})
	}
}

func TestGitGuardAllowsReadsAndOwnFileWritesForAnExecutor(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "a.py"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, tool, command string }{
		{name: "status", command: "git status"},
		{name: "diff", command: "git diff HEAD --stat"},
		{name: "log", command: "git log -3"},
		{name: "stash push of named paths around a test", command: "git stash push -- a.py && pytest; git stash pop"},
		{name: "stash pop", command: "git stash pop"},
		{name: "stash list", command: "git stash list"},
		{name: "checkout one file", command: "git checkout -- a.py"},
		{name: "checkout an existing file without --", command: "git checkout a.py"},
		{name: "restore one file", command: "git restore a.py"},
		{name: "branch show-current", command: "git branch --show-current"},
		{name: "branch list", command: "git branch -a"},
		{name: "tag list", command: "git tag -l 'v*'"},
		{name: "archive", command: "git archive HEAD pfm"},
		{name: "apply check", command: "git apply --check p.diff"},
		{name: "apply to the working tree", command: "git apply p.diff"},
		{name: "worktree list", command: "git worktree list"},
		{name: "config get", command: "git config --get user.name"},
		{name: "config single key read", command: "git -C /r config user.name"},
		{name: "rm dry run", command: "git rm -n f"},
		{name: "clean dry run", command: "git clean -nd"},
		{name: "notes show", command: "git notes show HEAD"},
		{name: "worktree.sh list", command: "bash .claude/scripts/worktree.sh list"},
		{name: "git only as an echo argument", command: "echo git commit"},
		{name: "heredoc body mentions git push", command: "cat > notes.txt <<'EOF'\nthen git push the branch\nEOF"},
		{name: "non-Bash tool", tool: "Read", command: "git push"},
		{name: "no git at all", command: "ls -la"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool := test.tool
			if tool == "" {
				tool = "Bash"
			}
			code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, tool, test.command, cwd, gitGuardExecutor))
			if code != 0 || stdout != "" || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q, want allowed silently", code, stdout, stderr)
			}
		})
	}
}

func TestGitGuardNamesEveryBlockedPartOfOneCall(t *testing.T) {
	command := "git status && git add f && git commit -m x; git push"
	code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, t.TempDir(), "general-smart-executor"))
	if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
	}
	reason := gitGuardDenyReason(t, stdout)
	for _, want := range []string{"git add f", "git commit -m x", "git push"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason=%q, want it to name %q", reason, want)
		}
	}
	if strings.Contains(reason, "git status") {
		t.Fatalf("reason=%q names the allowed git status", reason)
	}
}

func TestGitGuardDeniesAGitCommandItCannotRead(t *testing.T) {
	command := `bash -c "git commit -m 'x"`
	code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, t.TempDir(), "general-smart-executor"))
	if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
	}
	const unreadable = "could not read this command; split it so each git call is its own simple command"
	if reason := gitGuardDenyReason(t, stdout); !strings.Contains(reason, unreadable) {
		t.Fatalf("reason=%q, want the unreadable-command message", reason)
	}
}

func TestGitGuardFailsOpenLoudlyOnAMalformedPayload(t *testing.T) {
	code, stdout, stderr := runGitGuard(t, "not-json")
	if code != 0 || stdout != "" {
		t.Fatalf("code=%d stdout=%q, want fail-open with no output", code, stdout)
	}
	if !strings.Contains(stderr, "pfm internal git-guard: decode hook payload (fail-open):") {
		t.Fatalf("stderr=%q, want the named fail-open decode error", stderr)
	}
}
