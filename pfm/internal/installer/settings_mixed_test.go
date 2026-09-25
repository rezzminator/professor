package installer

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// An owned hook an operator keeps in a mixed entry under a matcher that is not
// the template's own satisfies that template for its event: install keeps the
// entry, appends no second copy, names the kept placement, and a repeat
// install changes nothing. Without a mixed copy the canonical entry is
// appended as before.
func TestMixedEntryOwnedHookIsWiredOnceAndNamed(t *testing.T) {
	for _, tc := range []struct {
		name, event, matcher, command string
		mixed                         bool
	}{
		{name: "callmeter", event: "PreToolUse", matcher: "", command: " internal callmeter", mixed: true},
		{name: "git-guard", event: "PreToolUse", matcher: "Edit", command: " internal git-guard", mixed: true},
		{name: "rr-dir", event: "SubagentStart", matcher: "operator-agent", command: " internal rr-dir", mixed: true},
		{name: "explore-deny", event: "PreToolUse", matcher: "", command: " internal explore-deny", mixed: true},
		{name: "canonical", event: "PreToolUse", matcher: "Edit", command: " internal git-guard", mixed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".claude")
			settings := filepath.Join(config, "settings.json")
			command := home + "/.local/bin/pfm" + tc.command
			fixture := `{"hooks":{"` + tc.event + `":[{"matcher":"` + tc.matcher + `","hooks":[
      {"type":"command","command":"` + command + `"},
      {"type":"command","command":"operator-hook"}]}]}}`
			if !tc.mixed {
				fixture = `{"hooks":{"` + tc.event + `":[{"matcher":"` + tc.matcher + `","hooks":[
      {"type":"command","command":"operator-hook"}]}]}}`
			}
			writeFixture(t, settings, fixture)
			install := func() string {
				var output bytes.Buffer
				installer := engine{
					options: Options{Mode: ModeApply, Home: home, ConfigDir: config, Stdout: &output},
					apply:   true, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
					stamp: "fixture",
				}
				if err := installer.wireSettings(); err != nil {
					t.Fatal(err)
				}
				return output.String()
			}
			output := install()
			raw := readFixture(t, settings)
			if got := hookCommandCount(t, raw, tc.event, command); got != 1 {
				t.Fatalf("%s copies under %s = %d, want 1\n%s", command, tc.event, got, raw)
			}
			preserved := "mixed " + tc.event + " hook entry preserved with its existing matcher at " + settings + ": " + command
			if tc.mixed {
				if got := hookMatcherCount(t, raw, tc.event, command, tc.matcher); got != 1 {
					t.Fatalf("mixed entry lost its owned hook: count=%d\n%s", got, raw)
				}
				if !strings.Contains(output, preserved) {
					t.Fatalf("kept placement not named %q:\n%s", preserved, output)
				}
			} else if strings.Contains(output, "preserved with its existing matcher") {
				t.Fatalf("clean settings reported a preserved mixed entry:\n%s", output)
			}
			if got := hookMatcherCount(t, raw, tc.event, "operator-hook", tc.matcher); got != 1 {
				t.Fatalf("operator hook moved or removed: count=%d\n%s", got, raw)
			}
			second := install()
			if again := readFixture(t, settings); again != raw {
				t.Fatalf("repeat install changed settings:\nbefore %s\nafter %s", raw, again)
			}
			if strings.Contains(second, "rewrite "+settings) {
				t.Fatalf("repeat install reported changed=true:\n%s", second)
			}
		})
	}
}
