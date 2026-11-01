package hookentry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// engineGuardFixture is the pcm-guard fixture with no Codex or OpenCode
// config dir named in the environment, plus the patch and script files the
// rows read: patches and scripts that write a framework file, and ones that
// do not.
func engineGuardFixture(t *testing.T) pcmGuardFixture {
	t.Helper()
	f := pcmGuardHome(t)
	for _, name := range []string{"CODEX_HOME", "OPENCODE_CONFIG_DIR", "XDG_CONFIG_HOME"} {
		t.Setenv(name, "")
	}
	// A compiled program past the read bound: no #!, so it is never read.
	big := filepath.Join(f.project, "big.bin")
	if err := os.WriteFile(big, make([]byte, engineGuardMaxRead+1), 0o644); err != nil {
		t.Fatal(err)
	}
	// Scripts past the read bound: run, they are blind whatever they name.
	pad := strings.Repeat("#", engineGuardMaxRead)
	for name, body := range map[string]string{"big.py": pad + "\n", "big-run.sh": "#!/bin/sh\n" + pad} {
		if err := os.WriteFile(filepath.Join(f.project, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"bad.diff":   "diff --git a/AGENTS.md b/AGENTS.md\n--- a/AGENTS.md\n+++ b/AGENTS.md\n@@ -1 +1 @@\n-x\n+y\n",
		"ok.diff":    "--- a/src/main.go\n+++ b/src/main.go\n@@ -1 +1 @@\n-x\n+y\n",
		"body.diff":  "--- a/src/main.go\n+++ b/src/main.go\n@@ -1,2 +1 @@\n--- a/.claude/x\n-x\n+y\n",
		"set.diff":   "--- settings.json\n+++ settings.json\n@@ -1 +1 @@\n-x\n+y\n",
		"bad.sh":     "echo y > AGENTS.md\n",
		"ok.sh":      "echo y > notes.md\n",
		"bad.py":     "open('.codex/config.toml', 'w').write('x')\n",
		"run-bad.sh": "#!/usr/bin/env bash\necho y > .opencode/opencode.jsonc\n",
		// Scripts reaching a sibling program through their own directory.
		"tools/near.sh":     "python3 -I \"$(dirname \"$0\")/ok.py\" \"$1\"\n",
		"tools/near-bad.sh": "python3 -I \"$(cd \"$(dirname -- \"${BASH_SOURCE[0]}\")\" && pwd -P)/bad.py\"\n",
		"tools/missing.sh":  "python3 \"$(dirname \"$0\")/gone.py\"\n",
		"tools/ok.py":       "print('ok')\n",
		"tools/bad.py":      "open('AGENTS.md', 'w').write('x')\n",
		"tools/ack.sh":      "cp AGENTS.md \"$D/acks/$1.md\"\n",
		"tools/ack-fn.sh":   "f() { cp AGENTS.md \"$1\"; }\nf \"$1\"\n",
		"tools/claim.sh":    "h=$(cat queue); mkdir $D/claims/$h && cat AGENTS.md > $D/claims/$h/owner\n",
	} {
		path := filepath.Join(f.project, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func runEngineGuard(t *testing.T, engine string, payload map[string]any) (int, string, string) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := EngineGuard([]string{"--engine", engine}, bytes.NewReader(raw), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// engineGuardShellCall is a shell call as each engine sends it: Codex's Bash
// with its sub-agent's agent_type (gitter and pcm are no exemption here),
// OpenCode's bash with the plugin's three keys.
func engineGuardShellCall(engine, command, cwd string) map[string]any {
	if engine == "codex" {
		return map[string]any{
			"tool_name": "Bash", "tool_input": map[string]any{"command": command}, "cwd": cwd, "agent_type": "gitter",
		}
	}
	return map[string]any{"tool_name": "bash", "tool_input": map[string]any{"command": command}, "cwd": cwd}
}

func requireEngineGuard(t *testing.T, engine string, payload map[string]any, want string) {
	t.Helper()
	code, stdout, stderr := runEngineGuard(t, engine, payload)
	if want == "" {
		if code != 0 || stdout != "" || stderr != "" {
			t.Fatalf(
				"%s: code=%d stdout=%q stderr=%q, want allowed in silence\npayload=%v",
				engine,
				code,
				stdout,
				stderr,
				payload,
			)
		}
		return
	}
	if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
		t.Fatalf("%s: code=%d stdout=%q stderr=%q, want a deny\npayload=%v", engine, code, stdout, stderr, payload)
	}
	if reason := gitGuardDenyReason(t, stdout); reason != want {
		t.Fatalf("%s: reason=\n%s\nwant\n%s\npayload=%v", engine, reason, want, payload)
	}
}

func TestEngineGuardGit(t *testing.T) {
	f := engineGuardFixture(t)
	denied := []string{
		"git commit -m x", "git push origin develop", "git tag v1", "git tag -d v1", "git merge topic",
		"git rebase main", "git reset --hard", "git cherry-pick abc", "git revert abc", "git am p.mbox",
		"git stash", "git stash push -- notes.md", "git stash pop", "git stash drop", "git checkout -b topic",
		"git checkout main", "git switch topic", "git branch topic", "git branch -D topic",
		"git worktree add ../w", "git worktree remove w", "git fetch", "git pull", "git clean -fd",
		"git rm notes.md", "git mv notes.md n.md", "git add notes.md", "git apply --index ok.diff",
		"git notes add -m x", "git update-ref refs/heads/x HEAD", "git config user.name x",
		"git submodule update", "git gc", "git prune", "git -C ../proj commit -m x",
		"git bisect start", "git sparse-checkout set src", "git init sub", "git reflog expire --all",
		"git read-tree HEAD", "git commit-tree HEAD^{tree}", "git maintenance run", "git co main",
	}
	allowed := []string{
		"git status", "git log --oneline", "git diff", "git show HEAD", "git blame notes.md", "git branch",
		"git branch --list", "git tag", "git tag -l", "git ls-files", "git ls-remote origin develop",
		"git rev-parse HEAD", "git config --get user.name", "git grep x", "git stash list", "git stash show",
		"git apply --check ok.diff", "git apply ok.diff", "git checkout -- notes.md",
		"git --version", "git rev-list HEAD", "git cat-file -p HEAD", "git reflog", "git merge-base a b",
	}
	for _, engine := range []string{"codex", "opencode"} {
		for _, command := range denied {
			requireEngineGuard(t, engine, engineGuardShellCall(engine, command, f.project),
				"git-guard blocked `"+command+"`.\n"+engineGuardGitBan)
		}
		for _, command := range allowed {
			requireEngineGuard(t, engine, engineGuardShellCall(engine, command, f.project), "")
		}
	}
}

func TestEngineGuardFrameworkFiles(t *testing.T) {
	f := engineGuardFixture(t)
	p := func(rel string) string { return filepath.Join(f.project, rel) }
	h := func(rel string) string { return filepath.Join(f.home, rel) }
	fileDeny := func(paths ...string) string {
		return "engine-guard blocked a write to " + strings.Join(paths, ", ") + ". " + engineGuardFileBan
	}
	blindDeny := func(command string) string {
		return "engine-guard blocked `" + command + "`: " + engineGuardBlindLine
	}
	codexPatch := func(patch string) map[string]any {
		return map[string]any{
			"tool_name": "apply_patch", "tool_input": map[string]any{"command": patch}, "cwd": f.project,
			"agent_type": "pcm",
		}
	}
	openCode := func(tool string, input map[string]any) map[string]any {
		return map[string]any{"tool_name": tool, "tool_input": input, "cwd": f.project}
	}
	envelope := func(header string) string { return "*** Begin Patch\n" + header + "\n+x\n*** End Patch\n" }
	rows := []struct {
		name, engine string
		payload      map[string]any
		want         string
	}{
		{"codex adds AGENTS.md", "codex", codexPatch(envelope("*** Add File: AGENTS.md")), fileDeny(p("AGENTS.md"))},
		{
			"codex moves a file into .codex", "codex",
			codexPatch(envelope("*** Update File: notes.md\n*** Move to: .codex/config.toml")),
			fileDeny(p(".codex/config.toml")),
		},
		{
			"codex deletes a clone template", "codex",
			codexPatch(envelope("*** Delete File: " + filepath.Join(f.clone, "templates", "global", "rules", "x.md"))),
			fileDeny(filepath.Join(f.clone, "templates", "global", "rules", "x.md")),
		},
		{"codex edits a project file", "codex", codexPatch(envelope("*** Update File: src/main.go")), ""},
		{
			"opencode writes .codex",
			"opencode",
			openCode("write", map[string]any{"filePath": p(".codex/config.toml")}),
			fileDeny(p(".codex/config.toml")),
		},
		{
			"opencode edits its own config", "opencode",
			openCode("edit", map[string]any{"filePath": h(".config/opencode/opencode.jsonc")}),
			fileDeny(h(".config/opencode/opencode.jsonc")),
		},
		{
			"opencode edits a whole-config-dir file",
			"opencode",
			openCode(
				"edit",
				map[string]any{"filePath": h(".claude/settings.json")},
			),
			fileDeny(h(".claude/settings.json")),
		},
		{
			"opencode patches CLAUDE.md", "opencode",
			openCode("apply_patch", map[string]any{"patchText": envelope("*** Update File: CLAUDE.md")}),
			fileDeny(p("CLAUDE.md")),
		},
		{
			"opencode writes a project file",
			"opencode",
			openCode("write", map[string]any{"filePath": p("src/main.go")}),
			"",
		},
		{"opencode reads AGENTS.md", "opencode", openCode("read", map[string]any{"filePath": p("AGENTS.md")}), ""},
		{
			"opencode bash in a workdir", "opencode",
			openCode("bash", map[string]any{"command": "echo x > AGENTS.md", "workdir": "src"}),
			fileDeny(p("src/AGENTS.md")),
		},
	}
	for _, engine := range []string{"codex", "opencode"} {
		for _, row := range []struct{ command, want string }{
			{"echo x > .opencode/agent/a.md", fileDeny(p(".opencode/agent/a.md"))},
			{"cp notes.md ~/.codex/hooks.json", fileDeny(h(".codex/hooks.json"))},
			{"sed -i s/a/b/ AGENTS.md", fileDeny(p("AGENTS.md"))},
			{"chmod 000 ~/.codex/hooks.json", fileDeny(h(".codex/hooks.json"))},
			{"echo ref > .git/HEAD", fileDeny(p(".git/HEAD"))},
			{"echo x > ~/.local/bin/pfm", fileDeny(h(".local/bin/pfm"))},
			{"vim AGENTS.md", fileDeny(p("AGENTS.md"))},
			{"git checkout -- CLAUDE.md", fileDeny(p("CLAUDE.md"))},
			{"git apply bad.diff", fileDeny(p("a/AGENTS.md"), p("AGENTS.md"), p("b/AGENTS.md"))},
			{"patch -p1 < bad.diff", fileDeny(p("a/AGENTS.md"), p("AGENTS.md"), p("b/AGENTS.md"))},
			{"bash bad.sh", fileDeny(p("AGENTS.md"))},
			{"python3 bad.py", fileDeny(p(".codex/config.toml"))},
			{"./run-bad.sh", fileDeny(p(".opencode/opencode.jsonc"))},
			{"python3 bad.py -h", fileDeny(p(".codex/config.toml"))},
			{"bash bad.sh --help", fileDeny(p("AGENTS.md"))},
			{"patch -d .claude -p0 < set.diff", fileDeny(p(".claude/settings.json"))},
			{"git apply --directory=.claude set.diff", fileDeny(p(".claude/settings.json"))},
			{"python3 big.py", blindDeny("python3 big.py")},
			{"./big-run.sh", blindDeny("./big-run.sh")},
			{"python3 new.py", blindDeny("python3 new.py")},
			{
				"rm " + filepath.Join(f.clone, "templates", "themes", "dusk", "theme.json"),
				fileDeny(filepath.Join(f.clone, "templates", "themes", "dusk", "theme.json")),
			},
			{"bash tools/near.sh x", ""},
			{"bash tools/near-bad.sh", fileDeny(p("AGENTS.md"), p("tools/AGENTS.md"))},
			{"bash tools/missing.sh", blindDeny("python3 " + p("tools/gone.py"))},
			{"bash tools/ack.sh chat", ""},
			{"bash tools/ack-fn.sh chat", engineGuardUnreadable},
			{"bash tools/claim.sh", ""},
			{"h=$(cat queue); mkdir $D/claims/$h && cat AGENTS.md", engineGuardUnreadable},
			{"bash", blindDeny("bash")},
			{"python3", blindDeny("python3")},
			{"vim", blindDeny("vim")},
			{"cat bad.diff | git apply", blindDeny("git apply")},
			{"echo x > notes.md", ""},
			{"git apply ok.diff", ""},
			{"bash ok.sh", ""},
			{"python3 --version", ""},
			{"bash ok.sh bad.sh", ""},
			{"git apply body.diff", ""},
			{"python3 -m pytest", ""},
			{"bash -o pipefail -c 'echo hi'", ""},
			{"./big.bin AGENTS.md", ""},
			{"cat AGENTS.md", ""},
		} {
			rows = append(rows, struct {
				name, engine string
				payload      map[string]any
				want         string
			}{engine + " " + row.command, engine, engineGuardShellCall(engine, row.command, f.project), row.want})
		}
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) { requireEngineGuard(t, row.engine, row.payload, row.want) })
	}
}

func TestEngineGuardEngineFlag(t *testing.T) {
	for _, args := range [][]string{nil, {"--engine"}, {"--engine", "claude"}, {"--engine", "bogus"}} {
		var stdout, stderr bytes.Buffer
		if code := EngineGuard(args, strings.NewReader(`{}`), &stdout, &stderr); code != 2 || stdout.Len() != 0 ||
			!strings.Contains(stderr.String(), "usage: pfm internal engine-guard --engine codex|opencode") {
			t.Fatalf(
				"args=%q: code=%d stdout=%q stderr=%q, want usage exit 2",
				args,
				code,
				stdout.String(),
				stderr.String(),
			)
		}
	}
}
