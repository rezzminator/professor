//go:build jail

package installer

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

// runVSCodeExtensionDriver runs the embedded extension.js under Node through
// vscodeExtensionDriverJS in the given mode and returns its stdout.
func runVSCodeExtensionDriver(t *testing.T, mode string) []byte {
	t.Helper()
	return runVSCodeExtensionDriverAt(t, mode, t.TempDir(), nil)
}

// TestVSCodeExtensionTerminalProfileStripsChatIdentityEnv runs the embedded
// extension.js under Node with a stubbed 'vscode' module and asserts
// nextTerminal's env carries PFM_AUTO_OPEN, a PROFESSOR_TERMINAL marker, and
// a JSON null for every chat-identity variable a launcher app could have
// inherited (see the comment beside nextTerminal's env in extension.js).
func TestVSCodeExtensionTerminalProfileStripsChatIdentityEnv(t *testing.T) {
	t.Parallel()
	output := runVSCodeExtensionDriver(t, "profile")

	var profile struct {
		Env map[string]any `json:"env"`
	}
	if err := json.Unmarshal(output, &profile); err != nil {
		t.Fatalf("decode terminal profile JSON: %v: %s", err, output)
	}

	if profile.Env["PFM_AUTO_OPEN"] != "pfm" {
		t.Fatalf("terminal profile env missing PFM_AUTO_OPEN=pfm: %v", profile.Env)
	}
	marker, ok := profile.Env["PROFESSOR_TERMINAL"].(string)
	if !ok || marker == "" {
		t.Fatalf("terminal profile env missing a PROFESSOR_TERMINAL marker: %v", profile.Env)
	}
	for _, key := range append(claudelaunch.IdentityHygiene(), "TMUX", "TMUX_PANE") {
		value, present := profile.Env[key]
		if !present {
			t.Fatalf("terminal profile env dropped %s entirely instead of nulling it: %v", key, profile.Env)
		}
		if value != nil {
			t.Fatalf("terminal profile env[%s] = %v, want JSON null (VS Code deletes the inherited var)", key, value)
		}
	}
	nulled := 0
	for _, value := range profile.Env {
		if value == nil {
			nulled++
		}
	}
	if want := len(claudelaunch.IdentityHygiene()) + 2; nulled != want {
		t.Fatalf(
			"terminal profile env nulls %d names, want exactly the %d identity and TMUX names: %v",
			nulled,
			want,
			profile.Env,
		)
	}
}

// TestVSCodeExtensionNewTerminalCommandsOpenTheProfessorProfile drives, under
// Node, the handlers the extension registers for professor.newChatTerminal and
// for VS Code's own workbench.action.terminal.new — the command the terminal
// view's + button and Ctrl+Shift+` run. Both must open the Professor
// contributed profile through newWithProfile, the route provideTerminalProfile
// serves, so every default-route terminal takes the next icon/colour pair off
// the one shared counter. A caller that passes its own config keeps it.
func TestVSCodeExtensionNewTerminalCommandsOpenTheProfessorProfile(t *testing.T) {
	t.Parallel()
	output := runVSCodeExtensionDriver(t, "route")

	var executed [][]any
	if err := json.Unmarshal(output, &executed); err != nil {
		t.Fatalf("decode executed commands JSON: %v: %s", err, output)
	}
	professor := map[string]any{"config": map[string]any{
		"extensionIdentifier": "professor.professor",
		"id":                  "professor.terminal",
		"title":               vscodeExtensionProfileTitle,
	}}
	want := [][]any{
		{"workbench.action.terminal.newWithProfile", professor},
		{"workbench.action.terminal.newWithProfile", professor},
		{"workbench.action.terminal.newWithProfile", map[string]any{
			"config":   map[string]any{"profileName": "bash"},
			"location": float64(2),
		}},
	}
	if !reflect.DeepEqual(executed, want) {
		t.Fatalf(
			"executed commands =\n%v\nwant (newChatTerminal, + with no options, + with a caller's own config) =\n%v",
			executed,
			want,
		)
	}
}
