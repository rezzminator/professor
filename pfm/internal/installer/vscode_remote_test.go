package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// shippedChrome reads the window chrome every shipped profile pins
// (templates/themes/{name}/vscode/chrome.json), by profile name in order.
func shippedChrome(t *testing.T) ([]string, map[string]map[string]string) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "templates", "themes")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read shipped themes %s: %v", root, err)
	}
	var names []string
	chrome := map[string]map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, entry.Name(), "vscode", "chrome.json"))
		if err != nil {
			t.Fatalf("read %s chrome: %v", entry.Name(), err)
		}
		pins := map[string]string{}
		if err := json.Unmarshal(raw, &pins); err != nil {
			t.Fatalf("decode %s chrome: %v", entry.Name(), err)
		}
		names = append(names, entry.Name())
		chrome[entry.Name()] = pins
	}
	if len(names) < 2 {
		t.Fatalf("shipped themes %v: want at least two to walk between", names)
	}
	return names, chrome
}

// TestVSCodeRemoteWindowRepaintsItsFrameToEveryThemeInTurn is the user's window:
// Remote-SSH from a Mac, Professor running on the remote host (extensionKind
// Workspace), a saved workspace file on the Mac (scheme vscode-local) that
// Peacock painted. Walking every shipped theme as a new generation, each one's
// own chrome.json and terminal colours land only in Workspace, the keys a theme
// drops go back to what Peacock left, and the ack and its chrome entry advance
// with every generation: the frame is never stuck on the first theme it got.
func TestVSCodeRemoteWindowRepaintsItsFrameToEveryThemeInTurn(t *testing.T) {
	t.Parallel()
	names, chrome := shippedChrome(t)
	const file = "/w/mac/project.code-workspace"
	target := "vscode-local://ssh-remote+fixture" + file
	original := vscodePainted()
	global := map[string]any{"editor.background": "#000000"}
	window := vscodeScenario{
		RemoteName: "ssh-remote", ExtensionKind: 2, WorkspaceFile: file, WorkspaceScheme: "vscode-local",
		Global: global, Workspace: original,
	}
	for i, name := range names {
		window.Steps = append(window.Steps, vscodeChromeStepSpec{
			Name: name, Contract: chromeContract(11+i, true, terminalWanted, chrome[name]),
		})
	}
	steps := runVSCodeScenario(t, t.TempDir(), window)
	for i, name := range names {
		got := steps[i]
		if want := frameMerged(
			frameMerged(original, terminalWanted),
			chrome[name],
		); !reflect.DeepEqual(
			got.Workspace,
			want,
		) {
			t.Errorf(
				"%s: Workspace frame = %v, want the theme's own chrome over Peacock's %v",
				name,
				got.Workspace,
				want,
			)
		}
		if want := global; !reflect.DeepEqual(got.Colors, want) {
			t.Errorf("%s: Global = %v, want %v", name, got.Colors, want)
		}
		if got.Ack == nil || got.Ack.Generation == nil || *got.Ack.Generation != 11+i || got.Ack.Error != "" {
			t.Errorf("%s: ack = %+v, want generation %d without error", name, got.Ack, 11+i)
		}
		assertChromeEntry(t, got, target, 11+i, sortedKeys(chrome[name]))
	}
}

func TestVSCodeRemoteShadowsInheritedThemeColorsWithoutChangingGlobal(t *testing.T) {
	t.Parallel()
	global := map[string]any{
		"[Solarized Dark]": map[string]any{
			"terminal.background":       "#123456",
			"titleBar.activeBackground": "#654321",
			"editor.background":         "#002b36",
		},
	}
	original := map[string]any{"editor.lineHighlightBackground": "#234567"}
	steps := runVSCodeScenario(t, t.TempDir(), vscodeScenario{
		RemoteName: "ssh-remote", ExtensionKind: 2, Folder: "/remote/project",
		Global: global, Workspace: original,
		Steps: []vscodeChromeStepSpec{
			{Name: "on", Contract: chromeContract(1, true, terminalWanted, chromeWanted)},
			{Name: "off", Contract: chromeContract(2, false, nil, nil)},
		},
	})
	for _, step := range steps {
		if !reflect.DeepEqual(step.Colors, global) {
			t.Errorf("%s changed client Global settings: %v", step.Step, step.Colors)
		}
	}
	block, ok := steps[0].Workspace["[Solarized Dark]"].(map[string]any)
	if !ok || block["terminal.background"] != terminalWanted["terminal.background"] ||
		block["titleBar.activeBackground"] != chromeWanted["titleBar.activeBackground"] {
		t.Errorf("inherited managed colours not shadowed: %v", steps[0].Workspace)
	}
	if !reflect.DeepEqual(steps[1].Workspace, original) {
		t.Errorf("off retained workspace overrides: %v, want %v", steps[1].Workspace, original)
	}
}

// TestVSCodeRemoteWindowServedFromTheClientPaintsNothing: VS Code runs
// Professor on the client's side of a Remote-SSH window when the client's
// copy allows it (an older "ui" extensionKind, or a remote.extensionKind
// override); there it would read the client's contract and paint the client's
// theme over a window whose chats live on the remote host. It writes no
// colour, no ack and no record, whatever the generation, and says once, in a
// warning, why and how to fix it.
func TestVSCodeRemoteWindowServedFromTheClientPaintsNothing(t *testing.T) {
	t.Parallel()
	const file = "/w/mac/project.code-workspace"
	original := vscodePainted()
	global := map[string]any{"editor.background": "#000000"}
	steps := runVSCodeScenario(t, t.TempDir(), vscodeScenario{
		RemoteName: "ssh-remote", ExtensionKind: 1, WorkspaceFile: file, Global: global, Workspace: original,
		Steps: []vscodeChromeStepSpec{
			{Name: "gen-1", Contract: chromeContract(1, true, terminalWanted, chromeWanted)},
			{Name: "gen-2", Contract: chromeContract(2, true, terminalWanted, chromeWanted)},
		},
	})
	for _, got := range steps {
		if !reflect.DeepEqual(got.Colors, global) || !reflect.DeepEqual(got.Workspace, original) {
			t.Errorf("%s: Global = %v, Workspace = %v, want both untouched", got.Step, got.Colors, got.Workspace)
		}
		if got.Writes != 0 || got.WorkspaceUpdates != 0 || got.Ack != nil || got.ChromeRecord != nil || got.Record {
			t.Errorf(
				"%s: writes=%d workspaceUpdates=%d ack=%+v chromeRecord=%v record=%v, want nothing written",
				got.Step, got.Writes, got.WorkspaceUpdates, got.Ack, got.ChromeRecord, got.Record,
			)
		}
	}
	last := steps[len(steps)-1]
	if len(last.Messages) != 1 || last.Messages[0].Kind != "warning" {
		t.Fatalf("messages = %+v, want one warning", last.Messages)
	}
	for _, part := range []string{"ssh-remote", "remote.extensionKind", "pfm install"} {
		if !strings.Contains(last.Messages[0].Message, part) {
			t.Errorf("warning %q does not name %q", last.Messages[0].Message, part)
		}
	}
}

// A remote window's pick must run the remote host's pfm or say plainly why it
// did not: the client-side copy and a remote host without pfm both warn in the
// focused window, and run nothing anywhere.
func TestNativeColorThemePickInARemoteWindowPfmDoesNotRunInWarns(t *testing.T) {
	t.Parallel()
	for name, scenario := range map[string]nativeScenario{
		"remote host without pfm": {ColorTheme: "Sonar", RemoteName: "ssh-remote", ExtensionKind: 2, NoPfm: true},
		"served from the UI side": {ColorTheme: "Sonar", RemoteName: "ssh-remote", ExtensionKind: 1},
	} {
		scenario.Steps = []nativeStep{
			{Name: "seed", Contract: nativeContract(1, "custom:lagoon")},
			{Name: "native-pick", Pick: pick("professor-ultrafunk")},
		}
		steps := runVSCodeNative(t, scenario, 0, "")
		last := steps[len(steps)-1]
		if len(last.Calls) != 0 {
			t.Errorf("%s: pfm ran %q, want nothing run", name, last.Calls)
		}
		var warned []string
		for _, message := range last.Messages {
			if message.Kind == "warning" && strings.Contains(message.Message, "Ultrafunk") {
				warned = append(warned, message.Message)
			}
		}
		if len(warned) != 1 || !strings.Contains(warned[0], "ssh-remote") {
			t.Errorf(
				"%s: messages = %+v, want one warning naming the pick and the ssh-remote window",
				name,
				last.Messages,
			)
		}
	}
	unfocused := runVSCodeNative(t, nativeScenario{
		ColorTheme: "Sonar", RemoteName: "ssh-remote", ExtensionKind: 1, Unfocused: true,
		Steps: []nativeStep{{Name: "native-pick", Pick: pick("professor-ultrafunk")}},
	}, 0, "")
	for _, message := range unfocused[len(unfocused)-1].Messages {
		if strings.Contains(message.Message, "Ultrafunk") {
			t.Errorf("an unfocused window warned about the pick: %q", message.Message)
		}
	}
}

func TestNativeRemoteFollowUsesWorkspaceTheme(t *testing.T) {
	t.Parallel()
	steps := runVSCodeNative(t, nativeScenario{
		ColorTheme: "Sonar", RemoteName: "ssh-remote", ExtensionKind: 2,
		Steps: []nativeStep{
			{Name: "seed", Contract: nativeContract(1, "custom:lagoon")},
			{Name: "flip", Contract: nativeContract(2, "custom:ultrafunk")},
		},
	}, 0, "")
	last := steps[len(steps)-1]
	if len(last.Updates) != 1 || last.Updates[0].Key != "colorTheme" || last.Updates[0].Target != 2 {
		t.Fatalf("remote follow must not change client-wide theme: %+v", last.Updates)
	}
	if len(last.Calls) != 0 {
		t.Fatalf("following a remote contract resubmitted a picker change: %v", last.Calls)
	}
}
