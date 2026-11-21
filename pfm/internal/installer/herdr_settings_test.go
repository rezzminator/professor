package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWireHerdrSettingsAddsOnlyWhatTheConfigLacks(t *testing.T) {
	for _, row := range []struct {
		name, original, want, change string
		settings                     map[string]any
		absent, dryRun               bool
	}{
		{
			name: "an empty config gains the shipped default", original: "",
			want: "onboarding = false\n", change: "[onboarding = false]",
		},
		{
			name:     "a top-level key goes before the first table",
			original: "# mine\ntheme = \"x\"\n\n[ui]\nsidebar_width = 20\n",
			want:     "# mine\ntheme = \"x\"\n\nonboarding = false\n[ui]\nsidebar_width = 20\n",
			change:   "[onboarding = false]",
		},
		{
			name: "the user's value is kept", original: "onboarding = true\n", want: "onboarding = true\n",
		},
		{
			name: "a null in herdr.settings drops the default", original: "theme = \"x\"\n", want: "theme = \"x\"\n",
			settings: map[string]any{"onboarding": nil},
		},
		{
			name:     "a table key goes under the table's header",
			original: "onboarding = true\n[ui] # sidebar\nsound = false\n",
			want:     "onboarding = true\n[ui] # sidebar\nsidebar_width = 30\nsound = false\n",
			settings: map[string]any{"ui": map[string]any{"sidebar_width": json.Number("30")}},
			change:   "[ui.sidebar_width = 30]",
		},
		{
			name:     "a header inside a multiline string is not the table",
			original: "onboarding = true\nnotes = \"\"\"\n[ui]\n\"\"\"\n",
			want:     "onboarding = true\nnotes = \"\"\"\n[ui]\n\"\"\"\n\n[ui]\nsidebar_width = 30\n",
			settings: map[string]any{"ui": map[string]any{"sidebar_width": json.Number("30")}},
			change:   "[ui.sidebar_width = 30]",
		},
		{
			name:     "a table the config lacks is added with every key",
			original: "onboarding = true\n",
			want:     "onboarding = true\n\n[terminal]\nargs = [\"-l\", 2]\nname = \"zsh\"\n",
			settings: map[string]any{"terminal": map[string]any{"name": "zsh", "args": []any{"-l", json.Number("2")}}},
			change:   "[terminal.args = [\"-l\", 2]; terminal.name = \"zsh\"]",
		},
		{name: "herdr absent leaves the config alone", original: "", want: "", absent: true},
		{name: "a dry run only reports", original: "", want: "", dryRun: true, change: "[onboarding = false]"},
	} {
		t.Run(row.name, func(t *testing.T) {
			installer, path, output := herdrShellInstaller(t, row.original, !row.absent)
			installer.options.HerdrSettings = row.settings
			installer.apply = !row.dryRun
			if err := installer.wireHerdrSettings(); err != nil {
				t.Fatalf("wireHerdrSettings: %v\noutput:\n%s", err, output)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != row.want {
				t.Errorf("%s:\n%s\nwant:\n%s", path, got, row.want)
			}
			var want string
			switch {
			case row.absent:
				want = "  skip    herdr settings: herdr absent\n"
			case row.change != "":
				want = "  change  set herdr settings " + row.change + " in " + path + "\n"
			default:
				want = "  ok      herdr settings set in " + path + "\n"
			}
			if output.String() != want {
				t.Errorf("output:\n%q\nwant:\n%q", output.String(), want)
			}
		})
	}
}

func TestWireHerdrSettingsKeepsAUserValueWhereATableWouldGo(t *testing.T) {
	installer, path, output := herdrShellInstaller(t, "onboarding = true\nui = 3\n", true)
	installer.options.HerdrSettings = map[string]any{"ui": map[string]any{"sidebar_width": json.Number("30")}}
	if err := installer.wireHerdrSettings(); err != nil {
		t.Fatalf("wireHerdrSettings: %v\noutput:\n%s", err, output)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "onboarding = true\nui = 3\n" {
		t.Errorf("%s = %q, %v; want it untouched", path, got, err)
	}
}

func TestWireHerdrSettingsAddsTheOtherTablesWhenOneRefuses(t *testing.T) {
	installer, path, output := herdrShellInstaller(t, "ui = { sound = false }\n", true)
	installer.options.HerdrSettings = map[string]any{"ui": map[string]any{"sidebar_width": json.Number("30")}}
	err := installer.wireHerdrSettings()
	want := "add herdr settings to " + path + ": cannot add sidebar_width without rewriting the existing ui table"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q\noutput:\n%s", err, want, output)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "onboarding = false\nui = { sound = false }\n" {
		t.Errorf("%s = %q, %v; want onboarding added above the inline table", path, got, err)
	}
	wantOutput := "  FAIL    " + want + "\n  change  set herdr settings [onboarding = false] in " + path + "\n"
	if output.String() != wantOutput {
		t.Errorf("output:\n%q\nwant:\n%q", output.String(), wantOutput)
	}
}

func TestWireHerdrSettingsRefusesAnUnparsableConfig(t *testing.T) {
	installer, path, output := herdrShellInstaller(t, "onboarding = \n", true)
	err := installer.wireHerdrSettings()
	want := "parse Herdr config " + path + " for its settings: toml: line 1 (last key \"onboarding\"): " +
		"expected value but found '\\n' instead"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q\noutput:\n%s", err, want, output)
	}
}

// TestExampleHerdrSettingsAreTheDefaults: the example's herdr.settings, which
// a first install seeds, is pfm's shipped Herdr settings.
func TestExampleHerdrSettingsAreTheDefaults(t *testing.T) {
	path := filepath.Join("..", "..", "..", "example.pfm.config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var example struct {
		Herdr struct {
			Settings map[string]any `json:"settings"`
		} `json:"herdr"`
	}
	if err := json.Unmarshal(raw, &example); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if !reflect.DeepEqual(example.Herdr.Settings, herdrSettingsDefault()) {
		t.Errorf("%s herdr.settings = %#v, want the shipped %#v", path, example.Herdr.Settings, herdrSettingsDefault())
	}
}
