package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestClaudePluginsDoctorNamesEachGapPerAccount(t *testing.T) {
	home := t.TempDir()
	complete := filepath.Join(home, ".claude")
	partial := filepath.Join(home, ".cc", "2")
	broken := filepath.Join(home, ".cc", "3")
	fresh := filepath.Join(home, ".cc", "4")
	for _, dir := range []string{complete, partial, broken, fresh} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeSettings := func(dir, content string) {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSettings(complete, `{"enabledPlugins":{"cache-live-control@cache-live-control":true,`+
		`"sub-agent-compact@sub-agent-compact":true},"env":{"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1",`+
		`"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000"}}`)
	writeSettings(partial, `{"enabledPlugins":{"cache-live-control@cache-live-control":true,`+
		`"sub-agent-compact@sub-agent-compact":false},"env":{"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"0"}}`)
	// A directory where the file belongs fails the read for any uid.
	if err := os.MkdirAll(filepath.Join(broken, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{
		{ID: 1, ConfigDir: complete},
		{ID: 2, ConfigDir: partial},
		{ID: 3, ConfigDir: broken},
		{ID: 4, ConfigDir: fresh},
	}}
	var output bytes.Buffer
	// The unreadable file is a failure; each missing plugin or env key a warning.
	tally := &doctorTally{}
	printClaudePluginsDoctor(&output, machine, tally)
	if tally.failures != 1 || tally.warnings != 2 {
		t.Fatalf("failures=%d warnings=%d, want 1 and 2\n%s", tally.failures, tally.warnings, output.String())
	}
	for _, want := range []string{
		"doctor: claude_plugins claude[1] ok\n",
		"doctor: claude_plugins claude[2] plugin sub-agent-compact@sub-agent-compact not enabled — run pfm",
		"doctor: claude_plugins claude[2] env CLAUDE_CODE_AUTO_COMPACT_WINDOW absent — run pfm install --yes",
		"doctor: claude_plugins claude[3] could not read " + filepath.Join(broken, "settings.json"),
		"doctor: claude_plugins claude[4] skipped: no settings.json at " + filepath.Join(fresh, "settings.json"),
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "claude[2] plugin cache-live-control") ||
		strings.Contains(output.String(), "claude[2] env CLAUDE_CODE_ENABLE_FUNCTION_HOOKS") {
		t.Fatalf("a present plugin or env key was reported missing:\n%s", output.String())
	}
}
