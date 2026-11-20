package claudelaunch

import (
	"reflect"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// TestResolveExtraSettings: every claude.settings value a launch carries that no registry row targets
// is one row, sorted by target, its value as `pfm config claude` shows it; a value a row targets, or a
// null the launch drops, is none.
func TestResolveExtraSettings(t *testing.T) {
	home := t.TempDir()
	machine := pfmconfig.Defaults(home, []string{home + "/.claude/projects/a", home + "/.cc/2/projects/b"})
	if got := ResolveExtraSettings(machine, 0); got != nil {
		t.Fatalf("no claude.settings: extras = %#v, want none", got)
	}
	machine.Claude.Settings = map[string]any{
		"env": map[string]any{
			"CLAUDE_CODE_TMUX_TRUECOLOR": nil,        // a shipped default's row names it
			"CLAUDE_CODE_NO_FLICKER":     "1",        // the noFlicker row names it
			"ANTHROPIC_BASE_URL":         "http://x", // a hygiene row unsets the inherited one, never this
			"MY_TOOL_OFF":                nil,        // dropped by the launch
			"MY_TOOL_FLAG":               "on",
		},
		"outputStyle":        "terse", // a shipped default's row names it
		"theme":              "dark",  // the theme row names it
		"permissions":        map[string]any{"allow": []any{"Bash(ls)"}},
		"spinnerTipsEnabled": false,
		"model":              nil,
	}
	row := func(target, value string) Resolved {
		return Resolved{
			Knob:  Knob{Name: target, Wire: WireSettings, Target: target, Source: SourceConfig},
			Value: value,
			Won:   configWord,
		}
	}
	want := []Resolved{
		row("env.ANTHROPIC_BASE_URL", "http://x"),
		row("env.MY_TOOL_FLAG", "on"),
		row("permissions", `{"allow":["Bash(ls)"]}`),
		row("spinnerTipsEnabled", "false"),
	}
	for _, account := range []int{0, 2} {
		if got := ResolveExtraSettings(machine, account); !reflect.DeepEqual(got, want) {
			t.Errorf("account %d extras =\n%#v\nwant\n%#v", account, got, want)
		}
	}
}
