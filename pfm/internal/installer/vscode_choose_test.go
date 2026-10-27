package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakePfm is the pfm the Choose Theme command finds at ~/.local/bin/pfm in a
// test: a script that logs its arguments and answers `theme list --json` and
// `theme use` with the given output and exit codes.
type fakePfm struct {
	list       string
	listStderr string
	listExit   int
	useStderr  string
	useExit    int
	// absent leaves ~/.local/bin/pfm out: a host pfm is not installed on.
	absent bool
}

// chooseWindow is the window the command runs in: remoteName "" a local one,
// kind where the extension host runs (1 UI, 2 Workspace), 0 left unset.
type chooseWindow struct {
	remoteName string
	kind       int
}

type chooseRun struct {
	Registered bool `json:"registered"`
	QuickPicks []struct {
		Items []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
			Detail      string `json:"detail"`
		} `json:"items"`
	} `json:"quickPicks"`
	Messages []struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"messages"`
	Contexts [][]any `json:"contexts"`
}

// runVSCodeChooseTheme runs the professor.chooseTheme handler in a local
// window (pick the quick-pick label chosen, "" a cancel) over a fake pfm, and
// returns what the extension showed and the argument line of every pfm run.
func runVSCodeChooseTheme(t *testing.T, pick string, pfm fakePfm) (chooseRun, []string) {
	t.Helper()
	return runVSCodeChooseThemeIn(t, chooseWindow{}, pick, pfm)
}

// runVSCodeChooseThemeIn is runVSCodeChooseTheme in the given window.
func runVSCodeChooseThemeIn(t *testing.T, window chooseWindow, pick string, pfm fakePfm) (chooseRun, []string) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	data := filepath.Join(home, "fake-pfm")
	for _, dir := range []string{bin, data} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"list.out": pfm.list, "list.err": pfm.listStderr, "use.err": pfm.useStderr}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(data, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> "$HOME/fake-pfm/calls.log"
case "$1 $2" in
  "theme list") cat "$HOME/fake-pfm/list.out"; cat "$HOME/fake-pfm/list.err" >&2; exit %d ;;
  "theme use") cat "$HOME/fake-pfm/use.err" >&2; exit %d ;;
esac
echo "fake pfm: unexpected arguments: $*" >&2
exit 64
`, pfm.listExit, pfm.useExit)
	if !pfm.absent {
		if err := testjail.WriteExecutable(filepath.Join(bin, "pfm"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	scenario, err := json.Marshal(map[string]any{
		"remoteName": window.remoteName, "extensionKind": window.kind, "pick": pick,
	})
	if err != nil {
		t.Fatal(err)
	}
	output := runVSCodeExtensionDriverAt(t, "choose", home, []string{"PROFESSOR_CHOOSE_SCENARIO=" + string(scenario)})
	var run chooseRun
	if err := json.Unmarshal(output, &run); err != nil {
		t.Fatalf("decode choose driver output: %v: %s", err, output)
	}
	var calls []string
	logged, err := os.ReadFile(filepath.Join(data, "calls.log"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(logged) > 0 {
		calls = strings.Split(strings.TrimSuffix(string(logged), "\n"), "\n")
	}
	return run, calls
}

const chooseThemeList = `{"current":"dark","themes":[
 {"name":"dusk","value":"custom:dusk","title":"Dusk","kind":"custom","current":false},
 {"name":"ember","value":"custom:ember","title":"Ember Glow","kind":"custom","current":false,
  "missing":["~/.claude-b","~/.claude-c"]},
 {"name":"dark","value":"dark","title":"dark","kind":"builtin","current":true},
 {"name":"light","value":"light","title":"light","kind":"builtin","current":false}],"errors":[]}`

// chooseThemeListColliding has a custom theme whose stem is a built-in id:
// only its value names it, the bare name being the built-in.
const chooseThemeListColliding = `{"current":"dark","themes":[
 {"name":"dark","value":"custom:dark","title":"Dark Custom","kind":"custom","current":false},
 {"name":"dark","value":"dark","title":"dark","kind":"builtin","current":true}],"errors":[]}`

const chooseThemeListWithErrors = `{"current":"dark","themes":[
 {"name":"dark","value":"dark","title":"dark","kind":"builtin","current":true}],
 "errors":[{"path":"~/.claude-b/themes","error":"permission denied"},
  {"path":"~/.claude-c/settings.json","error":"invalid JSON"}]}`

// TestVSCodeChooseThemeRunsPfmListThenUseInALocalWindow: the command lists the
// themes through `pfm theme list --json`, sets the picked one through
// `pfm theme use {value}` (the one token naming exactly the picked theme)
// and says so; a cancel runs nothing more; every
// failure reaches the user in pfm's own words, never silently.
func TestVSCodeChooseThemeRunsPfmListThenUseInALocalWindow(t *testing.T) {
	t.Parallel()
	const listCall, useCall = "theme list --json", "theme use custom:dusk"
	for _, tc := range []struct {
		name      string
		pick      string
		pfm       fakePfm
		calls     []string
		quickPick bool
		kind      string   // the one message shown, "" for none
		fragments []string // each must appear in that message
	}{
		{
			name: "picking a theme sets it", pick: "Dusk", pfm: fakePfm{list: chooseThemeList},
			calls: []string{listCall, useCall}, quickPick: true, kind: "info",
			fragments: []string{"Theme Dusk set: new chats follow; run /theme in open chats"},
		},
		{
			name: "a custom theme sharing a built-in's name is set by its value", pick: "Dark Custom",
			pfm:   fakePfm{list: chooseThemeListColliding},
			calls: []string{listCall, "theme use custom:dark"}, quickPick: true, kind: "info",
			fragments: []string{"Theme Dark Custom set"},
		},
		{name: "a cancel runs nothing more", pfm: fakePfm{list: chooseThemeList}, calls: []string{listCall}, quickPick: true},
		{
			name: "a use failure shows pfm's stderr", pick: "Dusk",
			pfm:   fakePfm{list: chooseThemeList, useExit: 1, useStderr: "pfm: theme use: refused in ~/.claude-b\n"},
			calls: []string{listCall, useCall}, quickPick: true, kind: "error",
			fragments: []string{"refused in ~/.claude-b"},
		},
		{
			name: "a use failure with no stderr shows the exit code", pick: "Dusk",
			pfm:   fakePfm{list: chooseThemeList, useExit: 3},
			calls: []string{listCall, useCall}, quickPick: true, kind: "error",
			fragments: []string{"exit code 3"},
		},
		{
			name: "a list failure shows pfm's stderr", pick: "Dusk",
			pfm:   fakePfm{listExit: 2, listStderr: "pfm: theme list: cannot read state\n"},
			calls: []string{listCall}, kind: "error", fragments: []string{"cannot read state"},
		},
		{
			name: "list output that is not JSON is an error", pick: "Dusk", pfm: fakePfm{list: "this is not json"},
			calls: []string{listCall}, kind: "error", fragments: []string{"JSON"},
		},
		{
			name: "unreadable theme sources are one warning naming each path",
			pfm: fakePfm{
				list: chooseThemeListWithErrors, listExit: 1, listStderr: "pfm: 2 unreadable\n",
			},
			calls: []string{listCall}, quickPick: true, kind: "warning",
			fragments: []string{"~/.claude-b/themes", "~/.claude-c/settings.json"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run, calls := runVSCodeChooseTheme(t, tc.pick, tc.pfm)
			if !run.Registered {
				t.Fatal("extension.js registers no professor.chooseTheme handler")
			}
			if !slices.Equal(calls, tc.calls) {
				t.Errorf("pfm runs = %q, want %q", calls, tc.calls)
			}
			if got := len(run.QuickPicks) == 1; got != tc.quickPick {
				t.Errorf("quick picks shown = %d, want one = %v", len(run.QuickPicks), tc.quickPick)
			}
			if tc.kind == "" {
				if len(run.Messages) != 0 {
					t.Errorf("messages = %+v, want none", run.Messages)
				}
				return
			}
			if len(run.Messages) != 1 || run.Messages[0].Kind != tc.kind {
				t.Fatalf("messages = %+v, want exactly one %s", run.Messages, tc.kind)
			}
			for _, fragment := range tc.fragments {
				if !strings.Contains(run.Messages[0].Message, fragment) {
					t.Errorf("%s message = %q, want it to hold %q", tc.kind, run.Messages[0].Message, fragment)
				}
			}
		})
	}
}

// TestVSCodeChooseThemeOffersTheCurrentThemeFirstAndMarksMissingOnes: the
// quick pick has one item per theme, titled by the theme's title and
// described by its value; the current theme comes first and says so; a theme
// an account lacks says where it is missing.
func TestVSCodeChooseThemeOffersTheCurrentThemeFirstAndMarksMissingOnes(t *testing.T) {
	t.Parallel()
	run, _ := runVSCodeChooseTheme(t, "", fakePfm{list: chooseThemeList})
	if len(run.QuickPicks) != 1 {
		t.Fatalf("quick picks shown = %d, want 1 (registered=%v)", len(run.QuickPicks), run.Registered)
	}
	type item struct{ label, description string }
	var got []item
	details := map[string]string{}
	for _, entry := range run.QuickPicks[0].Items {
		got = append(got, item{entry.Label, entry.Description})
		details[entry.Label] = entry.Detail
	}
	want := []item{{"dark", "dark"}, {"Dusk", "custom:dusk"}, {"Ember Glow", "custom:ember"}, {"light", "light"}}
	if !slices.Equal(got, want) {
		t.Errorf("quick pick items = %v, want %v", got, want)
	}
	if !strings.Contains(details["dark"], "current") {
		t.Errorf("current theme detail = %q, want it marked current", details["dark"])
	}
	if want := "missing in ~/.claude-b, ~/.claude-c"; !strings.Contains(details["Ember Glow"], want) {
		t.Errorf("Ember Glow detail = %q, want it to hold %q", details["Ember Glow"], want)
	}
	if details["Dusk"] != "" || details["light"] != "" {
		t.Errorf("details of plain themes = %q, %q, want none", details["Dusk"], details["light"])
	}
}

// TestVSCodeChooseThemeNeverRunsPfmInARemoteWindowPfmDoesNotRunIn: a remote
// window whose extension host has no pfm, or one the extension serves from
// the UI side, refuses with a message and executes nothing; the manifest
// shows the command in a local window or where pfm runs.
func TestVSCodeChooseThemeNeverRunsPfmInARemoteWindowPfmDoesNotRunIn(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		window chooseWindow
		pfm    fakePfm
	}{
		"remote host without pfm": {chooseWindow{"ssh-remote", 2}, fakePfm{list: chooseThemeList, absent: true}},
		"served from the UI side": {chooseWindow{"ssh-remote", 1}, fakePfm{list: chooseThemeList}},
	} {
		run, calls := runVSCodeChooseThemeIn(t, tc.window, "Dusk", tc.pfm)
		if !run.Registered {
			t.Fatal("extension.js registers no professor.chooseTheme handler")
		}
		if len(calls) != 0 || len(run.QuickPicks) != 0 {
			t.Errorf(
				"%s: remote window ran pfm %q and showed %d quick picks, want neither",
				name,
				calls,
				len(run.QuickPicks),
			)
		}
		// A window served from the client's side also says once, at activation, that it paints
		// nothing; the command's own refusal is the one naming Choose Theme.
		var refusals int
		for _, message := range run.Messages {
			if message.Kind == "warning" && strings.Contains(message.Message, "Choose Theme") {
				refusals++
			}
		}
		if refusals != 1 {
			t.Errorf(
				"%s: remote window messages = %+v, want exactly one Choose Theme warning saying why",
				name,
				run.Messages,
			)
		}
		if want := [][]any{{"setContext", "professor.pfmHere", false}}; !reflect.DeepEqual(run.Contexts, want) {
			t.Errorf("%s: activate set contexts %v, want %v", name, run.Contexts, want)
		}
	}

	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Contributes struct {
			Commands []struct {
				Command string `json:"command"`
				Title   string `json:"title"`
			} `json:"commands"`
			Menus struct {
				CommandPalette []struct {
					Command string `json:"command"`
					When    string `json:"when"`
				} `json:"commandPalette"`
			} `json:"menus"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	titled := false
	for _, command := range manifest.Contributes.Commands {
		titled = titled || command.Command == "professor.chooseTheme" && command.Title == "Professor: Choose Theme"
	}
	if !titled {
		t.Errorf("package.json contributes no professor.chooseTheme titled %q: %+v", "Professor: Choose Theme",
			manifest.Contributes.Commands)
	}
	hidden := false
	for _, entry := range manifest.Contributes.Menus.CommandPalette {
		hidden = hidden || entry.Command == "professor.chooseTheme" && entry.When == "!remoteName || professor.pfmHere"
	}
	if !hidden {
		t.Errorf(
			"package.json commandPalette has no `!remoteName || professor.pfmHere` entry for professor.chooseTheme: %+v",
			manifest.Contributes.Menus.CommandPalette,
		)
	}
}

// TestVSCodeChooseThemeRunsInARemoteWindowWhosePfmIsHere: on a Linux host
// driven through Remote-SSH the extension host runs pfm, so the command lists
// and sets the theme there as in a local window.
func TestVSCodeChooseThemeRunsInARemoteWindowWhosePfmIsHere(t *testing.T) {
	t.Parallel()
	run, calls := runVSCodeChooseThemeIn(t, chooseWindow{"ssh-remote", 2}, "Dusk", fakePfm{list: chooseThemeList})
	if want := []string{"theme list --json", "theme use custom:dusk"}; !slices.Equal(calls, want) {
		t.Fatalf("remote window whose host runs pfm ran %q, want %q", calls, want)
	}
	if want := [][]any{{"setContext", "professor.pfmHere", true}}; !reflect.DeepEqual(run.Contexts, want) {
		t.Errorf("activate set contexts %v, want %v", run.Contexts, want)
	}
}
