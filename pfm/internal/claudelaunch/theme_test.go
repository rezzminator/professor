package claudelaunch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestRefreshThemePin(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	statePath := filepath.Join(home, ".local", "state", "pfm", "theme", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{"separate", []string{
			flagSettings, `{"theme":"caller"}`, flagSettings,
			`{"theme":"dark","counter":9007199254740993}`,
		}, `{"counter":9007199254740993}`, false},
		{
			"inline",
			[]string{flagSettings + `={"theme":"dark","counter":9007199254740993}`},
			flagSettings + `={"counter":9007199254740993}`, false,
		},
		{"file", []string{flagSettings, "caller.json"}, "caller.json", false},
		{
			"unpinned",
			[]string{flagSettings, `{"counter":9007199254740993}`},
			`{"counter":9007199254740993}`, false,
		},
		{"malformed", []string{flagSettings, `{`}, "", true},
		{"missing value", []string{flagSettings}, "", true},
		{"no settings", []string{"--resume", "saved"}, "saved", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]string(nil), tc.args...)
			got, err := RefreshThemePin(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("RefreshThemePin=%q error=%v", got, err)
			}
			if tc.wantErr {
				return
			}
			if got[len(got)-1] != tc.want {
				t.Fatalf("refreshed=%q, want final %q", got, tc.want)
			}
			for index, word := range original {
				if tc.args[index] != word || (index < len(got)-1 && got[index] != word) {
					t.Fatalf("caller args changed: before=%q after=%q input=%q", original, got, tc.args)
				}
			}
		})
	}
}

func TestRenderThemeSyncOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, state        string
		synced, unreadable bool
	}{
		{name: "missing"},
		{name: "disabled", state: `{"enabled":false}`},
		{name: "enabled", state: `{"enabled":true}`, synced: true},
		{name: "malformed", state: `{`, unreadable: true},
		{name: "unreadable", unreadable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, machine := renderMachine(t)
			t.Setenv(paths.EnvHome, home)
			installHome := t.TempDir()
			installFakePFM(t, installHome)
			statePath := filepath.Join(home, ".local", "state", "pfm", "theme", "state.json")
			if tc.name != "missing" {
				if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
					t.Fatal(err)
				}
				if tc.name == "unreadable" {
					// A directory fails ReadFile even when the fence runs as root.
					if err := os.Mkdir(statePath, 0o700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(statePath, []byte(tc.state), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			machine.Claude.Theme = "dark"
			machine.Accounts[1].Claude = &pfmconfig.ClaudePrefs{Theme: "custom:launch-pin"}
			_, recorder := obs.Test(t)
			check := func(settings map[string]any) {
				t.Helper()
				value, carried := settings["theme"]
				if carried == tc.synced || (carried && value != "custom:launch-pin") {
					t.Errorf("theme=%v carried=%t, want carried=%t", value, carried, !tc.synced)
				}
			}
			for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
				_, parsed := renderParsed(t, Request{Purpose: purpose, Home: installHome, Account: 2}, machine)
				check(parsed.Settings)
			}
			payload, err := RenderHeadless(map[string]any{"theme": "custom:launch-pin"})
			if err != nil {
				t.Fatal(err)
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(payload), &settings); err != nil {
				t.Fatal(err)
			}
			check(settings)
			found := false
			for _, row := range Resolve(machine, 2) {
				if row.Knob.Name != "theme" {
					continue
				}
				found = true
				if tc.synced && (row.Value != "unset" || row.Won != "theme sync owns it") {
					t.Errorf("sync-owned theme row=%#v", row)
				}
				if !tc.synced && row.Value != "custom:launch-pin" {
					t.Errorf("pinned theme row=%#v", row)
				}
				if tc.unreadable && !strings.Contains(row.Won, "theme sync state unreadable") {
					t.Errorf("unreadable state not reported in theme row=%#v", row)
				}
			}
			if !found {
				t.Error("config output is missing the theme row")
			}
			if tc.unreadable && (!strings.Contains(recorder.Raw(), statePath) ||
				!strings.Contains(recorder.Raw(), "keeping configured theme")) {
				t.Errorf("state failure not logged with fallback and path: %s", recorder.Raw())
			}
		})
	}
}
