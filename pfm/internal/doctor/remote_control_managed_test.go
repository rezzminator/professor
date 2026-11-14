package doctor

import (
	"bytes"
	"os"
	"path/filepath"
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
		{name: "managed disable", want: "doctor: remote_control claude[1] BLOCKED: managed settings {root}/managed-settings.json disableRemoteControl=true\n"},
		{name: "unreadable managed drop-in", unread: true, want: "doctor: remote_control claude[1] FAIL managed policy read settings {root}/managed-settings.d/remote-control-policy.json: read {root}/managed-settings.d/remote-control-policy.json: is a directory\n"},
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
			if got := rooted(
				doctorRows(stdout.String(), "doctor: remote_control claude["),
				runtime.Paths.Home,
			); got != test.want {
				t.Errorf("Remote Control rows = %q, want %q", got, test.want)
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
		{name: "absent managed sources", want: "doctor: remote_control claude[1] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "managed gateway env", files: map[string]string{"managed-settings.json": `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.test"}}`}, want: "doctor: remote_control claude[1] BLOCKED: managed settings env ANTHROPIC_BASE_URL host=gateway.example.test is not api.anthropic.com\n", warnings: 1},
		{name: "managed helper", files: map[string]string{"managed-settings.d/policy.json": `{"apiKeyHelper":"echo secret"}`}, want: "doctor: remote_control claude[1] BLOCKED: managed settings apiKeyHelper is incompatible with Remote Control\n", warnings: 1},
		{name: "managed env overrides account gateway", account: `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.test"}}`, files: map[string]string{"managed-settings.json": `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`}, want: "doctor: remote_control claude[1] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "managed empty helper overrides account helper", account: `{"apiKeyHelper":"echo secret"}`, files: map[string]string{"managed-settings.json": `{"apiKeyHelper":""}`}, want: "doctor: remote_control claude[1] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "conflicting managed env never claims on", files: map[string]string{"managed-settings.json": `{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.test"}}`, "managed-settings.d/policy.json": `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`}, want: "doctor: remote_control claude[1] BLOCKED: managed settings have conflicting Remote Control environment or helper values; precedence not verified\n", warnings: 1},
		{name: "monolithic policy without drop-in directory", files: map[string]string{"managed-settings.json": `{"disableRemoteControl":true}`}, want: "doctor: remote_control claude[1] BLOCKED: managed settings {root}/managed-settings.json disableRemoteControl=true\n", warnings: 1},
		{name: "visible regular drop-in", files: map[string]string{"managed-settings.d/10-policy.json": `{"disableRemoteControl":true}`}, want: "doctor: remote_control claude[1] BLOCKED: managed settings {root}/managed-settings.d/10-policy.json disableRemoteControl=true\n", warnings: 1},
		{name: "visible symlink drop-in", files: map[string]string{"policy.data": `{"disableRemoteControl":true}`}, links: map[string]string{"managed-settings.d/10-policy.json": "policy.data"}, want: "doctor: remote_control claude[1] BLOCKED: managed settings {root}/managed-settings.d/10-policy.json disableRemoteControl=true\n", warnings: 1},
		{name: "false managed value overrides account true", account: `{"disableRemoteControl":true}`, files: map[string]string{"managed-settings.json": `{"disableRemoteControl":false}`}, want: "doctor: remote_control claude[1] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "absent managed value retains account true", account: `{"disableRemoteControl":true}`, files: map[string]string{"managed-settings.json": `{}`}, want: "doctor: remote_control claude[1] BLOCKED: settings disableRemoteControl=true\n", warnings: 1},
		{name: "matching sources", files: map[string]string{"managed-settings.json": `{"disableRemoteControl":true}`, "managed-settings.d/10-policy.json": `{"disableRemoteControl":true}`}, want: "doctor: remote_control claude[1] BLOCKED: managed settings {root}/managed-settings.json, {root}/managed-settings.d/10-policy.json disableRemoteControl=true\n", warnings: 1},
		{name: "conflicting sources never claim on", files: map[string]string{"managed-settings.json": `{"disableRemoteControl":true}`, "managed-settings.d/10-policy.json": `{"disableRemoteControl":false}`}, want: "doctor: remote_control claude[1] BLOCKED: managed settings disableRemoteControl has conflicting values in {root}/managed-settings.json, {root}/managed-settings.d/10-policy.json; effective managed policy precedence not verified\n", warnings: 1},
		{name: "hidden non-json and directory entries ignored", files: map[string]string{"managed-settings.d/.hidden.json": `broken`, "managed-settings.d/policy.txt": `broken`}, dirs: []string{"managed-settings.d/directory.json"}, want: "doctor: remote_control claude[1] on (knob on; no blocker seen; login and connection not verified)\n"},
		{name: "malformed policy", files: map[string]string{"managed-settings.d/policy.json": `secret`}, want: "doctor: remote_control claude[1] FAIL managed policy parse settings {root}/managed-settings.d/policy.json: invalid settings object\n", failures: 1},
		{name: "invalid managed boolean", files: map[string]string{"managed-settings.json": `{"disableRemoteControl":"secret"}`}, want: "doctor: remote_control claude[1] FAIL managed policy parse settings {root}/managed-settings.json: invalid settings object\n", failures: 1},
		{name: "null managed object", files: map[string]string{"managed-settings.json": `null`}, want: "doctor: remote_control claude[1] FAIL managed policy parse settings {root}/managed-settings.json: expected settings object\n", failures: 1},
		{name: "broken policy symlink", links: map[string]string{"managed-settings.d/policy.json": "absent"}, want: "doctor: remote_control claude[1] FAIL managed policy read settings {root}/managed-settings.d/policy.json: open {root}/managed-settings.d/policy.json: no such file or directory\n", failures: 1},
		{name: "broken managed directory symlink", links: map[string]string{"managed-settings.d": "absent"}, want: "doctor: remote_control claude[1] FAIL managed policy enumerate {root}/managed-settings.d: open {root}/managed-settings.d: no such file or directory\n", failures: 1},
		{name: "unreadable monolithic file", dirs: []string{"managed-settings.json"}, want: "doctor: remote_control claude[1] FAIL managed policy read settings {root}/managed-settings.json: read {root}/managed-settings.json: is a directory\n", failures: 1},
		{name: "enumeration fails", files: map[string]string{"managed-settings.d": `not a directory`}, want: "doctor: remote_control claude[1] FAIL managed policy enumerate {root}/managed-settings.d: open {root}/managed-settings.d: not a directory\n", failures: 1},
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
			if got := rooted(output.String(), root, account); got != test.want || tally.warnings != test.warnings ||
				tally.failures != test.failures {
				t.Errorf("output = %q, want %q; warnings=%d failures=%d, want %d %d",
					got, test.want, tally.warnings, tally.failures, test.warnings, test.failures)
			}
		})
	}
}
