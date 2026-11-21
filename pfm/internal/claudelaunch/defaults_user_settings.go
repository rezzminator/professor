package claudelaunch

import (
	"bytes"
	"encoding/json"
)

// UserSettingDefault is one settings value Claude Code honours only from an account's own
// settings.json on some path, so no --settings can stand in for it: pfm install writes it there.
type UserSettingDefault struct {
	Key    string
	Value  json.RawMessage // compact JSON
	Reason string
}

// UserSettingDefaults are the defaults file's userSettings keys, sorted, each with its value in
// EffectiveSettings(claude); a key claude.settings sets to null is left out, as its launches drop it.
func UserSettingDefaults(claude map[string]any) []UserSettingDefault {
	effective := EffectiveSettings(claude)
	list := make([]UserSettingDefault, 0, len(defaults.userSettings))
	for _, value := range defaults.userSettings {
		setting, present := effective[value.key]
		if !present {
			continue
		}
		encoded, err := json.Marshal(setting)
		if err != nil {
			// EffectiveSettings holds only values json.Unmarshal produced, which always marshal.
			panic("claudelaunch: user setting " + value.key + ": " + err.Error())
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, encoded); err != nil {
			panic("claudelaunch: user setting " + value.key + ": " + err.Error())
		}
		list = append(list, UserSettingDefault{value.key, compact.Bytes(), value.reason})
	}
	return list
}
