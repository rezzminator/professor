package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtensionsLoad(t *testing.T) {
	for _, tc := range []struct{ name, block, field string }{
		{"on", `{"ponytail":{"enabled":true,"engines":["claude","codex","opencode"],"env":{"PONYTAIL_SUBAGENT_MATCHER":"^worker$"},"options":{"ledger":false,"sendNames":false,"webhookUrl":""}}}`, ""},
		{"off", `{"ponytail":{"enabled":false,"engines":[]}}`, ""},
		{"unknown", `{"other":{"enabled":true,"engines":[]}}`, "extensions.other"},
		{"array", `[]`, "extensions"},
		{"null", `null`, "extensions"},
		{"entry", `{"ponytail":true}`, "extensions.ponytail"},
		{"enabled", `{"ponytail":{"enabled":"true","engines":[]}}`, "extensions.ponytail.enabled"},
		{"missing switch", `{"ponytail":{"engines":[]}}`, "extensions.ponytail.enabled"},
		{"engine", `{"ponytail":{"enabled":true,"engines":["other"]}}`, "extensions.ponytail.engines[0]"},
		{"engines shape", `{"ponytail":{"enabled":true,"engines":"codex"}}`, "extensions.ponytail.engines"},
		{"engine type", `{"ponytail":{"enabled":true,"engines":[1]}}`, "extensions.ponytail.engines"},
		{"env shape", `{"ponytail":{"enabled":true,"engines":[],"env":[]}}`, "extensions.ponytail.env"},
		{"env value", `{"ponytail":{"enabled":true,"engines":[],"env":{"PONYTAIL_X":false}}}`, "extensions.ponytail.env.PONYTAIL_X"},
		{"env null", `{"ponytail":{"enabled":true,"engines":[],"env":{"PONYTAIL_X":null}}}`, "extensions.ponytail.env.PONYTAIL_X"},
		{"env prefix", `{"ponytail":{"enabled":true,"engines":[],"env":{"OTHER_X":"x"}}}`, "extensions.ponytail.env.OTHER_X"},
		{"env case", `{"ponytail":{"enabled":true,"engines":[],"env":{"PONYTAIL_x":"x"}}}`, "extensions.ponytail.env.PONYTAIL_x"},
		{"options", `{"ponytail":{"enabled":true,"engines":[],"options":[]}}`, "extensions.ponytail.options"},
		{"unknown field", `{"ponytail":{"enabled":true,"engines":[],"extra":true}}`, "extensions.ponytail.extra"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, FileName)
			if err := os.WriteFile(path, []byte(`{"version":2,"extensions":`+tc.block+`}`), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path, home, nil)
			if tc.field != "" {
				if err == nil || !strings.Contains(err.Error(), tc.field) {
					t.Fatalf("want field %s, got %v", tc.field, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid extension refused: %v", err)
			}
			raw, err := Marshal(got, false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `"extensions"`) {
				t.Fatalf("extension lost on marshal: %s", raw)
			}
		})
	}
}

func TestExtensionOptionsPreserveLargeNumbers(t *testing.T) {
	const large = "9007199254740993"
	extensions, err := decodeExtensions(
		json.RawMessage(`{"ponytail":{"enabled":true,"engines":["claude"],"options":{"id":` + large + `}}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := extensions["ponytail"].Options["id"]; got != json.Number(large) {
		t.Fatalf("option rounded on ingestion: %v (%T)", got, got)
	}
	var out bytes.Buffer
	(Config{Extensions: extensions}).ReportExtensions(&out)
	if !strings.Contains(out.String(), large) {
		t.Fatalf("option rounded on display: %s", &out)
	}
}

func TestExtensionReportsRedactSeparatedSecretNames(t *testing.T) {
	machine := Config{Extensions: map[string]Extension{"ponytail": {
		Enabled: true, Engines: []string{"codex"},
		Env:     map[string]string{"PONYTAIL_API_KEY": "fixture-secret-env"},
		Options: map[string]any{"api-key": "fixture-secret-option"},
	}}}
	var out bytes.Buffer
	machine.ReportExtensions(&out)
	if strings.Contains(out.String(), "fixture-secret-") || !strings.Contains(out.String(), "redacted") {
		t.Fatalf("extension credential leaked: %s", &out)
	}
}

func TestExtensionEnvironmentOwnership(t *testing.T) {
	for _, mode := range []string{"on", "off", "dropped"} {
		t.Run(mode, func(t *testing.T) {
			ext := Extension{
				Enabled: mode != "off",
				Engines: []string{"codex"},
				Env:     map[string]string{"PONYTAIL_SUBAGENT_MATCHER": "configured"},
			}
			if mode == "dropped" {
				ext.Engines = nil
			}
			machine := Config{Extensions: map[string]Extension{"ponytail": ext}}
			got := strings.Join(
				machine.ExtensionEnvironment(
					"codex",
					[]string{"PONYTAIL_USER_EXPORT=user", "PONYTAIL_SUBAGENT_MATCHER=inherited"},
				),
				"\n",
			)
			if !strings.Contains(got, "PONYTAIL_USER_EXPORT=user") {
				t.Errorf("unowned extension export lost: %s", got)
			}
			want := "inherited"
			if mode == "on" {
				want = "configured"
			}
			if !strings.Contains(got, "PONYTAIL_SUBAGENT_MATCHER="+want) {
				t.Errorf("configured env precedence wrong: %s", got)
			}
		})
	}
}
