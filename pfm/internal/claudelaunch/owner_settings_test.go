package claudelaunch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestRenderFoldsTheProjectsAndTheCallersSettings: the project's claude.settings and a caller's
// --settings (a file or inline JSON) reach Claude inside the one --settings the launch carries, never as
// a second flag pfm's own would override: the project's over the defaults and pfm's, the caller's over all.
func TestRenderFoldsTheProjectsAndTheCallersSettings(t *testing.T) {
	home, machine := renderMachine(t)
	file := filepath.Join(home, "mine.json")
	body := `{"env":{"OWNER_OWN":"1","CLAUDE_CODE_AGENT_TEAMS_MODE":"x"},"theme":"owner",` +
		`"hooks":{"Stop":[{"hooks":[{"type":"command","command":"owner-stop"}]}]}}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	projectBody := `{"env":{"PROJECT_OWN":"1","CLAUDE_CODE_AGENT_TEAMS_MODE":"project","CLAUDE_CODE_TMUX_TRUECOLOR":null},` +
		`"theme":"project",` +
		`"outputStyle":"project","showThinkingSummaries":false,` +
		`"hooks":{"Stop":[{"hooks":[{"type":"command","command":"project-stop"}]}]}}`
	project, err := DecodeOwnerSettings([]byte(projectBody), "claude.settings")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		purpose Purpose
		args    []string
	}{
		{PurposeLauncher, []string{"--settings", file, "--verbose"}},
		{PurposeLauncher, []string{"--settings=" + file, "--verbose"}},
		{PurposeLauncher, []string{"--settings", body, "--verbose"}},
		// A query carries no pfm hooks, so the project's hooks object is the one the caller's join.
		{PurposeQuery, []string{"--settings", body, "--verbose"}},
	} {
		args := row.args
		t.Run(args[0], func(t *testing.T) {
			launch, err := Render(Request{Purpose: row.purpose, Home: home, Args: args, Settings: project}, machine)
			if err != nil {
				t.Fatal(err)
			}
			var payloads []string
			verbose := 0
			for index, word := range launch.Argv {
				if word == flagSettings {
					payloads = append(payloads, launch.Argv[index+1])
				} else if value, inline := strings.CutPrefix(word, flagSettings+"="); inline {
					payloads = append(payloads, value)
				}
				if word == "--verbose" {
					verbose++
				}
			}
			if len(payloads) != 1 || verbose != 1 {
				t.Fatalf("argv %q carries --settings %d times and --verbose %d times, want once each",
					launch.Argv, len(payloads), verbose)
			}
			var got struct {
				Theme    string            `json:"theme"`
				Style    string            `json:"outputStyle"`
				Thinking *bool             `json:"showThinkingSummaries"`
				Env      map[string]string `json:"env"`
				Hooks    map[string][]struct {
					Hooks []struct {
						Command string `json:"command"`
					} `json:"hooks"`
				} `json:"hooks"`
			}
			if err := json.Unmarshal([]byte(payloads[0]), &got); err != nil {
				t.Fatalf("--settings %q: %v", payloads[0], err)
			}
			stop := got.Hooks["Stop"]
			var commands []string
			for _, group := range stop {
				for _, hook := range group.Hooks {
					commands = append(commands, hook.Command)
				}
			}
			_, truecolor := got.Env["CLAUDE_CODE_TMUX_TRUECOLOR"]
			if got.Theme != "owner" || got.Style != "project" || got.Thinking == nil || *got.Thinking || truecolor ||
				got.Env["OWNER_OWN"] != "1" || got.Env["PROJECT_OWN"] != "1" ||
				got.Env["CLAUDE_CODE_AGENT_TEAMS_MODE"] != "x" || got.Env[envFunctionHooks] != "1" ||
				got.Env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] != "0" || len(commands) < 2 ||
				!reflect.DeepEqual(commands[len(commands)-2:], []string{"project-stop", "owner-stop"}) {
				t.Fatalf("--settings = theme %q, outputStyle %q, showThinkingSummaries %v, env %v, Stop hooks %q;"+
					" want the caller's theme, the project's outputStyle and showThinkingSummaries false, the"+
					" caller's and the project's env beside pfm's, the project's null removing the truecolor default,"+
					" and pfm's Stop hooks, the project's, then the"+
					" caller's", got.Theme, got.Style, got.Thinking, got.Env, commands)
			}
		})
	}
	fresh, err := DecodeOwnerSettings([]byte(projectBody), "claude.settings")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project, fresh) {
		t.Errorf("Render wrote into the project's settings:\n%#v\nwant\n%#v", project, fresh)
	}
}

// TestSettingsArgvRefusesUnreadableSettings: a caller's settings pfm cannot read refuse the launch.
func TestSettingsArgvRefusesUnreadableSettings(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--settings", missing}, "--settings " + missing + ": read the settings file: open " + missing +
			": no such file or directory"},
		{[]string{"--settings", "{not json"}, "--settings inline JSON: not a JSON object (invalid character 'n'" +
			" looking for beginning of object key string)"},
		{[]string{"--settings", `{"hooks":[]}`}, `--settings inline JSON: "hooks" is not a JSON object`},
		{[]string{"--settings", `{"env":"X=1"}`}, `--settings inline JSON: "env" is not a JSON object`},
	} {
		_, _, err := settingsArgv(tc.args, nil, map[string]any{})
		if err == nil || err.Error() != tc.want {
			t.Errorf("settingsArgv(%q) error = %v, want %q", tc.args, err, tc.want)
		}
	}
	rest, payload, err := settingsArgv([]string{"--", "--settings", "x"}, nil, map[string]any{})
	if err != nil || !reflect.DeepEqual(rest, []string{"--", "--settings", "x"}) || payload != "{}" {
		t.Errorf("after --: rest %q payload %q err %v, want the words untouched", rest, payload, err)
	}
}

// The layered claude.settings: the machine's, then the project's, over the shipped defaults, each key
// merged as a caller's --settings is.
func TestEffectiveSettingsLayersOverTheDefaults(t *testing.T) {
	machine := map[string]any{"cleanupPeriodDays": float64(90), "env": map[string]any{"A": "machine", "B": "machine"}}
	// A null turns off the shipped default under it.
	project := map[string]any{
		"env":         map[string]any{"B": "project", "CLAUDE_CODE_TMUX_TRUECOLOR": nil},
		"outputStyle": "terse",
	}
	layered, err := LayerSettings(nil, machine, project)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"cleanupPeriodDays": float64(90), "outputStyle": "terse",
		"env": map[string]any{"A": "machine", "B": "project", "CLAUDE_CODE_TMUX_TRUECOLOR": nil},
	}
	if !reflect.DeepEqual(layered, want) {
		t.Fatalf("LayerSettings = %#v, want %#v", layered, want)
	}
	if machine["env"].(map[string]any)["B"] != "machine" {
		t.Fatalf("LayerSettings wrote into its first layer: %#v", machine)
	}
	if none, err := LayerSettings(nil, nil); none != nil || err != nil {
		t.Fatalf("LayerSettings of no layer = %#v, %v; want nil", none, err)
	}
	env := EffectiveEnv(layered)
	_, truecolor := env["CLAUDE_CODE_TMUX_TRUECOLOR"]
	if env["A"] != "machine" || env["B"] != "project" || env["ANTHROPIC_SMALL_FAST_MODEL"] != "claude-haiku-5-5" ||
		truecolor || len(env) != 8 {
		t.Fatalf("EffectiveEnv = %#v", env)
	}
	for _, tc := range []struct {
		claude map[string]any
		days   int
		err    string
	}{
		{nil, 36500, ""},
		{map[string]any{"cleanupPeriodDays": float64(1)}, 1, ""},
		{map[string]any{"cleanupPeriodDays": float64(0)}, 0, "claude.settings.cleanupPeriodDays must be a positive whole number of days, got 0"},
		{map[string]any{"cleanupPeriodDays": 1.5}, 0, "claude.settings.cleanupPeriodDays must be a positive whole number of days, got 1.5"},
		{map[string]any{"cleanupPeriodDays": "30"}, 0, "claude.settings.cleanupPeriodDays must be a positive whole number of days, got 30"},
	} {
		days, err := CleanupPeriodDays(tc.claude)
		if days != tc.days || (err == nil) != (tc.err == "") || err != nil && err.Error() != tc.err {
			t.Errorf("CleanupPeriodDays(%v) = %d, %v; want %d, %q", tc.claude, days, err, tc.days, tc.err)
		}
	}
}
