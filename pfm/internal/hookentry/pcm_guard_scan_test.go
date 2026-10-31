package hookentry

import (
	"slices"
	"testing"
)

// A literal is one plain string: interpolation, an escape or anything around
// it leaves it unread.
func TestPcmGuardLiteralReadsOnlyPlainStrings(t *testing.T) {
	for _, tc := range []struct {
		lang, text, want string
		ok               bool
	}{
		{pcmGuardPython, `'CLAUDE.md'`, "CLAUDE.md", true},
		{pcmGuardPython, ` r"a/b" `, "a/b", true},
		{pcmGuardPython, `'''x.md'''`, "x.md", true},
		{pcmGuardPython, `f'{d}/x'`, "", false},
		{pcmGuardPython, `'a\nb'`, "", false},
		{pcmGuardPython, `'a' + 'b'`, "", false},
		{pcmGuardPython, "`x`", "", false},
		{pcmGuardNode, "`x.md`", "x.md", true},
		{pcmGuardNode, "`${d}/x`", "", false},
		{pcmGuardPerl, `'$x'`, "$x", true},
		{pcmGuardPerl, `"$d/x"`, "", false},
		{pcmGuardRuby, `"#{d}/x"`, "", false},
		{pcmGuardRuby, `"x.md"`, "x.md", true},
	} {
		if got, ok := pcmGuardLiteral(tc.lang, tc.text); got != tc.want || ok != tc.ok {
			t.Errorf("pcmGuardLiteral(%s, %q) = %q, %v; want %q, %v", tc.lang, tc.text, got, ok, tc.want, tc.ok)
		}
	}
}

// A python name is followed only through its one plain assignment; every
// other binding, or a second one, leaves it unread.
func TestPcmGuardPyValueFollowsOneBinding(t *testing.T) {
	for _, tc := range []struct {
		code, expr, want string
		ok               bool
	}{
		{"p = '/tmp/x'\nopen(p, 'w')", "p", "/tmp/x", true},
		{"p = Path('/tmp', 'x')", "p", "/tmp/x", true},
		{"q = '/tmp/q'\np = q", "p", "/tmp/q", true},
		{"p = 'a' # note", "p", "a", true},
		{"p = 'a' # p = 'b'", "p", "", false}, // the raw text counts too: over-counted, never missed
		{"", "os.path.join('a', '/b', 'c')", "/b/c", true},
		{"p = '/tmp/x'\np = 'CLAUDE.md'", "p", "", false},
		{"p = '/tmp/x'\np += '/CLAUDE.md'", "p", "", false},
		{"p = '/tmp/x'\na, p = 1, 'CLAUDE.md'", "p", "", false},
		{"p = '/tmp/x'\nfor p in ['CLAUDE.md']: pass", "p", "", false},
		{"p = '/tmp/x'\nwith open('y') as p: pass", "p", "", false},
		{"p = '/tmp/x'\ndef f(p='CLAUDE.md'): pass", "p", "", false},
		{"p = '/tmp/x'\nif (p := 'CLAUDE.md'): pass", "p", "", false},
		{"p = q = '/tmp/x'", "p", "", false},
		{"p = sys.argv[1]", "p", "", false},
		{"", "Path('a').parent", "", false},
	} {
		if got, ok := pcmGuardPyValue(tc.code, tc.expr, 0); got != tc.want || ok != tc.ok {
			t.Errorf("pcmGuardPyValue(%q, %q) = %q, %v; want %q, %v", tc.code, tc.expr, got, ok, tc.want, tc.ok)
		}
	}
}

// The hook runs on every Bash call: code cut off anywhere reads without a
// panic, and a write seen whole is never read as a read when cut short.
func TestPcmGuardWritesSurviveTruncatedCode(t *testing.T) {
	for lang, code := range map[string]string{
		pcmGuardPython: "from pathlib import Path\np = '''/tmp/x'''  # c\nopen(p, mode='w'); os.rename('a', \"b\")\n" +
			"Path('x').write_text(s.upper()); shutil.copy(r'a', os.path.join('b', 'c'))",
		pcmGuardNode: "const fs = require('fs'); fs.openSync(`x`, 'w'); fs.cpSync('a', \"b\", {recursive: true})",
		pcmGuardPerl: `open(my $f, ">", "x") or die; unlink "a", 'b' if 1; copy("a", "b");`,
		pcmGuardRuby: `File.open("x", "w") { |f| f.puts 1 }; FileUtils.cp "a", "b", verbose: true; open("y")`,
	} {
		whole, ok := pcmGuardWrites(lang, code)
		if !ok || len(whole) == 0 {
			t.Fatalf("%s: pcmGuardWrites(whole) = %q, %v; want its writes", lang, whole, ok)
		}
		for i := range len(code) {
			pcmGuardWrites(lang, code[:i])
		}
	}
}

// The tables reach past the brief's minimum: io.FileIO, IO.copy_stream, and
// a module whose writes the tables cannot see sends the code to the reading
// before.
func TestPcmGuardWritesReadTheExtendedTables(t *testing.T) {
	for _, tc := range []struct {
		lang, code string
		want       []string
		ok         bool
	}{
		{pcmGuardPython, "import io; io.FileIO('CLAUDE.md', 'w')", []string{"CLAUDE.md"}, true},
		{pcmGuardPython, "import sqlite3; sqlite3.connect('CLAUDE.md')", nil, false},
		{pcmGuardPython, "x = os; x.remove('CLAUDE.md')", nil, false},
		{pcmGuardRuby, `IO.copy_stream("a", "CLAUDE.md")`, []string{"a", "CLAUDE.md"}, true},
		{pcmGuardRuby, `f = File; f.write("CLAUDE.md", "x")`, nil, false},
		{pcmGuardPerl, `use Path::Tiny; path("CLAUDE.md")->spew("x")`, nil, false},
		{pcmGuardNode, `const {writeFileSync} = require('fs')`, nil, false},
	} {
		if got, ok := pcmGuardWrites(tc.lang, tc.code); !slices.Equal(got, tc.want) || ok != tc.ok {
			t.Errorf("pcmGuardWrites(%s, %q) = %q, %v; want %q, %v", tc.lang, tc.code, got, ok, tc.want, tc.ok)
		}
	}
}

// Arguments split at top-level commas only; quotes and brackets hold theirs.
func TestPcmGuardSplitTopKeepsNestedCommas(t *testing.T) {
	got := pcmGuardSplitTop(`'a,b', f(c, d), [e, f], "g\",h",`, pcmGuardPython)
	want := []string{`'a,b'`, ` f(c, d)`, ` [e, f]`, ` "g\",h"`}
	if !slices.Equal(got, want) {
		t.Fatalf("pcmGuardSplitTop = %q, want %q", got, want)
	}
	if got := pcmGuardSplitTop(" ", pcmGuardPython); len(got) != 0 {
		t.Fatalf("pcmGuardSplitTop(blank) = %q, want none", got)
	}
}
