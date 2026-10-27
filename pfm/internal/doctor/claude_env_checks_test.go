package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestClaudeEnvDoctor: each claude.env key missing from, or different in, the
// settings file install writes is a FAIL naming both values; a key pfm set and
// claude.env dropped is a FAIL while it stays; a hand-set key already holding
// the claude.env value is ok with no write recorded; an unreadable ledger or
// settings file is a FAIL, never ok; a key no plugin's prefix names is a warning.
func TestClaudeEnvDoctor(t *testing.T) {
	const key = "FORWARD_COMPACTION_CEILING_NATIVE"
	for _, test := range []struct {
		name               string
		env                map[string]string
		settings           string // "" = absent
		ledger             string // "" = absent; {settings} expands
		lines              []string
		failures, warnings int
	}{
		{
			name:  "empty claude.env and no ledger",
			env:   map[string]string{},
			lines: []string{"doctor: claude_env ok — claude.env is empty"},
		},
		{
			name:     "every key in place",
			env:      map[string]string{key: "1"},
			settings: `{"env":{"` + key + `":"1","OTHER":"x"}}`,
			ledger:   `{"version":1,"files":{"{settings}":{"` + key + `":"1"}}}`,
			lines:    []string{"doctor: claude_env ok — 1 keys in {settings}"},
		},
		{
			name:     "explicit install target differs from primary",
			env:      map[string]string{key: "1"},
			settings: `{"env":{"` + key + `":"wrong-primary"}}`,
			ledger:   `{"version":1,"target":"{custom}","files":{"{custom}":{"` + key + `":"1"}}}`,
			lines:    []string{"doctor: claude_env ok — 1 keys in {custom}"},
		},
		{
			name:     "a key missing",
			env:      map[string]string{key: "1"},
			settings: `{"env":{}}`,
			ledger:   `{"version":1,"files":{"{settings}":{"` + key + `":"1"}}}`,
			lines: []string{
				`doctor: claude_env FAIL ` + key + ` missing from {settings} env; claude.env sets "1" — run pfm install --yes`,
			},
			failures: 1,
		},
		{
			name:     "a key with another value",
			env:      map[string]string{key: "1"},
			settings: `{"env":{"` + key + `":"0"}}`,
			ledger:   `{"version":1,"files":{"{settings}":{"` + key + `":"1"}}}`,
			lines: []string{
				`doctor: claude_env FAIL ` + key + ` in {settings} env is "0"; claude.env sets "1" — run pfm install --yes`,
			},
			failures: 1,
		},
		{
			name:     "a dropped key left behind",
			env:      map[string]string{},
			settings: `{"env":{"` + key + `":"1"}}`,
			ledger:   `{"version":1,"files":{"{settings}":{"` + key + `":"1"}}}`,
			lines: []string{
				"doctor: claude_env FAIL " + key + " left in {settings} env after its removal from claude.env — run pfm install --yes",
			},
			failures: 1,
		},
		{
			name:     "a hand-set key holding the claude.env value, no write recorded",
			env:      map[string]string{key: "1"},
			settings: `{"env":{"` + key + `":"1"}}`,
			lines:    []string{"doctor: claude_env ok — 1 keys in {settings}"},
		},
		{
			name:     "an unreadable ledger",
			env:      map[string]string{key: "1"},
			settings: `{}`,
			ledger:   `{bad`,
			lines:    []string{"doctor: claude_env FAIL read the ownership ledger "},
			failures: 1,
		},
		{
			name:     "an unreadable settings file",
			env:      map[string]string{key: "1"},
			settings: `{bad`,
			ledger:   `{"version":1,"files":{"{settings}":{"` + key + `":"1"}}}`,
			lines:    []string{"doctor: claude_env FAIL read {settings}: "},
			failures: 1,
		},
		{
			name:     "the documented shared plugin log knob is known",
			env:      map[string]string{installer.PluginLogDirEnv: "/logs"},
			settings: `{"env":{"PLUGIN_LOG_DIR":"/logs"}}`,
			lines:    []string{"doctor: claude_env ok — 1 keys in {settings}"},
		},
		{
			name:     "an operator log folder needs manual reconciliation, not install",
			env:      map[string]string{installer.PluginLogDirEnv: "/configured-logs"},
			settings: `{"env":{"PLUGIN_LOG_DIR":"/operator-logs"}}`,
			lines:    []string{`doctor: claude_env FAIL PLUGIN_LOG_DIR in {settings} env is "/operator-logs"; claude.env sets "/configured-logs" — set env.PLUGIN_LOG_DIR in {settings} to the claude.env value; pfm preserves hand-set values`},
			failures: 1,
		},
		{
			name:     "a key no plugin's prefix names",
			env:      map[string]string{"NOPE_X": "1"},
			settings: `{"env":{"NOPE_X":"1"}}`,
			ledger:   `{"version":1,"files":{"{settings}":{"NOPE_X":"1"}}}`,
			lines: []string{
				"doctor: claude_env NOPE_X: no plugin pfm installs is named by its prefix — name it {PLUGIN}_{OPTION} after a plugin pfm installs",
				"doctor: claude_env ok — 1 keys in {settings}",
			},
			warnings: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			settings := filepath.Join(home, ".cc", "1", "settings.json")
			custom := filepath.Join(home, "custom", "settings.json")
			expand := func(text string) string {
				return strings.NewReplacer("{settings}", settings, "{custom}", custom).Replace(text)
			}
			if strings.Contains(test.ledger, "{custom}") {
				writePluginLogsFile(t, custom, `{"env":{"`+key+`":"1"}}`)
			}
			if test.settings != "" {
				if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(settings, []byte(test.settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			managedRoot := installer.ManagedRoot(home)
			if test.ledger != "" {
				if err := os.MkdirAll(managedRoot, 0o700); err != nil {
					t.Fatal(err)
				}
				ledger := filepath.Join(managedRoot, "claude-env-ownership.json")
				if err := os.WriteFile(ledger, []byte(expand(test.ledger)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			tally := &doctorTally{}
			printClaudeEnvDoctor(&out, managedRoot, settings, test.env,
				installer.ClaudePluginBuild{Version: "0.80.0", Home: home, Plugins: pfmconfig.DefaultClaudePlugins()},
				tally)
			for _, line := range test.lines {
				if !strings.Contains(out.String(), expand(line)) {
					t.Fatalf("missing %q:\n%s", expand(line), out.String())
				}
			}
			if tally.failures != test.failures || tally.warnings != test.warnings {
				t.Fatalf("failures=%d warnings=%d, want %d %d:\n%s",
					tally.failures, tally.warnings, test.failures, test.warnings, out.String())
			}
			if test.failures > 0 && strings.Contains(out.String(), "claude_env ok") {
				t.Fatalf("a failing check also printed ok:\n%s", out.String())
			}
		})
	}
}

// Missing plugin-log readiness has one doctor owner even when an install ledger
// or explicit claude.env also expects it in that physical settings file.
func TestClaudeEnvPluginLogMissingHasOneRepair(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint("explicit=", explicit), func(t *testing.T) {
			fixture := newPluginLogsFixture(t)
			settings := filepath.Join(fixture.store, "settings.json")
			writePluginLogsFile(t, settings, `{}`)
			managed := installer.ManagedRoot(fixture.home)
			raw, err := json.Marshal(map[string]any{
				"version": 1, "pluginLogDir": fixture.folder,
				"files": map[string]any{settings: map[string]string{installer.PluginLogDirEnv: fixture.folder}},
			})
			if err != nil {
				t.Fatal(err)
			}
			writePluginLogsFile(t, filepath.Join(managed, "claude-env-ownership.json"), string(raw))
			build := installer.ClaudePluginBuild{Version: "0.82.0-alpha", Home: fixture.home, SourceRepo: fixture.clone}
			var env map[string]string
			if explicit {
				env = map[string]string{installer.PluginLogDirEnv: fixture.folder}
			}
			var out bytes.Buffer
			tally := &doctorTally{}
			printClaudeEnvDoctor(&out, managed, settings, env, build, tally)
			printPluginLogsDoctor(&out, []string{fixture.store}, build, &paths.MapEnv{},
				func(string, uint32) error { return nil }, tally)
			if tally.failures+tally.warnings != 1 ||
				strings.Count(out.String(), " — set ")+strings.Count(out.String(), " — run pfm install") != 1 {
				t.Fatalf("one missing PLUGIN_LOG_DIR has multiple findings/repairs: failures=%d warnings=%d\n%s",
					tally.failures, tally.warnings, out.String())
			}
			if !strings.Contains(out.String(), "sets no env.PLUGIN_LOG_DIR — set it to the shared log folder") {
				t.Fatalf("missing actionable log repair: %s", out.String())
			}
		})
	}
}
