package claudelaunch

import (
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// The optional Remote Control name must never consume a positional prompt.
func TestRenderRemoteControlInteractiveDoors(t *testing.T) {
	home, machine := renderMachine(t)
	for _, test := range []struct {
		name     string
		purpose  Purpose
		chatName string
		args     []string
		want     bool
	}{
		{"fresh named", PurposeInteractive, "desk", []string{"explain this"}, true},
		{"fresh unnamed prompt", PurposeInteractive, "", []string{"explain this"}, true},
		{"resume", PurposeResume, "desk", []string{"--resume", "session"}, true},
		{"launcher", PurposeLauncher, "", []string{"explain this"}, true},
		{"query", PurposeQuery, "", []string{"explain this"}, false},
		{"print short", PurposeLauncher, "desk", []string{"-p", "explain this"}, false},
		{"print long", PurposeInteractive, "desk", []string{"--print", "explain this"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			launch, err := Render(
				Request{Home: home, Purpose: test.purpose, Name: test.chatName, Args: test.args},
				machine,
			)
			if err != nil {
				t.Fatal(err)
			}
			at := -1
			for i, arg := range launch.Argv {
				if arg == "--remote-control" {
					if at != -1 {
						t.Fatalf("duplicate Remote Control flag: %q", launch.Argv)
					}
					at = i
				}
			}
			if (at >= 0) != test.want {
				t.Fatalf("Remote Control present=%v, want %v; argv=%q", at >= 0, test.want, launch.Argv)
			}
			if at >= 0 {
				if at+1 >= len(launch.Argv) {
					t.Fatal("Remote Control optional argument has no safe following flag")
				}
				if test.chatName != "" && launch.Argv[at+1] != test.chatName {
					t.Fatalf("Remote Control name=%q, want %q", launch.Argv[at+1], test.chatName)
				}
				if test.chatName == "" && !strings.HasPrefix(launch.Argv[at+1], "-") {
					t.Fatalf("Remote Control swallowed positional prompt: %q", launch.Argv)
				}
			}
			parsed, err := Parse(append([]string{launch.Binary}, launch.Argv...))
			if err != nil {
				t.Fatal(err)
			}
			for _, prompt := range test.args {
				if prompt != "explain this" {
					continue
				}
				found := false
				for _, word := range parsed.Rest {
					found = found || word == prompt
				}
				if !found {
					t.Fatalf("positional prompt lost: rest=%q", parsed.Rest)
				}
			}
			if _, exists := parsed.Settings["remoteControlAtStartup"]; exists {
				t.Fatal("Remote Control used an ignored settings key")
			}
		})
	}
}

func TestRenderRemoteControlAccountOverride(t *testing.T) {
	home, machine := renderMachine(t)
	prefs := machine.Claude
	prefs.RemoteControl = false
	machine.Accounts[0].Claude = &prefs
	for _, account := range []int{0, machine.Accounts[0].ID} {
		_, parsed := renderParsed(t, Request{Home: home, Account: account}, machine)
		if parsed.RemoteControl != (account == 0) {
			t.Fatalf("account %d: remote control=%v", account, parsed.RemoteControl)
		}
	}
}

func TestRefreshRemoteControlPreservesOptionValues(t *testing.T) {
	for _, flag := range []string{"--append-system-prompt", "--output-format", "--allowedTools", "--disallowedTools", "--add-dir", "--agent", "--agents", "--fallback-model", "--permission-mode", "--permission-prompt-tool", "--input-format", "--json-schema", "--max-turns", "--max-budget-usd", "--plugin-dir", "--settings-sources", "--tools", "--betas", "--append-system-prompt-file"} {
		for _, value := range []string{"--remote-control", "--print", "-p"} {
			t.Run(flag+"/"+value, func(t *testing.T) {
				args := []string{flag, value, "--settings", "{}", "prompt"}
				for _, enabled := range []bool{false, true} {
					want := append([]string(nil), args...)
					if enabled {
						want = append([]string{"--remote-control", "desk"}, want...)
					}
					got := RefreshRemoteControl(args, enabled, "desk")
					if !slices.Equal(got, want) {
						t.Fatalf("enabled=%t argv=%q want %q", enabled, got, want)
					}
					if again := RefreshRemoteControl(got, enabled, "desk"); !slices.Equal(again, want) {
						t.Fatalf("replayed argv=%q want %q", again, want)
					}
				}
			})
		}
	}
}

func TestRefreshRemoteControlStoredRecipes(t *testing.T) {
	for _, test := range []struct {
		name     string
		args     []string
		enabled  bool
		chatName string
		want     []string
	}{
		{"explicit remote name", []string{"--remote-control", "explicit", "--settings", "{}"}, true, "", []string{"--remote-control", "explicit", "--settings", "{}"}},
		{"explicit equals remote name", []string{"--remote-control=explicit", "--settings", "{}"}, true, "", []string{"--remote-control", "explicit", "--settings", "{}"}},
		{"minimal unnamed prompt", []string{"prompt"}, true, "", []string{"--remote-control", "--", "prompt"}},
		{"old named", []string{"--settings", "{}"}, true, "desk", []string{"--remote-control", "desk", "--settings", "{}"}},
		{"leading dash name stays a value", []string{"--settings", "{}"}, true, "-desk", []string{"--remote-control=-desk", "--settings", "{}"}},
		{"off removes old", []string{"--remote-control", "old", "--settings", "{}"}, false, "desk", []string{"--settings", "{}"}},
		{"replaces equals", []string{"--remote-control=old", "--settings", "{}"}, true, "desk", []string{"--remote-control", "desk", "--settings", "{}"}},
		{"existing CLI name", []string{"--name", "desk", "prompt"}, true, "", []string{"--remote-control", "desk", "--name", "desk", "prompt"}},
		{"print removes old", []string{"--remote-control", "old", "-p", "prompt"}, true, "desk", []string{"-p", "prompt"}},
		{"positional print word", []string{"--system-prompt", "-p", "--settings", "{}"}, true, "desk", []string{"--remote-control", "desk", "--system-prompt", "-p", "--settings", "{}"}},
		{"prompt after terminator", []string{"--", "--remote-control"}, true, "", []string{"--remote-control", "--", "--remote-control"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := RefreshRemoteControl(test.args, test.enabled, test.chatName)
			if !slices.Equal(got, test.want) {
				t.Fatalf("argv=%q want %q", got, test.want)
			}
		})
	}
}

func TestResolveRemoteControlSources(t *testing.T) {
	_, machine := renderMachine(t)
	for _, account := range []int{0, machine.Accounts[0].ID} {
		if account != 0 {
			prefs := machine.Claude
			prefs.RemoteControl = false
			machine.Accounts[0].Claude = &prefs
			machine.Sources["accounts[0].claude.remoteControl"] = pfmconfig.SourceFile
		}
		found := false
		for _, row := range Resolve(machine, account) {
			if row.Knob.Name != "remoteControl" {
				continue
			}
			found = true
			wantValue, wantSource := "true", "default"
			if account != 0 {
				wantValue, wantSource = "false", "account"
			}
			if row.Value != wantValue || row.Won != wantSource {
				t.Fatalf("account %d row=%#v", account, row)
			}
		}
		if !found {
			t.Fatal("remoteControl resolution missing")
		}
	}
}

func TestRenderRemoteControlNameDoesNotRenameResume(t *testing.T) {
	home, machine := renderMachine(t)
	_, parsed := renderParsed(
		t,
		Request{Home: home, Purpose: PurposeResume, Resume: "session", RemoteControlName: "desk"},
		machine,
	)
	if !parsed.RemoteControl || parsed.RemoteControlName != "desk" {
		t.Fatalf("remote control=%v name=%q", parsed.RemoteControl, parsed.RemoteControlName)
	}
	if parsed.Name != "" {
		t.Fatalf("resume retitled conversation: %q", parsed.Name)
	}
}
