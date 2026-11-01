package hookentry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pcmGuardScript places a script file the fixture's interpreters run, so a
// heredoc fed to it is the script's data.
func (f pcmGuardFixture) script(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join(f.home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Interpreter and shell code is judged by what it writes: a read, a heredoc
// that is a script's data, a write outside the prompt files and a text that
// merely names a prompt file pass, each run where its paths are protected.
func TestPcmGuardAdmitsReadsDataAndWritesElsewhere(t *testing.T) {
	f := pcmGuardHome(t)
	f.script(t, ".claude/skills/tracetree/tracetree.py")
	f.script(t, "proj/tool.sh")
	for _, command := range []string{
		// A: the heredoc is stdin data for a script that exists.
		`python3 $HOME/.claude/skills/tracetree/tracetree.py write /tmp/x 1 <<'HOPS'` +
			"\nTHREAD 9: read .claude/scripts/test.sh, CLAUDE.md and templates/global/agents/x.md\n" +
			"ANSWER\n- open('CLAUDE.md', 'w') is pcm's [templates/global/rules/x.md:4]\nHOPS",
		`python3 -u ` + filepath.Join(f.home, ".claude", "skills", "tracetree", "tracetree.py") +
			` read <<< "templates/global/rules/x.md CLAUDE.md"`,
		// B: a read in inline code.
		`python3 -c "import json; print(json.load(open('templates/refresh-map.json')))"`,
		`node -e "console.log(require('fs').readFileSync('CLAUDE.md','utf8'))"`,
		`perl -e 'open(my $f, "<", "CLAUDE.md"); print <$f>'`,
		`ruby -e 'puts File.read("CLAUDE.md")'`,
		// C: a /tmp write whose text names prompt files.
		`python3 - <<'EOF'` + "\np='/tmp/x/review.md'\ns=open(p).read()\n" +
			"s=s.replace('a','templates/global/rules/x.md and CLAUDE.md')\nopen(p,'w').write(s)\nEOF",
		`python3 -c "p='/tmp/x/review.md'; s=open(p).read(); open(p, 'w').write(s + 'CLAUDE.md')"`,
		// Doc text whose markdown link reads like a call, written to targets the name takes in turn.
		`python3 - <<'EOF'` + "\np='/tmp/x/a.md'\ns=open(p).read()\n" +
			"s=s.replace('a','see [hooks](hooks.md) and `.claude/engine-sync.json` # not code')\n" +
			"open(p,'w').write(s)\np='/tmp/x/b.md'\nopen(p,'w').write(s)\nEOF",
		`python3 -c "from pathlib import Path; Path('/tmp/x/out.md').write_text(open('CLAUDE.md').read())"`,
		`node -e "require('fs').writeFileSync('/tmp/x/out.md', require('fs').readFileSync('CLAUDE.md'))"`,
		// A read program on stdin.
		`python3 - <<'EOF'` + "\nprint(open('CLAUDE.md').read())\nEOF",
		`python3 -m json.tool <<'EOF'` + "\n{\"open('CLAUDE.md','w')\": \"templates/global/rules/x.md\"}\nEOF",
		// Shell code is parsed: reads pass.
		`bash <<'EOF'` + "\ncat CLAUDE.md\nsed -n 1,5p .claude/agents/a.md\nEOF",
		`sh -s <<< "grep -n rule CLAUDE.md"`,
		`dash -c 'cat CLAUDE.md'`,
		`bash ` + filepath.Join(f.project, "tool.sh") + ` <<'EOF'` + "\nrm CLAUDE.md\nEOF",
		// A copy reads its source; an open whose first argument is a path is no Path.open.
		`python3 -c "import shutil; shutil.copy('CLAUDE.md', '/tmp/x/out.md')"`,
		`python3 -c "from PIL import Image; Image.open('templates/global/logo.png')"`,
		// An unattributable write whose prose names a protected directory as a word.
		`python3 - <<'EOF'` + "\nb='/tmp/x'\np=b+'/checks.sh'\ns=open(p).read()\n" +
			"s=s.replace('a','echo \"FAIL templates steps: plugin logs\"')\nopen(p,'w').write(s)\nEOF",
	} {
		t.Run(command, func(t *testing.T) { pcmGuardRequireAllow(t, pcmGuardBash(t, command, f.clone, "")) })
	}
}

// D: a part the guard cannot read is denied only when the command names a
// prompt file as a path element; a longer name holding one is no mention.
func TestPcmGuardMentionsArePathElements(t *testing.T) {
	f := pcmGuardHome(t)
	for _, command := range []string{
		`sed -i s/1/2/ "$F" # .claude-plugin/plugin.json`,
		`echo x > "$F" # ~/.claude.json`,
		`echo x > "$F" # my_templates_x CLAUDE.md.bak`,
	} {
		t.Run(command, func(t *testing.T) { pcmGuardRequireAllow(t, pcmGuardBash(t, command, f.project, "")) })
	}
	for _, command := range []string{
		`sed -i s/1/2/ "$F" # .claude/plugin.json`,
		`echo x > "$F" # see Templates`,
		`echo x > "$F" # docs/claude.md`,
	} {
		pcmGuardRequireDeny(t, pcmGuardBash(t, command, f.project, ""), "could not read this command")
	}
	pcmGuardRequireAllow(t, pcmGuardBash(t, `echo x > "$F" # templates/themes_old`, f.clone, pcmGuardWriter))
}

// Every interpreter write that reaches a prompt file stays denied: an
// attributable target, an escape to a shell or eval, a target the code
// builds, and stdin code behind options.
func TestPcmGuardDeniesInterpreterWritesItCanOrCannotAttribute(t *testing.T) {
	f := pcmGuardHome(t)
	for _, command := range []string{
		`python3 - <<'EOF'` + "\np='CLAUDE.md'\nopen(p,'w').write('x')\nEOF",
		`python3 - <<'EOF'` + "\nfrom pathlib import Path\nPath('.claude/agents/a.md').write_text('x')\nEOF",
		`python3 - <<'EOF'` + "\nimport subprocess\nsubprocess.run(['rm','CLAUDE.md'])\nEOF",
		`python3 - <<'EOF'` + "\nfor p in ['CLAUDE.md']: open(p,'w').write('x')\nEOF",
		`python3 -W ignore <<'EOF'` + "\nopen('CLAUDE.md','w').write('x')\nEOF",
		`python3 -c "import shutil; shutil.copy('/tmp/x', 'CLAUDE.md')"`,
		`python3 -c "import os; os.rename('/tmp/x', '.claude/agents/a.md')"`,
		`python3 -c "p='/tmp/x'; p='CLAUDE.md'; open(p, 'w')"`,
		`python3 -c "p: str = 'CLAUDE.md'; open(p, mode='a')"`,
		`python3 -c "import shutil; s='/tmp/a'; s='/tmp/b'; shutil.copy(s, 'CLAUDE.md')"`,
		`python3 -c "print(f'{open(\"CLAUDE.md\", \"w\")}')"`,
		`python3 -c "import os; os.system('rm CLAUDE.md')"`,
		`python3 -c "o = open; o('CLAUDE.md', 'w')"`,
		`python3 -c "from os import remove; remove('CLAUDE.md')"`,
		`python3 -c "import os; getattr(os, 'remove')('CLAUDE.md')"`,
		`python3 -c "open('CLAUDE.md', m).write('x')"`,
		`python3 -c "import os; os.path.join and open(os.path.join('.claude', 'agents', 'a.md'), 'w')"`,
		`python3 -c "from pathlib import Path; p = Path('CLAUDE.md'); p.unlink()"`,
		`python3 -c "exec(__import__('sys').stdin.read())" <<'EOF'` + "\nopen('CLAUDE.md','w')\nEOF",
		`python3 -i ` + f.script(t, "proj/x.py") + ` <<'EOF'` + "\nopen('CLAUDE.md','w')\nEOF",
		`python3 -X dev - <<'EOF'` + "\nimport os\nos.remove('CLAUDE.md')\nEOF",
		`node -e "const p='CLAUDE.md'; require('fs').writeFileSync(p, 'x')"`,
		`node -e "require('fs')['writeFileSync']('CLAUDE.md', 'x')"`,
		`node -e "require('child_process').execSync('rm CLAUDE.md')"`,
		`node -e "require('fs').rmSync('.claude/agents/a.md')"`,
		`node <<'EOF'` + "\nrequire('fs').appendFileSync('CLAUDE.md', 'x')\nEOF",
		`perl -e 'open(my $f, ">", "CLAUDE.md")'`,
		`perl -e 'open(F, ">>CLAUDE.md")'`,
		`perl -e 'system("rm CLAUDE.md")'`,
		`perl -e 'rename "/tmp/x", "CLAUDE.md"'`,
		`ruby -e 'File.open("CLAUDE.md", "w") { |f| f.puts 1 }'`,
		`ruby -e 'FileUtils.rm_rf ".claude"'`,
		"ruby -e '`rm CLAUDE.md`'",
		`bash -s <<'EOF'` + "\nrm CLAUDE.md\nEOF",
		`bash - <<'EOF'` + "\necho x > .claude/agents/a.md\nEOF",
		`bash -c 'cat' <<'EOF'` + "\nrm CLAUDE.md\nEOF",
		`zsh <<< "rm CLAUDE.md"`,
		`ksh <<'EOF'` + "\npython3 -c \"open('CLAUDE.md','w')\"\nEOF",
		// Bindings across lines, a moved cwd, archives, calls by name, metadata.
		`python3 - <<'EOF'` + "\np = '/tmp/x'\ndef f(\n    p):\n    open(p, 'w')\nf('CLAUDE.md')\nEOF",
		`python3 -c "import os; os.chdir('.claude/agents'); open('a.md', 'w')"`,
		`python3 -c "import shutil; shutil.unpack_archive('/tmp/a.zip', '.claude')"`,
		`python3 -c "import zipfile; zipfile.ZipFile('/tmp/a.zip').extractall('.claude')"`,
		`python3 -c "import operator, pathlib; operator.methodcaller('write_text', 'x')(pathlib.Path('CLAUDE.md'))"`,
		`python3 -c "import os; os.chmod('CLAUDE.md', 0)"`,
		`python3 -c "import shutil; shutil.copy('/tmp/x/CLAUDE.md', '.')"`,
		`node -e "require('fs').chmodSync('CLAUDE.md', 0)"`,
		`node -e "const fs = require('fs'); new fs.WriteStream('CLAUDE.md')"`,
		`perl -e '$^I=""; @ARGV=("CLAUDE.md"); while(<>){print}'`,
		`ruby -e 'File::write("CLAUDE.md", "x")'`,
		`ruby -e '$stdout.reopen("CLAUDE.md", "w"); puts 1'`,
	} {
		t.Run(command, func(t *testing.T) {
			pcmGuardRequireDeny(t, pcmGuardBash(t, command, f.project, ""), pcmGuardOnlyPcm)
		})
	}
	// A protected directory named as a whole literal in code whose target is computed.
	rmtree := `python3 -c "import shutil; b=''; shutil.rmtree(b + 'templates')"`
	t.Run(rmtree, func(t *testing.T) {
		pcmGuardRequireDeny(t, pcmGuardBash(t, rmtree, f.clone, ""), pcmGuardOnlyPcm)
	})
}

// Shell code nested in heredocs is read to the parse bound: a write inside
// it is still found, and past the bound the command is unreadable.
func TestPcmGuardBoundsNestedShellPrograms(t *testing.T) {
	f := pcmGuardHome(t)
	pcmGuardRequireDeny(t, pcmGuardBash(t, pcmGuardNestHeredoc("rm CLAUDE.md", 3), f.project, ""), pcmGuardOnlyPcm)
	payload := pcmGuardBash(t, pcmGuardNestHeredoc("echo x > notes.md # CLAUDE.md", 10), f.project, "")
	pcmGuardRequireDeny(t, payload, "could not read this command")
	if _, stdout, _ := runPcmGuard(payload); strings.Contains(stdout, pcmGuardOnlyPcm) {
		t.Fatalf("stdout=%q, want only the unreadable line: notes.md is no prompt file", stdout)
	}
}

// pcmGuardNestHeredoc feeds command to bash through levels nested heredocs.
func pcmGuardNestHeredoc(command string, levels int) string {
	for i := range levels {
		command = fmt.Sprintf("bash <<'E%[1]d'\n%[2]s\nE%[1]d", i, command)
	}
	return command
}
