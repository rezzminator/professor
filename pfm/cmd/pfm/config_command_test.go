package main

import (
	"bytes"
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
	printResolvedConfig(&stdout, commandRuntime{Config: machine, Paths: paths.Values{Home: home}})
	for _, want := range []string{
		"config tmux.titles.enabled=true (default)",
		"config nameSync.interval=15m0s (default)",
		"config claude.autoCompactMain=unset (default)",
		"config claude.autoCompactSubagent=unset (default)",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("config show missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestConfigShowDisplaysTheCompactThresholds(t *testing.T) {
	home := t.TempDir()
	machine := pfmconfig.Defaults(home, nil)
	machine.Claude.AutoCompactMain, machine.Claude.AutoCompactSubagent = 200000, 150000
	machine.Sources["claude.autoCompactMain"] = pfmconfig.SourceFile
	machine.Sources["claude.autoCompactSubagent"] = pfmconfig.SourceFile
	var stdout bytes.Buffer
	printResolvedConfig(&stdout, commandRuntime{Config: machine, Paths: paths.Values{Home: home}})
	for _, want := range []string{
		"config claude.autoCompactMain=200000 (file)",
		"config claude.autoCompactSubagent=150000 (file)",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("config show missing %q:\n%s", want, stdout.String())
		}
	}
}
