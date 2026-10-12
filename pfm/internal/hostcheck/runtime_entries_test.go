package hostcheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnclassifiedPreservesEvidencedRuntimeEntries(t *testing.T) {
	for _, scenario := range []string{"regular", "symlink", "new output", "zsh"} {
		t.Run(scenario, func(t *testing.T) {
			env := fixtureEnv(t)
			for _, name := range []string{"debug", "daemon.lock", "daemon.status.json", "tmp", "chrome", "dev-mods", "keybindings.json", "fixture-plugin", "custom-hook.log", "future-entry"} {
				writeFile(t, filepath.Join(env.Store, name), "data")
			}
			scripts := filepath.Join(env.Store, "scripts")
			target := scripts
			if scenario == "symlink" {
				target = filepath.Join(env.Home, "operator-hooks")
			}
			script := filepath.Join(target, "hook.sh")
			body := "#!/bin/sh\nLOG=\"$HOME/.claude/custom-hook.log\"\necho hello >> \"$LOG\"\n"
			if scenario == "zsh" {
				body = "#!/bin/zsh\nfiles=(/tmp/*.txt(N))\nprintf '%s' $files\n"
			}
			writeFile(t, script, body)
			if scenario == "symlink" {
				if err := os.Symlink(target, scripts); err != nil {
					t.Fatal(err)
				}
			}
			command := filepath.Join(scripts, "hook.sh")
			if scenario == "new output" {
				command += `; printf hello >> "$HOME/.claude/new.log"`
			}
			if scenario == "zsh" {
				command += `; printf hello >> "$HOME/.claude/custom-hook.log"`
			}
			raw, err := json.Marshal(
				map[string]any{
					"hooks": map[string]any{
						"Stop": []any{
							map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}},
						},
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(env.Store, "settings.json"), string(raw))
			writeFile(
				t,
				filepath.Join(env.Store, "plugins", "installed_plugins.json"),
				`{"version":2,"plugins":{"fixture-plugin@market":[{"scope":"user"}]}}`,
			)
			rows := detect(t, "unclassified", env)
			if len(rows) != 1 || rows[0].Path != filepath.Join(env.Store, "future-entry") {
				t.Fatalf("expected only unknown entry, got %+v", rows)
			}
		})
	}
}

func TestRuntimeEvidenceRejectsMalformedOrUnresolvedProvenance(t *testing.T) {
	for _, tc := range []struct{ name, registry, script string }{
		{"malformed registry", `{"plugins":{"fixture-plugin@market":null}}`, "#!/bin/sh\nLOG=\"$UNSET/.claude/custom.log\"\n"},
		{"missing registry object", `{}`, "#!/bin/sh\nLOG=\"$(printf /tmp)/custom.log\"\n"},
		{"script parse failure", `{"plugins":{}}`, "#!/bin/sh\necho \"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := fixtureEnv(t)
			script := filepath.Join(env.Home, "hook.sh")
			writeFile(t, script, tc.script)
			raw, err := json.Marshal(
				map[string]any{
					"hooks": map[string]any{
						"Stop": []any{
							map[string]any{"hooks": []any{map[string]any{"type": "command", "command": script}}},
						},
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(env.Store, "settings.json"), string(raw))
			writeFile(t, filepath.Join(env.Store, "plugins", "installed_plugins.json"), tc.registry)
			writeFile(t, filepath.Join(env.Store, "fixture-plugin"), "data")
			writeFile(t, filepath.Join(env.Store, "custom.log"), "data")
			rows := detect(t, "unclassified", env)
			reported := map[string]bool{}
			unread := false
			for _, row := range rows {
				reported[filepath.Base(row.Path)] = true
				unread = unread || strings.Contains(row.Problem, "UNREADABLE")
			}
			if !reported["fixture-plugin"] || !reported["custom.log"] || !unread {
				t.Fatalf("malformed evidence must remain visible: %+v", rows)
			}
		})
	}
}
