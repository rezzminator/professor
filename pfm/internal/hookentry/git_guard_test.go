package hookentry

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testjail/fakegit"
)

const (
	gitGuardDenied   = `"permissionDecision":"deny"`
	gitGuardExecutor = "flights:foreman"
)

func gitGuardPayload(t *testing.T, tool, command, cwd, agentType string) string {
	t.Helper()
	payload := map[string]any{"tool_name": tool, "tool_input": map[string]any{"command": command}}
	if cwd != "" {
		payload["cwd"] = cwd
	}
	if agentType != "" {
		// Claude Code sets agent_id only inside a sub-agent, beside its type.
		payload["agent_type"] = agentType
		payload["agent_id"] = "a" + agentType
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(raw)
}

// guardGitModel answers the guards' git reads from the fixture trees, so no test starts a git process.
var guardGitModel = &fakegit.Runner{}

func runGitGuard(t *testing.T, payload string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := GitGuardWith(strings.NewReader(payload), &stdout, &stderr, guardGitModel)
	return code, stdout.String(), stderr.String()
}

func TestGitGuardAllowsGitterEverything(t *testing.T) {
	for _, command := range []string{
		"git commit -m x", "git worktree add ../x", "git archive HEAD", "bash .claude/scripts/worktree.sh prune",
	} {
		t.Run(command, func(t *testing.T) {
			code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, "/tmp", "gitter"))
			if code != 0 || stdout != "" || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q, want allowed silently", code, stdout, stderr)
			}
		})
	}
}

func TestGitGuardAllowsOnlyExactNativeWriterIdentities(t *testing.T) {
	for _, test := range []struct {
		name, metadata string
		allow          bool
	}{
		{name: "gitter", metadata: `,"agent_type":"gitter"`, allow: true},
		{name: "gpt-gitter is an ordinary agent", metadata: `,"agent_type":"gpt-gitter"`},
		{name: "omitted"},
		{name: "empty", metadata: `,"agent_type":""`},
		{name: "ordinary", metadata: `,"agent_type":"flights:foreman"`},
		{name: "plugin-namespaced lookalike", metadata: `,"agent_type":"flights:gitter"`},
		{name: "gitter lookalike", metadata: `,"agent_type":"gitter-api"`},
		{name: "prefix lookalike", metadata: `,"agent_type":"xgitter"`},
		{name: "canonical path", metadata: `,"agent_type":"agents/gitter"`},
		{name: "suffix lookalike path", metadata: `,"agent_type":"agents/gitter-x"`},
		{name: "native relative path", metadata: `,"agent_type":"./gitter"`},
		{name: "native absolute path", metadata: `,"agent_type":"/agents/gitter-x"`},
		{name: "native backslash path", metadata: `,"agent_type":"agents\\gitter-x"`},
		{name: "whitespace", metadata: `,"agent_type":"gitter "`},
		{name: "case", metadata: `,"agent_type":"Gitter"`},
		{name: "role spoof", metadata: `,"agent_type":"flights:foreman","role":"gitter"`},
		{name: "model spoof", metadata: `,"agent_type":"flights:foreman","model":"gpt-gitter"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := `{"tool_name":"Bash","tool_input":{"command":"git commit -m x"}` + test.metadata + `}`
			code, stdout, stderr := runGitGuard(t, payload)
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q, want a silent successful hook", code, stdout, stderr)
			}
			if test.allow {
				if stdout != "" {
					t.Fatalf("stdout=%q, want allowed with no output", stdout)
				}
			} else if !strings.Contains(stdout, gitGuardDenied) {
				t.Fatalf("stdout=%q, want a deny", stdout)
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
	if err := testjail.WriteExecutableNeverRun(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	without := t.TempDir()
	if err := os.WriteFile(filepath.Join(without, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, cwd, want string }{
		{"repo ships worktree.sh, cwd is a subdirectory", filepath.Join(withScript, "pfm"), "git-guard blocked `git worktree add -b b .worktrees/b HEAD`.\nOnly gitter writes this: spawn Agent(subagent_type: \"gitter\") with the repo path and the exact change. Never retry, rephrase, split or wrap the denied command to get past the guard.\nworktrees go through {repo}/.claude/scripts/worktree.sh — the main chat's alone, run while git tracks it (and its worktree/ helpers) unchanged from HEAD; every other caller goes to gitter."},
		{"repo without worktree.sh", without, "git-guard blocked `git worktree add -b b .worktrees/b HEAD`.\nOnly gitter writes this: spawn Agent(subagent_type: \"gitter\") with the repo path and the exact change. Never retry, rephrase, split or wrap the denied command to get past the guard.\ngitter creates and removes worktrees (Phase SETUP)."},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := runGitGuard(
				t,
				gitGuardPayload(t, "Bash", "git worktree add -b b .worktrees/b HEAD", test.cwd, ""),
			)
			if code != 0 || gitGuardDecision(t, stdout) != "deny" {
				t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
			}
			if got := strings.ReplaceAll(gitGuardDenyReason(t, stdout), withScript, "{repo}"); got != test.want {
				t.Errorf("reason = %q, want %q", got, test.want)
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
		"bash .claude/scripts/worktree.sh list",
		"bash -x .claude/scripts/worktree.sh create x",
		"git bisect start",
		"git sparse-checkout set x",
		"git init",
		"git archive HEAD",
		"git archive HEAD pfm",
		"git reflog expire --all",
		"git submodule init",
		"git submodule set-url plugins/x y",
		"git submodule set-branch -b main plugins/x",
		"git submodule absorbgitdirs",
		"git submodule foreach 'git commit -m x'",
		"git submodule --quiet foreach --recursive git push",
		"git config edit",
		"git config set a.b c",
		"git config unset a.b",
		"git config rename-section a b",
		"git config remove-section a",
		"git config --file /r/x a.b c",
		"git worktree",
		"git worktree move a b",
		"git remote prune origin",
		"git remote set-head origin -a",
		`eval "git commit -m x"`,
		"eval git push",
		"dash -c 'git commit -m x'",
		"ksh -c 'git commit -m x'",
		`find . -name x -exec git commit -m x \;`,
		"find . -execdir git add {} +",
		`find . -ok git rm {} \;`,
		`find . -okdir git checkout main \;`,
		"for c in status commit; do git $c; done",
		"c=commit; git $c -m x",
		"g''it push",
	} {
		t.Run(command, func(t *testing.T) {
			code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, cwd, gitGuardExecutor))
			if code != 0 || gitGuardDecision(t, stdout) != "deny" {
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
		{name: "ls-remote", command: "git ls-remote origin develop"},
		{name: "version", command: "git --version"},
		{name: "reflog", command: "git reflog"},
		{name: "stash push of named paths around a test", command: "git stash push -- a.py && pytest; git stash pop"},
		{name: "stash pop", command: "git stash pop"},
		{name: "stash list", command: "git stash list"},
		{name: "checkout one file", command: "git checkout -- a.py"},
		{name: "checkout an existing file without --", command: "git checkout a.py"},
		{name: "restore one file", command: "git restore a.py"},
		{name: "branch show-current", command: "git branch --show-current"},
		{name: "branch list", command: "git branch -a"},
		{name: "tag list", command: "git tag -l 'v*'"},
		{name: "apply check", command: "git apply --check p.diff"},
		{name: "apply to the working tree", command: "git apply p.diff"},
		{name: "worktree list", command: "git worktree list"},
		{name: "worktree list porcelain piped", command: "git worktree list --porcelain | grep worktree"},
		{name: "submodule bare", command: "git submodule"},
		{name: "submodule status", command: "git -C /r submodule status"},
		{name: "submodule summary", command: "git submodule summary"},
		{name: "submodule foreach read", command: "git submodule foreach git status"},
		{name: "submodule quiet foreach read", command: "git submodule --quiet foreach 'git log -1'"},
		{
			name:    "submodule recursive foreach read",
			command: `git -C /r submodule foreach --quiet --recursive 'git -C "$toplevel/$sm_path" status --porcelain'`,
		},
		{name: "remote bare", command: "git remote"},
		{name: "remote verbose", command: "git remote -v"},
		{name: "remote show", command: "git remote show origin"},
		{name: "remote get-url", command: "git remote get-url origin"},
		{name: "config get subcommand", command: "git config get user.name"},
		{name: "config list subcommand", command: "git config list --show-origin"},
		{name: "config get all", command: "git config get --all --regexp alias"},
		{name: "config get-regexp", command: "git config --get-regexp alias"},
		{name: "eval read", command: `eval "git status"`},
		{name: "dash read", command: "dash -c 'git log -1'"},
		{name: "find exec read", command: `find . -name '*.go' -exec git log -1 -- {} \;`},
		{name: "loop over two reads", command: "for c in status log; do git $c; done"},
		{name: "loop over one read with a flag", command: "for c in log; do git $c -1; done"},
		{name: "variable read", command: "c=status; git $c"},
		{name: "loop over repos", command: "for r in a b; do git -C $r status; done"},
		{
			name:    "loop over plugin paths",
			command: "for p in plugins/x plugins/y; do git -C /r/$p status --porcelain; done",
		},
		{
			name:    "loop over quoted subcommands",
			command: `for c in "status --short" "branch --show-current"; do git -C /r $c; done`,
		},
		{name: "config get", command: "git config --get user.name"},
		{name: "config single key read", command: "git -C /r config user.name"},
		{name: "rm dry run", command: "git rm -n f"},
		{name: "clean dry run", command: "git clean -nd"},
		{name: "notes show", command: "git notes show HEAD"},
		{name: "worktree.sh read, not run", command: "cat .claude/scripts/worktree.sh"},
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
	code, stdout, stderr := runGitGuard(
		t,
		gitGuardPayload(t, "Bash", command, t.TempDir(), "flights:foreman-sonnet"),
	)
	if code != 0 ||
		stdout != "{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"git-guard blocked `git add f`, `git commit -m x`, `git push`.\\nOnly gitter writes this: spawn Agent(subagent_type: \\\"gitter\\\") with the repo path and the exact change. Never retry, rephrase, split or wrap the denied command to get past the guard.\"}}\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
	}
	reason := gitGuardDenyReason(t, stdout)
	for _, want := range []string{"git add f", "git commit -m x", "git push"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason=%q, want it to name %q", reason, want)
		}
	}
	if reason != "git-guard blocked `git add f`, `git commit -m x`, `git push`.\nOnly gitter writes this: spawn Agent(subagent_type: \"gitter\") with the repo path and the exact change. Never retry, rephrase, split or wrap the denied command to get past the guard." {
		t.Fatalf("reason=%q names the allowed git status", reason)
	}
}

func TestGitGuardDeniesAGitCommandItCannotRead(t *testing.T) {
	gitGuardRequireUnreadable(t, `bash -c "git commit -m 'x"`, t.TempDir())
}

// A -c string holding an expansion the parse cannot know runs commands no
// part shows, so one in a command naming git is denied as unreadable.
func TestGitGuardDeniesAnUnreadShellStringInACommandNamingGit(t *testing.T) {
	for _, command := range []string{`bash -c "git push $X"`, `sh -c "git commit -m x $Y"`} {
		gitGuardRequireUnreadable(t, command, t.TempDir())
	}
}

// An unread -c string in a command that never names git is not the git
// guard's to refuse.
func TestGitGuardAllowsAnUnreadShellStringWithoutGit(t *testing.T) {
	for _, command := range []string{`bash -c "echo $X"`, `bash -c "echo $X legit"`} {
		gitGuardRequireAllowed(t, command, t.TempDir())
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

// gitGuardRequireUnreadable fails unless the guard denied command with the
// unreadable-command message.
func gitGuardRequireUnreadable(t *testing.T, command, cwd string) {
	t.Helper()
	code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, cwd, "flights:foreman-sonnet"))
	if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
	}
	// The whole reason: the route line forbids the split this deny asks for.
	const unreadable = "git-guard could not read this command; split it so each git call is its own simple command."
	if reason := gitGuardDenyReason(t, stdout); reason != unreadable {
		t.Fatalf("reason=%q, want exactly %q", reason, unreadable)
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

// gitGuardRequireBounded fails unless the guard denied command with the
// parse-bound message: both bounds and the remedy named, and never the
// advice to split git calls the command may not hold.
func gitGuardRequireBounded(t *testing.T, command, cwd string) {
	t.Helper()
	code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", command, cwd, gitGuardExecutor))
	if code != 0 ||
		stdout != "{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"git-guard could not read this command in full: it passes a parse bound (over 64 KiB, or a `-c` string nested past 8 levels). Shorten the command; write long content to a file with the Write tool.\"}}\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
	}
	reason := gitGuardDenyReason(t, stdout)
	for _, want := range []string{"64 KiB", "nested past 8 levels", "Shorten the command", "Write tool"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason=%q, want the parse-bound message naming %q", reason, want)
		}
	}
	if reason != "git-guard could not read this command in full: it passes a parse bound (over 64 KiB, or a `-c` string nested past 8 levels). Shorten the command; write long content to a file with the Write tool." {
		t.Fatalf("reason=%q tells the agent to split its git calls", reason)
	}
}

// A command past the parser's size bound is never read as parsed: a git read
// that the guard would allow, padded past 64 KiB, is denied by the bound.
func TestGitGuardDeniesACommandOverTheSizeBound(t *testing.T) {
	command := "git status; echo " + strings.Repeat("x", 64<<10)
	gitGuardRequireBounded(t, command, t.TempDir())
}

// A git call inside -c strings nested past the parser's depth bound is never
// read as parsed; at the bound itself the git call is still inspected.
func TestGitGuardDeniesShellNestingOverTheDepthBound(t *testing.T) {
	cwd := t.TempDir()
	gitGuardRequireBounded(t, gitGuardNestShell("git status", 9), cwd)
	code, stdout, stderr := runGitGuard(
		t,
		gitGuardPayload(t, "Bash", gitGuardNestShell("git worktree add x", 8), cwd, "flights:foreman-sonnet"),
	)
	if code != 0 ||
		stdout != "{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"git-guard blocked `git worktree add x`.\\nOnly gitter writes this: spawn Agent(subagent_type: \\\"gitter\\\") with the repo path and the exact change. Never retry, rephrase, split or wrap the denied command to get past the guard.\\ngitter creates and removes worktrees (Phase SETUP).\"}}\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
	}
	if reason := gitGuardDenyReason(
		t,
		stdout,
	); reason != "git-guard blocked `git worktree add x`.\nOnly gitter writes this: spawn Agent(subagent_type: \"gitter\") with the repo path and the exact change. Never retry, rephrase, split or wrap the denied command to get past the guard.\ngitter creates and removes worktrees (Phase SETUP)." {
		t.Fatalf("reason=%q, want the worktree add read at depth 8", reason)
	}
}

// gitGuardBoundSpellings are git writes the parser resolves to git, each
// spelled so the word git is never followed by whitespace.
var gitGuardBoundSpellings = []string{`"git" push`, `'git' push`, `G=git; $G push --force`}

// Past the size bound nothing is read, so every spelling of git is denied.
func TestGitGuardDeniesAnySpellingOfGitOverTheSizeBound(t *testing.T) {
	for _, call := range gitGuardBoundSpellings {
		t.Run(call, func(t *testing.T) {
			gitGuardRequireBounded(t, call+" # "+strings.Repeat("x", 70000), t.TempDir())
		})
	}
}

// Past the depth bound the innermost -c string is not read, so every
// spelling of git inside it is denied.
func TestGitGuardDeniesAnySpellingOfGitOverTheDepthBound(t *testing.T) {
	for _, call := range gitGuardBoundSpellings {
		t.Run(call, func(t *testing.T) {
			gitGuardRequireBounded(t, gitGuardNestShell(call, 9), t.TempDir())
		})
	}
}

// Past the size bound the guard reads nothing, so a command whose only git
// sits inside a word ("digits") is still denied, with the parse-bound reason.
func TestGitGuardDeniesAnOversizeCommandWithGitOnlyInsideAWord(t *testing.T) {
	gitGuardRequireBounded(t, "echo "+strings.Repeat("digits ", 10<<10), t.TempDir())
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

// gitGuardRemedy is the remedy every git-guard deny reason carries.
const gitGuardRemedy = "Only gitter writes this: spawn Agent(subagent_type: \"gitter\") with the repo path and the" +
	" exact change. Never retry, rephrase, split or wrap the denied command to get past the guard."

// gitGuardStashTail follows the remedy on a whole-tree stash deny.
const gitGuardStashTail = "\nTo park only your own files, `git stash push -- <path>...` is allowed (restore with" +
	" `git stash pop`); a whole-tree stash moves every other agent's uncommitted work."

// A whole-tree pathspec reaches the guard as written (an unquoted `*`, never
// the file names a shell would expand it to; `?*`, `**`, `./*`, `:(glob)**`,
// `**/*` likewise), and one checkout operand that is not an existing path is a
// revision whatever glob characters it carries: each is denied by its words,
// quotes removed.
func TestGitGuardDeniesWholeTreeAndRevisionCheckouts(t *testing.T) {
	cwd := gitGuardTwoFiles(t)
	for _, tc := range []struct{ command, written, tail string }{
		{"git checkout -- *", "git checkout -- *", ""},
		{"git stash push -- *", "git stash push -- *", gitGuardStashTail},
		{`git checkout 'HEAD^{/fi.*}'`, "git checkout HEAD^{/fi.*}", ""},
		{`git checkout ':/fi.*'`, "git checkout :/fi.*", ""},
		{`git checkout '?*'`, "git checkout ?*", ""},
		{"git checkout -- '?*'", "git checkout -- ?*", ""},
		{"git checkout -- '**'", "git checkout -- **", ""},
		{"git checkout -- './*'", "git checkout -- ./*", ""},
		{"git checkout -- ':(glob)**'", "git checkout -- :(glob)**", ""},
		{"git checkout -- '**/*'", "git checkout -- **/*", ""},
		{"git stash push -- '?*'", "git stash push -- ?*", gitGuardStashTail},
	} {
		code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", tc.command, cwd, gitGuardExecutor))
		if code != 0 {
			t.Fatalf("%s: code=%d stderr=%q, want 0", tc.command, code, stderr)
		}
		want := "git-guard blocked `" + tc.written + "`.\n" + gitGuardRemedy + tc.tail
		if reason := gitGuardDenyReason(t, stdout); reason != want {
			t.Errorf("%s: reason =\n%q\nwant\n%q", tc.command, reason, want)
		}
	}
}

// A narrow glob after the separator restores only the files it names.
func TestGitGuardAllowsCheckoutOfANarrowGlob(t *testing.T) {
	cwd := gitGuardTwoFiles(t)
	gitGuardRequireAllowed(t, "git checkout -- *.txt", cwd)
	gitGuardRequireAllowed(t, "git checkout -- 'src/*.go'", cwd)
}

// gitGuardAnyWide reads a glob as wide when its literal prefix names the top
// level and every element after it is all-star.
func TestGitGuardAnyWideJudgesWholeTreeGlobs(t *testing.T) {
	top := t.TempDir()
	if err := os.Mkdir(filepath.Join(top, ".git"), 0o700); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	for pathspec, want := range map[string]bool{
		"*": true, "?*": true, "**": true, "./*": true, ":(glob)**": true, "**/*": true, top + "/*": true,
		"src/*": false, "*.go": false, ".*": false, "?": false,
	} {
		if got := gitGuardAnyWide([]string{pathspec}, top); got != want {
			t.Errorf("gitGuardAnyWide(%q) = %v, want %v", pathspec, got, want)
		}
	}
}

// A command that does not parse and names git only before a tab still
// mentions git, so it is denied as unreadable.
func TestGitGuardDeniesAnUnparsableCommandNamingGitBeforeATab(t *testing.T) {
	gitGuardRequireUnreadable(t, "git\tpush origin main && (", t.TempDir())
}

// Quoted git is the word git: a non-word character sits on each side.
func TestGitGuardDeniesAnUnparsableCommandNamingQuotedGit(t *testing.T) {
	gitGuardRequireUnreadable(t, `"git" push origin main && (`, t.TempDir())
}

// git inside a word is not git: an unparsable command whose only git sits in
// a longer word is not the git guard's to refuse.
func TestGitGuardAllowsAnUnparsableCommandWithGitOnlyInsideAWord(t *testing.T) {
	for _, command := range []string{"echo digits && (", "echo digit && (", "echo legit x && (", "echo mygit_x && ("} {
		gitGuardRequireAllowed(t, command, t.TempDir())
	}
}

// The main chat (no agent_id) runs a repo's worktree script, any verb, while
// git tracks the script and its worktree/ helpers unchanged from HEAD; an
// edited or untracked script or helper, a command that changes directory and
// every sub-agent go to gitter.
func TestGitGuardMainChatRunsTheCommittedWorktreeScript(t *testing.T) {
	git := func(repo string, args ...string) {
		t.Helper()
		argv := append([]string{
			"git", "-C", repo, "-c", "user.name=probe", "-c", "user.email=probe@example.invalid",
			"-c", "commit.gpgsign=false",
		}, args...)
		result, err := guardGitModel.Run(context.Background(), argv, deps.RunOptions{})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("git %v: exit %d: %v: %s", args, result.ExitCode, err, result.Stderr)
		}
	}
	// helper writes a file into the script's worktree/ helper directory.
	helper := func(repo, name, body string) {
		t.Helper()
		lib := filepath.Join(repo, filepath.FromSlash(gitGuardWorktreeSh+"/../worktree"), name)
		if err := os.MkdirAll(filepath.Dir(lib), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lib, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	repoWith := func(commit, edit, helpers bool) string {
		t.Helper()
		repo := t.TempDir()
		script := filepath.Join(repo, filepath.FromSlash(gitGuardWorktreeSh))
		if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := testjail.WriteExecutableNeverRun(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if helpers {
			helper(repo, "land.sh", "land() { :; }\n")
		}
		git(repo, "init", "-q")
		if commit {
			git(repo, "add", ".")
			git(repo, "commit", "-q", "-m", "probe")
		}
		if edit {
			if err := testjail.WriteExecutableNeverRun(script, []byte("#!/bin/sh\ngit push\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return repo
	}
	committed, edited, untracked := repoWith(
		true,
		false,
		false,
	), repoWith(
		true,
		true,
		false,
	), repoWith(
		false,
		false,
		false,
	)
	withHelpers, helperEdited, helperUntracked := repoWith(true, false, true), repoWith(true, false, true),
		repoWith(true, false, true)
	helper(helperEdited, "land.sh", "land() { git push; }\n")
	helper(helperUntracked, "extra.sh", "extra() { git push; }\n")
	script := "." + string(filepath.Separator) + filepath.FromSlash(gitGuardWorktreeSh)
	for _, row := range []struct {
		name, command, cwd, agentType, blocked, lost string
		allow                                        bool
	}{
		{name: "main chat create", command: "bash " + script + " create x", cwd: committed, allow: true},
		{name: "main chat land", command: script + " land x -m 'wip'", cwd: committed, allow: true},
		{name: "main chat remove", command: script + " remove x --delete-branch", cwd: committed, allow: true},
		{
			name:    "main chat absolute committed script from another repo",
			command: filepath.Join(committed, gitGuardWorktreeSh) + " land x", cwd: untracked, allow: true,
		},
		{name: "main chat list", command: script + " list", cwd: committed, allow: true},
		{name: "main chat prune", command: script + " prune", cwd: committed, allow: true},
		{name: "main chat discard", command: script + " remove x --discard", cwd: committed, allow: true},
		{name: "main chat no verb", command: "bash " + script, cwd: committed, allow: true},
		{name: "main chat committed helpers", command: script + " land x", cwd: withHelpers, allow: true},
		{name: "main chat edited helper", command: script + " land x", cwd: helperEdited},
		{name: "main chat untracked helper", command: script + " land x", cwd: helperUntracked},
		{name: "main chat after cd", command: "cd " + edited + " && " + script + " land x", cwd: committed},
		{
			name: "main chat after cd to a committed script", command: "cd " + committed + "; " + script + " land x",
			cwd: untracked, allow: true,
		},
		{
			name:    "main chat after a relative cd",
			command: "cd " + filepath.Base(committed) + " && " + script + " list", cwd: filepath.Dir(committed),
			allow: true,
		},
		{
			name:    "main chat absolute script after cd",
			command: "cd " + untracked + " && " + filepath.Join(committed, gitGuardWorktreeSh) + " list",
			cwd:     untracked, allow: true,
		},
		{
			name: "main chat after an unknown cd", command: "cd $D && " + script + " land x", cwd: committed,
			blocked: script + " land x", lost: "cd $D",
		},
		{
			name: "main chat after a subshell cd", command: "(cd " + committed + "); " + script + " land x",
			cwd: committed, blocked: script + " land x", lost: "cd " + committed,
		},
		{
			name: "main chat after a conditional cd", command: "true && cd " + committed + "; " + script + " land x",
			cwd: committed, blocked: script + " land x", lost: "cd " + committed,
		},
		{
			name: "main chat after pushd", command: "pushd " + committed + " && " + script + " land x", cwd: committed,
			blocked: script + " land x", lost: "pushd " + committed,
		},
		{name: "main chat edited script", command: script + " land x", cwd: edited},
		{name: "main chat untracked script", command: script + " create x", cwd: untracked},
		{name: "sub-agent land", command: script + " land x", cwd: committed, agentType: gitGuardExecutor},
		{name: "sub-agent create", command: script + " create x", cwd: committed, agentType: gitGuardExecutor},
		{name: "sub-agent list", command: script + " list", cwd: committed, agentType: gitGuardExecutor},
	} {
		t.Run(row.name, func(t *testing.T) {
			code, stdout, stderr := runGitGuard(t, gitGuardPayload(t, "Bash", row.command, row.cwd, row.agentType))
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q, want a silent successful hook", code, stdout, stderr)
			}
			if row.allow {
				if stdout != "" {
					t.Fatalf("stdout=%q, want allowed with no output", stdout)
				}
				return
			}
			blocked := cmp.Or(row.blocked, strings.TrimPrefix(row.command, "cd "+edited+" && "))
			want := "git-guard blocked `" + blocked + "`."
			reason := gitGuardDenyReason(t, stdout)
			if !strings.HasPrefix(reason, want) {
				t.Fatalf("reason=%q, want it to start %q", reason, want)
			}
			lost := "git-guard cannot follow `" + row.lost + "` to the worktree script it runs:" +
				" run the script by its absolute path, without the directory change."
			if row.lost != "" && !strings.Contains(reason, lost) {
				t.Fatalf("reason=%q, want it to contain %q", reason, lost)
			}
		})
	}
}

// gitGuardDecision is the permissionDecision a guard's stdout carries, "" for a silent allow.
func gitGuardDecision(t *testing.T, stdout string) string {
	t.Helper()
	if stdout == "" {
		return ""
	}
	var response struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("decode decision %q: %v", stdout, err)
	}
	return response.HookSpecificOutput.PermissionDecision
}
