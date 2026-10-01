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

func TestGitGuardStashDenyNamesThePathStash(t *testing.T) {
	cwd := t.TempDir()
	command := "git stash && go test ./...; git stash pop"
	code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, cwd, gitGuardExecutor))
	if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
	}
	reason := gitGuardDenyReason(t, stdout)
	for _, want := range []string{"git-guard blocked", "git stash push -- <path>..."} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason=%q, want it to contain %q", reason, want)
		}
	}
	code, stdout, stderr = runGitGuard(t, gitGuardPayload(t, "Bash", "git stash push -- a.go", cwd, gitGuardExecutor))
	if code != 0 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want the path stash allowed with no output", code, stdout, stderr)
	}
}

func TestGitGuardNamesEveryBlockedPartOfOneCall(t *testing.T) {
	command := "git status && git add f && git commit -m x; git push"
	code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, t.TempDir(), "general-executor"))
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
	gitGuardRequireUnreadable(t, `bash -c "git commit -m 'x"`, t.TempDir())
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

// gitGuardRequireUnreadable fails unless the guard denied command with the
// unreadable-command message.
func gitGuardRequireUnreadable(t *testing.T, command, cwd string) {
	t.Helper()
	code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, cwd, "general-executor"))
	if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
	}
	const unreadable = "could not read this command; split it so each git call is its own simple command"
	if reason := gitGuardDenyReason(t, stdout); !strings.Contains(reason, unreadable) {
		t.Fatalf("reason=%q, want the unreadable-command message", reason)
	}
}

// gitGuardNestShell wraps command in levels of `bash -c "…"`.
func gitGuardNestShell(command string, levels int) string {
	for range levels {
		escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(command)
		command = `bash -c "` + escaped + `"`
	}
	return command
}

// A command past the parser's size bound is never read as parsed: a git read
// that the guard would allow, padded past 64 KiB, is denied as unreadable.
func TestGitGuardDeniesACommandOverTheSizeBound(t *testing.T) {
	command := "git status; echo " + strings.Repeat("x", 64<<10)
	gitGuardRequireUnreadable(t, command, t.TempDir())
}

// A git call inside -c strings nested past the parser's depth bound is never
// read as parsed; at the bound itself the git call is still inspected.
func TestGitGuardDeniesShellNestingOverTheDepthBound(t *testing.T) {
	cwd := t.TempDir()
	gitGuardRequireUnreadable(t, gitGuardNestShell("git status", 9), cwd)
	code, stdout, stderr := runGitGuard(
		t,
		gitGuardPayload(t, "Bash", gitGuardNestShell("git worktree add x", 8), cwd, "general-executor"),
	)
	if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
	}
	if reason := gitGuardDenyReason(t, stdout); !strings.Contains(reason, "git worktree add x") {
		t.Fatalf("reason=%q, want the worktree add read at depth 8", reason)
	}
}

// gitGuardTwoFiles makes a cwd holding a.txt and b.txt, so an unquoted glob
// there has something a shell would expand it to.
func gitGuardTwoFiles(t *testing.T) string {
	t.Helper()
	cwd := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(cwd, name), []byte("x\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return cwd
}

// gitGuardRequireAllowed fails unless the guard let command through silently.
func gitGuardRequireAllowed(t *testing.T, command, cwd string) {
	t.Helper()
	code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, cwd, gitGuardExecutor))
	if code != 0 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want allowed with no output", code, stdout, stderr)
	}
}

// gitGuardRequireBlocked fails unless the guard denied command by name.
func gitGuardRequireBlocked(t *testing.T, command, cwd string) {
	t.Helper()
	code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, cwd, gitGuardExecutor))
	if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
	}
	if reason := gitGuardDenyReason(t, stdout); !strings.Contains(reason, "git-guard blocked `"+command+"`") {
		t.Fatalf("reason=%q, want it to name %q as blocked", reason, command)
	}
}

// An unquoted `*` reaches the guard as written, a whole-tree pathspec, never
// as the file names a shell would expand it to.
func TestGitGuardDeniesCheckoutOfAnUnquotedStar(t *testing.T) {
	gitGuardRequireBlocked(t, "git checkout -- *", gitGuardTwoFiles(t))
}

func TestGitGuardDeniesStashPushOfAnUnquotedStar(t *testing.T) {
	gitGuardRequireBlocked(t, "git stash push -- *", gitGuardTwoFiles(t))
}

// One checkout operand carrying a glob metacharacter is a pathspec: git
// forbids `*`, `?` and `[` in a ref name, so it is never a branch switch.
func TestGitGuardAllowsCheckoutOfAGlobPathspec(t *testing.T) {
	gitGuardRequireAllowed(t, "git checkout *.txt", gitGuardTwoFiles(t))
}

// A command that does not parse and names git only before a tab still
// mentions git, so it is denied as unreadable.
func TestGitGuardDeniesAnUnparsableCommandNamingGitBeforeATab(t *testing.T) {
	gitGuardRequireUnreadable(t, "git\tpush origin main && (", t.TempDir())
}

// A command past the size bound that never mentions git is not the git
// guard's to refuse. Its padding carries "git" inside a word ("digits"), so
// it passes the guard's first substring filter and reaches the bound.
func TestGitGuardAllowsAnOversizeCommandThatNeverMentionsGit(t *testing.T) {
	gitGuardRequireAllowed(t, "echo "+strings.Repeat("digits ", 10<<10), t.TempDir())
}
