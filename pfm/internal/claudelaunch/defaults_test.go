package claudelaunch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestParseDefaults(t *testing.T) {
	for _, row := range []struct {
		name, raw, err string
		want           launchDefaults
	}{
		{
			name: "every section with its reasons",
			raw: `{"settings":{"env":{"B":"2","A":"1"},"style":"x","flags":{"k": [1, 2]}},` +
				`"registry":{"arrow":false},"reasons":{"settings.env.A":"a","settings.env.B":"b",` +
				`"settings.style":"s","settings.flags":"f","registry.arrow":"r"}}`,
			want: launchDefaults{
				settings: []defaultValue{
					{"flags", json.RawMessage(`{"k":[1,2]}`), "f"},
					{"style", json.RawMessage(`"x"`), "s"},
				},
				env: []defaultValue{
					{"A", json.RawMessage(`"1"`), "a"},
					{"B", json.RawMessage(`"2"`), "b"},
				},
				registry: []defaultValue{{"arrow", json.RawMessage(`false`), "r"}},
			},
		},
		{name: "a settings value without a reason", raw: `{"settings":{"style":"x"}}`, err: "settings.style has no reason"},
		{name: "an env variable without a reason", raw: `{"settings":{"env":{"A":"1"}}}`, err: "settings.env.A has no reason"},
		{name: "a registry value without a reason", raw: `{"registry":{"arrow":false}}`, err: "registry.arrow has no reason"},
		{
			name: "a reason naming no value",
			raw:  `{"settings":{"style":"x"},"reasons":{"settings.style":"s","settings.gone":"g"}}`,
			err:  "reasons name no value: [settings.gone]",
		},
		{
			name: "an env variable that is not a string",
			raw:  `{"settings":{"env":{"A":1}},"reasons":{"settings.env.A":"a"}}`,
			err:  "settings.env.A is not a string",
		},
		{name: "an env that is not an object", raw: `{"settings":{"env":"A=1"}}`, err: "settings.env is not an object" +
			" (json: cannot unmarshal string into Go value of type map[string]interface {})"},
		{name: "an unknown section", raw: `{"globals":{}}`, err: `decode: json: unknown field "globals"`},
	} {
		t.Run(row.name, func(t *testing.T) {
			got, err := parseDefaults([]byte(row.raw))
			if row.err != "" {
				if err == nil || err.Error() != row.err {
					t.Fatalf("parseDefaults(%s) error = %v, want %q", row.raw, err, row.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, row.want) {
				t.Fatalf("parseDefaults(%s) = %#v, %v, want %#v", row.raw, got, err, row.want)
			}
		})
	}
}

// TestDefaultsReachEveryLaunch: every value of the shipped defaults file reaches every managed launch and
// every headless run unchanged, so none is shadowed by a key pfm computes.
func TestDefaultsReachEveryLaunch(t *testing.T) {
	var file struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(defaultsJSON, &file); err != nil {
		t.Fatalf("decode defaults.json: %v", err)
	}
	if len(file.Settings) == 0 {
		t.Fatal("defaults.json carries no settings")
	}
	check := func(t *testing.T, payload map[string]any) {
		t.Helper()
		for key, want := range file.Settings {
			if key != settingsEnvKey {
				if !reflect.DeepEqual(payload[key], want) {
					t.Errorf("--settings %s = %#v, want %#v from defaults.json", key, payload[key], want)
				}
				continue
			}
			env, _ := payload[settingsEnvKey].(map[string]any)
			for name, value := range want.(map[string]any) {
				if env[name] != value {
					t.Errorf("--settings env %s = %#v, want %#v from defaults.json", name, env[name], value)
				}
			}
		}
	}
	home, machine := renderMachine(t)
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
		_, parsed := renderParsed(t, Request{Purpose: purpose, Home: home, Account: 2, SessionID: "S"}, machine)
		check(t, parsed.Settings)
	}
	headless, err := RenderHeadless(nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(headless), &payload); err != nil {
		t.Fatalf("headless --settings %q: %v", headless, err)
	}
	check(t, payload)
}

// TestSettingsTemplateCarriesTheDefaults: the project template pfm init copies into a project as
// pfm.config.json, and the machine file's example pfm install seeds, list every shipped launch default
// at its value under claude.settings, so either file shows them all; a default added to defaults.json
// and not to both fails here.
func TestSettingsTemplateCarriesTheDefaults(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	repo := filepath.Join(filepath.Dir(source), "..", "..", "..")
	var file struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(defaultsJSON, &file); err != nil {
		t.Fatalf("decode defaults.json: %v", err)
	}
	for _, path := range []string{
		filepath.Join(repo, "templates", "project", "pfm.config.json"),
		filepath.Join(repo, "example.pfm.config.json"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Claude struct {
				Settings map[string]any `json:"settings"`
			} `json:"claude"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if !reflect.DeepEqual(config.Claude.Settings, file.Settings) {
			t.Errorf(
				"%s claude.settings = %#v, want defaults.json settings %#v",
				path,
				config.Claude.Settings,
				file.Settings,
			)
		}
	}
}
