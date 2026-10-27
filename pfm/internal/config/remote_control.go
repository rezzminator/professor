package config

import "fmt"

// applyRemoteControl resolves an optional boolean before EffectiveClaude copies
// account preferences. In particular an unrelated account override inherits the
// resolved machine value, not the bool zero value.
func applyRemoteControl(prefs *ClaudePrefs, raw *bool, inherited bool, sources map[string]Source, index int) {
	prefs.RemoteControl = inherited
	if raw == nil {
		return
	}
	prefs.RemoteControl = *raw
	key := "claude.remoteControl"
	if index >= 0 {
		key = fmt.Sprintf("accounts[%d].claude.remoteControl", index)
	}
	sources[key] = SourceFile
}
