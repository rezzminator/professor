package main

import (
	"bytes"
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
			"testdata/%s: %v\nwant:\n%s\nactual:\n%s\nGOLDEN cmd/pfm/testdata/%s = %q",
			file,
			err,
			want,
			got,
			file,
			got,
		)
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
	stdout.Reset()
	stderr.Reset()
	if code := runConfig([]string{"claude", "--account", "2"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("account code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(
		stdout.String(),
		"configDir wire=env target=CLAUDE_CONFIG_DIR value="+machine.Accounts[1].ConfigDir+" source=account",
	) {
		t.Errorf("account output=%s", stdout.String())
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
