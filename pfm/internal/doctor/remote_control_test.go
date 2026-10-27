package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Remote Control can be requested but unavailable behind an API gateway.
// Exercise Run so an unregistered check cannot silently pass this regression.
func TestDoctorRemoteControlReportsSettingsGateway(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	for _, account := range runtime.Config.Accounts {
		path := filepath.Join(account.ConfigDir, "settings.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var settings map[string]any
		if err := json.Unmarshal(raw, &settings); err != nil {
			t.Fatal(err)
		}
		env, ok := settings["env"].(map[string]any)
		if !ok {
			env = map[string]any{}
			settings["env"] = env
		}
		env["ANTHROPIC_BASE_URL"] = "https://user:secret@gateway.example.test/private?token=secret"
		raw, err = json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	runDoctor(nil, &stdout, &stderr, runtime)
	var rows []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "doctor: remote_control claude[") {
			rows = append(rows, line)
		}
	}
	if len(rows) != len(runtime.Config.Accounts) || len(rows) == 0 {
		t.Fatalf(
			"missing per-account Remote Control rows: got %d, accounts %d\n%s",
			len(rows), len(runtime.Config.Accounts), stdout.String(),
		)
	}
	for _, row := range rows {
		if !strings.Contains(row, "BLOCKED") || !strings.Contains(row, "ANTHROPIC_BASE_URL") ||
			!strings.Contains(row, "gateway.example.test") {
			t.Errorf("gateway was not reported as a blocker: %s", row)
		}
		if strings.Contains(row, "secret") || strings.Contains(row, "/private") || strings.Contains(row, "https://") {
			t.Errorf("Remote Control row leaked a URL or credential: %s", row)
		}
	}
}

func TestDoctorRemoteControlWithoutRoster(t *testing.T) {
	for _, test := range []struct {
		name, settings, want string
		off                  bool
	}{
		{"on", `{}`, "on (knob on", false},
		{"off", `{}`, "off by config", true},
		{"gateway", `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.test"}}`, "BLOCKED: settings env", false},
		{"malformed", `{`, "FAIL parse settings", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			runtime.Config.Accounts = nil
			runtime.Config.Claude.RemoteControl = !test.off
			dir := filepath.Join(runtime.Paths.Home, ".claude")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(test.settings), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			runDoctor(nil, &stdout, &stderr, runtime)
			if !strings.Contains(stdout.String(), "doctor: remote_control claude[0] "+test.want) {
				t.Fatalf("rosterless Remote Control verdict missing: %s", stdout.String())
			}
		})
	}
}

func TestRemoteControlDoctorStates(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings string
		launch   map[string]string
		off      bool
		missing  bool
		unread   bool
		dangling bool
		want     string
		warnings int
		failures int
	}{
		{name: "on is intent only", settings: `{}`, want: "on (knob on; no blocker seen; login and connection not verified)"},
		{name: "off by config", settings: `{}`, off: true, want: "off by config"},
		{name: "no settings has no settings blockers", missing: true, want: "on (knob on"},
		{name: "missing settings cannot hide surviving launch blocker", missing: true, launch: map[string]string{"DISABLE_GROWTHBOOK": "1"}, want: "BLOCKED: launch env DISABLE_GROWTHBOOK", warnings: 1},
		{name: "unreadable", unread: true, want: "FAIL read settings", failures: 1},
		{name: "dangling settings symlink", dangling: true, want: "FAIL read settings", failures: 1},
		{name: "off cannot hide unreadable settings", off: true, unread: true, want: "FAIL read settings", failures: 1},
		{name: "invalid JSON", settings: `{"env":`, want: "FAIL parse settings", failures: 1},
		{name: "null object", settings: `null`, want: "FAIL parse settings", failures: 1},
		{name: "wrong env type", settings: `{"env":{"ANTHROPIC_API_KEY":123}}`, want: "FAIL parse settings", failures: 1},
		{name: "wrong disable type", settings: `{"disableRemoteControl":"true"}`, want: "FAIL parse settings", failures: 1},
		{name: "official API", settings: `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`, want: "on (knob on"},
		{name: "official API case insensitive", settings: `{}`, launch: map[string]string{"ANTHROPIC_BASE_URL": "https://API.ANTHROPIC.COM/"}, want: "on (knob on"},
		{name: "settings gateway", settings: `{"env":{"ANTHROPIC_BASE_URL":"https://user:secret@gateway.example.test/secret?token=secret"}}`, want: "BLOCKED: settings env ANTHROPIC_BASE_URL host=gateway.example.test", warnings: 1},
		{name: "settings symlink", settings: `{"disableRemoteControl":true}`, want: "BLOCKED: settings disableRemoteControl=true", warnings: 1},
		{name: "explicit setting off", settings: `{"disableRemoteControl":false}`, want: "on (knob on"},
		{name: "helper incompatible", settings: `{"apiKeyHelper":"echo secret"}`, want: "BLOCKED: settings apiKeyHelper is incompatible", warnings: 1},
		{name: "malformed URL is private", settings: `{"env":{"ANTHROPIC_BASE_URL":"https://secret:%secret@bad"}}`, want: "BLOCKED: settings env ANTHROPIC_BASE_URL has an invalid URL host", warnings: 1},
		{name: "opaque URL is private", settings: `{"env":{"ANTHROPIC_BASE_URL":"secret:secret"}}`, want: "BLOCKED: settings env ANTHROPIC_BASE_URL has an invalid URL host", warnings: 1},
		{name: "deceptive hostname", settings: `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com.example.test/secret"}}`, want: "BLOCKED: settings env ANTHROPIC_BASE_URL host=api.anthropic.com.example.test", warnings: 1},
		{name: "IPv6 gateway", settings: `{"env":{"ANTHROPIC_BASE_URL":"http://[::1]:1234/secret"}}`, want: "BLOCKED: settings env ANTHROPIC_BASE_URL host=::1", warnings: 1},
		{name: "process gateway removed by launch hygiene", settings: `{}`, launch: map[string]string{"ANTHROPIC_BASE_URL": "https://gateway.example.test/secret"}, want: "on (knob on"},
		{name: "launch cannot hide settings gateway", settings: `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.test/secret"}}`, launch: map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com"}, want: "BLOCKED: settings env ANTHROPIC_BASE_URL host=gateway.example.test", warnings: 1},
		{name: "false env flags", settings: `{"env":{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":"0","DISABLE_GROWTHBOOK":"false"}}`, want: "on (knob on"},
		{name: "settings false overrides surviving process flag", settings: `{"env":{"DISABLE_GROWTHBOOK":"false"}}`, launch: map[string]string{"DISABLE_GROWTHBOOK": "1"}, want: "on (knob on"},
		{name: "settings empty overrides surviving process token", settings: `{"env":{"CLAUDE_CODE_OAUTH_TOKEN":""}}`, launch: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "secret"}, want: "on (knob on"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			settings := filepath.Join(dir, "settings.json")
			switch {
			case test.unread:
				if err := os.Mkdir(settings, 0o700); err != nil {
					t.Fatal(err)
				}
			case !test.missing:
				shared := filepath.Join(t.TempDir(), "settings.json")
				if !test.dangling {
					if err := os.WriteFile(shared, []byte(test.settings), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(shared, settings); err != nil {
					t.Fatal(err)
				}
			}
			machine := config.Config{
				Claude:   config.ClaudePrefs{RemoteControl: !test.off},
				Accounts: []config.Account{{ID: 7, ConfigDir: dir}},
			}
			var output bytes.Buffer
			tally := &doctorTally{}
			printRemoteControlDoctor(
				&output,
				filepath.Join(t.TempDir(), "managed-settings.d"),
				machine,
				&paths.MapEnv{Values: test.launch},
				tally,
			)
			if !strings.Contains(output.String(), "doctor: remote_control claude[7] "+test.want) ||
				tally.warnings != test.warnings || tally.failures != test.failures {
				t.Fatalf("warnings=%d failures=%d, want %d and %d; output=%s",
					tally.warnings, tally.failures, test.warnings, test.failures, output.String())
			}
			if strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "https://") {
				t.Fatalf("Remote Control diagnostics leaked a credential or URL: %s", output.String())
			}
		})
	}
}

func TestRemoteControlDoctorEnvBlockers(t *testing.T) {
	// Settings env applies after launch hygiene. Only surviving process values block.
	// This independently pins the observed launch contract without copying its strip list.
	for _, name := range []string{
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_GROWTHBOOK",
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
	} {
		for _, source := range []string{"settings env", "launch env"} {
			t.Run(name+"/"+source, func(t *testing.T) {
				dir := t.TempDir()
				value := "secret"
				if strings.Contains(name, "DISABLE") {
					value = "1"
				}
				settings := `{}`
				launch := map[string]string{}
				if source == "settings env" {
					settings = fmt.Sprintf(`{"env":{%q:%q}}`, name, value)
				} else {
					launch[name] = value
				}
				if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600); err != nil {
					t.Fatal(err)
				}
				machine := config.Config{
					Claude:   config.ClaudePrefs{RemoteControl: true},
					Accounts: []config.Account{{ID: 1, ConfigDir: dir}},
				}
				var output bytes.Buffer
				tally := &doctorTally{}
				printRemoteControlDoctor(
					&output,
					filepath.Join(t.TempDir(), "managed-settings.d"),
					machine,
					&paths.MapEnv{Values: launch},
					tally,
				)
				want, warnings := "BLOCKED: "+source+" "+name, 1
				if source == "launch env" && name != "DISABLE_GROWTHBOOK" && name != "CLAUDE_CODE_OAUTH_TOKEN" {
					want, warnings = "on (knob on", 0
				}
				if !strings.Contains(output.String(), want) ||
					tally.warnings != warnings || tally.failures != 0 || strings.Contains(output.String(), "secret") {
					t.Fatalf("warnings=%d failures=%d output=%s", tally.warnings, tally.failures, output.String())
				}
			})
		}
	}
}

func TestRemoteControlDoctorAccountOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	machine := config.Config{
		Claude: config.ClaudePrefs{RemoteControl: false},
		Accounts: []config.Account{
			{ID: 1, ConfigDir: dir},
			{ID: 2, ConfigDir: dir, Claude: &config.ClaudePrefs{RemoteControl: true}},
		},
	}
	var output bytes.Buffer
	tally := &doctorTally{}
	printRemoteControlDoctor(&output, filepath.Join(t.TempDir(), "managed-settings.d"), machine, &paths.MapEnv{}, tally)
	if !strings.Contains(output.String(), "claude[1] off by config") ||
		!strings.Contains(output.String(), "claude[2] on (knob on") || tally.warnings != 0 || tally.failures != 0 {
		t.Fatalf("account override/shared settings report: warnings=%d failures=%d output=%s",
			tally.warnings, tally.failures, output.String())
	}
}
