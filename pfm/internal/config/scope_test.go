package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The machine file reads the scope keys with the project reader's validation.
func TestMachineScopeKeys(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{"valid", `{"envs":{"TEAM_MODE":"strict"},"harnessPrompt":{"extend":"team.md"},` +
			`"claude":{"settings":{"env":{"X":"1"},"cleanupPeriodDays":9}}}`, ""},
		{"null keys", `{"envs":null,"harnessPrompt":null,"claude":{"settings":null}}`, ""},
		{"reserved env", `{"envs":{"PFM_STATE_DB":"/x"}}`, "config {path}: envs.PFM_STATE_DB is set by pfm itself on launch"},
		{"NUL env", `{"envs":{"TEAM_MODE":"a\u0000b"}}`, "config {path}: envs.TEAM_MODE must not contain NUL"},
		{"lower-case env", `{"envs":{"team":"x"}}`, `config {path}: envs name "team" must be upper snake: A-Z, 0-9 and _, starting with a letter`},
		{"both prompt keys", `{"harnessPrompt":{"extend":"a.md","replace":"b.md"}}`, `config {path}: harnessPrompt sets both "extend" and "replace": keep one`},
		{"neither prompt key", `{"harnessPrompt":{}}`, `config {path}: harnessPrompt needs "extend" or "replace"`},
		{"settings not an object", `{"claude":{"settings":["x"]}}`, "config {path}: claude.settings: not a JSON object (json: cannot unmarshal array into Go value of type map[string]interface {})"},
		{"settings hooks not an object", `{"claude":{"settings":{"hooks":[]}}}`, `config {path}: claude.settings: "hooks" is not a JSON object`},
		{"settings reserved env", `{"claude":{"settings":{"env":{"CODEX_HOME":"/x"}}}}`, "config {path}: claude.settings.env.CODEX_HOME is set by pfm itself on launch"},
		{"account settings", `{"accounts":[{"id":1,"configDir":"~/one","claude":{"settings":{}}}]}`, "config {path}: accounts[0].settings is machine-wide: set claude.settings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path, t.TempDir(), nil)
			want := strings.ReplaceAll(tc.want, "{path}", path)
			if tc.want != "" {
				if err == nil || err.Error() != want {
					t.Fatalf("Load error = %v, want %q", err, want)
				}
				// ReadMachineScope validates the scope keys alone; an account block is Load's.
				if _, err := ReadMachineScope(
					path,
				); tc.name != "account settings" &&
					(err == nil || err.Error() != want) {
					t.Fatalf("ReadMachineScope error = %v, want %q", err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			scope, err := ReadMachineScope(path)
			if err != nil || !reflect.DeepEqual(scope, loaded.Scope()) {
				t.Fatalf("ReadMachineScope = %#v, %v; Load's scope %#v", scope, err, loaded.Scope())
			}
			if tc.name == "null keys" {
				if !reflect.DeepEqual(scope, Scope{Path: path}) || loaded.Source(KeyEnvs) != SourceDefault {
					t.Fatalf("null scope keys = %#v, envs source %s", scope, loaded.Source(KeyEnvs))
				}
				return
			}
			wantScope := Scope{
				Path: path, Envs: map[string]string{"TEAM_MODE": "strict"},
				HarnessPrompt:  &HarnessPrompt{Key: "extend", Relative: "team.md", Dir: filepath.Dir(path)},
				ClaudeSettings: map[string]any{"env": map[string]any{"X": "1"}, "cleanupPeriodDays": float64(9)},
			}
			if !reflect.DeepEqual(scope, wantScope) {
				t.Fatalf("scope = %#v, want %#v", scope, wantScope)
			}
			for _, key := range []string{KeyEnvs, KeyHarnessPrompt, KeyClaudeSettings} {
				if loaded.Source(key) != SourceFile {
					t.Errorf("source of %s = %s, want file", key, loaded.Source(key))
				}
			}
			encoded, err := Marshal(loaded, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			if again, err := ReadMachineScope(path); err != nil || !reflect.DeepEqual(again, wantScope) {
				t.Fatalf("scope after a Marshal round trip = %#v, %v", again, err)
			}
		})
	}
}

// A project file is read for the scope keys alone; every other key it sets is a warning, never read.
func TestReadProjectScope(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		legacy        bool
		wantEnvs      map[string]string
		warnings      []string
		err           string
	}{
		{name: "absent"},
		{
			name: "scope keys only", content: `{"envs":{"TEAM_MODE":"strict"},"claude":{"settings":{}}}`,
			wantEnvs: map[string]string{"TEAM_MODE": "strict"},
		},
		{
			name:     "machine-only keys",
			content:  `{"version":2,"theme":"x","envs":{"TEAM_MODE":"strict"},"claude":{"binary":"c","cache1h":false}}`,
			wantEnvs: map[string]string{"TEAM_MODE": "strict"},
			warnings: []string{
				"claude.binary is machine-only; ignored in {path}",
				"claude.cache1h is machine-only; ignored in {path}",
				"theme is machine-only; ignored in {path}",
				"version is machine-only; ignored in {path}",
			},
		},
		{
			name: "installPing is machine-only", content: `{"installPing":false,"envs":{"TEAM_MODE":"strict"}}`,
			wantEnvs: map[string]string{"TEAM_MODE": "strict"},
			warnings: []string{"installPing is machine-only; ignored in {path}"},
		},
		{
			name: "retired key", content: `{"claude":{"helperModel":"m"}}`,
			warnings: []string{
				"claude.helperModel is retired: set claude.settings.env.ANTHROPIC_SMALL_FAST_MODEL instead; ignored in {path}",
			},
		},
		{
			name: "leftover settings file", legacy: true,
			warnings: []string{"{root}: pfm.settings.json is no longer read: move its keys into pfm.config.json"},
		},
		{name: "unknown key", content: `{"claude":{"permissions":{}}}`, err: `parse config {path}: json: unknown field "permissions"`},
		{name: "invalid scope key", content: `{"envs":{"CODEX_HOME":"/x"}}`, err: "config {path}: envs.CODEX_HOME is set by pfm itself on launch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := PathIn(root)
			if tc.content != "" {
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.legacy {
				if err := os.WriteFile(filepath.Join(root, RetiredProjectFile), []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			scope, warnings, err := ReadProjectScope(root, filepath.Join(t.TempDir(), FileName))
			replace := strings.NewReplacer("{path}", path, "{root}", root).Replace
			if tc.err != "" {
				if err == nil || err.Error() != replace(tc.err) {
					t.Fatalf("error = %v, want %q", err, replace(tc.err))
				}
				return
			}
			want := make([]string, 0, len(tc.warnings))
			for _, warning := range tc.warnings {
				want = append(want, replace(warning))
			}
			if err != nil || !reflect.DeepEqual(scope.Envs, tc.wantEnvs) || !reflect.DeepEqual(warnings, want) &&
				len(warnings)+len(want) != 0 {
				t.Fatalf(
					"ReadProjectScope = %#v, %q, %v; want envs %v, warnings %q",
					scope,
					warnings,
					err,
					tc.wantEnvs,
					want,
				)
			}
		})
	}
}

// A project root whose pfm.config.json is the machine file reads nothing as a project scope.
func TestReadProjectScopeOfTheMachineFile(t *testing.T) {
	root := t.TempDir()
	content := `{"version":2,"theme":"x","envs":{"TEAM_MODE":"strict"}}`
	if err := os.WriteFile(PathIn(root), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), FileName)
	if err := os.Symlink(PathIn(root), link); err != nil {
		t.Fatal(err)
	}
	for _, machine := range []string{PathIn(root), link} {
		scope, warnings, err := ReadProjectScope(root, machine)
		if err != nil || !reflect.DeepEqual(scope, Scope{Path: PathIn(root)}) || len(warnings) != 0 {
			t.Fatalf("machine %s: ReadProjectScope = %#v, %q, %v; want an empty scope", machine, scope, warnings, err)
		}
	}
	// A project file linking to the machine file is the machine's too.
	linked := t.TempDir()
	if err := os.Symlink(PathIn(root), PathIn(linked)); err != nil {
		t.Fatal(err)
	}
	if scope, warnings, err := ReadProjectScope(linked, PathIn(root)); err != nil ||
		!reflect.DeepEqual(scope, Scope{Path: PathIn(linked)}) || len(warnings) != 0 {
		t.Fatalf("linked project file: ReadProjectScope = %#v, %q, %v; want an empty scope", scope, warnings, err)
	}
	scope, warnings, err := ReadProjectScope(root, filepath.Join(t.TempDir(), FileName))
	if err != nil || scope.Envs["TEAM_MODE"] != "strict" || len(warnings) != 2 {
		t.Fatalf("another machine file: ReadProjectScope = %#v, %q, %v", scope, warnings, err)
	}
}

// A project file that is a link or no regular file is refused, never followed.
func TestReadProjectScopeRefusesALink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, PathIn(root)); err != nil {
		t.Fatal(err)
	}
	_, _, err := ReadProjectScope(root, "")
	if want := "config " + PathIn(root) + ": not a regular file"; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}
