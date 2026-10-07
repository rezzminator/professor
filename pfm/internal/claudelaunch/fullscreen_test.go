package claudelaunch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAccountSettings writes content as dir's settings.json; an empty
// content leaves the file absent.
func writeAccountSettings(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if content == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWantsFullscreen(t *testing.T) {
	for _, test := range []struct {
		name, settings string
		want           bool
		wantErr        bool
	}{
		{name: "fullscreen", settings: `{"tui":"fullscreen"}`, want: true},
		{name: "tui default", settings: `{"tui":"default"}`},
		{name: "absent key", settings: `{"theme":"dark"}`},
		{name: "absent file"},
		{name: "nested tui is not top level", settings: `{"env":{"tui":"fullscreen"}}`},
		{name: "invalid json", settings: `{"tui":`, wantErr: true},
		{name: "tui not a string", settings: `{"tui":1}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeAccountSettings(t, dir, test.settings)
			got, err := WantsFullscreen(dir)
			if got != test.want || (err != nil) != test.wantErr {
				t.Fatalf("WantsFullscreen = %v, %v; want %v, error %v", got, err, test.want, test.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), filepath.Join(dir, "settings.json")) {
				t.Errorf("error %q does not name the settings path", err)
			}
		})
	}
}

func TestWantsFullscreenUnreadableIsAnError(t *testing.T) {
	dir := t.TempDir()
	// A directory where the file belongs fails the read for any uid.
	if err := os.MkdirAll(filepath.Join(dir, "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := WantsFullscreen(dir); got || err == nil {
		t.Fatalf("WantsFullscreen = %v, %v; want false and an error", got, err)
	}
}

func TestRenderNoFlickerFollowsAccountSettings(t *testing.T) {
	for _, test := range []struct {
		name, settings string
		purpose        Purpose
		want           bool
	}{
		{name: "fullscreen interactive", settings: `{"tui":"fullscreen"}`, purpose: PurposeInteractive, want: true},
		{name: "fullscreen resume", settings: `{"tui":"fullscreen"}`, purpose: PurposeResume, want: true},
		{name: "fullscreen launcher", settings: `{"tui":"fullscreen"}`, purpose: PurposeLauncher, want: true},
		{name: "fullscreen query", settings: `{"tui":"fullscreen"}`, purpose: PurposeQuery},
		{name: "tui default", settings: `{"tui":"default"}`, purpose: PurposeInteractive},
		{name: "absent key", settings: `{}`, purpose: PurposeInteractive},
		{name: "absent file", purpose: PurposeInteractive},
		{name: "invalid json still launches", settings: `{"tui":`, purpose: PurposeInteractive},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, machine := renderMachine(t)
			writeAccountSettings(t, machine.Accounts[1].ConfigDir, test.settings)
			_, parsed := renderParsed(t, Request{Purpose: test.purpose, Home: home, Account: 2}, machine)
			value, present := parsed.SettingsEnv[envNoFlicker]
			if present != test.want || (present && value != "1") {
				t.Fatalf("%s present=%v value=%q, want present=%v", envNoFlicker, present, value, test.want)
			}
		})
	}
}

func TestRenderNoFlickerFollowsRequestConfigDir(t *testing.T) {
	home, machine := renderMachine(t)
	override := filepath.Join(t.TempDir(), "override")
	writeAccountSettings(t, override, `{"tui":"fullscreen"}`)
	_, parsed := renderParsed(
		t,
		Request{Purpose: PurposeInteractive, Home: home, Account: 2, ConfigDir: override},
		machine,
	)
	if parsed.SettingsEnv[envNoFlicker] != "1" {
		t.Fatalf("override config dir asks for fullscreen, env=%v", parsed.SettingsEnv)
	}
}

func TestResolveNoFlickerReportsAccountSettings(t *testing.T) {
	for _, test := range []struct {
		name, settings, want string
	}{
		{name: "fullscreen", settings: `{"tui":"fullscreen"}`, want: "true"},
		{name: "absent file", want: "false"},
		{name: "invalid json", settings: `{"tui":`, want: "settings.json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, machine := renderMachine(t)
			writeAccountSettings(t, machine.Accounts[1].ConfigDir, test.settings)
			for _, row := range Resolve(machine, 2) {
				if row.Knob.Name != knobNoFlicker {
					continue
				}
				if !strings.Contains(row.Value, test.want) || row.Won != "account" {
					t.Fatalf("noFlicker row = %q won %q, want %q from account", row.Value, row.Won, test.want)
				}
				return
			}
			t.Fatal("no noFlicker row")
		})
	}
}
