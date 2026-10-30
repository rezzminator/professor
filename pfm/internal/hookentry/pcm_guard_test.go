package hookentry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	pcmGuardOnlyPcm    = "Only pcm writes"
	pcmGuardOnlyThemer = "Only themer writes"
)

// pcmGuardFixture is one HOME: the Professor clone at its default place
// (~/.professor, no marker) with a flight worktree, the store ~/.claude whose
// rules/x.md links into the clone, a seat config dir (~/.cc/1, the chat's
// CLAUDE_CONFIG_DIR) whose rules link to the store's, and a project.
type pcmGuardFixture struct {
	home, clone, project, seat string
}

func pcmGuardHome(t *testing.T) pcmGuardFixture {
	t.Helper()
	home := t.TempDir()
	f := pcmGuardFixture{
		home: home, clone: filepath.Join(home, ".professor"),
		project: filepath.Join(home, "proj"), seat: filepath.Join(home, ".cc", "1"),
	}
	for _, file := range []string{
		".professor/CLAUDE.md", ".professor/templates/global/rules/x.md",
		".professor/templates/themes/dusk/theme.json", ".professor/templates/project/agents/a.md",
		".professor/.worktrees/f/templates/global/rules/y.md", ".professor/docs/design/x.md",
		".claude/settings.json", ".claude/user-info.md", ".claude/projects/p/memory/m.md", ".claude/agents/g.md",
		"proj/CLAUDE.md", "proj/.claude/agents/a.md", "proj/src/main.go", "proj/notes.md",
	} {
		path := filepath.Join(home, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		".claude/rules/x.md":   ".professor/templates/global/rules/x.md",
		".cc/1/rules":          ".claude/rules",
		".cc/1/settings.json":  ".claude/settings.json",
		"proj/notes/link.md":   ".professor/templates/global/rules/x.md",
		"proj/notes/theme.lnk": ".professor/templates/themes/dusk/theme.json",
	} {
		path := filepath.Join(home, link)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(home, target), path); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", f.seat)
	return f
}

func pcmGuardPayload(t *testing.T, tool string, input map[string]any, cwd, agentType string) string {
	t.Helper()
	payload := map[string]any{"tool_name": tool, "tool_input": input, "cwd": cwd}
	if agentType != "" {
		payload["agent_type"] = agentType
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(raw)
}

func pcmGuardBash(t *testing.T, command, cwd, agentType string) string {
	t.Helper()
	return pcmGuardPayload(t, bashTool, map[string]any{"command": command}, cwd, agentType)
}

// pcmGuardEdit is the payload of an edit tool writing path.
func pcmGuardEdit(t *testing.T, tool, path, agentType string) string {
	t.Helper()
	key := "file_path"
	if tool == "NotebookEdit" {
		key = "notebook_path"
	}
	return pcmGuardPayload(t, tool, map[string]any{key: path}, "/", agentType)
}

func runPcmGuard(payload string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := PcmGuard(strings.NewReader(payload), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// pcmGuardRequireDeny fails unless the guard denied the payload naming want.
func pcmGuardRequireDeny(t *testing.T, payload, want string) {
	t.Helper()
	code, stdout, stderr := runPcmGuard(payload)
	if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny\npayload=%s", code, stdout, stderr, payload)
	}
	if reason := gitGuardDenyReason(t, stdout); !strings.Contains(reason, want) {
		t.Fatalf("reason=%q, want %q\npayload=%s", reason, want, payload)
	}
}

// pcmGuardRequireAllow fails unless the guard let the payload through in silence.
func pcmGuardRequireAllow(t *testing.T, payload string) {
	t.Helper()
	if code, stdout, stderr := runPcmGuard(payload); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want allowed in silence\npayload=%s", code, stdout, stderr, payload)
	}
}

// pcmGuardEditTargets are prompt files outside the themes, each written
// through an edit tool's absolute path.
func (f pcmGuardFixture) editTargets() []string {
	return []string{
		filepath.Join(f.project, "CLAUDE.md"),
		filepath.Join(f.project, ".claude", "agents", "a.md"),
		filepath.Join(f.project, ".claude", "settings.json"),
		filepath.Join(f.clone, "templates", "global", "rules", "x.md"),
		filepath.Join(f.clone, "templates", "global", "rules", "new.md"),
		filepath.Join(f.clone, ".worktrees", "f", "templates", "global", "rules", "y.md"),
		filepath.Join(f.home, ".claude", "CLAUDE.md"),
		filepath.Join(f.home, ".claude", "rules", "x.md"),
		filepath.Join(f.home, ".claude", "rules", "new.md"),
		filepath.Join(f.home, ".claude", "agents", "g.md"),
		filepath.Join(f.home, ".claude", "skills", "s", "SKILL.md"),
		filepath.Join(f.home, ".claude", "commands", "c.md"),
		filepath.Join(f.seat, "rules", "x.md"),
		filepath.Join(f.seat, "agents", "new.md"),
	}
}

// pcmGuardBashWrites is every Bash write form, each run from the project.
func (f pcmGuardFixture) bashWrites() []string {
	return []string{
		`echo x > CLAUDE.md`,
		`echo x >> .claude/agents/a.md`,
		`echo x &> CLAUDE.md`,
		`echo x 2> CLAUDE.md`,
		`echo x >| CLAUDE.md`,
		`{ echo a; echo b; } > CLAUDE.md`,
		`for i in 1 2; do echo $i; done >> CLAUDE.md`,
		`> CLAUDE.md`,
		`cat > CLAUDE.md <<'EOF'` + "\nnew\nEOF",
		`echo x | tee -a notes.md CLAUDE.md`,
		`echo x | sudo tee .claude/agents/a.md`,
		`sed -i 's/a/b/' CLAUDE.md`,
		`sed -i.bak -e 's/a/b/' .claude/agents/a.md`,
		`sed --in-place=.orig 's/a/b/' CLAUDE.md`,
		`sed -ni 's/a/b/p' CLAUDE.md`,
		`sed -i -e's/a/b/' CLAUDE.md`,
		`perl -pi -e 's/a/b/' CLAUDE.md`,
		`perl -i.bak -pe 's/a/b/' .claude/agents/a.md`,
		`cp /tmp/x CLAUDE.md`,
		`cp /tmp/CLAUDE.md .`,
		`cp -r /tmp/agents/a.md .claude/agents/`,
		`cp -t .claude/agents /tmp/new.md`,
		`cp -t.claude/agents /tmp/new.md`,
		`cp --target-directory=.claude/agents /tmp/new.md`,
		`mv /tmp/x CLAUDE.md`,
		`mv CLAUDE.md /tmp/old.md`,
		`install -m 644 /tmp/x .claude/agents/b.md`,
		`rsync -a /tmp/x.md .claude/agents/`,
		`ln -sf /tmp/x CLAUDE.md`,
		`ln -s /tmp/elsewhere/CLAUDE.md`,
		`rm CLAUDE.md`,
		`rm -rf .claude`,
		`rm CLAUDE.*`,
		`unlink CLAUDE.md`,
		`touch .claude/agents/new.md`,
		`touch -d 2020-01-01 CLAUDE.md`,
		`truncate -s 0 CLAUDE.md`,
		`dd if=/dev/zero of=CLAUDE.md bs=1 count=1`,
		`python3 -c "open('CLAUDE.md','w').write('x')"`,
		`python3 -c"open('CLAUDE.md','w').write('x')"`,
		`node -pe"require('fs').writeFileSync('CLAUDE.md', 'x')"`,
		`python3 - <<'EOF'` + "\nopen('.claude/agents/a.md', 'w').write('x')\nEOF",
		`python3 <<< "import os; os.remove('CLAUDE.md')"`,
		`node -e "require('fs').writeFileSync('CLAUDE.md', 'x')"`,
		`perl -e 'unlink "CLAUDE.md"'`,
		`ruby -e 'File.write("CLAUDE.md", "x")'`,
		`bash -c "echo x > CLAUDE.md"`,
		`sh -c 'rm .claude/agents/a.md'`,
		`bash <<'EOF'` + "\nrm CLAUDE.md\nEOF",
		`cd .claude && echo x > agents/a.md`,
		`cd /tmp && cd ` + f.project + ` && rm CLAUDE.md`,
		`echo x > "$HOME/.claude/rules/new.md"`,
		`echo x > ~/.claude/agents/z.md`,
		`echo x > "$DIR/CLAUDE.md"`,
		`git show HEAD:CLAUDE.md > CLAUDE.md`,
		`timeout 5 env A=1 tee CLAUDE.md`,
		`echo x > notes/link.md`,
		`echo x > ` + filepath.Join(f.clone, "templates", "project", "agents", "a.md"),
		`cd ` + f.clone + ` && sed -i s/a/b/ templates/project/agents/a.md`,
		`cd ` + f.clone + ` && (cd /tmp) && echo x > templates/project/agents/a.md`,
		`cd ` + f.clone + ` && bash -c 'cd /tmp' && rm templates/project/agents/a.md`,
		`cd ` + f.clone + ` && pushd /tmp && popd && rm templates/project/agents/a.md`,
		`dash -c 'echo x > CLAUDE.md'`,
		`ksh -c 'rm .claude/agents/a.md'`,
		`cp /tmp/new.md .cl*/agents/`,
		`echo x >& CLAUDE.md`,
		`echo x &>> CLAUDE.md`,
		`node --eval "require('fs').writeFileSync('CLAUDE.md', 'x')"`,
		`rm -rf ` + filepath.Join(f.clone, "templates", "global", "rules") + `/*`,
		`echo x > ` + filepath.Join(f.seat, "rules", "x.md"),
	}
}

func TestPcmGuardDeniesEveryEditToolToTheMainChatAndOtherAgents(t *testing.T) {
	f := pcmGuardHome(t)
	for _, agent := range []string{"", "general-purpose", "flights-foreman", "PCM", "pcm2", "themer"} {
		for _, tool := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit"} {
			for _, target := range f.editTargets() {
				t.Run(agent+"/"+tool+"/"+target, func(t *testing.T) {
					pcmGuardRequireDeny(t, pcmGuardEdit(t, tool, target, agent), pcmGuardOnlyPcm)
				})
			}
		}
	}
}

func TestPcmGuardDeniesEveryBashWriteFormToTheMainChatAndOtherAgents(t *testing.T) {
	f := pcmGuardHome(t)
	for _, agent := range []string{"", "general-purpose"} {
		for _, command := range f.bashWrites() {
			t.Run(agent+"/"+command, func(t *testing.T) {
				pcmGuardRequireDeny(t, pcmGuardBash(t, command, f.project, agent), pcmGuardOnlyPcm)
			})
		}
	}
}

func TestPcmGuardAllowsPcmEveryPromptFile(t *testing.T) {
	f := pcmGuardHome(t)
	for _, tool := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit"} {
		for _, target := range f.editTargets() {
			pcmGuardRequireAllow(t, pcmGuardEdit(t, tool, target, pcmGuardWriter))
		}
	}
	for _, command := range f.bashWrites() {
		pcmGuardRequireAllow(t, pcmGuardBash(t, command, f.project, pcmGuardWriter))
	}
}

// A theme is themer's alone: pcm and everyone else are denied it, by path or
// through a link resolving into it, and themer is denied every other prompt file.
func TestPcmGuardKeepsThemesForThemerAlone(t *testing.T) {
	f := pcmGuardHome(t)
	theme := filepath.Join(f.clone, "templates", "themes", "dusk", "theme.json")
	worktreeTheme := filepath.Join(f.clone, ".worktrees", "f", "templates", "themes", "new", "theme.json")
	for _, agent := range []string{pcmGuardWriter, "", "general-purpose"} {
		for _, target := range []string{theme, worktreeTheme} {
			pcmGuardRequireDeny(t, pcmGuardEdit(t, "Write", target, agent), pcmGuardOnlyThemer)
		}
		pcmGuardRequireDeny(t, pcmGuardBash(t, `echo x > notes/theme.lnk`, f.project, agent), pcmGuardOnlyThemer)
		pcmGuardRequireDeny(t, pcmGuardBash(t, `rm -rf templates/themes`, f.clone, agent), pcmGuardOnlyThemer)
	}
	for _, target := range []string{theme, worktreeTheme} {
		pcmGuardRequireAllow(t, pcmGuardEdit(t, "Edit", target, pcmGuardThemer))
	}
	pcmGuardRequireAllow(t, pcmGuardBash(t, `cp /tmp/t.json templates/themes/dusk/theme.json`, f.clone, pcmGuardThemer))
	pcmGuardRequireDeny(t, pcmGuardBash(t, `echo x > CLAUDE.md`, f.project, pcmGuardThemer), pcmGuardOnlyPcm)
}

// The store's rules/x.md and a project link both resolve into the clone's
// templates; the deny names the file the link reaches.
func TestPcmGuardResolvesSymlinksIntoTheClone(t *testing.T) {
	f := pcmGuardHome(t)
	resolved := filepath.Join(paths.PhysicalPath(f.clone), "templates", "global", "rules", "x.md")
	for _, payload := range []string{
		pcmGuardEdit(t, "Edit", filepath.Join(f.home, ".claude", "rules", "x.md"), "general-purpose"),
		pcmGuardEdit(t, "Edit", filepath.Join(f.seat, "rules", "x.md"), ""),
		pcmGuardBash(t, `sed -i s/a/b/ notes/link.md`, f.project, ""),
	} {
		code, stdout, stderr := runPcmGuard(payload)
		if code != 0 || !strings.Contains(stdout, gitGuardDenied) {
			t.Fatalf("code=%d stdout=%q stderr=%q, want a deny", code, stdout, stderr)
		}
		link := filepath.Join(f.project, "notes", "link.md")
		if reason := gitGuardDenyReason(t, stdout); strings.Contains(payload, "notes/link.md") &&
			!strings.Contains(reason, link) {
			t.Fatalf("reason=%q, want the link as written", reason)
		}
	}
	// A recorded clone elsewhere moves the guard with it.
	moved := filepath.Join(f.home, "src", "professor")
	if err := os.MkdirAll(filepath.Join(moved, "templates", "global"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(f.home, moved); err != nil {
		t.Fatal(err)
	}
	pcmGuardRequireDeny(
		t,
		pcmGuardEdit(t, "Write", filepath.Join(moved, "templates", "global", "n.md"), ""),
		pcmGuardOnlyPcm,
	)
	pcmGuardRequireAllow(t, pcmGuardEdit(t, "Write", resolved, ""))
}

func TestPcmGuardAllowsReadsGitAndEveryOtherPath(t *testing.T) {
	f := pcmGuardHome(t)
	for _, command := range []string{
		`cat CLAUDE.md`,
		`grep -rn rule .claude CLAUDE.md`,
		`sed -n 1,5p CLAUDE.md`,
		`sed 's/a/b/' CLAUDE.md > notes.md`,
		`head -20 .claude/agents/a.md`,
		`cp CLAUDE.md /tmp/copy.md`,
		`diff CLAUDE.md notes.md`,
		`git add CLAUDE.md .claude`,
		`git checkout -- CLAUDE.md`,
		`git diff ` + f.clone + `/templates`,
		`git commit -m "edit CLAUDE.md"`,
		`echo x > notes.md`,
		`echo x > src/main.go 2>&1`,
		`echo x >&2`,
		`cat > notes.md <<'EOF'` + "\nCLAUDE.md and .claude/agents are pcm's\nEOF",
		`echo CLAUDE.md > notes.md`,
		`python3 -c "print(1)"`,
		`python3 scripts/check.py CLAUDE.md`,
		`echo x > ~/.claude/settings.json`,
		`echo x > ~/.claude/user-info.md`,
		`echo x >> ~/.claude/projects/p/memory/m.md`,
		`echo x > ` + filepath.Join(f.seat, "settings.json"),
		`echo x > ` + filepath.Join(f.clone, "docs", "design", "x.md"),
		`rm -rf /tmp/scratch`,
		`echo x > "$OUT"`,
	} {
		pcmGuardRequireAllow(t, pcmGuardBash(t, command, f.project, ""))
	}
	for _, target := range []string{
		filepath.Join(f.project, "src", "main.go"),
		filepath.Join(f.project, "CLAUDE.md.txt"),
		filepath.Join(f.home, ".claude", "settings.json"),
		filepath.Join(f.home, ".claude", "user-info.md"),
		filepath.Join(f.home, ".claude", "projects", "p", "memory", "m.md"),
		filepath.Join(f.home, ".claude", "plans", "plan.md"),
		filepath.Join(f.seat, "settings.json"),
		filepath.Join(f.clone, "docs", "design", "x.md"),
		filepath.Join(f.clone, "pfm", "internal", "x.go"),
	} {
		pcmGuardRequireAllow(t, pcmGuardEdit(t, "Write", target, ""))
	}
	pcmGuardRequireAllow(
		t,
		pcmGuardPayload(t, "Read", map[string]any{"file_path": filepath.Join(f.project, "CLAUDE.md")}, "/", ""),
	)
}

// What the guard cannot read is denied when the command names a prompt
// file, and allowed when it does not.
func TestPcmGuardDeniesWhatItCannotReadWhenItNamesAPromptFile(t *testing.T) {
	f := pcmGuardHome(t)
	for _, command := range []string{
		`echo "unterminated > CLAUDE.md`,
		`F=$(ls CLAUDE.md); echo x > "$F"`,
		`cp /tmp/x "$(dirname CLAUDE.md)"`,
		gitGuardNestShell("echo x > notes.md # .claude", 9),
		`echo x > notes.md # CLAUDE.md ` + strings.Repeat("x", 65536),
	} {
		pcmGuardRequireDeny(t, pcmGuardBash(t, command, f.project, ""), "could not read this command")
	}
	for _, command := range []string{`echo "unterminated > notes.md`, `echo x > "$F"`} {
		pcmGuardRequireAllow(t, pcmGuardBash(t, command, f.project, ""))
	}
	// pcm is refused only an unreadable command naming a theme.
	pcmGuardRequireAllow(t, pcmGuardBash(t, `echo "unterminated > CLAUDE.md`, f.project, pcmGuardWriter))
	pcmGuardRequireDeny(t, pcmGuardBash(t, `echo "unterminated > templates/themes/x`, f.clone, pcmGuardWriter),
		"could not read this command")
}

func TestPcmGuardFailsOpenLoudlyOnAMalformedPayload(t *testing.T) {
	pcmGuardHome(t)
	code, stdout, stderr := runPcmGuard("not-json")
	if code != 0 || stdout != "" {
		t.Fatalf("code=%d stdout=%q, want fail-open with no output", code, stdout)
	}
	if !strings.Contains(stderr, "pfm internal pcm-guard: decode hook payload (fail-open):") {
		t.Fatalf("stderr=%q, want the named fail-open decode error", stderr)
	}
	if code, stdout, stderr := runPcmGuard(""); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("empty payload: code=%d stdout=%q stderr=%q, want silence", code, stdout, stderr)
	}
}

// With no HOME the store and the clone cannot be placed: a CLAUDE.md and a
// .claude path are still denied by name, and the gap is named on stderr.
func TestPcmGuardWithoutHomeJudgesByNameAndSaysSo(t *testing.T) {
	f := pcmGuardHome(t)
	t.Setenv("HOME", "")
	code, stdout, stderr := runPcmGuard(pcmGuardEdit(t, "Write", filepath.Join(f.project, "CLAUDE.md"), ""))
	if code != 0 || !strings.Contains(stdout, gitGuardDenied) || !strings.Contains(stderr, "HOME is empty") {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a deny and the named HOME gap", code, stdout, stderr)
	}
	// The chat's CLAUDE_CONFIG_DIR needs no HOME to be placed.
	pcmGuardRequireDeny(t, pcmGuardEdit(t, "Write", filepath.Join(f.seat, "agents", "new.md"), ""), pcmGuardOnlyPcm)
}
