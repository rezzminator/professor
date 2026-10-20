package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestDoctorRemoteControlManagedPolicy(t *testing.T) {
	for _, test := range []struct {
		name   string
		unread bool
		want   string
	}{
		{name: "managed disable", want: "BLOCKED: managed settings"},
		{name: "unreadable managed drop-in", unread: true, want: "FAIL managed policy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			dir := runtime.Paths.ManagedSettingsDir
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if test.unread {
				// A visible JSON symlink to a directory is unreadable for any uid.
				if err := os.Symlink(t.TempDir(), filepath.Join(dir, "remote-control-policy.json")); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "managed-settings.json"),
				[]byte(`{"disableRemoteControl":true}`), 0o600); err != nil {
				t.Fatal(err)
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
				t.Fatalf("Remote Control rows=%v, accounts=%d", rows, len(runtime.Config.Accounts))
			}
			for _, row := range rows {
				if !strings.Contains(row, test.want) {
					t.Errorf("managed policy ignored: want %q in %s", test.want, row)
				}
			}
		})
	}
}

func TestRemoteControlDoctorManagedSources(t *testing.T) {
	for _, test := range []struct {
		name     string
		files    map[string]string
		links    map[string]string
		dirs     []string
		account  string
		want     string
		warnings int
		failures int
	}{
		{name: "absent managed sources", want: "on (knob on"},
		{name: "managed gateway env", files: map[string]string{"managed-settings.json": `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.test"}}`}, want: "BLOCKED: managed settings env ANTHROPIC_BASE_URL host=gateway.example.test", warnings: 1},
		{name: "managed helper", files: map[string]string{"managed-settings.d/policy.json": `{"apiKeyHelper":"echo secret"}`}, want: "BLOCKED: managed settings apiKeyHelper", warnings: 1},
		{name: "managed env overrides account gateway", account: `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.test"}}`, files: map[string]string{"managed-settings.json": `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`}, want: "on (knob on"},
		{name: "managed empty helper overrides account helper", account: `{"apiKeyHelper":"echo secret"}`, files: map[string]string{"managed-settings.json": `{"apiKeyHelper":""}`}, want: "on (knob on"},
		{name: "conflicting managed env never claims on", files: map[string]string{"managed-settings.json": `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.test"}}`, "managed-settings.d/policy.json": `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`}, want: "BLOCKED: managed settings have conflicting Remote Control environment or helper values", warnings: 1},
		{name: "monolithic policy without drop-in directory", files: map[string]string{"managed-settings.json": `{"disableRemoteControl":true}`}, want: "BLOCKED: managed settings", warnings: 1},
		{name: "visible regular drop-in", files: map[string]string{"managed-settings.d/10-policy.json": `{"disableRemoteControl":true}`}, want: "BLOCKED: managed settings", warnings: 1},
		{name: "visible symlink drop-in", files: map[string]string{"policy.data": `{"disableRemoteControl":true}`}, links: map[string]string{"managed-settings.d/10-policy.json": "policy.data"}, want: "BLOCKED: managed settings", warnings: 1},
		{name: "false managed value overrides account true", account: `{"disableRemoteControl":true}`, files: map[string]string{"managed-settings.json": `{"disableRemoteControl":false}`}, want: "on (knob on"},
		{name: "absent managed value retains account true", account: `{"disableRemoteControl":true}`, files: map[string]string{"managed-settings.json": `{}`}, want: "BLOCKED: settings disableRemoteControl=true", warnings: 1},
		{name: "matching sources", files: map[string]string{"managed-settings.json": `{"disableRemoteControl":true}`, "managed-settings.d/10-policy.json": `{"disableRemoteControl":true}`}, want: "BLOCKED: managed settings", warnings: 1},
		{name: "conflicting sources never claim on", files: map[string]string{"managed-settings.json": `{"disableRemoteControl":true}`, "managed-settings.d/10-policy.json": `{"disableRemoteControl":false}`}, want: "BLOCKED: managed settings disableRemoteControl has conflicting values", warnings: 1},
		{name: "hidden non-json and directory entries ignored", files: map[string]string{"managed-settings.d/.hidden.json": `broken`, "managed-settings.d/policy.txt": `broken`}, dirs: []string{"managed-settings.d/directory.json"}, want: "on (knob on"},
		{name: "malformed policy", files: map[string]string{"managed-settings.d/policy.json": `secret`}, want: "FAIL managed policy parse settings", failures: 1},
		{name: "invalid managed boolean", files: map[string]string{"managed-settings.json": `{"disableRemoteControl":"secret"}`}, want: "FAIL managed policy parse settings", failures: 1},
		{name: "null managed object", files: map[string]string{"managed-settings.json": `null`}, want: "FAIL managed policy parse settings", failures: 1},
		{name: "broken policy symlink", links: map[string]string{"managed-settings.d/policy.json": "absent"}, want: "FAIL managed policy read settings", failures: 1},
		{name: "broken managed directory symlink", links: map[string]string{"managed-settings.d": "absent"}, want: "FAIL managed policy enumerate", failures: 1},
		{name: "unreadable monolithic file", dirs: []string{"managed-settings.json"}, want: "FAIL managed policy read settings", failures: 1},
		{name: "enumeration fails", files: map[string]string{"managed-settings.d": `not a directory`}, want: "FAIL managed policy enumerate", failures: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for name, content := range test.files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for name, target := range test.links {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, target), path); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range test.dirs {
				if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			account := t.TempDir()
			settings := test.account
			if settings == "" {
				settings = `{}`
			}
			if err := os.WriteFile(filepath.Join(account, "settings.json"), []byte(settings), 0o600); err != nil {
				t.Fatal(err)
			}
			machine := config.Config{
				Claude:   config.ClaudePrefs{RemoteControl: true},
				Accounts: []config.Account{{ID: 1, ConfigDir: account}},
			}
			var output bytes.Buffer
			tally := &doctorTally{}
			printRemoteControlDoctor(
				&output,
				filepath.Join(root, "managed-settings.d"),
				machine,
				&paths.MapEnv{},
				tally,
			)
			if !strings.Contains(output.String(), test.want) || tally.warnings != test.warnings ||
				tally.failures != test.failures {
				t.Fatalf(
					"warnings=%d failures=%d, want %d and %d; output=%s",
					tally.warnings,
					tally.failures,
					test.warnings,
					test.failures,
					output.String(),
				)
			}
			if strings.Contains(output.String(), "secret") {
				t.Fatalf("managed policy leaked a value: %s", output.String())
			}
		})
	}
}
