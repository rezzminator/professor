package installer

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// TestVSCodePromptLintManifestAndAssets: the command reads "Professor: Prompt
// Lint" in the palette (no icon), no editor/title entry carries it,
// cancelPromptLint is not contributed, the settings carry the settled
// defaults, the prompt and schema are staged beside prompt-lint.js, and the schema
// is one pfm's validator accepts and holds a finding to.
func TestVSCodePromptLintManifestAndAssets(t *testing.T) {
	t.Parallel()
	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/package.json")
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		Command  string `json:"command"`
		Title    string `json:"title"`
		Category string `json:"category"`
		Icon     string `json:"icon"`
		When     string `json:"when"`
		Group    string `json:"group"`
	}
	var manifest struct {
		Contributes struct {
			Commands []entry `json:"commands"`
			Menus    struct {
				CommandPalette []entry         `json:"commandPalette"`
				EditorTitle    json.RawMessage `json:"editor/title"`
			} `json:"menus"`
			Configuration struct {
				Properties map[string]struct {
					Default any `json:"default"`
				} `json:"properties"`
			} `json:"configuration"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	const when = "!remoteName || professor.pfmHere"
	want := entry{Command: "professor.promptLint", Title: "Prompt Lint", Category: "Professor"}
	if !slices.Contains(manifest.Contributes.Commands, want) {
		t.Errorf("commands %+v lack %+v", manifest.Contributes.Commands, want)
	}
	if manifest.Contributes.Menus.EditorTitle != nil {
		t.Errorf(
			"manifest has an editor/title entry %s; the Prompt Lint link is a CodeLens",
			manifest.Contributes.Menus.EditorTitle,
		)
	}
	for _, command := range manifest.Contributes.Commands {
		if command.Command == "professor.cancelPromptLint" {
			t.Errorf("professor.cancelPromptLint is contributed, so the palette lists it: %+v", command)
		}
	}
	if !slices.Contains(
		manifest.Contributes.Menus.CommandPalette,
		entry{Command: "professor.promptLint", When: when},
	) {
		t.Errorf("commandPalette %+v lacks professor.promptLint", manifest.Contributes.Menus.CommandPalette)
	}
	defaults := map[string]any{
		"professor.promptLint.model":   "opus",
		"professor.promptLint.effort":  "medium",
		"professor.promptLint.timeout": 900.0,
		"professor.promptLint.globs": []any{
			"**/SKILL.md", "**/agents/*.md", "**/commands/**/*.md", "**/CLAUDE.md",
			"**/AGENTS.md", "**/*.prompt.md", "**/*.agent.md", "**/*.instructions.md",
		},
	}
	for key, want := range defaults {
		if got := manifest.Contributes.Configuration.Properties[key].Default; !reflect.DeepEqual(got, want) {
			t.Errorf("%s default = %v, want %v", key, got, want)
		}
	}

	files, err := assetFiles(nativeScheduler())
	if err != nil {
		t.Fatal(err)
	}
	staged := map[string]bool{}
	for _, file := range files {
		staged[file.path] = true
	}
	for _, name := range []string{"prompt-lint.js", "prompt-lint.md", "prompt-lint.schema.json"} {
		if !staged[vscodeExtensionSource+"/"+name] {
			t.Errorf("assetFiles() does not stage %s/%s", vscodeExtensionSource, name)
		}
	}

	schemaRaw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/prompt-lint.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("prompt-lint.schema.json does not resolve: %v", err)
	}
	for _, tc := range []struct {
		instance string
		valid    bool
	}{
		{`{"findings":[]}`, true},
		{`{"findings":[{"line":1,"endLine":2,"severity":"info","rule":"cut-test","message":"m","rewrite":"r"}]}`, true},
		{`{"findings":[{"line":0,"endLine":1,"severity":"info","rule":"x","message":"m"}]}`, false},
		{`{"findings":[{"line":1,"endLine":1,"severity":"fatal","rule":"x","message":"m"}]}`, false},
		{`{"findings":[{"line":1,"endLine":1,"severity":"info","rule":"Not Kebab","message":"m"}]}`, false},
		{`{"findings":[{"line":1,"severity":"info","rule":"x","message":"m"}]}`, false},
		{`{}`, false},
	} {
		var instance any
		if err := json.Unmarshal([]byte(tc.instance), &instance); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(instance); (err == nil) != tc.valid {
			t.Errorf("schema validate %s = %v, want valid %v", tc.instance, err, tc.valid)
		}
	}
}
