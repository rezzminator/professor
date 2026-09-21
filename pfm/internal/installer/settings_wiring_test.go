package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestEveryClaudeSettingsFileGetsCompleteHookWiring(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude", "settings.json")
	secondary := filepath.Join(home, ".cc", "4", "settings.json")
	writeFixture(
		t,
		canonical,
		`{"hooks":{"UserPromptSubmit":[{"matcher":"","hooks":[{"type":"command","command":"canonical-keep"}]}]}}`,
	)
	writeFixture(
		t,
		secondary,
		`{"hooks":{"SessionEnd":[{"matcher":"","hooks":[{"type":"command","command":"secondary-keep"}]}]}}`,
	)
	canonicalAlias := filepath.Join(home, ".cc", "1", "settings.json")
	if err := os.MkdirAll(filepath.Dir(canonicalAlias), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canonical, canonicalAlias); err != nil {
		t.Fatal(err)
	}

	now := func() time.Time { return time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC) }
	runner := &outputRunner{printOutput: "state = not running\n"}
	if _, err := Run(context.Background(), Options{
		Mode:       ModeApply,
		Home:       home,
		ConfigDirs: []string{filepath.Join(home, ".claude"), filepath.Join(home, ".cc", "4")},
		Now:        now,
		Runner:     runner,
	}); err != nil {
		t.Fatal(err)
	}
	wantedProbe := "systemctl --user is-active --quiet pfm-name-sync.service"
	if schedulerIsLaunchd {
		wantedProbe = "launchctl print gui/" + strconv.Itoa(os.Getuid()) + "/" + launchdLabel
	}
	if len(runner.calls) == 0 || runner.calls[0] != wantedProbe {
		t.Fatalf(
			"installer did not probe the running-service gate before touching fixtures; fake runner calls: %v",
			runner.calls,
		)
	}

	for _, path := range []string{canonical, secondary} {
		for _, command := range []string{
			home + "/.local/bin/pfm internal clear-kill",
		} {
			if got := hookCommandCount(t, readFixture(t, path), "", command); got != 1 {
				t.Fatalf("%s has %d copies of %q, want exactly one\n%s", path, got, command, readFixture(t, path))
			}
		}
	}

	var second bytes.Buffer
	report, err := Run(context.Background(), Options{
		Mode:       ModeApply,
		Home:       home,
		ConfigDirs: []string{filepath.Join(home, ".claude"), filepath.Join(home, ".cc", "4")},
		Now:        now,
		Stdout:     &second,
		Runner:     &fakeRunner{},
	})
	if err != nil || report.Changed != 0 {
		t.Fatalf("second apply report=%#v err=%v\n%s", report, err, second.String())
	}

	// pfm writes no Codex hook, so an install over a Codex home with none
	// leaves no hooks.json behind at all.
	if _, err := os.Stat(filepath.Join(home, ".codex", "hooks.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("install wrote a Codex hooks file it owns nothing in: %v", err)
	}
}

func TestSettingsInstallAddsWaveHooksCleanupAndOwnsOnlyItsEntries(t *testing.T) {
	home := filepath.Join("neutral", "home")
	raw := []byte(
		`{"hooks":{"UserPromptSubmit":[{"matcher":"","hooks":[{"type":"command","command":"manual-keep"}]}]},"cleanupPeriodDays":42}`,
	)
	updated, changed, owned, err := updateSettings(raw, home, false, nil)
	if err != nil || !changed {
		t.Fatalf("updateSettings changed=%v err=%v", changed, err)
	}
	var document map[string]any
	if err := json.Unmarshal(updated, &document); err != nil {
		t.Fatal(err)
	}
	if got := document["cleanupPeriodDays"]; got != float64(42) {
		t.Fatalf("cleanupPeriodDays=%v, want existing value 42", got)
	}
	prefix := home + "/.local/bin/pfm"
	for _, hook := range []struct {
		event, matcher, command string
	}{
		{"PreToolUse", "Agent|Task", prefix + " internal explore-deny"},
		{"SubagentStart", "rr|super-rr", prefix + " internal rr-dir"},
		{"UserPromptSubmit", "", prefix + " internal epic-inject"},
		{"UserPromptSubmit", "", prefix + " internal reload-intercept"},
		{"UserPromptSubmit", "", prefix + " internal compact-nudge"},
		{"UserPromptSubmit", "", prefix + " usage-hook"},
		{"SessionStart", "", prefix + " internal launcher-repair"},
		{"SessionEnd", "", prefix + " internal clear-kill"},
	} {
		if got := hookCommandCount(t, string(updated), hook.event, hook.command); got != 1 {
			t.Fatalf("%s %s count=%d, want 1\n%s", hook.event, hook.command, got, updated)
		}
		if got := hookMatcherCount(t, string(updated), hook.event, hook.command, hook.matcher); got != 1 {
			t.Fatalf("%s %s matcher count=%d, want 1\n%s", hook.event, hook.command, got, updated)
		}
	}
	if want := len(claudeHookTemplates(home)); len(owned) != want {
		t.Fatalf("owned hooks=%d, want %d: %#v", len(owned), want, owned)
	}

	withManual := append(
		[]byte(
			`{"hooks":{"PreToolUse":[{"matcher":"Agent|Task","hooks":[{"type":"command","command":"operator-keep"}]}]}}`,
		),
		'\n',
	)
	updated, _, owned, err = updateSettings(withManual, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	removed, changed, _, err := updateSettings(updated, home, true, owned)
	if err != nil || !changed {
		t.Fatalf("uninstall changed=%v err=%v", changed, err)
	}
	if strings.Contains(string(removed), prefix+" ") || !strings.Contains(string(removed), "operator-keep") {
		t.Fatalf("uninstall ownership result=%s", removed)
	}
}

// TestSettingsInstallWiresReloadInterceptHookAndDedupes pins T2's settings
// contract on its own: a settings.json that has never seen the hook gains it
// under UserPromptSubmit with an empty matcher (the epic-inject shape), and
// one that already carries it TWICE — the shape a hand-edited or
// double-installed settings.json can reach — keeps exactly one copy.
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

func TestInstallPausesAutomaticDreamHooksAcrossClaudeAndCodex(t *testing.T) {
	home := t.TempDir()
	pfm := filepath.Join(home, ".local", "bin", "pfm")
	claudeRaw := []byte(`{
  "hooks": {
    "PreToolUse": [{"matcher":"Agent|Task","hooks":[
      {"type":"command","command":"` + pfm + ` dream hook agent-inject"},
      {"type":"command","command":"claude-neighbor"}
    ]}],
    "UserPromptSubmit": [{"matcher":"","hooks":[
      {"type":"command","command":"` + pfm + ` dream hook nudge"}
    ]}],
    "SessionStart": [{"matcher":"startup","hooks":[
      {"type":"command","command":"bash ` + home + `/.claude/hooks/dreamer-agent-inject.sh"}
    ]}],
    "PostToolUse": [{"matcher":"Agent","hooks":[
      {"type":"command","command":"bash ` + home + `/.claude/hooks/dreamer-nudge.sh"}
    ]}]
  }
}`)
	claudeUpdated, _, _, err := updateSettings(claudeRaw, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"dream hook agent-inject",
		"dream hook nudge",
		"dreamer-agent-inject.sh",
		"dreamer-nudge.sh",
	} {
		if strings.Contains(string(claudeUpdated), forbidden) {
			t.Fatalf("paused Claude settings retained %q:\n%s", forbidden, claudeUpdated)
		}
	}
	if !strings.Contains(string(claudeUpdated), "claude-neighbor") {
		t.Fatalf("pausing Dream hooks removed a neighboring Claude hook:\n%s", claudeUpdated)
	}

	codexRaw := []byte(`{
  "hooks": {
    "AfterToolUse": [{"matcher":"spawn_agent","hooks":[
      {"type":"command","command":"` + pfm + ` dream hook codex-subagent-inject"},
      {"type":"command","command":"codex-neighbor"}
    ]}],
    "SessionStart": [{"matcher":"startup","hooks":[
      {"type":"command","command":"` + home + `/.local/bin/cc-fleet dream hook codex-subagent-inject"}
    ]}]
  }
}`)
	codexUpdated, _, _, err := updateCodexHooks(codexRaw, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(codexUpdated), "dream hook codex-subagent-inject") {
		t.Fatalf("paused Codex hooks retained automatic Dream injection:\n%s", codexUpdated)
	}
	if !strings.Contains(string(codexUpdated), "codex-neighbor") {
		t.Fatalf("pausing Dream hooks removed a neighboring Codex hook:\n%s", codexUpdated)
	}
}

func TestInstallPausesDreamHooksAcrossUnknownEventsAndPreservesMalformedNeighbors(t *testing.T) {
	home := t.TempDir()
	raw := []byte(`{
  "hooks": {
    "Notification": [null, {"matcher":"", "hooks":[
      {"type":"command","command":"pfm dream hook nudge"},
      {"type":"command","command":"notification-neighbor"},
      "malformed-hook"
    ]}],
    "Stop": [{"matcher":"", "hooks":[
      {"type":"command","command":"bash /fixture/dreamer-agent-inject.sh"}
    ]}]
  }
}`)

	updated, changed, _, err := updateSettings(raw, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("install did not change a document containing retired Dream hooks")
	}
	var document map[string]any
	if err := json.Unmarshal(updated, &document); err != nil {
		t.Fatal(err)
	}
	events, ok := document["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks document has wrong shape: %#v", document["hooks"])
	}
	if _, ok := events["Stop"]; ok {
		t.Fatalf("event containing only a retired Dream hook survived: %#v", events["Stop"])
	}
	notification, ok := events["Notification"].([]any)
	if !ok || len(notification) != 2 {
		t.Fatalf(
			"Notification entries=%#v, want the malformed entry and its surviving neighbor",
			events["Notification"],
		)
	}
	if notification[0] != nil {
		t.Fatalf("non-map Notification entry was not preserved: %#v", notification)
	}
	entry, ok := notification[1].(map[string]any)
	if !ok {
		t.Fatalf("surviving Notification entry has wrong shape: %#v", notification[1])
	}
	hooks, ok := entry["hooks"].([]any)
	if !ok || len(hooks) != 2 {
		t.Fatalf("malformed hook or neighbor was not preserved: %#v", entry["hooks"])
	}
	malformed, neighbor := false, false
	for _, hook := range hooks {
		if hook == "malformed-hook" {
			malformed = true
		}
		if got, ok := hook.(map[string]any); ok && got["command"] == "notification-neighbor" {
			neighbor = true
		}
	}
	if !malformed || !neighbor {
		t.Fatalf("surviving hooks malformed=%v neighbor=%v: %#v", malformed, neighbor, hooks)
	}
	if strings.Contains(string(updated), "dream hook nudge") ||
		strings.Contains(string(updated), "dreamer-agent-inject.sh") {
		t.Fatalf("retired Dream hook survived in an unknown event:\n%s", updated)
	}

	again, changedAgain, _, err := updateSettings(updated, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changedAgain || !bytes.Equal(again, updated) {
		t.Fatalf("unknown-event cleanup was not idempotent: changed=%v\n%s", changedAgain, again)
	}
}

func TestRetiredHookCommandMatchingRecognizesAllDreamAliases(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{name: "bare pfm agent inject", command: "pfm dream hook agent-inject", want: "dream-agent-inject"},
		{name: "path cc fleet nudge", command: "/opt/legacy/.local/bin/cc-fleet dream hook nudge", want: "dream-nudge"},
		{
			name:    "bare codex injection",
			command: "cc-fleet dream hook codex-subagent-inject",
			want:    "dream-codex-subagent-inject",
		},
		{
			name:    "legacy shell shim",
			command: "bash /opt/legacy/hooks/dreamer-agent-inject.sh --fixture",
			want:    "dream-agent-inject",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, retired := retiredHookCommandName(tc.command)
			if !retired || got != tc.want {
				t.Fatalf("retiredHookCommandName(%q)=(%q,%v), want (%q,true)", tc.command, got, retired, tc.want)
			}
		})
	}
	for _, command := range []string{
		"pfm dream hook agent-injector",
		"/opt/legacy/bin/pfm-helper dream hook nudge",
		"cc-fleet dream hook codex-subagent-injector",
	} {
		if name, retired := retiredHookCommandName(command); retired {
			t.Fatalf("near-miss command %q classified as retired %q", command, name)
		}
	}
}

// TestInstallOwnershipLedgerClaimsHooksAlreadyPresentInSettings pins the
// deep-doctor defect: a host whose settings.json already carries every
// expected hook command — because it predates the ownership ledger, or a
// prior install ran before the ledger existed for that hook — never gets
// those hooks claimed. nextSettingsHookOwnership only credits a hook whose
// per-call count went UP (before[key] != after[key]); a hook that was already
// there and stays unchanged contributes zero "added" count forever, so the
// ledger stays empty for it and doctor reports permanent ownership drift
// (`ownership=0 file=1`) for a hook that is, in fact, correctly wired.
func TestInstallOwnershipLedgerClaimsHooksAlreadyPresentInSettings(t *testing.T) {
	home := t.TempDir()

	// Build the settings document a fully-wired install produces — this is
	// exactly what a real host's settings.json looks like today, regardless
	// of whether any ledger ever tracked it.
	preexisting, _, _, err := updateSettings([]byte("{}\n"), home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Today's install run starts from an EMPTY ownership ledger against that
	// already-wired file — the exact shape `wireSettings` reads via
	// `ownership[physical]` when the ledger has no entry for this path yet.
	_, changed, owned, err := updateSettings(preexisting, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("an already fully-wired settings.json was unexpectedly rewritten")
	}
	if want := len(claudeHookTemplates(home)); len(owned) != want {
		t.Fatalf(
			"owned hooks=%d, want %d — every already-present expected hook must be claimed: %#v",
			len(owned),
			want,
			owned,
		)
	}
	for key, count := range owned {
		if count != 1 {
			t.Fatalf("owned[%#v]=%d, want 1", key, count)
		}
	}

	// The doctor hook probe must agree: an already-owned hook is never drift.
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	writeFixture(t, settingsPath, string(preexisting))
	encoded, err := encodeSettingsHookOwnership(
		map[string]settingsHookCounts{physicalSettingsPath(settingsPath): owned},
	)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(
		t,
		settingsHookOwnershipPath(managedRootForHome(home)),
		string(encoded),
	)
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude")}}}
	for _, result := range ProbeExpectedHooks(home, machine) {
		if result.State == "drift" {
			t.Fatalf("hook probe reported drift for an already-owned hook: %#v", result)
		}
	}
}

// TestInstallOwnershipLedgerClaimsHooksDespiteForeignHooksPresent pins the
// deep-doctor defect one step past TestInstallOwnershipLedgerClaimsHooksAlreadyPresentInSettings:
// nextSettingsHookOwnership's "already fully-wired" claim only fires when
// the WHOLE settings.json document is "pure" — every hook command in it,
// not just the installer's own, matches a canonical (event, matcher,
// command) triple. A real host's settings.json is essentially never that
// clean: it also carries third-party hooks (a monitoring agent's
// PreToolUse hook, an operator's own SessionStart/SessionEnd scripts) that
// no installer template produced. Because a single foreign hook anywhere
// in the document flips `pure` to false for the ENTIRE file, an otherwise
// fully-wired host with even one foreign hook never gets its eight
// expected hooks claimed, and `pfm doctor` reports permanent ownership
// drift (`ownership=0 file=1`) for hooks that are, in fact, correctly
// wired — on every install, forever.
func TestInstallOwnershipLedgerClaimsHooksDespiteForeignHooksPresent(t *testing.T) {
	home := t.TempDir()

	// Build the settings document a fully-wired install produces, exactly
	// as TestInstallOwnershipLedgerClaimsHooksAlreadyPresentInSettings does.
	preexisting, _, _, err := updateSettings([]byte("{}\n"), home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(preexisting, &document); err != nil {
		t.Fatal(err)
	}
	// Foreign hooks a real host carries that no installer template
	// produced — invented placeholder commands shaped like a third-party
	// monitoring hook, an operator's own SessionStart script, and a
	// SessionEnd repo sync — parked at events the installer also wires, so
	// the document mixes canonical and foreign entries exactly like a live
	// host does.
	foreignPreToolUse := `node "/opt/example-monitor/hook-handler.js" PreToolUse`
	foreignSessionStart := `bash /opt/example-tools/cc-memory-wire.sh`
	foreignSessionEnd := `cd /opt/example-repo && git pull --ff-only`
	appendHookWithMatcher(document, "PreToolUse", "", foreignPreToolUse)
	appendHookWithMatcher(document, "SessionStart", "", foreignSessionStart)
	appendHookWithMatcher(document, "SessionEnd", "", foreignSessionEnd)
	withForeign, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}

	// Today's install run starts from an EMPTY ownership ledger, exactly as
	// wireSettings does for a path the ledger has no entry for yet.
	updated, _, owned, err := updateSettings(withForeign, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}

	prefix := home + "/.local/bin/pfm"
	expectedKeys := []settingsHookKey{
		{Event: "PreToolUse", Matcher: "Agent|Task", Command: prefix + " internal explore-deny"},
		{Event: "SubagentStart", Matcher: "rr|super-rr", Command: prefix + " internal rr-dir"},
		{Event: "UserPromptSubmit", Matcher: "", Command: prefix + " internal epic-inject"},
		{Event: "UserPromptSubmit", Matcher: "", Command: prefix + " internal reload-intercept"},
		{Event: "UserPromptSubmit", Matcher: "", Command: prefix + " internal compact-nudge"},
		{Event: "UserPromptSubmit", Matcher: "", Command: prefix + " usage-hook"},
		{Event: "SessionStart", Matcher: "", Command: prefix + " internal launcher-repair"},
		{Event: "SessionEnd", Matcher: "", Command: prefix + " internal clear-kill"},
		{Event: "UserPromptSubmit", Matcher: "", Command: prefix + " internal exit-intercept"},
		{Event: "SessionEnd", Matcher: "", Command: prefix + " internal exit-close"},
	}
	if len(owned) != len(expectedKeys) {
		t.Fatalf(
			"owned hooks=%d, want %d — every already-present expected hook must be claimed even with foreign hooks in the document: %#v",
			len(owned),
			len(expectedKeys),
			owned,
		)
	}
	for _, key := range expectedKeys {
		if owned[key] != 1 {
			t.Fatalf("owned[%#v]=%d, want 1", key, owned[key])
		}
	}
	for _, foreign := range []string{foreignPreToolUse, foreignSessionStart, foreignSessionEnd} {
		for key := range owned {
			if key.Command == foreign {
				t.Fatalf("foreign hook %q was wrongly claimed into the ownership ledger: %#v", foreign, owned)
			}
		}
	}
	for event, foreign := range map[string]string{
		"PreToolUse":   foreignPreToolUse,
		"SessionStart": foreignSessionStart,
		"SessionEnd":   foreignSessionEnd,
	} {
		if got := hookCommandCount(t, string(updated), event, foreign); got != 1 {
			t.Fatalf(
				"foreign %s hook count=%d after wiring, want 1 (installer must leave it untouched)\n%s",
				event,
				got,
				updated,
			)
		}
	}

	// The doctor hook probe must agree: an already-owned hook is never
	// drift, foreign hooks in the document notwithstanding.
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	writeFixture(t, settingsPath, string(withForeign))
	encoded, err := encodeSettingsHookOwnership(
		map[string]settingsHookCounts{physicalSettingsPath(settingsPath): owned},
	)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(
		t,
		settingsHookOwnershipPath(managedRootForHome(home)),
		string(encoded),
	)
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude")}}}
	for _, result := range ProbeExpectedHooks(home, machine) {
		if result.State == "drift" {
			t.Fatalf("hook probe reported drift for an already-owned hook despite foreign hooks present: %#v", result)
		}
	}
}

// TestSettingsInstallRemovesRetiredClearHideAndKeepsOneClearKill pins the
// deep-doctor defect: the kill-rename retired `internal clear-hide` in favor
// of `internal clear-kill` (cmd/pfm/main.go's `internal` dispatch has no
// `clear-hide` case at all — it falls through to the usage error), but the
// installer's SessionEnd wiring only recognizes and dedups the CURRENT
// clear-kill command. A settings.json still carrying the pre-rename command
// keeps it forever; the installer neither removes it nor even notices it.
func TestSettingsInstallRemovesRetiredClearHideAndKeepsOneClearKill(t *testing.T) {
	home := filepath.Join("neutral", "home")
	binary := home + "/.local/bin/pfm"
	retired := binary + " internal clear-hide"
	raw := []byte(`{"hooks":{"SessionEnd":[{"matcher":"","hooks":[{"type":"command","command":"` + retired + `"}]}]}}`)

	updated, changed, owned, err := updateSettings(raw, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatalf("retired clear-hide hook was not rewritten at all")
	}
	if strings.Contains(string(updated), "clear-hide") {
		t.Fatalf("retired command %q survived installer wiring:\n%s", retired, updated)
	}
	clearKill := binary + " internal clear-kill"
	if got := hookCommandCount(t, string(updated), "SessionEnd", clearKill); got != 1 {
		t.Fatalf("clear-kill count=%d after wiring, want exactly 1:\n%s", got, updated)
	}
	if owned[settingsHookKey{Event: "SessionEnd", Command: clearKill}] != 1 {
		t.Fatalf("owned ledger did not claim the replacement clear-kill hook: %#v", owned)
	}
}

// TestSettingsInstallRemovesRetiredChatGroupHookOnApply pins the retired
// chat-group feature's live-host contract: a settings.json written by an
// installer that predates the purge still carries the installer-owned
// UserPromptSubmit entry `pfm chat group hook`. Once "group" joined
// retiredHookCommands, the very next `pfm install` APPLY — not only an
// eventual uninstall — must strip that stale entry, while its same-event
// installer neighbors (usage-hook, epic-inject) and an unrelated operator
// hook sitting in its own entry survive untouched.
func TestSettingsInstallRemovesRetiredChatGroupHookOnApply(t *testing.T) {
	home := filepath.Join("neutral", "home")
	pfm := home + "/.local/bin/pfm"
	raw := []byte(`{
  "hooks": {
    "UserPromptSubmit": [
      {"matcher":"","hooks":[
        {"type":"command","command":"` + pfm + ` chat group hook"},
        {"type":"command","command":"` + pfm + ` usage-hook"},
        {"type":"command","command":"` + pfm + ` internal epic-inject"}
      ]},
      {"matcher":"custom","hooks":[
        {"type":"command","command":"operator-owned-hook"}
      ]}
    ]
  }
}`)

	updated, changed, _, err := updateSettings(raw, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("apply did not change a document containing the retired chat-group hook")
	}
	if strings.Contains(string(updated), "chat group hook") {
		t.Fatalf("retired chat-group hook survived an install apply:\n%s", updated)
	}
	for _, survivor := range []string{
		pfm + " usage-hook", pfm + " internal epic-inject",
	} {
		if got := hookCommandCount(t, string(updated), "UserPromptSubmit", survivor); got != 1 {
			t.Fatalf("apply dropped installer-owned neighbor %q: count=%d\n%s", survivor, got, updated)
		}
	}
	if got := hookCommandCount(t, string(updated), "UserPromptSubmit", "operator-owned-hook"); got != 1 {
		t.Fatalf("apply touched an unrelated operator hook in its own entry: count=%d\n%s", got, updated)
	}
}

func TestShimAssetEmitsConfiguredCodexPostureOnly(t *testing.T) {
	raw, err := readAsset("shim/pfm.zsh")
	if err != nil {
		t.Fatal(err)
	}
	renderedBytes, err := renderShimAsset(raw, Options{
		CodexYolo: map[int]bool{1: true, 2: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(renderedBytes)
	for _, want := range []string{
		"PFM_CODEX_YOLO",
		"[1]=1",
		"[2]=0",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered shim missing %q:\n%s", want, rendered)
		}
	}
	for _, retired := range []string{"typeset -gA PFM_CLAUDE_PROMPTED", "_cc_run() {", "CC_AUTONOMY_FLAGS="} {
		if strings.Contains(rendered, retired) {
			t.Fatalf("rendered shim retained retired Claude posture surface %q:\n%s", retired, rendered)
		}
	}
	if strings.Contains(rendered, "codex --dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("rendered shim retained unconditional Codex posture flags:\n%s", rendered)
	}
}

// TestStatusLineRewriteOwnsTheOverlayAndPreservesCustomCommands pins issue
// #14 F1.c on updateSettings alone: every form pfm install or a predecessor
// has ever pointed statusLine.command at — empty, the legacy shell script,
// the bare `pfm statusline` a hand re-point often reaches for, and the
// absolute pfm-binary form the installer itself wrote before the overlay
// existed — converges on the pfm-statusline overlay, while a genuinely
// custom command is left untouched.
func TestStatusLineRewriteOwnsTheOverlayAndPreservesCustomCommands(t *testing.T) {
	home := filepath.Join("neutral", "home")
	overlay := StatusLineOverlayCommand(home)
	absoluteRaw := home + "/.local/bin/pfm statusline"

	for _, testCase := range []struct {
		name string
		raw  string
	}{
		{"empty settings.json gets the overlay fresh", `{}`},
		{"legacy shell script rewrites to the overlay", `{"statusLine":{"type":"command","command":"bash ~/.claude/statusline-command.sh"}}`},
		{"bare pfm statusline rewrites to the overlay", `{"statusLine":{"type":"command","command":"pfm statusline"}}`},
		{"absolute pfm statusline rewrites to the overlay", fmt.Sprintf(`{"statusLine":{"type":"command","command":%q}}`, absoluteRaw)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			updated, changed, _, err := updateSettings([]byte(testCase.raw), home, false, nil)
			if err != nil || !changed {
				t.Fatalf("updateSettings changed=%v err=%v", changed, err)
			}
			var document map[string]any
			if err := json.Unmarshal(updated, &document); err != nil {
				t.Fatal(err)
			}
			status, _ := document["statusLine"].(map[string]any)
			if got, _ := status["command"].(string); got != overlay {
				t.Fatalf("statusLine.command=%q, want overlay %q:\n%s", got, overlay, updated)
			}
			if got, _ := status["type"].(string); got != "command" {
				t.Fatalf("statusLine.type=%q, want %q:\n%s", got, "command", updated)
			}
		})
	}

	t.Run("second apply of the overlay is a no-op", func(t *testing.T) {
		// A near-empty document always reports changed=true on its first pass
		// (hooks and cleanupPeriodDays get added alongside statusLine), so the
		// no-op claim is only meaningful against a document already fully
		// wired once — exactly what a real second `pfm install` sees.
		raw := fmt.Sprintf(`{"statusLine":{"type":"command","command":%q,"padding":0}}`, overlay)
		firstPass, _, owned, err := updateSettings([]byte(raw), home, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, changed, _, err := updateSettings(firstPass, home, false, owned)
		if err != nil {
			t.Fatal(err)
		}
		if changed {
			t.Fatalf("second apply over a fully-wired settings.json reported changed=true:\n%s", firstPass)
		}
	})

	t.Run("a genuinely custom statusLine command is preserved", func(t *testing.T) {
		raw := `{"statusLine":{"type":"command","command":"~/bin/my-own-statusline.sh"}}`
		updated, _, _, err := updateSettings([]byte(raw), home, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(updated, &document); err != nil {
			t.Fatal(err)
		}
		status, _ := document["statusLine"].(map[string]any)
		if got, _ := status["command"].(string); got != "~/bin/my-own-statusline.sh" {
			t.Fatalf("custom statusLine command was rewritten to %q:\n%s", got, updated)
		}
	})

	t.Run("uninstall removes only an overlay or raw pfm-owned statusLine", func(t *testing.T) {
		for _, command := range []string{overlay, absoluteRaw, "pfm statusline"} {
			raw := fmt.Sprintf(`{"statusLine":{"type":"command","command":%q}}`, command)
			removed, changed, _, err := updateSettings([]byte(raw), home, true, nil)
			if err != nil || !changed {
				t.Fatalf("uninstall of owned command %q changed=%v err=%v", command, changed, err)
			}
			if strings.Contains(string(removed), "statusLine") {
				t.Fatalf("uninstall left statusLine behind for owned command %q:\n%s", command, removed)
			}
		}
		raw := `{"statusLine":{"type":"command","command":"~/bin/my-own-statusline.sh"}}`
		removed, changed, _, err := updateSettings([]byte(raw), home, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		if changed || !strings.Contains(string(removed), "my-own-statusline.sh") {
			t.Fatalf("uninstall touched a custom statusLine command: changed=%v\n%s", changed, removed)
		}
	})
}

func hookMatcherCount(t *testing.T, raw, event, command, matcher string) int {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	events, _ := document["hooks"].(map[string]any)
	count := 0
	entries, _ := events[event].([]any)
	for _, value := range entries {
		entry, _ := value.(map[string]any)
		if entry["matcher"] != matcher {
			continue
		}
		hooks, _ := entry["hooks"].([]any)
		for _, hookValue := range hooks {
			hook, _ := hookValue.(map[string]any)
			if hook["command"] == command {
				count++
			}
		}
	}
	return count
}

func TestExploreDenyMatcherMigrationPreservesMixedOperatorEntry(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	settings := filepath.Join(config, "settings.json")
	pfm := home + "/.local/bin/pfm"
	writeFixture(t, settings, `{
  "hooks": {
    "PreToolUse": [{"matcher":"","hooks":[
      {"type":"command","command":"`+pfm+` internal explore-deny"},
      {"type":"command","command":"operator-all-tools-hook"}
    ]}]
  }
}`)
	var output bytes.Buffer
	installer := engine{
		options: Options{Mode: ModeApply, Home: home, ConfigDir: config, Stdout: &output},
		apply:   true, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
		stamp: "fixture",
	}
	if err := installer.wireSettings(); err != nil {
		t.Fatal(err)
	}
	raw := readFixture(t, settings)
	if got := hookMatcherCount(t, raw, "PreToolUse", "operator-all-tools-hook", ""); got != 1 {
		t.Fatalf("mixed operator hook matcher was narrowed: count=%d\n%s", got, raw)
	}
	if !strings.Contains(output.String(), "mixed") || !strings.Contains(output.String(), "matcher") {
		t.Fatalf("mixed matcher preservation was silent:\n%s", output.String())
	}
}

func TestDreamHookPauseRetiresEveryCopyAndPreservesNeighbors(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	secondaryPath := filepath.Join(home, ".cc", "3", "settings.json")
	pfm := home + "/.local/bin/pfm"
	writeFixture(t, settingsPath, `{
  "hooks": {
    "PreToolUse": [{"matcher":"Agent","hooks":[
      {"type":"command","command":"bash `+home+`/.claude/hooks/dreamer-agent-inject.sh --fixture"},
      {"type":"command","command":"agent-neighbor"}
    ]}],
    "SessionStart": [{"matcher":"startup","hooks":[
      {"type":"command","command":"bash `+home+`/.claude/hooks/dreamer-nudge.sh"},
      {"type":"command","command":"nudge-neighbor"}
    ]}],
    "PostToolUse": [{"matcher":"Agent","hooks":[
      {"type":"command","command":"`+pfm+` dream hook agent-inject"},
      {"type":"command","command":"operator-keep"}
    ]}],
    "UserPromptSubmit": [{"matcher":"","hooks":[
      {"type":"command","command":"`+pfm+` chat group hook"},
      {"type":"command","command":"`+pfm+` usage-hook"}
    ]}],
    "SessionEnd": [{"matcher":"","hooks":[
      {"type":"command","command":"`+pfm+` internal clear-kill"}
    ]}]
  }
}`)
	writeFixture(
		t,
		secondaryPath,
		`{"hooks":{"PreToolUse":[{"matcher":"Agent","hooks":[{"type":"command","command":"secondary-keep"}]}]}}`,
	)

	now := func() time.Time { return time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC) }
	if _, err := Run(context.Background(), Options{
		Mode:       ModeApply,
		Home:       home,
		ConfigDirs: []string{filepath.Join(home, ".claude"), filepath.Join(home, ".cc", "3")},
		Now:        now,
		Runner:     &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	ownershipPath := settingsHookOwnershipPath(managedRootForHome(home))
	if info, err := os.Stat(ownershipPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("ownership ledger mode=%v err=%v, want 0600", info, err)
	}
	settings := readFixture(t, settingsPath)
	for _, forbidden := range []string{
		"dreamer-agent-inject.sh", "dreamer-nudge.sh",
		"dream hook agent-inject", "dream hook nudge",
	} {
		if strings.Contains(settings, forbidden) {
			t.Fatalf("paused settings retained %q:\n%s", forbidden, settings)
		}
	}
	for _, neighbor := range []string{"agent-neighbor", "nudge-neighbor", "operator-keep"} {
		if !strings.Contains(settings, neighbor) {
			t.Fatalf("dream migration dropped same-event sibling %q:\n%s", neighbor, settings)
		}
	}
	secondary := readFixture(t, secondaryPath)
	for _, command := range []string{
		pfm + " internal explore-deny",
		pfm + " internal epic-inject",
		pfm + " internal compact-nudge",
		pfm + " internal launcher-repair",
	} {
		if got := hookCommandCount(t, secondary, "", command); got != 1 {
			t.Fatalf("managed secondary settings missing new installer hook %q: count=%d\n%s", command, got, secondary)
		}
	}

	var second bytes.Buffer
	report, err := Run(context.Background(), Options{
		Mode:       ModeApply,
		Home:       home,
		ConfigDirs: []string{filepath.Join(home, ".claude"), filepath.Join(home, ".cc", "3")},
		Now:        now,
		Stdout:     &second,
		Runner:     &fakeRunner{},
	})
	if err != nil || report.Changed != 0 {
		t.Fatalf("second apply report=%#v err=%v\n%s", report, err, second.String())
	}

	if _, err := Run(context.Background(), Options{
		Mode:       ModeUninstall,
		Home:       home,
		ConfigDirs: []string{filepath.Join(home, ".claude"), filepath.Join(home, ".cc", "3")},
		Now:        now,
		Runner:     &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	settings = readFixture(t, settingsPath)
	for event, command := range map[string]string{
		"UserPromptSubmit": pfm + " chat group hook",
		"SessionEnd":       pfm + " internal clear-kill",
	} {
		if got := hookCommandCount(t, settings, event, command); got != 0 {
			t.Fatalf("uninstall retained installer-owned %s hook: count=%d\n%s", event, got, settings)
		}
	}
	if got := hookCommandCount(t, settings, "UserPromptSubmit", pfm+" usage-hook"); got != 0 {
		t.Fatalf("uninstall retained installer-owned usage hook: count=%d\n%s", got, settings)
	}
	if !strings.Contains(settings, "operator-keep") {
		t.Fatalf("uninstall deleted unrelated hook:\n%s", settings)
	}
	secondary = readFixture(t, secondaryPath)
	for _, command := range []string{
		pfm + " chat group hook",
		pfm + " usage-hook",
		pfm + " internal clear-kill",
		pfm + " dream hook agent-inject",
		pfm + " dream hook nudge",
		pfm + " internal explore-deny",
		pfm + " internal epic-inject",
		pfm + " internal compact-nudge",
		pfm + " internal launcher-repair",
	} {
		if got := hookCommandCount(t, secondary, "", command); got != 0 {
			t.Fatalf("uninstall retained installer-owned secondary hook %q: count=%d\n%s", command, got, secondary)
		}
	}
	if !strings.Contains(secondary, "secondary-keep") {
		t.Fatalf("uninstall deleted unrelated secondary hook:\n%s", secondary)
	}
	if _, err := os.Stat(ownershipPath); !os.IsNotExist(err) {
		t.Fatalf("uninstall retained hook ownership ledger: %v", err)
	}
}

func hookCommandCount(t *testing.T, raw, event, wanted string) int {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("decode settings fixture: %v\n%s", err, raw)
	}
	events, _ := document["hooks"].(map[string]any)
	count := 0
	for eventName, eventValue := range events {
		if event != "" && eventName != event {
			continue
		}
		entries, _ := eventValue.([]any)
		for _, entryValue := range entries {
			entry, _ := entryValue.(map[string]any)
			hooks, _ := entry["hooks"].([]any)
			for _, hookValue := range hooks {
				hook, _ := hookValue.(map[string]any)
				if hook["command"] == wanted {
					count++
				}
			}
		}
	}
	return count
}

func TestUninstallRefusesToStrandOwnedHookInInvalidCodexJSON(t *testing.T) {
	home := t.TempDir()
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	writeFixture(t, hooksPath, "{broken\n")
	expected := ExpectedHook{
		Event:   "SessionStart",
		Matcher: codexClearMatcher,
		Command: filepath.Join(home, ".local", "bin", "pfm") + " internal clear-kill",
	}
	physical := physicalSettingsPath(hooksPath)
	encoded, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{
		physical: {{Event: expected.Event, Matcher: expected.Matcher, Command: expected.Command}: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, settingsHookOwnershipPath(managed), string(encoded))
	installer := engine{options: Options{Mode: ModeUninstall, Home: home}, managedRoot: managed}
	err = installer.wireCodexHooks()
	if err == nil || !strings.Contains(err.Error(), "refuse to strand owned hooks in invalid Codex hooks JSON") {
		t.Fatalf("wireCodexHooks error=%v", err)
	}
}

// The retired Codex clear-kill hook is never written fresh: a host with no
// existing hooks.json gets none created, across every configured home.
func TestCodexHookWiringWritesNoClearKillHookAcrossConfiguredHomes(t *testing.T) {
	home := t.TempDir()
	homes := []string{filepath.Join(home, ".codex"), filepath.Join(home, ".codex-2")}
	installer := engine{
		options: Options{Mode: ModeApply, Home: home, CodexHomes: homes, Stdout: io.Discard},
		apply:   true, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
	}
	if err := installer.wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	for _, codexHome := range homes {
		hooksPath := filepath.Join(codexHome, "hooks.json")
		if _, err := os.Stat(hooksPath); err == nil {
			raw := readFixture(t, hooksPath)
			if strings.Contains(raw, "internal clear-kill") {
				t.Fatalf("%s carries the retired clear-kill hook:\n%s", hooksPath, raw)
			}
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", hooksPath, err)
		}
	}
}

// A Codex home carrying a leftover clear-kill hook from a prior install has
// it stripped, not repaired.
func TestCodexHookWiringStripsALeftoverAcrossConfiguredHomes(t *testing.T) {
	home := t.TempDir()
	homes := []string{filepath.Join(home, ".codex"), filepath.Join(home, ".codex-2")}
	canonical := filepath.Join(home, ".local", "bin", "pfm") + " internal clear-kill"
	for _, codexHome := range homes {
		writeFixture(t, filepath.Join(codexHome, "hooks.json"), fmt.Sprintf(
			`{"hooks":{"SessionStart":[{"matcher":%q,"hooks":[{"type":"command","command":%q}]}]}}`,
			codexClearMatcher, canonical,
		))
	}
	installer := engine{
		options: Options{Mode: ModeApply, Home: home, CodexHomes: homes, Stdout: io.Discard},
		apply:   true, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
	}
	if err := installer.wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	for _, codexHome := range homes {
		raw := readFixture(t, filepath.Join(codexHome, "hooks.json"))
		if got := hookCommandCount(t, raw, "SessionStart", canonical); got != 0 {
			t.Fatalf("%s has %d leftover clear-kill hooks, want zero\n%s", codexHome, got, raw)
		}
	}
}
