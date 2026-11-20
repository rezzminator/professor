package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestConfigShowNamesEveryKeyAndItsSource(t *testing.T) {
	var shown strings.Builder
	for _, body := range []string{
		`{"version":2}`,
		`{"version":2,"mcp":{"thirdParty":{"b":{"type":"stdio","command":"b"},"a":{"type":"stdio","command":"a"}}},` +
			`"doctor":{"ignoreWarnings":["vscode-link","vscode-index"]},"chat":{"backend":"tmux"}}`,
		`{"version":2,"mcp":{"thirdParty":{}},"chat":{"backend":"auto"}}`,
		`{"version":2,"chat":{"backend":"herdr"}}`,
	} {
		home := t.TempDir()
		path := filepath.Join(home, pfmconfig.FileName)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		machine, err := pfmconfig.Load(path, home, nil)
		if err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		printResolvedConfig(&stdout, commandRuntime{Config: machine, Paths: paths.Values{Home: home}})
		shown.WriteString("## " + body + "\n" + rooted(stdout.String(), home))
	}
	t.Setenv(paths.EnvStateDB, "/env/state.db")
	t.Setenv(paths.EnvCacheDB, "/env/cache.db")
	home := t.TempDir()
	var stdout bytes.Buffer
	printResolvedConfig(&stdout, commandRuntime{
		Config: pfmconfig.Defaults(home, nil),
		Paths:  paths.Values{StateDB: "/env/state.db", CacheDB: "/env/cache.db"},
	})
	shown.WriteString("## env state databases\n" + rooted(stdout.String(), home))
	golden(t, "config_show.txt", shown.String())
}

// golden compares got with testdata/{file}.
func golden(t *testing.T, file, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil || string(want) != got {
		t.Fatalf(
			"testdata/%s: %v · %s\nwant:\n%s\nactual:\n%s\nGOLDEN cmd/pfm/testdata/%s = %q",
			file,
			err,
			goldenFirstDifference(string(want), got),
			want,
			got,
			file,
			got,
		)
	}
}

// goldenFirstDifference names the first line a golden and the output disagree on, so the failure's
// first line carries it where only that line survives (a CI log's per-test excerpt).
func goldenFirstDifference(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	for index := 0; index < len(wantLines) || index < len(gotLines); index++ {
		var wantLine, gotLine string
		if index < len(wantLines) {
			wantLine = wantLines[index]
		}
		if index < len(gotLines) {
			gotLine = gotLines[index]
		}
		if index >= len(wantLines) || index >= len(gotLines) || wantLine != gotLine {
			return fmt.Sprintf("line %d: actual %q, want %q", index+1, gotLine, wantLine)
		}
	}
	return "no line differs"
}

func TestGoldenFirstDifferenceNamesTheFirstLineThatDiffers(t *testing.T) {
	for _, row := range []struct{ want, got, diff string }{
		{"a\nb\n", "a\nb\n", "no line differs"},
		{"a\nb\nc\n", "a\nB\nc\n", `line 2: actual "B", want "b"`},
		{"a\nb\n", "a\n", `line 2: actual "", want "b"`},
	} {
		if got := goldenFirstDifference(row.want, row.got); got != row.diff {
			t.Errorf("goldenFirstDifference(%q, %q) = %q, want %q", row.want, row.got, got, row.diff)
		}
	}
}

func TestConfigClaude(t *testing.T) {
	home := t.TempDir()
	machine := pfmconfig.Defaults(
		home,
		[]string{home + "/.claude/projects/one", home + "/.cc/2/projects/two", home + "/.cc/3/projects/three"},
	)
	runtime := commandRuntime{Config: machine, Paths: paths.Values{Home: home}}
	var stdout, stderr bytes.Buffer
	if code := runConfig([]string{"claude"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("machine code=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{"cache1h wire=env", "source=constant", "source=default"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("machine output lacks %q", want)
		}
	}
	if !strings.HasSuffix(stdout.String(), "\nextra claude.settings: none\n") {
		t.Errorf("machine output does not end with the empty extra section:\n%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	// claude.settings keys no knob names reach every launch, so the view lists them after the rows.
	runtime.Config.Claude.Settings = map[string]any{
		"env": map[string]any{
			"MY_TOOL_FLAG":               "on",
			"MY_TOOL_OFF":                nil,
			"CLAUDE_CODE_TMUX_TRUECOLOR": nil,
		},
		"spinnerTipsEnabled": false,
	}
	if code := runConfig([]string{"claude", "--account", "2"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("account code=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{
		"configDir wire=env target=CLAUDE_CONFIG_DIR value=" + machine.Accounts[1].ConfigDir + " source=account\n",
		"\nenv.CLAUDE_CODE_TMUX_TRUECOLOR wire=settings target=env.CLAUDE_CODE_TMUX_TRUECOLOR value=unset source=config\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("account output lacks %q:\n%s", want, stdout.String())
		}
	}
	wantExtra := "\nextra claude.settings: 2 (no knob above; every launch carries them)\n" +
		"env.MY_TOOL_FLAG wire=settings target=env.MY_TOOL_FLAG value=on source=config\n" +
		"spinnerTipsEnabled wire=settings target=spinnerTipsEnabled value=false source=config\n"
	if !strings.HasSuffix(stdout.String(), wantExtra) {
		t.Errorf("account output does not end with %q:\n%s", wantExtra, stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runConfig([]string{"claude", "--account", "99"}, &stdout, &stderr, runtime); code != 2 {
		t.Errorf("unknown account code=%d", code)
	}
	if got := stderr.String(); got != "pfm config claude: account 99 is not configured (configured: 1,2,3)\n" {
		t.Errorf("unknown account stderr=%q", got)
	}
}
