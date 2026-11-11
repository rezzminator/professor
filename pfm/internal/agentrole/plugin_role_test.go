package agentrole

import (
	"path/filepath"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// A cc seat's <plugin>:<agent> role reads the agent from the folder Claude Code
// loads the plugin from, through the config dir's plugin records; a plain role
// still walks the .claude/agents ladder.
func TestResolveClaudePluginRole(t *testing.T) {
	for _, test := range []struct {
		name     string
		role     string
		prepare  func(t *testing.T, config, market, cache, repo string)
		wantBody string
		wantPath func(config, market, cache, repo string) string
		wantErr  func(config, market, cache, repo string) string
	}{
		{
			name: "plugin role found in a directory marketplace",
			role: "flights:foreman",
			prepare: func(t *testing.T, config, market, _, _ string) {
				enablePlugin(t, config, "flights@professor", true)
				knowDirectoryMarketplace(t, config, market)
				mustWrite(t, filepath.Join(market, "plugins", "flights", "agents", "foreman.md"),
					"---\nname: foreman\n---\nBuild it.\n")
			},
			wantBody: "Build it.\n",
			wantPath: func(_, market, _, _ string) string {
				return filepath.Join(market, "plugins", "flights", "agents", "foreman.md")
			},
		},
		{
			name: "plugin role found by its frontmatter name in the recorded install",
			role: "flights:lander",
			prepare: func(t *testing.T, config, _, cache, _ string) {
				enablePlugin(t, config, "flights@professor", true)
				mustWrite(t, filepath.Join(config, "plugins", "installed_plugins.json"),
					`{"version":2,"plugins":{"flights@professor":[{"scope":"user","installPath":"`+cache+`"}]}}`)
				mustWrite(t, filepath.Join(cache, "agents", "land.md"), "---\nname: lander\n---\nLand it.\n")
			},
			wantBody: "Land it.\n",
			wantPath: func(_, _, cache, _ string) string { return filepath.Join(cache, "agents", "land.md") },
		},
		{
			name: "plugin not enabled",
			role: "flights:foreman",
			prepare: func(t *testing.T, config, market, _, _ string) {
				enablePlugin(t, config, "flights@professor", false)
				knowDirectoryMarketplace(t, config, market)
			},
			wantErr: func(config, _, _, _ string) string {
				return `agent role "flights:foreman": plugin flights is not enabled in ` +
					filepath.Join(config, "settings.json")
			},
		},
		{
			name: "plugin record unreadable",
			role: "flights:foreman",
			prepare: func(t *testing.T, config, _, _, _ string) {
				enablePlugin(t, config, "flights@professor", true)
				mustWrite(t, filepath.Join(config, "plugins", "known_marketplaces.json"), "{")
			},
			wantErr: func(config, _, _, _ string) string {
				return `agent role "flights:foreman": could not read the records of plugin flights: parse ` +
					filepath.Join(config, "plugins", "known_marketplaces.json") + ": unexpected end of JSON input"
			},
		},
		{
			name: "enabled plugin without the agent",
			role: "flights:hunter",
			prepare: func(t *testing.T, config, market, _, _ string) {
				enablePlugin(t, config, "flights@professor", true)
				knowDirectoryMarketplace(t, config, market)
				mustWrite(t, filepath.Join(market, "plugins", "flights", "agents", "foreman.md"),
					"---\nname: foreman\n---\nBuild it.\n")
			},
			wantErr: func(_, market, _, _ string) string {
				return "agent role: no claude role \"flights:hunter\" found\n  searched flights@professor: " +
					filepath.Join(market, "plugins", "flights", "agents") + " has no agent hunter"
			},
		},
		{
			name: "plugin role with an empty agent half",
			role: "flights:",
			prepare: func(t *testing.T, config, market, _, _ string) {
				enablePlugin(t, config, "flights@professor", true)
				knowDirectoryMarketplace(t, config, market)
				mustWrite(t, filepath.Join(market, "plugins", "flights", "agents", "foreman.md"),
					"---\n---\nBuild it.\n")
			},
			wantErr: func(_, _, _, _ string) string {
				return `agent role "flights:": want <plugin>:<agent>, each one name`
			},
		},
		{
			name: "plugin role whose agent half leaves the agents folder",
			role: "flights:../foreman",
			prepare: func(t *testing.T, config, market, _, _ string) {
				enablePlugin(t, config, "flights@professor", true)
				knowDirectoryMarketplace(t, config, market)
				mustWrite(t, filepath.Join(market, "plugins", "flights", "foreman.md"),
					"---\nname: foreman\n---\nOutside.\n")
			},
			wantErr: func(_, _, _, _ string) string {
				return `agent role "flights:../foreman": want <plugin>:<agent>, each one name`
			},
		},
		{
			name: "plain role unchanged",
			role: "gitter",
			prepare: func(t *testing.T, config, _, _, repo string) {
				enablePlugin(t, config, "flights@professor", true)
				mustWrite(t, filepath.Join(repo, ".claude", "agents", "gitter.md"), "---\nname: gitter\n---\nCommit.\n")
			},
			wantBody: "Commit.\n",
			wantPath: func(_, _, _, repo string) string { return filepath.Join(repo, ".claude", "agents", "gitter.md") },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, market, cache, repo, home := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", config)
			test.prepare(t, config, market, cache, repo)
			body, artifact, err := Resolve(pfmengine.Claude, test.role, repo, home)
			if test.wantErr != nil {
				if want := test.wantErr(config, market, cache, repo); err == nil || err.Error() != want {
					t.Fatalf("Resolve(%q) err = %v, want %q", test.role, err, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q): %v", test.role, err)
			}
			if want := test.wantPath(config, market, cache, repo); body != test.wantBody || artifact.Path != want {
				t.Fatalf(
					"Resolve(%q) = %q from %s, want %q from %s",
					test.role,
					body,
					artifact.Path,
					test.wantBody,
					want,
				)
			}
		})
	}
}

// enablePlugin writes config's settings.json with id enabled or disabled.
func enablePlugin(t *testing.T, config, id string, enabled bool) {
	t.Helper()
	value := "false"
	if enabled {
		value = "true"
	}
	mustWrite(t, filepath.Join(config, "settings.json"), `{"enabledPlugins":{"`+id+`":`+value+`}}`)
}

// knowDirectoryMarketplace records market as the professor directory
// marketplace listing flights at ./plugins/flights.
func knowDirectoryMarketplace(t *testing.T, config, market string) {
	t.Helper()
	mustWrite(t, filepath.Join(config, "plugins", "known_marketplaces.json"),
		`{"professor":{"source":{"source":"directory","path":"`+market+`"},"installLocation":"`+market+`"}}`)
	mustWrite(t, filepath.Join(market, ".claude-plugin", "marketplace.json"),
		`{"name":"professor","plugins":[{"name":"flights","source":"./plugins/flights"}]}`)
}
