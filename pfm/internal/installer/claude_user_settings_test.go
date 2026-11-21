package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const userSettingsChange = "set Claude user settings [skipDangerousModePermissionPrompt="

func readUserSettings(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestWireClaudeUserSettingsAddsOnlyAbsentKeys(t *testing.T) {
	for _, row := range []struct {
		name, settings, want string
		claude               map[string]any
		apply, written       bool
	}{
		{
			name:     "an absent key is added after the others, every other byte kept",
			settings: "{\n  \"model\": \"opus\",\n  \"env\": {\"B\": \"1\", \"A\": \"2\"},\n  \"cleanupPeriodDays\": 36500\n}\n",
			want: "{\n  \"model\": \"opus\",\n  \"env\": {\"B\": \"1\", \"A\": \"2\"},\n  \"cleanupPeriodDays\": 36500,\n" +
				"  \"skipDangerousModePermissionPrompt\": true\n}\n",
			apply: true, written: true,
		},
		{
			name:  "an absent file is created",
			want:  "{\n  \"skipDangerousModePermissionPrompt\": true\n}\n",
			apply: true, written: true,
		},
		{
			name:     "a value the user set is kept",
			settings: "{\n  \"skipDangerousModePermissionPrompt\": false\n}\n",
			want:     "{\n  \"skipDangerousModePermissionPrompt\": false\n}\n",
			apply:    true, written: false,
		},
		{
			name:     "a claude.settings value is the one written",
			settings: "{}\n",
			claude:   map[string]any{"skipDangerousModePermissionPrompt": false},
			want:     "{\n  \"skipDangerousModePermissionPrompt\": false\n}\n",
			apply:    true, written: true,
		},
		{
			name:     "a claude.settings null writes nothing",
			settings: "{}\n",
			claude:   map[string]any{"skipDangerousModePermissionPrompt": nil},
			want:     "{}\n",
			apply:    true, written: false,
		},
		{
			name:     "a dry run only reports",
			settings: "{}\n",
			want:     "{}\n",
			apply:    false, written: true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			account := fullscreenAccount(t, row.settings, "")
			installer, output := fullscreenEngine(t, row.apply, account)
			installer.options.ClaudeSettings = row.claude
			if err := installer.wireClaudeUserSettings(); err != nil {
				t.Fatalf("wireClaudeUserSettings: %v\noutput:\n%s", err, output)
			}
			path := filepath.Join(account.ConfigDir, "settings.json")
			got, err := os.ReadFile(path)
			if row.want == "" {
				if !os.IsNotExist(err) {
					t.Errorf("%s exists (%v): %q", path, err, got)
				}
			} else if string(got) != row.want {
				t.Errorf("%s:\n%s\nwant:\n%s", path, got, row.want)
			}
			if wrote := strings.Contains(output.String(), userSettingsChange); wrote != row.written {
				t.Errorf("output reports a change=%v, want %v:\n%s", wrote, row.written, output)
			}
			if row.claude == nil && !row.written &&
				!strings.Contains(output.String(), "claude user settings set in "+path) {
				t.Errorf("output lacks the already-set line:\n%s", output)
			}
		})
	}
}

func TestWireClaudeUserSettingsWritesASharedFileOnceThroughItsLink(t *testing.T) {
	store := t.TempDir()
	shared := filepath.Join(store, "settings.json")
	if err := os.WriteFile(shared, []byte("{}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	first, second := fullscreenAccount(t, "", ""), fullscreenAccount(t, "", "")
	for _, account := range []string{first.ConfigDir, second.ConfigDir} {
		if err := os.Symlink(shared, filepath.Join(account, "settings.json")); err != nil {
			t.Fatal(err)
		}
	}
	installer, output := fullscreenEngine(t, true, first, second)
	installer.options.ClaudeSettings = nil
	if err := installer.wireClaudeUserSettings(); err != nil {
		t.Fatalf("wireClaudeUserSettings: %v\noutput:\n%s", err, output)
	}
	if got, want := readUserSettings(t, store), "{\n  \"skipDangerousModePermissionPrompt\": true\n}\n"; got != want {
		t.Errorf("%s:\n%s\nwant:\n%s", shared, got, want)
	}
	if count := strings.Count(output.String(), userSettingsChange); count != 1 {
		t.Errorf("changes reported %d times, want 1:\n%s", count, output)
	}
	info, err := os.Lstat(filepath.Join(first.ConfigDir, "settings.json"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("account settings.json no longer a link: %v, %v", info, err)
	}
	if mode, err := os.Stat(shared); err != nil || mode.Mode().Perm() != 0o640 {
		t.Errorf("shared mode = %v, %v; want 0640 kept", mode, err)
	}
}

func TestWireClaudeUserSettingsReportsAnUnparsableFile(t *testing.T) {
	account := fullscreenAccount(t, "{not json", "")
	installer, output := fullscreenEngine(t, true, account)
	installer.options.ClaudeSettings = nil
	err := installer.wireClaudeUserSettings()
	if err == nil || !strings.Contains(err.Error(), "settings unreadable, state unknown") {
		t.Fatalf("err=%v, want the unreadable-settings failure\noutput:\n%s", err, output)
	}
	if got := readUserSettings(t, account.ConfigDir); got != "{not json" {
		t.Errorf("settings rewritten to %q", got)
	}
}
