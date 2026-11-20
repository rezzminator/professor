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

// accountTUI is a claude.settings layer setting tui to null: the launch carries no renderer of its
// own, so the account's settings.json decides.
var accountTUI = map[string]any{"tui": nil}

func TestWantsFullscreen(t *testing.T) {
	for _, test := range []struct {
		name, settings string
		claude         map[string]any
		want           bool
		wantErr        bool
	}{
		{name: "fullscreen", settings: `{"tui":"fullscreen"}`, claude: accountTUI, want: true},
		{name: "tui default", settings: `{"tui":"default"}`, claude: accountTUI},
		{name: "absent key", settings: `{"theme":"dark"}`, claude: accountTUI},
		{name: "absent file", claude: accountTUI},
		{name: "nested tui is not top level", settings: `{"env":{"tui":"fullscreen"}}`, claude: accountTUI},
		{name: "invalid json", settings: `{"tui":`, claude: accountTUI, wantErr: true},
		{name: "tui not a string", settings: `{"tui":1}`, claude: accountTUI, wantErr: true},
		{name: "shipped default outranks the account's tui default", settings: `{"tui":"default"}`, want: true},
		{name: "shipped default with no settings file", want: true},
		{name: "shipped default leaves an unparsable account file unread", settings: `{"tui":`, want: true},
		{name: "claude.settings tui not a string", settings: `{"tui":"fullscreen"}`, claude: map[string]any{"tui": 1}, wantErr: true},
		{name: "claude.settings tui default outranks the account's fullscreen", settings: `{"tui":"fullscreen"}`, claude: map[string]any{"tui": "default"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeAccountSettings(t, dir, test.settings)
			got, err := WantsFullscreen(dir, test.claude)
			if got != test.want || (err != nil) != test.wantErr {
				t.Fatalf("WantsFullscreen = %v, %v; want %v, error %v", got, err, test.want, test.wantErr)
			}
			if err != nil && test.claude["tui"] == nil &&
				!strings.Contains(err.Error(), filepath.Join(dir, "settings.json")) {
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
	if got, err := WantsFullscreen(dir, accountTUI); got || err == nil {
		t.Fatalf("WantsFullscreen = %v, %v; want false and an error", got, err)
	}
}

func TestRenderNoFlickerFollowsAccountSettings(t *testing.T) {
	for _, test := range []struct {
		name, settings string
		claude         map[string]any
		args           []string
		purpose        Purpose
		want           bool
	}{
		{name: "caller --settings tui default", args: []string{"--settings", `{"tui":"default"}`}, purpose: PurposeInteractive},
		{name: "caller --settings= tui fullscreen", settings: `{"tui":"default"}`, claude: accountTUI, args: []string{`--settings={"tui":"fullscreen"}`}, purpose: PurposeInteractive, want: true},
		{name: "claude.settings tui not a string launches without", claude: map[string]any{"tui": 1}, settings: `{"tui":"fullscreen"}`, purpose: PurposeInteractive},
		{name: "fullscreen interactive", settings: `{"tui":"fullscreen"}`, claude: accountTUI, purpose: PurposeInteractive, want: true},
		{name: "fullscreen resume", settings: `{"tui":"fullscreen"}`, claude: accountTUI, purpose: PurposeResume, want: true},
		{name: "fullscreen launcher", settings: `{"tui":"fullscreen"}`, claude: accountTUI, purpose: PurposeLauncher, want: true},
		{name: "fullscreen query", settings: `{"tui":"fullscreen"}`, claude: accountTUI, purpose: PurposeQuery},
		{name: "tui default", settings: `{"tui":"default"}`, claude: accountTUI, purpose: PurposeInteractive},
		{name: "absent key", settings: `{}`, claude: accountTUI, purpose: PurposeInteractive},
		{name: "absent file", claude: accountTUI, purpose: PurposeInteractive},
		{name: "invalid json still launches", settings: `{"tui":`, claude: accountTUI, purpose: PurposeInteractive},
		{name: "shipped default interactive", settings: `{"tui":"default"}`, purpose: PurposeInteractive, want: true},
		{name: "shipped default query", purpose: PurposeQuery},
		{name: "claude.settings tui default", settings: `{"tui":"fullscreen"}`, claude: map[string]any{"tui": "default"}, purpose: PurposeInteractive},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, machine := renderMachine(t)
			machine.Claude.Settings = test.claude
			writeAccountSettings(t, machine.Accounts[1].ConfigDir, test.settings)
			_, parsed := renderParsed(
				t,
				Request{Purpose: test.purpose, Home: home, Account: 2, Args: test.args},
				machine,
			)
			value, present := parsed.SettingsEnv[envNoFlicker]
			if present != test.want || (present && value != "1") {
				t.Fatalf("%s present=%v value=%q, want present=%v", envNoFlicker, present, value, test.want)
			}
		})
	}
}

func TestRenderNoFlickerFollowsRequestConfigDir(t *testing.T) {
	home, machine := renderMachine(t)
	machine.Claude.Settings = accountTUI
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
		claude               map[string]any
	}{
		{name: "fullscreen", settings: `{"tui":"fullscreen"}`, claude: accountTUI, want: "true"},
		{name: "absent file", claude: accountTUI, want: "false"},
		{name: "invalid json", settings: `{"tui":`, claude: accountTUI, want: "parse {config}/settings.json: unexpected end of JSON input"},
		{name: "shipped default", settings: `{"tui":"default"}`, want: "true"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, machine := renderMachine(t)
			machine.Claude.Settings = test.claude
			writeAccountSettings(t, machine.Accounts[1].ConfigDir, test.settings)
			for _, row := range Resolve(machine, 2) {
				if row.Knob.Name != knobNoFlicker {
					continue
				}
				if got := strings.ReplaceAll(
					row.Value,
					machine.Accounts[1].ConfigDir,
					"{config}",
				); got != test.want ||
					row.Won != "account" {
					t.Fatalf("noFlicker row = %q, want %q (won %q)", got, test.want, row.Won)
				}
				return
			}
			t.Fatal("no noFlicker row")
		})
	}
}
