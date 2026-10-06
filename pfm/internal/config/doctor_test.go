package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMarshalDoctorIgnoreWarningsRoundTrip(t *testing.T) {
	home := t.TempDir()
	machine := Defaults(home, nil)
	want := []string{"vscode-link", "vscode-settings"}
	machine.Doctor.IgnoreWarnings = want
	content, err := Marshal(machine, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, FileName)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Doctor.IgnoreWarnings, want) {
		t.Fatalf("Doctor.IgnoreWarnings = %#v, want %#v", loaded.Doctor.IgnoreWarnings, want)
	}
}

func TestMarshalDoctorWithoutIgnoreWarnings(t *testing.T) {
	machine := Defaults(t.TempDir(), nil)
	content, err := Marshal(machine, false)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatal(err)
	}
	if block, found := value["doctor"]; found {
		t.Fatalf("doctor block = %s, want omitted with nil IgnoreWarnings", block)
	}
}

// TestDoctorIgnoreWarningsLoadsAndValidatesAtLoad pins doctor.ignoreWarnings
// at data entry: an absent or null key is today's behaviour (nothing
// ignored, source default), a list of well-formed IDs loads with source
// file, and a malformed entry is a config error naming its index — never an
// entry quietly dropped.
func TestDoctorIgnoreWarningsLoadsAndValidatesAtLoad(t *testing.T) {
	for _, test := range []struct {
		name, body string
		want       []string
		source     Source
		err        string
	}{
		{name: "absent", body: "", source: SourceDefault},
		{name: "null", body: `, "doctor": {"ignoreWarnings": null}`, source: SourceDefault},
		{name: "empty block", body: `, "doctor": {}`, source: SourceDefault},
		{name: "empty list", body: `, "doctor": {"ignoreWarnings": []}`, want: []string{}, source: SourceFile},
		{
			name:   "ids",
			body:   `, "doctor": {"ignoreWarnings": ["vscode-link", "vscode-settings"]}`,
			want:   []string{"vscode-link", "vscode-settings"},
			source: SourceFile,
		},
		{name: "empty id", body: `, "doctor": {"ignoreWarnings": [""]}`, err: "doctor.ignoreWarnings[0] must be a doctor warning id"},
		{name: "underscore", body: `, "doctor": {"ignoreWarnings": ["vscode-link", "vscode_link"]}`, err: "doctor.ignoreWarnings[1] must be"},
		{name: "upper case", body: `, "doctor": {"ignoreWarnings": ["VSCode-link"]}`, err: "doctor.ignoreWarnings[0] must be"},
		{name: "padded", body: `, "doctor": {"ignoreWarnings": [" vscode-link"]}`, err: "doctor.ignoreWarnings[0] must be"},
		{name: "not a string", body: `, "doctor": {"ignoreWarnings": [1]}`, err: "ignoreWarnings"},
		{name: "unknown doctor key", body: `, "doctor": {"ignore": ["vscode-link"]}`, err: "ignore"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := loadTmuxConfig(t, test.body)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("Load error = %v, want one containing %q", err, test.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Doctor.IgnoreWarnings, test.want) {
				t.Fatalf("Doctor.IgnoreWarnings = %#v, want %#v", got.Doctor.IgnoreWarnings, test.want)
			}
			if source := got.Source(keyDoctorIgnoreWarnings); source != test.source {
				t.Fatalf("doctor.ignoreWarnings source = %q, want %q", source, test.source)
			}
		})
	}
}

func TestMarshalDoctorExplicitEmptyIgnoreWarnings(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, FileName)
	if err := os.WriteFile(path, []byte(`{"version":2,"doctor":{"ignoreWarnings":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	machine, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := Marshal(machine, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source(keyDoctorIgnoreWarnings) != SourceFile ||
		!reflect.DeepEqual(loaded.Doctor.IgnoreWarnings, []string{}) {
		t.Fatalf(
			"empty warning list provenance=%q value=%#v",
			loaded.Source(keyDoctorIgnoreWarnings),
			loaded.Doctor.IgnoreWarnings,
		)
	}
}
