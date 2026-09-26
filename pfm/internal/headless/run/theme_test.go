package run

import (
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestArgumentsThemeIsPassedPerRun(t *testing.T) {
	machine := pfmconfig.Config{Claude: pfmconfig.ClaudePrefs{Theme: "from-config"}}
	for _, test := range []struct {
		name     string
		settings map[string]any
		want     string
	}{
		{"no theme passed", nil, `{"outputStyle":"default"}`},
		{"theme passed", map[string]any{"theme": "t"}, `{"outputStyle":"default","theme":"t"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			args, err := arguments(Request{Engine: pfmengine.Claude, Config: machine, Settings: test.settings})
			if err != nil {
				t.Fatal(err)
			}
			if !containsPair(args, "--settings", test.want) {
				t.Fatalf("args = %q, want settings %s", args, test.want)
			}
		})
	}
}

func TestArgumentsWithoutAccountStillGetsPassedSettings(t *testing.T) {
	args, err := arguments(Request{
		Engine: pfmengine.Claude, WithoutAccount: true,
		Settings: map[string]any{"theme": "t"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(args, "--settings", `{"outputStyle":"default","theme":"t"}`) {
		t.Fatalf("without-account args = %q", args)
	}
}
