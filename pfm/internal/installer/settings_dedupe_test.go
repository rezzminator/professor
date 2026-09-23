package installer

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

// callmeterEvents is the seven-event registration docs/design/hooks/callmeter.md
// § The hooks names, with each event's matcher.
var callmeterEvents = map[string]string{
	"PreToolUse":         "Bash",
	"PostToolUse":        "*",
	"PostToolUseFailure": "*",
	"PostToolBatch":      "",
	"SubagentStart":      "*",
	"SubagentStop":       "*",
	"Stop":               "",
}

// settingsHookObjects returns every hook object carrying command under event,
// with the matcher of the entry it sits in.
func settingsHookObjects(t *testing.T, raw []byte, event, command string) ([]map[string]any, []string) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode settings: %v\n%s", err, raw)
	}
	events, _ := document["hooks"].(map[string]any)
	entries, _ := events[event].([]any)
	var hooks []map[string]any
	var matchers []string
	for _, entryValue := range entries {
		entry, _ := entryValue.(map[string]any)
		matcher, _ := entry["matcher"].(string)
		values, _ := entry["hooks"].([]any)
		for _, value := range values {
			hook, _ := value.(map[string]any)
			if hook["command"] == command {
				hooks = append(hooks, hook)
				matchers = append(matchers, matcher)
			}
		}
	}
	return hooks, matchers
}

func TestSettingsInstallDropsDuplicateCompactNudge(t *testing.T) {
	home := filepath.Join("neutral", "home")
	compactNudge := home + "/.local/bin/pfm internal compact-nudge"
	raw := []byte(`{"hooks":{"UserPromptSubmit":[
		{"matcher":"","hooks":[{"type":"command","command":"` + compactNudge + `"}]},
		{"matcher":"","hooks":[{"type":"command","command":"` + compactNudge + `"}]}
	]}}`)

	updated, changed, owned, err := updateSettings(raw, home, false, nil)
	if err != nil || !changed {
		t.Fatalf("updateSettings changed=%v err=%v", changed, err)
	}
	if got := hookCommandCount(t, string(updated), "", compactNudge); got != 1 {
		t.Fatalf("compact-nudge count=%d after install, want exactly 1\n%s", got, updated)
	}
	if got := owned[settingsHookKey{Event: "UserPromptSubmit", Command: compactNudge}]; got != 1 {
		t.Fatalf("ledger owns %d compact-nudge hooks, want 1: %#v", got, owned)
	}
}

func TestSettingsInstallRemovesRRDirMovedUnderPostToolUse(t *testing.T) {
	home := filepath.Join("neutral", "home")
	rrDir := home + "/.local/bin/pfm internal rr-dir"
	raw := []byte(`{"hooks":{"PostToolUse":[
		{"matcher":"rr|super-rr","hooks":[{"type":"command","command":"` + rrDir + `"}]}
	]}}`)

	updated, changed, owned, err := updateSettings(raw, home, false, nil)
	if err != nil || !changed {
		t.Fatalf("updateSettings changed=%v err=%v", changed, err)
	}
	if got := hookCommandCount(t, string(updated), "PostToolUse", rrDir); got != 0 {
		t.Fatalf("rr-dir under PostToolUse count=%d, want the moved hook removed\n%s", got, updated)
	}
	if got := hookMatcherCount(t, string(updated), "SubagentStart", rrDir, hookRRDirMatcher); got != 1 {
		t.Fatalf("rr-dir under SubagentStart %q count=%d, want 1\n%s", hookRRDirMatcher, got, updated)
	}
	if got := hookCommandCount(t, string(updated), "", rrDir); got != 1 {
		t.Fatalf("rr-dir count=%d across the file, want exactly 1\n%s", got, updated)
	}
	if got := owned[settingsHookKey{Event: "PostToolUse", Matcher: hookRRDirMatcher, Command: rrDir}]; got != 0 {
		t.Fatalf("ledger still owns the removed PostToolUse rr-dir: %#v", owned)
	}
	if got := owned[settingsHookKey{Event: "SubagentStart", Matcher: hookRRDirMatcher, Command: rrDir}]; got != 1 {
		t.Fatalf("ledger owns %d SubagentStart rr-dir hooks, want 1: %#v", got, owned)
	}
}

func TestSettingsInstallWritesSevenAsyncCallmeterHooks(t *testing.T) {
	home := filepath.Join("neutral", "home")
	callmeter := home + "/.local/bin/pfm internal callmeter"

	updated, changed, owned, err := updateSettings([]byte("{}\n"), home, false, nil)
	if err != nil || !changed {
		t.Fatalf("updateSettings changed=%v err=%v", changed, err)
	}
	if got := hookCommandCount(t, string(updated), "", callmeter); got != len(callmeterEvents) {
		t.Fatalf("callmeter count=%d, want %d\n%s", got, len(callmeterEvents), updated)
	}
	for event, matcher := range callmeterEvents {
		hooks, matchers := settingsHookObjects(t, updated, event, callmeter)
		if len(hooks) != 1 || matchers[0] != matcher {
			t.Fatalf(
				"%s callmeter hooks=%d matchers=%q, want one under %q\n%s",
				event,
				len(hooks),
				matchers,
				matcher,
				updated,
			)
		}
		if hooks[0]["async"] != true {
			t.Fatalf("%s callmeter hook async=%#v, want true\n%s", event, hooks[0]["async"], updated)
		}
		if got := owned[settingsHookKey{Event: event, Matcher: matcher, Command: callmeter}]; got != 1 {
			t.Fatalf("ledger owns %d %s callmeter hooks, want 1: %#v", got, event, owned)
		}
	}

	again, changedAgain, _, err := updateSettings(updated, home, false, owned)
	if err != nil || changedAgain {
		t.Fatalf("a converged file was rewritten: changed=%v err=%v\n%s", changedAgain, err, again)
	}
}

func TestSettingsInstallConvergesCallmeterAsyncAndKeepsOperatorHooks(t *testing.T) {
	home := filepath.Join("neutral", "home")
	callmeter := home + "/.local/bin/pfm internal callmeter"
	operatorEntry := `{"matcher":"*","hooks":[{"type":"command","command":"operator-notify","async":false,"timeout":7}]}`
	raw := []byte(`{"hooks":{
		"PostToolUse":[
			{"matcher":"*","hooks":[{"type":"command","command":"` + callmeter + `"}]},
			` + operatorEntry + `
		],
		"Stop":[{"matcher":"","hooks":[{"type":"command","command":"` + callmeter + `","async":false}]}]
	}}`)

	updated, changed, _, err := updateSettings(raw, home, false, nil)
	if err != nil || !changed {
		t.Fatalf("updateSettings changed=%v err=%v", changed, err)
	}
	for _, event := range []string{"PostToolUse", "Stop"} {
		hooks, _ := settingsHookObjects(t, updated, event, callmeter)
		if len(hooks) != 1 || hooks[0]["async"] != true {
			t.Fatalf("%s callmeter hooks=%#v, want one with async true\n%s", event, hooks, updated)
		}
	}

	var want any
	if err := json.Unmarshal([]byte(operatorEntry), &want); err != nil {
		t.Fatal(err)
	}
	wantBytes, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(updated, &document); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, entryValue := range document["hooks"].(map[string]any)["PostToolUse"].([]any) {
		gotBytes, err := json.Marshal(entryValue)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(gotBytes, wantBytes) {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("operator PostToolUse entry changed or vanished, want %s unchanged once\n%s", wantBytes, updated)
	}
}

func TestSettingsInstallWiresReloadInterceptHookAndDedupes(t *testing.T) {
	home := filepath.Join("neutral", "home")
	prefix := home + "/.local/bin/pfm"
	reloadIntercept := prefix + " internal reload-intercept"

	updated, changed, owned, err := updateSettings([]byte("{}\n"), home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("wiring an empty settings.json reported no change")
	}
	if got := hookCommandCount(t, string(updated), "UserPromptSubmit", reloadIntercept); got != 1 {
		t.Fatalf("reload-intercept count=%d after wiring an empty settings.json, want 1\n%s", got, updated)
	}
	if got := hookMatcherCount(t, string(updated), "UserPromptSubmit", reloadIntercept, ""); got != 1 {
		t.Fatalf("reload-intercept matcher count=%d, want 1 empty matcher\n%s", got, updated)
	}
	if owned[settingsHookKey{Event: "UserPromptSubmit", Command: reloadIntercept}] != 1 {
		t.Fatalf("owned ledger did not claim the reload-intercept hook: %#v", owned)
	}

	twice := []byte(`{"hooks":{"UserPromptSubmit":[{"matcher":"","hooks":[
		{"type":"command","command":"` + reloadIntercept + `"},
		{"type":"command","command":"` + reloadIntercept + `"}
	]}]}}`)
	deduped, changed, _, err := updateSettings(twice, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a doubled reload-intercept hook was not rewritten")
	}
	if got := hookCommandCount(t, string(deduped), "UserPromptSubmit", reloadIntercept); got != 1 {
		t.Fatalf("reload-intercept count=%d after dedupe, want exactly 1\n%s", got, deduped)
	}
}
