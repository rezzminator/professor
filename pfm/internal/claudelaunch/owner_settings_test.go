package claudelaunch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestRenderFoldsTheCallersSettings: a caller's --settings (a file or inline JSON) reaches Claude inside
// the one --settings the launch carries, never as a second flag pfm's own would override.
func TestRenderFoldsTheCallersSettings(t *testing.T) {
	home, machine := renderMachine(t)
	file := filepath.Join(home, "mine.json")
	body := `{"env":{"OWNER_OWN":"1","CLAUDE_CODE_AGENT_TEAMS_MODE":"x"},"theme":"owner",` +
		`"hooks":{"Stop":[{"hooks":[{"type":"command","command":"owner-stop"}]}]}}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--settings", file, "--verbose"},
		{"--settings=" + file, "--verbose"},
		{"--settings", body, "--verbose"},
	} {
		t.Run(args[0], func(t *testing.T) {
			launch, err := Render(Request{Purpose: PurposeLauncher, Home: home, Args: args}, machine)
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
				Theme string            `json:"theme"`
				Env   map[string]string `json:"env"`
				Hooks map[string][]struct {
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
			if got.Theme != "owner" || got.Env["OWNER_OWN"] != "1" || got.Env["CLAUDE_CODE_AGENT_TEAMS_MODE"] != "x" ||
				got.Env[envFunctionHooks] != "1" || len(commands) < 2 || commands[len(commands)-1] != "owner-stop" {
				t.Fatalf("--settings = theme %q, env %v, Stop hooks %q; want the caller's theme and env beside pfm's"+
					" env, and pfm's Stop hooks then the caller's", got.Theme, got.Env, commands)
			}
		})
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
	} {
		_, _, err := settingsArgv(tc.args, map[string]any{})
		if err == nil || err.Error() != tc.want {
			t.Errorf("settingsArgv(%q) error = %v, want %q", tc.args, err, tc.want)
		}
	}
	rest, payload, err := settingsArgv([]string{"--", "--settings", "x"}, map[string]any{})
	if err != nil || !reflect.DeepEqual(rest, []string{"--", "--settings", "x"}) || payload != "{}" {
		t.Errorf("after --: rest %q payload %q err %v, want the words untouched", rest, payload, err)
	}
}
