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

// A knob nobody can read is a knob nobody has: both new keys must show up in
// `pfm config show` with their source, like every other resolved value.
func TestConfigShowDisplaysTheTmuxTitlesAndNameSyncKeys(t *testing.T) {
	home := t.TempDir()
	machine := pfmconfig.Defaults(home, nil)
	var stdout bytes.Buffer
	printResolvedConfig(
		&stdout,
		commandRuntime{
			Config: machine,
			Paths:  paths.Values{Home: home, StateDB: machine.State.DB, CacheDB: machine.State.CacheDB},
		},
	)
	for _, want := range []string{
		"config tmux.titles.enabled=true (default)",
		"config nameSync.interval=15m0s (default)",
		"config state.db=" + machine.State.DB + " (default)",
		"config state.cacheDb=" + machine.State.CacheDB + " (default)",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("config show missing %q:\n%s", want, stdout.String())
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

func TestConfigShowThirdPartyMCP(t *testing.T) {
	for _, tc := range []struct{ name, mcp, want string }{
		{"two from file", `{"thirdParty":{"b":{"type":"stdio","command":"b"},"a":{"type":"stdio","command":"a"}}}`, "config mcp.thirdParty=a,b (file)\n"},
		{"absent", `{}`, "config mcp.thirdParty=none (default)\n"},
		{"empty from file", `{"thirdParty":{}}`, "config mcp.thirdParty=none (default)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, pfmconfig.FileName)
			if err := os.WriteFile(path, []byte(`{"version":2,"mcp":`+tc.mcp+`}`), 0o600); err != nil {
				t.Fatal(err)
			}
			machine, err := pfmconfig.Load(path, home, nil)
			if err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			printResolvedConfig(&stdout, commandRuntime{Config: machine, Paths: paths.Values{Home: home}})
			portLine := fmt.Sprintf(
				"config mcp.http.port=%d (%s)\n",
				machine.MCP.HTTP.Port,
				machine.Source("mcp.http.port"),
			)
			if !strings.Contains(stdout.String(), portLine+tc.want) {
				t.Fatalf("config show lacks consecutive lines %q:\n%s", portLine+tc.want, stdout.String())
			}
		})
	}
}
