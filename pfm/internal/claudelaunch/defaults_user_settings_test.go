package claudelaunch

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestUserSettingDefaults: the shipped user settings carry their effective value, a claude.settings
// value replacing the default and a null leaving the key out.
func TestUserSettingDefaults(t *testing.T) {
	const reason = "Claude Code counts bypass consent from an account's own settings.json alone on some paths" +
		" (a background worker, the bypass consent check), never from --settings, so pfm install writes it there" +
		" too, keeping a value the file already holds."
	for _, row := range []struct {
		name   string
		claude map[string]any
		want   []UserSettingDefault
	}{
		{"the shipped default", nil, []UserSettingDefault{
			{"skipDangerousModePermissionPrompt", json.RawMessage(`true`), reason},
		}},
		{"a claude.settings value", map[string]any{"skipDangerousModePermissionPrompt": false}, []UserSettingDefault{
			{"skipDangerousModePermissionPrompt", json.RawMessage(`false`), reason},
		}},
		{"a claude.settings null", map[string]any{"skipDangerousModePermissionPrompt": nil}, []UserSettingDefault{}},
	} {
		t.Run(row.name, func(t *testing.T) {
			if got := UserSettingDefaults(row.claude); !reflect.DeepEqual(got, row.want) {
				t.Fatalf("UserSettingDefaults(%v) = %q, want %q", row.claude, got, row.want)
			}
		})
	}
}
