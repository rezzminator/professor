package themesync

import (
	"strings"
	"testing"
	"time"
)

func TestVscodeContractKeysAndValues(t *testing.T) {
	palette := tokyoNight(t)
	contract := palette.vscodeContract(ModeAuto, Dark, 4)
	if !contract.Enabled || contract.Generation != 4 || contract.Palette != "tokyo-night" ||
		contract.Title != "Tokyo Night" ||
		contract.Mode != "auto" ||
		contract.Appearance != "dark" {
		t.Fatalf("contract = %+v", contract)
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
	variant := palette.Dark
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
		{"stale ack after the grace window", vscodeAck{Generation: 3}, true, true, stale, VerdictDrift, "generation 3"},
		{"no ack after the grace window", vscodeAck{}, false, true, stale, VerdictDrift, "no ack"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict, detail := vscodeVerdict(tc.ack, tc.haveAck, 4, tc.running, tc.age)
			if verdict != tc.want || !strings.Contains(detail, tc.detail) {
				t.Fatalf("vscodeVerdict = %s %q, want %s containing %q", verdict, detail, tc.want, tc.detail)
			}
		})
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
		if got := vscodeRunning(ps); got != want {
			t.Errorf("vscodeRunning(%q) = %v, want %v", ps, got, want)
		}
	}
}
