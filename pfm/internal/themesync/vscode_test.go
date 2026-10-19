package themesync

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestVscodeContractKeysAndValues(t *testing.T) {
	applied := tokyoNightApplied(t)
	contract := applied.vscodeContract(Dark, 4)
	if !contract.Enabled || contract.Generation != 4 || contract.Palette != "custom:tokyo-night" ||
		contract.Title != "Tokyo Night · neon pink" || contract.Mode != "dark" || contract.Appearance != "dark" {
		t.Fatalf("contract = %+v", contract)
	}
	if contract.Colors["terminal.background"] != "#0f0f1a" || contract.Colors["terminal.ansiRed"] != "#ff3d6e" {
		t.Fatalf("colors = %v", contract.Colors)
	}
	auto, err := resolveValue("auto", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := auto.Applied.vscodeContract(Light, 2); got.Mode != "auto" || got.Appearance != "light" ||
		got.Colors["terminal.background"] != "#ffffff" {
		t.Fatalf("auto contract under a light appearance = %+v", got)
	}
	wantKeys := []string{
		"terminal.background",
		"terminal.foreground",
		"terminalCursor.foreground",
		"terminal.selectionBackground",
		"terminal.ansiBlack",
		"terminal.ansiRed",
		"terminal.ansiGreen",
		"terminal.ansiYellow",
		"terminal.ansiBlue",
		"terminal.ansiMagenta",
		"terminal.ansiCyan",
		"terminal.ansiWhite",
		"terminal.ansiBrightBlack",
		"terminal.ansiBrightRed",
		"terminal.ansiBrightGreen",
		"terminal.ansiBrightYellow",
		"terminal.ansiBrightBlue",
		"terminal.ansiBrightMagenta",
		"terminal.ansiBrightCyan",
		"terminal.ansiBrightWhite",
		"terminal.tab.activeBorder",
	}
	if len(contract.Colors) != len(wantKeys) {
		t.Fatalf("colors has %d keys, want %d: %v", len(contract.Colors), len(wantKeys), contract.Colors)
	}
	for _, key := range wantKeys {
		if _, ok := contract.Colors[key]; !ok {
			t.Errorf("colors lacks %s", key)
		}
	}
	variant := applied.Primary
	if contract.Colors["terminal.ansiRed"] != variant.ANSI[1] ||
		contract.Colors["terminal.ansiBrightWhite"] != variant.ANSI[15] ||
		contract.Colors["terminal.tab.activeBorder"] != variant.Accent ||
		contract.Colors["terminal.background"] != variant.Background {
		t.Fatalf("colors = %v", contract.Colors)
	}
	if off := vscodeOffContract(5); off.Enabled || off.Generation != 5 || len(off.Colors) != 0 || off.Colors == nil {
		t.Fatalf("off contract = %+v, want enabled false, generation 5, an empty (non-nil) colors object", off)
	}
}

func TestVscodeContractCarriesTheWindowChrome(t *testing.T) {
	applied := tokyoNightApplied(t)
	variant := applied.Primary
	contract := applied.vscodeContract(Dark, 4)
	want := map[string]string{
		"titleBar.activeBackground":         variant.Surface,
		"activityBar.activeBackground":      variant.Surface,
		"activityBarTop.activeBackground":   variant.Surface,
		"statusBar.background":              variant.Surface,
		"titleBar.activeForeground":         variant.Foreground,
		"activityBar.foreground":            variant.Foreground,
		"activityBarTop.foreground":         variant.Foreground,
		"commandCenter.foreground":          variant.Foreground,
		"statusBar.foreground":              variant.Foreground,
		"titleBar.inactiveBackground":       variant.Background,
		"activityBar.background":            variant.Background,
		"activityBarTop.background":         variant.Background,
		"statusBar.inactiveBackground":      variant.Background,
		"activityBarBadge.foreground":       variant.Background,
		"statusBar.debuggingForeground":     variant.Background,
		"statusBarItem.remoteForeground":    variant.Background,
		"titleBar.inactiveForeground":       variant.Muted,
		"activityBar.inactiveForeground":    variant.Muted,
		"activityBarTop.inactiveForeground": variant.Muted,
		"activityBar.activeBorder":          variant.Accent,
		"activityBarTop.activeBorder":       variant.Accent,
		"activityBarBadge.background":       variant.Accent,
		"sash.hoverBorder":                  variant.Accent,
		"statusBar.debuggingBackground":     variant.Accent,
		"statusBarItem.remoteBackground":    variant.Accent,
		"commandCenter.border":              variant.Overlay,
		"statusBarItem.hoverBackground":     variant.Overlay,
	}
	if len(want) != 27 {
		t.Fatalf("test table has %d chrome keys, want 27", len(want))
	}
	if len(contract.Chrome) != len(want) {
		t.Fatalf("chrome has %d keys, want %d: %v", len(contract.Chrome), len(want), contract.Chrome)
	}
	for key, value := range want {
		if value == "" {
			t.Errorf("fixture variant has no value for %s", key)
		}
		if got, ok := contract.Chrome[key]; !ok || got != value {
			t.Errorf("chrome[%s] = %q (present %v), want %q", key, got, ok, value)
		}
	}
	for key := range contract.Chrome {
		if _, ok := contract.Colors[key]; ok {
			t.Errorf("%s is in both colors and chrome", key)
		}
	}
	for key := range contract.Colors {
		if !strings.HasPrefix(key, "terminal") {
			t.Errorf("colors carries the non-terminal key %s", key)
		}
	}
	if len(contract.Colors) != 21 {
		t.Errorf("colors has %d keys, want the 21 terminal keys", len(contract.Colors))
	}

	off := vscodeOffContract(5)
	if off.Chrome == nil || len(off.Chrome) != 0 {
		t.Fatalf("off chrome = %#v, want an empty non-nil map", off.Chrome)
	}
	wire, err := json.MarshalIndent(off, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"chrome": {}`) {
		t.Errorf("off contract serialises as %s, want \"chrome\": {}", wire)
	}
}

func TestVscodeVerdictJudgesWindowChrome(t *testing.T) {
	stale := 30 * time.Second
	broken := vscodeAck{Generation: 4, Chrome: map[string]vscodeChromeAck{
		"/w/zeta/zeta.code-workspace":   {Generation: 4, Keys: []string{"statusBar.background"}},
		"/w/alpha/alpha.code-workspace": {Generation: 4, Error: "read-only"},
		"/w/beta/beta.code-workspace":   {Generation: 4, Error: "bad json"},
	}}
	verdict, detail := vscodeVerdict(broken, true, 4, true, stale, "/s/vscode-ack.json")
	want := "window chrome alpha.code-workspace: read-only; window chrome beta.code-workspace: bad json"
	if verdict != VerdictError || detail != want {
		t.Errorf("chrome error = %s %q, want ERROR %q", verdict, detail, want)
	}

	clean := vscodeAck{Generation: 4, Chrome: map[string]vscodeChromeAck{
		"/w/a/a.code-workspace": {Generation: 4, Keys: []string{"statusBar.background"}},
		"/w/b/b.code-workspace": {Generation: 4, Keys: []string{"statusBar.background"}},
		"/w/c/c.code-workspace": {Generation: 3, Keys: []string{"statusBar.background"}},
	}}
	verdict, detail = vscodeVerdict(clean, true, 4, true, stale, "/s/vscode-ack.json")
	if verdict != VerdictInSync || detail != "window chrome in 2 local workspaces" {
		t.Errorf("two clean entries = %s %q, want IN-SYNC with 2 local workspaces", verdict, detail)
	}

	one := vscodeAck{Generation: 4, Chrome: map[string]vscodeChromeAck{"/w/a/a.code-workspace": {Generation: 4}}}
	verdict, detail = vscodeVerdict(one, true, 4, true, stale, "/s/vscode-ack.json")
	if verdict != VerdictInSync || detail != "window chrome in 1 local workspace" {
		t.Errorf("one clean entry = %s %q, want IN-SYNC with 1 local workspace", verdict, detail)
	}

	verdict, detail = vscodeVerdict(vscodeAck{Generation: 4}, true, 4, true, stale, "/s/vscode-ack.json")
	if verdict != VerdictInSync || detail != "" {
		t.Errorf("ack without chrome = %s %q, want IN-SYNC with no detail", verdict, detail)
	}

	staleOnly := vscodeAck{Generation: 4, Chrome: map[string]vscodeChromeAck{"/w/a/a.code-workspace": {Generation: 3}}}
	verdict, detail = vscodeVerdict(staleOnly, true, 4, true, stale, "/s/vscode-ack.json")
	if verdict != VerdictInSync || detail != "" {
		t.Errorf("only an old-generation chrome entry = %s %q, want IN-SYNC with no detail", verdict, detail)
	}

	// A window that failed at an earlier generation and has not opened since
	// is no current failure: the row stays IN-SYNC and names it as waiting.
	staleError := vscodeAck{Generation: 4, Chrome: map[string]vscodeChromeAck{
		"/w/a/a.code-workspace":     {Generation: 4, Keys: []string{"statusBar.background"}},
		"/w/old/old.code-workspace": {Generation: 3, Error: "Peacock repaints"},
	}}
	verdict, detail = vscodeVerdict(staleError, true, 4, true, stale, "/s/vscode-ack.json")
	wantDetail := "window chrome in 1 local workspace; window chrome old.code-workspace failed at generation 3 " +
		"(Peacock repaints); retried when it next opens"
	if verdict != VerdictInSync || detail != wantDetail {
		t.Errorf("an old-generation error = %s %q, want IN-SYNC %q", verdict, detail, wantDetail)
	}

	// A new generation not yet acknowledged reads PENDING, never the old error.
	verdict, _ = vscodeVerdict(staleError, true, 5, true, time.Second, "/s/vscode-ack.json")
	if verdict != VerdictPending {
		t.Errorf("an old error under a new generation = %s, want PENDING", verdict)
	}
}

func TestVscodeChromeOfAGoneWorkspaceNoLongerCounts(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.psVscode = "/Applications/Visual Studio Code.app/Contents/MacOS/Electron\n"
	h.rearm()
	present := h.path("work/kept.code-workspace")
	h.write("work/kept.code-workspace", "{}\n")
	gone := h.path("work/moved.code-workspace")
	ack := map[string]any{
		"generation": 1, "appliedAt": "2026-10-16T03:00:00Z", "keys": []string{"terminal.background"}, "error": "",
		"chrome": map[string]any{
			present: map[string]any{"generation": 1, "keys": []string{"statusBar.background"}, "error": ""},
			gone: map[string]any{
				"generation": 1, "keys": []string{"statusBar.background"}, "error": "Peacock repaints",
			},
		},
	}
	raw, err := json.Marshal(ack)
	if err != nil {
		t.Fatal(err)
	}
	h.write(".local/state/pfm/theme/vscode-ack.json", string(raw))
	wantVerdict(t, h.status(), "vscode", VerdictInSync, "window chrome in 1 local workspace")

	h.write("work/moved.code-workspace", "{}\n")
	wantVerdict(t, h.status(), "vscode", VerdictError, "window chrome moved.code-workspace: Peacock repaints")
}

func TestOffNamesTheWorkspacesStillCarryingChrome(t *testing.T) {
	h := newHarness(t)
	h.on()
	painted := h.path("work/painted.code-workspace")
	h.write("work/painted.code-workspace", "{}\n")
	gone := h.path("work/gone.code-workspace")
	record := map[string]any{
		painted: map[string]any{"generation": 1, "keys": map[string]any{
			"statusBar.background": map[string]any{"value": "#221634", "prior": nil},
		}},
		gone: map[string]any{"generation": 1, "keys": map[string]any{
			"statusBar.background": map[string]any{"value": "#221634", "prior": nil},
		}},
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	h.write(".local/state/pfm/theme/vscode-chrome.json", string(raw))
	off, err := Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, off, "vscode", VerdictPending, "window chrome stays in "+painted+" until")
	if strings.Contains(row(t, off, "vscode").Detail, "gone.code-workspace") {
		t.Errorf("off named a workspace file that no longer exists: %q", row(t, off, "vscode").Detail)
	}

	h.on()
	h.write(".local/state/pfm/theme/vscode-chrome.json", "{}\n")
	off, err = Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, off, "vscode", VerdictApplied, "the extension clears the colours")

	h.on()
	h.write(".local/state/pfm/theme/vscode-chrome.json", "not json")
	off, err = Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, off, "vscode", VerdictError, "vscode-chrome.json")
}

func TestVscodeContractWithoutChromeReadsDriftUntilTheNextSync(t *testing.T) {
	h := newHarness(t)
	h.on()
	rel := ".local/state/pfm/theme/current.json"
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(h.read(rel)), &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["chrome"]; !ok {
		t.Fatalf("current.json after on has no chrome field: %s", h.read(rel))
	}
	delete(fields, "chrome")
	old, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	h.write(rel, string(old)+"\n")
	wantVerdict(t, h.status(), "vscode", VerdictDrift, "current.json differs from the render")
	h.sync(true)
	if !strings.Contains(h.read(rel), `"chrome": {`) || strings.Contains(h.read(rel), `"chrome": {}`) {
		t.Errorf("current.json after the next sync lacks the chrome keys: %s", h.read(rel))
	}
	if got := row(t, h.status(), "vscode"); got.Verdict == VerdictDrift {
		t.Errorf("vscode row after the next sync = %s %q, want no DRIFT", got.Verdict, got.Detail)
	}
}

func TestVscodeVerdictMatrix(t *testing.T) {
	fresh := 3 * time.Second
	stale := 30 * time.Second
	cases := []struct {
		name    string
		ack     vscodeAck
		haveAck bool
		running bool
		age     time.Duration
		want    Verdict
		detail  string
	}{
		{"ack at the generation", vscodeAck{Generation: 4}, true, true, stale, VerdictInSync, ""},
		{"ack error", vscodeAck{Generation: 4, Error: "boom"}, true, true, stale, VerdictError, "boom"},
		{
			"stale ack and VS Code closed",
			vscodeAck{Generation: 3},
			true,
			false,
			stale,
			VerdictPending,
			"applies when VS Code starts",
		},
		{
			"no ack ever and VS Code closed",
			vscodeAck{},
			false,
			false,
			stale,
			VerdictPending,
			"applies when VS Code starts",
		},
		{
			"stale ack inside the grace window",
			vscodeAck{Generation: 3},
			true,
			true,
			fresh,
			VerdictPending,
			"waiting for the extension",
		},
		{
			"no ack ever after the grace window",
			vscodeAck{},
			false,
			true,
			stale,
			VerdictPending,
			"reload a VS Code window once: no Professor extension has acknowledged a theme yet (/s/vscode-ack.json absent)",
		},
		{
			"older generation after the grace window",
			vscodeAck{Generation: 2},
			true,
			true,
			stale,
			VerdictDrift,
			"generation 2, want 4",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict, detail := vscodeVerdict(tc.ack, tc.haveAck, 4, tc.running, tc.age, "/s/vscode-ack.json")
			if verdict != tc.want || !strings.Contains(detail, tc.detail) {
				t.Fatalf("vscodeVerdict = %s %q, want %s containing %q", verdict, detail, tc.want, tc.detail)
			}
		})
	}
}

func TestVscodeRunningOutsideMacOSSeesTheServerAndTheDesktopApp(t *testing.T) {
	cases := map[string]bool{
		"/h/.vscode-server/cli/servers/Stable-abc/server/node /h/.vscode-server/cli/servers/" +
			"Stable-abc/server/out/server-main.js\n": true,
		"/h/.vscode-server/bin/abc/node /h/.vscode-server/bin/abc/out/server-main.js\n": true,
		"/usr/share/code/code --type=renderer\n":                                        true,
		"/snap/code/180/usr/share/code/code\n":                                          true,
		"/bin/zsh -l\n/usr/bin/vim /h/.vscode-server/data/Machine/settings.json\n":      false,
		"/Applications/Visual Studio Code.app/Contents/MacOS/Electron\n":                false,
	}
	for ps, want := range cases {
		if got := vscodeRunning(ps, vscodeLinuxMarkers); got != want {
			t.Errorf("vscodeRunning(%q, linux) = %v, want %v", ps, got, want)
		}
	}
}

func TestVscodeRunningIsTheMainBinaryOnly(t *testing.T) {
	cases := map[string]bool{
		"/Applications/Visual Studio Code.app/Contents/MacOS/Electron\n":                                      true,
		"/Applications/Visual Studio Code.app/Contents/MacOS/Code\n":                                          true,
		"/Applications/Code.app/Contents/MacOS/Code --foo\n":                                                  true,
		"/Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper.app/Contents/MacOS/Code Helper": false,
		"/bin/zsh -l\n/usr/bin/vim notes-on-Code.app.txt\n":                                                   false,
	}
	for ps, want := range cases {
		if got := vscodeRunning(ps, vscodeAppMarkers); got != want {
			t.Errorf("vscodeRunning(%q) = %v, want %v", ps, got, want)
		}
	}
}
