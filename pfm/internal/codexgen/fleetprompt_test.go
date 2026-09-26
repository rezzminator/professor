package codexgen

import (
	"path/filepath"
	"strings"
	"testing"
)

// fleetPromptMarkers are lines from each of the composed prompt's three parts
// — shared head, Codex middle, shared tail — so a role carrying only one part
// is as loud a failure as a role carrying none.
var fleetPromptMarkers = []string{"# Model Selection", "NEVER change the active account", "# The Verdict"}

// A compiled role file holds its own body only: the fleet prompt lives in each
// Codex home's config.toml, and a --agent-role seat composes it at launch
// (agentrole). Both compilers — project roles and machine-global roles — are
// asserted from the file they wrote.
func TestNoCompiledCodexRoleCarriesTheFleetPrompt(t *testing.T) {
	prompt, err := codexFleetPrompt()
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range fleetPromptMarkers {
		if !strings.Contains(prompt, marker) {
			t.Fatalf("the composed Codex prompt does not carry %q, so this test cannot pin it", marker)
		}
	}
	root := t.TempDir()
	home := t.TempDir()
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "# Fixture\n")
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "agents", "dev.md"),
		"---\nname: dev\ndescription: Project role.\ntools: Read\nmodel: sonnet\n---\n\nPROJECT-ROLE-BODY\n",
	)
	writeTestFile(
		t,
		filepath.Join(home, ".professor", "templates", "global", "agents", "gamma.md"),
		"---\nname: gamma\ndescription: Global role.\ntools: Read\nmodel: sonnet\n---\n\nGLOBAL-ROLE-BODY\n",
	)
	if result, err := Run(Options{Root: root, Home: home, Mode: ModeBuild}); err != nil || !result.OK {
		t.Fatalf("build: result=%#v err=%v", result, err)
	}
	if _, err := RunGlobalAgents(GlobalAgentsOptions{Home: home}); err != nil {
		t.Fatalf("RunGlobalAgents: %v", err)
	}
	cases := []struct {
		name string
		path string
		body string
	}{
		{name: "project role", path: filepath.Join(root, ".codex", "agents", "dev.toml"), body: "PROJECT-ROLE-BODY"},
		{
			name: "global role",
			path: filepath.Join(filepath.Join(home, ".codex", "agents"), "gamma.toml"),
			body: "GLOBAL-ROLE-BODY",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := string(mustReadTestFile(t, testCase.path))
			for _, marker := range fleetPromptMarkers {
				if strings.Contains(got, marker) {
					t.Fatalf(
						"%s carries the fleet prompt line %q — a role file holds its own body only",
						testCase.path,
						marker,
					)
				}
			}
			if !strings.Contains(got, testCase.body) {
				t.Fatalf("%s lost its own body", testCase.path)
			}
			if err := validateTOML(got); err != nil {
				t.Fatalf("%s does not parse as TOML : %v", testCase.path, err)
			}
		})
	}
}
