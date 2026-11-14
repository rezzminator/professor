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
	if got, want := rooted(
		doctorRows(stdout.String(), "doctor: remote_control claude["),
		runtime.Paths.Home,
	), "doctor: remote_control claude[1] BLOCKED: settings env ANTHROPIC_BASE_URL host=gateway.example.test is not api.anthropic.com\n"; got != want {
		t.Errorf("Remote Control rows = %q, want %q", got, want)
	}
}

func TestDoctorRemoteControlWithoutRoster(t *testing.T) {
	for _, test := range []struct {
		name, settings, want string
		off                  bool
	}{
		{"on", `{}`, "doctor: remote_control claude[0] on (knob on; no blocker seen; login and connection not verified)\n", false},
		{"off", `{}`, "doctor: remote_control claude[0] off by config (claude.remoteControl=false)\n", true},
		{"gateway", `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.test"}}`, "doctor: remote_control claude[0] BLOCKED: settings env ANTHROPIC_BASE_URL host=gateway.example.test is not api.anthropic.com\n", false},
		{"malformed", `{`, "doctor: remote_control claude[0] FAIL parse settings {root}/.claude/settings.json: invalid settings object\n", false},
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
			if got := rooted(
				doctorRows(stdout.String(), "doctor: remote_control claude[0]"),
				runtime.Paths.Home,
			); got != test.want {
				t.Errorf("rosterless Remote Control row = %q, want %q", got, test.want)
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
		{name: "on is intent only", settings: `{}`, want: "doctor: remote_control claude[7] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "off by config", settings: `{}`, off: true, want: "doctor: remote_control claude[7] off by config (claude.remoteControl=false)\n"},
		{name: "no settings has no settings blockers", missing: true, want: "doctor: remote_control claude[7] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "missing settings cannot hide surviving launch blocker", missing: true, launch: map[string]string{"DISABLE_GROWTHBOOK": "1"}, want: "doctor: remote_control claude[7] BLOCKED: launch env DISABLE_GROWTHBOOK disables Remote Control discovery\n", warnings: 1},
		{name: "unreadable", unread: true, want: "doctor: remote_control claude[7] FAIL read settings {root}/001/settings.json: read {root}/001/settings.json: is a directory\n", failures: 1},
		{name: "dangling settings symlink", dangling: true, want: "doctor: remote_control claude[7] FAIL read settings {root}/001/settings.json: open {root}/001/settings.json: no such file or directory\n", failures: 1},
		{name: "off cannot hide unreadable settings", off: true, unread: true, want: "doctor: remote_control claude[7] FAIL read settings {root}/001/settings.json: read {root}/001/settings.json: is a directory\n", failures: 1},
		{name: "invalid JSON", settings: `{"env":`, want: "doctor: remote_control claude[7] FAIL parse settings {root}/001/settings.json: invalid settings object\n", failures: 1},
		{name: "null object", settings: `null`, want: "doctor: remote_control claude[7] FAIL parse settings {root}/001/settings.json: expected settings object\n", failures: 1},
		{name: "wrong env type", settings: `{"env":{"ANTHROPIC_API_KEY":123}}`, want: "doctor: remote_control claude[7] FAIL parse settings {root}/001/settings.json: invalid settings object\n", failures: 1},
		{name: "wrong disable type", settings: `{"disableRemoteControl":"true"}`, want: "doctor: remote_control claude[7] FAIL parse settings {root}/001/settings.json: invalid settings object\n", failures: 1},
		{name: "official API", settings: `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`, want: "doctor: remote_control claude[7] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "official API case insensitive", settings: `{}`, launch: map[string]string{"ANTHROPIC_BASE_URL": "https://API.ANTHROPIC.COM/"}, want: "doctor: remote_control claude[7] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "settings gateway", settings: `{"env":{"ANTHROPIC_BASE_URL":"https://user:secret@gateway.example.test/secret?token=secret"}}`, want: "doctor: remote_control claude[7] BLOCKED: settings env ANTHROPIC_BASE_URL host=gateway.example.test is not api.anthropic.com\n", warnings: 1},
		{name: "settings symlink", settings: `{"disableRemoteControl":true}`, want: "doctor: remote_control claude[7] BLOCKED: settings disableRemoteControl=true\n", warnings: 1},
		{name: "explicit setting off", settings: `{"disableRemoteControl":false}`, want: "doctor: remote_control claude[7] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "helper incompatible", settings: `{"apiKeyHelper":"echo secret"}`, want: "doctor: remote_control claude[7] BLOCKED: settings apiKeyHelper is incompatible with Remote Control\n", warnings: 1},
		{name: "malformed URL is private", settings: `{"env":{"ANTHROPIC_BASE_URL":"https://secret:%secret@bad"}}`, want: "doctor: remote_control claude[7] BLOCKED: settings env ANTHROPIC_BASE_URL has an invalid URL host\n", warnings: 1},
		{name: "opaque URL is private", settings: `{"env":{"ANTHROPIC_BASE_URL":"secret:secret"}}`, want: "doctor: remote_control claude[7] BLOCKED: settings env ANTHROPIC_BASE_URL has an invalid URL host\n", warnings: 1},
		{name: "deceptive hostname", settings: `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com.example.test/secret"}}`, want: "doctor: remote_control claude[7] BLOCKED: settings env ANTHROPIC_BASE_URL host=api.anthropic.com.example.test is not api.anthropic.com\n", warnings: 1},
		{name: "IPv6 gateway", settings: `{"env":{"ANTHROPIC_BASE_URL":"http://[::1]:1234/secret"}}`, want: "doctor: remote_control claude[7] BLOCKED: settings env ANTHROPIC_BASE_URL host=::1 is not api.anthropic.com\n", warnings: 1},
		{name: "process gateway removed by launch hygiene", settings: `{}`, launch: map[string]string{"ANTHROPIC_BASE_URL": "https://gateway.example.test/secret"}, want: "doctor: remote_control claude[7] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "launch cannot hide settings gateway", settings: `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.test/secret"}}`, launch: map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com"}, want: "doctor: remote_control claude[7] BLOCKED: settings env ANTHROPIC_BASE_URL host=gateway.example.test is not api.anthropic.com\n", warnings: 1},
		{name: "false env flags", settings: `{"env":{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":"0","DISABLE_GROWTHBOOK":"false"}}`, want: "doctor: remote_control claude[7] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "settings false overrides surviving process flag", settings: `{"env":{"DISABLE_GROWTHBOOK":"false"}}`, launch: map[string]string{"DISABLE_GROWTHBOOK": "1"}, want: "doctor: remote_control claude[7] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "settings empty overrides surviving process token", settings: `{"env":{"CLAUDE_CODE_OAUTH_TOKEN":""}}`, launch: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "secret"}, want: "doctor: remote_control claude[7] on (knob on; no blocker seen; login and connection not verified)\n"},
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
			if got := rooted(output.String(), filepath.Dir(dir)); got != test.want || tally.warnings != test.warnings ||
				tally.failures != test.failures {
				t.Errorf("output = %q, want %q; warnings=%d failures=%d, want %d %d",
					got, test.want, tally.warnings, tally.failures, test.warnings, test.failures)
			}
		})
	}
}

func TestRemoteControlDoctorEnvBlockers(t *testing.T) {
	// Settings env applies after launch hygiene. Only surviving process values block.
	// This independently pins the observed launch contract without copying its strip list.
	wants := map[string]string{
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC/settings env": "doctor: remote_control claude[1] BLOCKED: settings env CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC disables Remote Control discovery\n",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC/launch env":   "doctor: remote_control claude[1] on (knob on; no blocker seen; login and connection not verified)\n",
		"DISABLE_GROWTHBOOK/settings env":                       "doctor: remote_control claude[1] BLOCKED: settings env DISABLE_GROWTHBOOK disables Remote Control discovery\n",
		"DISABLE_GROWTHBOOK/launch env":                         "doctor: remote_control claude[1] BLOCKED: launch env DISABLE_GROWTHBOOK disables Remote Control discovery\n",
		"ANTHROPIC_API_KEY/settings env":                        "doctor: remote_control claude[1] BLOCKED: settings env ANTHROPIC_API_KEY is incompatible with Remote Control\n",
		"ANTHROPIC_API_KEY/launch env":                          "doctor: remote_control claude[1] on (knob on; no blocker seen; login and connection not verified)\n",
		"ANTHROPIC_AUTH_TOKEN/settings env":                     "doctor: remote_control claude[1] BLOCKED: settings env ANTHROPIC_AUTH_TOKEN is incompatible with Remote Control\n",
		"ANTHROPIC_AUTH_TOKEN/launch env":                       "doctor: remote_control claude[1] on (knob on; no blocker seen; login and connection not verified)\n",
		"CLAUDE_CODE_OAUTH_TOKEN/settings env":                  "doctor: remote_control claude[1] BLOCKED: settings env CLAUDE_CODE_OAUTH_TOKEN is incompatible with Remote Control\n",
		"CLAUDE_CODE_OAUTH_TOKEN/launch env":                    "doctor: remote_control claude[1] BLOCKED: launch env CLAUDE_CODE_OAUTH_TOKEN is incompatible with Remote Control\n",
	}
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
				if got := rooted(
					output.String(),
					filepath.Dir(dir),
				); got != wants[name+"/"+source] ||
					tally.failures != 0 {
					t.Errorf("failures=%d output = %q, want %q", tally.failures, got, wants[name+"/"+source])
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
	if output.String() != `doctor: remote_control claude[1] off by config (claude.remoteControl=false)
doctor: remote_control claude[2] on (knob on; no blocker seen; login and connection not verified)
` || tally.warnings != 0 || tally.failures != 0 {
		t.Fatalf("account override/shared settings report: warnings=%d failures=%d output=%s",
			tally.warnings, tally.failures, output.String())
	}
}
