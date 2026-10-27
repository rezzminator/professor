package claudelaunch

import (
	"encoding/json"
	"testing"
)

// Every Claude launch pfm renders turns auto mode off through settings, so
// the auto-mode permission classifier never runs in a fleet chat. A payload
// without the key reports which renderer and purpose dropped it.
func TestEveryLaunchDisablesAutoMode(t *testing.T) {
	home, machine := renderMachine(t)
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
		request := Request{Purpose: purpose, Home: home, Account: 2}
		if purpose == PurposeResume {
			request.Resume = "R"
		}
		_, parsed := renderParsed(t, request, machine)
		permissions, _ := parsed.Settings["permissions"].(map[string]any)
		if permissions["disableAutoMode"] != "disable" {
			t.Errorf(
				"purpose %d: --settings permissions=%#v, want disableAutoMode=disable",
				purpose,
				parsed.Settings["permissions"],
			)
		}
	}
	headless, err := RenderHeadless(nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Permissions map[string]any `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(headless), &payload); err != nil {
		t.Fatalf("headless payload %q: %v", headless, err)
	}
	if payload.Permissions["disableAutoMode"] != "disable" {
		t.Errorf("headless payload %s lacks permissions.disableAutoMode=disable", headless)
	}
}
