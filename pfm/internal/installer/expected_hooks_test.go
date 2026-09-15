package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
)

func TestProbeExpectedHooksStatesAndOwnership(t *testing.T) {
	t.Run("all present", func(t *testing.T) {
		home, machine := stageExpectedHookFixtures(t)
		results := ProbeExpectedHooks(home, machine)
		if len(results) != len(ExpectedHooks(home, machine)) {
			t.Fatalf("results=%d expected=%d: %#v", len(results), len(ExpectedHooks(home, machine)), results)
		}
		for _, result := range results {
			if result.State != "ok" {
				t.Errorf("unexpected hook result=%#v", result)
			}
		}
	})

	t.Run("missing owned hook", func(t *testing.T) {
		home, machine := stageExpectedHookFixtures(t)
		hook := findExpectedHook(t, home, machine, "claude[2]", "usage")
		removeHookFixture(t, hook)
		assertHookState(t, ProbeExpectedHooks(home, machine), hook, "missing")
		assertHookState(t, ProbeExpectedHooks(home, machine), hook, "drift")
	})

	t.Run("stale binary", func(t *testing.T) {
		home, machine := stageExpectedHookFixtures(t)
		hook := findExpectedHook(t, home, machine, "claude[2]", "clear-kill")
		raw, err := os.ReadFile(hook.File)
		if err != nil {
			t.Fatal(err)
		}
		stale := strings.ReplaceAll(string(raw), hook.Command, "/usr/local/bin/pfm internal clear-kill")
		if err := os.WriteFile(hook.File, []byte(stale), 0o600); err != nil {
			t.Fatal(err)
		}
		assertHookState(t, ProbeExpectedHooks(home, machine), hook, "broken")
	})
}

// TestProbeExpectedHooksFlagsRetiredHookCommandsAsStale pins the deep-doctor
// defect: a settings.json that still carries the pre-rename `internal
// clear-hide` command (retired in favor of `internal clear-kill`; `internal
// clear-hide` is not a recognized subcommand at all — cmd/pfm/main.go's
// `internal` dispatch falls through to its usage error for it) produces NO
// probe row at all. ProbeExpectedHooks only ever walks the installer's own
// `expected` hook list; a command that matches no expected hook AND is not
// (and never was) claimed by the ownership ledger is invisible end to end —
// doctor prints nothing about a stale command sitting in a live host's hooks.
func TestProbeExpectedHooksFlagsRetiredHookCommandsAsStale(t *testing.T) {
	home, machine := stageExpectedHookFixtures(t)
	hook := findExpectedHook(t, home, machine, "claude[2]", "clear-kill")
	retired := strings.Replace(hook.Command, "internal clear-kill", "internal clear-hide", 1)
	if retired == hook.Command {
		t.Fatalf("fixture command %q did not contain internal clear-kill", hook.Command)
	}

	raw, err := os.ReadFile(hook.File)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	appendHookWithMatcher(document, hook.Event, hook.Matcher, retired)
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook.File, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	results := ProbeExpectedHooks(home, machine)
	var retiredResult *HookProbeResult
	warnings := 0
	for index, result := range results {
		if result.State != "ok" {
			warnings++
		}
		if strings.Contains(result.Hook.Command, "clear-hide") {
			retiredResult = &results[index]
		}
	}
	if retiredResult == nil {
		t.Fatalf("doctor hook probe said nothing about the retired command %q: %#v", retired, results)
	}
	if !strings.Contains(strings.ToLower(retiredResult.State), "stale") {
		t.Fatalf("retired command result state=%q, want it to say stale: %#v", retiredResult.State, retiredResult)
	}
	if warnings == 0 {
		t.Fatalf("retired command did not surface as a warning-worthy probe result: %#v", results)
	}
}

// TestProbeExpectedHooksReportsAnUnknownPFMHookAsStale pins issue #24
// finding 2's doctor half: a settings.json carrying a hook of pfm's own
// shape naming a subcommand THIS binary does not implement — left behind by
// a rollback to an older pfm, or by a newer pfm's own hook surviving a
// revert — produces NO probe row before unknownPFMHookCommand exists,
// exactly like the already-fixed table-retired case above, but for a
// subcommand no table entry or template will ever know by name.
func TestProbeExpectedHooksReportsAnUnknownPFMHookAsStale(t *testing.T) {
	setTestImplementedSubcommands(t)
	home, machine := stageExpectedHookFixtures(t)
	hook := findExpectedHook(t, home, machine, "claude[2]", "exit-intercept")
	unknown := strings.Replace(hook.Command, "internal exit-intercept", "internal hook-from-a-newer-pfm", 1)
	if unknown == hook.Command {
		t.Fatalf("fixture command %q did not contain internal exit-intercept", hook.Command)
	}

	raw, err := os.ReadFile(hook.File)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	appendHookWithMatcher(document, hook.Event, hook.Matcher, unknown)
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook.File, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	results := ProbeExpectedHooks(home, machine)
	var unknownResult *HookProbeResult
	for index, result := range results {
		if strings.Contains(result.Hook.Command, "hook-from-a-newer-pfm") {
			unknownResult = &results[index]
		}
	}
	if unknownResult == nil {
		t.Fatalf("doctor hook probe said nothing about the unknown pfm hook %q: %#v", unknown, results)
	}
	if unknownResult.State != "stale" {
		t.Fatalf("unknown pfm hook result state=%q, want stale: %#v", unknownResult.State, unknownResult)
	}
	if unknownResult.Hook.Name != "unknown:hook-from-a-newer-pfm" {
		t.Fatalf(
			"unknown pfm hook result name=%q, want %q: %#v",
			unknownResult.Hook.Name,
			"unknown:hook-from-a-newer-pfm",
			unknownResult,
		)
	}
	if !strings.Contains(unknownResult.Error, "does not implement") {
		t.Fatalf(
			"unknown pfm hook result error=%q, want it to say this pfm does not implement the subcommand",
			unknownResult.Error,
		)
	}
}

// TestClaudeHookTemplatesIncludesReloadIntercept pins T2's installer half
// directly at the template source doctor and the settings wiring both read:
// the `/reload` UserPromptSubmit hook must be present, on the right event,
// with an empty matcher (the epic-inject shape), pointing at the binary's
// own `internal reload-intercept` subcommand.
func TestClaudeHookTemplatesIncludesReloadIntercept(t *testing.T) {
	home := filepath.Join("neutral", "home")
	templates := claudeHookTemplates(home)
	if got := commandByName(templates, "reload-intercept"); got != home+"/.local/bin/pfm internal reload-intercept" {
		t.Fatalf("reload-intercept command=%q", got)
	}
	found := false
	for _, template := range templates {
		if template.Name != "reload-intercept" {
			continue
		}
		found = true
		if template.Event != "UserPromptSubmit" || template.Matcher != "" {
			t.Fatalf("reload-intercept template=%#v, want UserPromptSubmit with an empty matcher", template)
		}
	}
	if !found {
		t.Fatal("claudeHookTemplates dropped the reload-intercept hook")
	}
}

func TestExpectedHooksIncludesCodexAppendixAcrossAccounts(t *testing.T) {
	home := t.TempDir()
	machine := pfmconfig.Config{
		CodexAccounts: []pfmconfig.CodexAccount{
			{ID: 1, Home: filepath.Join(home, ".codex")},
			{ID: 2, Home: filepath.Join(home, ".codex-2")},
		},
	}
	hooks := ExpectedHooks(home, machine)
	if len(hooks) != 2 {
		t.Fatalf("hooks=%#v", hooks)
	}
	for _, hook := range hooks {
		if hook.Name != "codex-appendix" || hook.Event != "SessionStart" {
			t.Fatalf("hook=%#v", hook)
		}
	}
}

// A leftover Codex SessionStart clear-kill hook — canonical command, wrong
// type, or the old shell-parent shape — is stripped outright, never
// repaired or migrated forward: there is nothing left to converge it toward.
func TestCodexHookWiringStripsALeftoverClearKillHookInEveryShape(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".local", "bin", "pfm") + " internal clear-kill"
	legacyParent := filepath.Join(home, ".local", "bin", "pfm") + ` internal clear-kill --parent "$PPID"`
	raw := []byte(fmt.Sprintf(
		`{"hooks":{"SessionStart":[`+
			`{"matcher":%q,"hooks":[{"type":"prompt","command":%q}]},`+
			`{"matcher":%q,"hooks":[{"type":"command","command":%q}]}`+
			`]}}`,
		codexClearMatcher, canonical,
		codexClearMatcher, legacyParent,
	))
	updated, changed, owned, err := updateCodexHooks(raw, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("Codex hook wiring did not strip the leftover clear-kill hook")
	}
	if len(owned) != 1 {
		t.Fatalf("appendix ownership=%#v", owned)
	}
	if got := hookCommandCount(t, string(updated), "SessionStart", canonical); got != 0 {
		t.Fatalf("canonical clear-kill count=%d, want zero:\n%s", got, updated)
	}
	if got := hookCommandCount(t, string(updated), "SessionStart", legacyParent); got != 0 {
		t.Fatalf("shell-parent clear-kill count=%d, want zero:\n%s", got, updated)
	}
	if hookCommandCount(t, string(updated), "SessionStart", codexHookTemplate(home).Command) != 1 {
		t.Fatalf("missing appendix: %s", updated)
	}

	// Idempotent: a second pass over the already-converged file changes
	// nothing further.
	again, changedAgain, _, err := updateCodexHooks(updated, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changedAgain {
		t.Fatalf("a converged Codex hooks file was rewritten again:\n%s", again)
	}
}

func stageExpectedHookFixtures(t *testing.T) (string, pfmconfig.Config) {
	t.Helper()
	home := t.TempDir()
	machine := pfmconfig.Config{
		Accounts: []pfmconfig.Account{{ID: 2, ConfigDir: filepath.Join(home, ".cc", "2")}},
	}
	ownership := map[string]settingsHookCounts{}
	seen := map[string]bool{}
	for _, hook := range ExpectedHooks(home, machine) {
		physical := physicalSettingsPath(hook.File)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		raw := []byte("{\"hooks\":{}}\n")
		updated, _, owned, err := updateSettings(raw, home, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, hook.File, string(updated))
		ownership[physical] = owned
	}
	encoded, err := encodeSettingsHookOwnership(ownership)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(
		t,
		filepath.Join(home, ".local", "share", "pfm", "install", "settings-hook-ownership.json"),
		string(encoded),
	)
	return home, machine
}

func findExpectedHook(t *testing.T, home string, machine pfmconfig.Config, target, name string) ExpectedHook {
	t.Helper()
	for _, hook := range ExpectedHooks(home, machine) {
		if hook.Target == target && hook.Name == name {
			return hook
		}
	}
	t.Fatalf("expected hook %s %s not found", target, name)
	return ExpectedHook{}
}

func removeHookFixture(t *testing.T, hook ExpectedHook) {
	t.Helper()
	raw, err := os.ReadFile(hook.File)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	key := settingsHookKey{Event: hook.Event, Matcher: hook.Matcher, Command: hook.Command}
	if !removeOwnedSettingsHooks(document, settingsHookCounts{key: 1}) {
		t.Fatalf("fixture did not contain %#v", key)
	}
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook.File, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertHookState(t *testing.T, results []HookProbeResult, hook ExpectedHook, state string) {
	t.Helper()
	for index := range results {
		result := &results[index]
		if result.Hook.File == hook.File && result.Hook.Event == hook.Event && result.Hook.Name == hook.Name &&
			result.State == state {
			return
		}
	}
	t.Fatalf("hook %s/%s has no %s result: %#v", hook.Target, hook.Name, state, results)
}

// The milestone reminder rides the same event and shape as epic-inject: one
// UserPromptSubmit hook, empty matcher, the binary's own subcommand.
func TestClaudeHookTemplatesIncludesCompactNudge(t *testing.T) {
	home := filepath.Join("neutral", "home")
	templates := claudeHookTemplates(home)
	if got := commandByName(templates, "compact-nudge"); got != home+"/.local/bin/pfm internal compact-nudge" {
		t.Fatalf("compact-nudge command=%q", got)
	}
	for _, template := range templates {
		if template.Name == "compact-nudge" && (template.Event != "UserPromptSubmit" || template.Matcher != "") {
			t.Fatalf("compact-nudge template=%#v, want UserPromptSubmit with an empty matcher", template)
		}
	}
}

// TestClaudeHookTemplatesIncludesExitCloseAndExitIntercept pins the one
// place a typo could silently break the /exit terminal-close pipeline: the
// two hooks ride DIFFERENT events on purpose — exit-intercept has to see
// the typed prompt before the model does (UserPromptSubmit), while
// exit-close has to run only once the chat has actually ended (SessionEnd).
// Swapping either event would either fire the closer on every prompt or
// never let the intercept catch "e"/"/e" before the model sees it.
func TestClaudeHookTemplatesIncludesExitCloseAndExitIntercept(t *testing.T) {
	home := filepath.Join("neutral", "home")
	templates := claudeHookTemplates(home)

	if got := commandByName(templates, "exit-intercept"); got != home+"/.local/bin/pfm internal exit-intercept" {
		t.Fatalf("exit-intercept command=%q", got)
	}
	if got := commandByName(templates, "exit-close"); got != home+"/.local/bin/pfm internal exit-close" {
		t.Fatalf("exit-close command=%q", got)
	}

	foundIntercept, foundClose := false, false
	for _, template := range templates {
		switch template.Name {
		case "exit-intercept":
			foundIntercept = true
			if template.Event != "UserPromptSubmit" || template.Matcher != "" {
				t.Fatalf("exit-intercept template=%#v, want UserPromptSubmit with an empty matcher", template)
			}
		case "exit-close":
			foundClose = true
			if template.Event != "SessionEnd" || template.Matcher != "" {
				t.Fatalf("exit-close template=%#v, want SessionEnd with an empty matcher", template)
			}
		}
	}
	if !foundIntercept {
		t.Fatal("claudeHookTemplates dropped the exit-intercept hook")
	}
	if !foundClose {
		t.Fatal("claudeHookTemplates dropped the exit-close hook")
	}
}

// TestReportHooksClaudeAbsentSkipsPerAccountNotPerHook pins D2's first row:
// with Claude absent, every claude[N] target collapses to ONE named skip
// line instead of nine per-hook rows, and none of it counts a warning —
// mirroring the installer's own choice never to wire Claude hooks on a host
// with no Claude Code binary.
func TestReportHooksClaudeAbsentSkipsPerAccountNotPerHook(t *testing.T) {
	home, machine := stageExpectedHookFixtures(t)
	var output bytes.Buffer
	warnings, failures := ReportHooks(&output, home, machine, true)
	if warnings != 0 || failures != 0 {
		t.Fatalf("warnings=%d failures=%d, want 0/0\n%s", warnings, failures, output.String())
	}
	if got := strings.Count(
		output.String(),
		"doctor: hook claude[2] skipped (no Claude Code binary installed)",
	); got != 1 {
		t.Fatalf("want exactly one skip line for claude[2], got %d:\n%s", got, output.String())
	}
	if strings.Contains(output.String(), "MISSING") {
		t.Fatalf("a Claude-absent hook row was still reported MISSING:\n%s", output.String())
	}
}

// TestReportHooksClaudePresentStillFailsOnAMissingHook pins the other half:
// with Claude present, a missing hook must still count as a failure exactly
// as before — the absence skip never masks a genuine installer defect.
func TestReportHooksClaudePresentStillFailsOnAMissingHook(t *testing.T) {
	home, machine := stageExpectedHookFixtures(t)
	hook := findExpectedHook(t, home, machine, "claude[2]", "usage")
	removeHookFixture(t, hook)
	var output bytes.Buffer
	_, failures := ReportHooks(&output, home, machine, false)
	if failures == 0 {
		t.Fatalf("a genuinely missing hook must still fail with Claude present:\n%s", output.String())
	}
	if !strings.Contains(
		output.String(),
		"doctor: hook claude[2] settings.json UserPromptSubmit usage MISSING — run pfm install",
	) {
		t.Fatalf("missing the expected MISSING row:\n%s", output.String())
	}
}

// TestReportHooksRowsCountMissingBrokenAndDriftWarnings pins every row shape
// ReportHooks prints and its two-tier split — ok is silent; missing, broken,
// and stale are each a failure (owned by install); drift is a warning.
func TestReportHooksRowsCountMissingBrokenAndDriftWarnings(t *testing.T) {
	saved := HookProbeOverride
	t.Cleanup(func() { HookProbeOverride = saved })
	home := t.TempDir()
	HookProbeOverride = func(string, pfmconfig.Config) []HookProbeResult {
		return []HookProbeResult{
			{
				Hook: ExpectedHook{
					Target: "claude[1]",
					File:   filepath.Join(home, ".claude", "settings.json"),
					Event:  "SessionEnd",
					Name:   "clear-kill",
				},
				State: "ok",
			},
			{
				Hook: ExpectedHook{
					Target: "codex",
					File:   filepath.Join(home, ".codex", "hooks.json"),
					Event:  "SessionStart",
					Name:   "clear-kill",
				},
				State: "missing",
			},
			{
				Hook: ExpectedHook{
					Target: "claude[2]",
					File:   filepath.Join(home, ".cc", "2", "settings.json"),
					Event:  "UserPromptSubmit",
					Name:   "usage",
				},
				State: "broken",
				Error: "parse error",
			},
			{
				Hook: ExpectedHook{
					Target: "ownership",
					File:   filepath.Join(home, "ledger.json"),
					Event:  "SessionEnd",
					Name:   "unexpected",
				},
				State: "drift",
				Error: "ledger owns 1 hook absent from expectations",
			},
			{
				Hook: ExpectedHook{
					Target: "codex",
					File:   filepath.Join(home, ".codex", "hooks.json"),
					Event:  "Stop",
					Name:   "usage",
				},
				State: "stale",
			},
		}
	}
	var output bytes.Buffer
	if warnings, failures := ReportHooks(&output, home, pfmconfig.Config{}, false); warnings != 1 || failures != 3 {
		t.Fatalf("warnings=%d failures=%d, want 1/3\n%s", warnings, failures, output.String())
	}
	for _, wanted := range []string{
		"doctor: hook claude[1] settings.json SessionEnd clear-kill ok",
		"doctor: hook codex hooks.json SessionStart clear-kill MISSING — run pfm install",
		"doctor: hook claude[2] settings.json UserPromptSubmit usage broken error=parse error",
		"doctor: hook ownership ledger.json SessionEnd unexpected drift error=ledger owns 1 hook absent from expectations",
		"doctor: hook codex hooks.json Stop usage stale — run pfm install",
	} {
		if !strings.Contains(output.String(), wanted) {
			t.Errorf("output missing %q:\n%s", wanted, output.String())
		}
	}
}

// TestReportHooksCountsMissingAsFailureAndDriftAsWarning is M2's regression
// test for issue #24 finding 1: ReportHooks must split into (warnings,
// failures) so `pfm update` can gate on failures alone while still reporting
// advisory drift. A missing hook is a state `pfm install --yes` owns and did
// not produce (failure); a drift row is advisory (warning). Unfixed, ReportHooks
// returns a single int and this test does not compile — that compile failure
// IS the watched-failing run.
func TestReportHooksCountsMissingAsFailureAndDriftAsWarning(t *testing.T) {
	saved := HookProbeOverride
	t.Cleanup(func() { HookProbeOverride = saved })
	home := t.TempDir()
	HookProbeOverride = func(string, pfmconfig.Config) []HookProbeResult {
		return []HookProbeResult{
			{
				Hook: ExpectedHook{
					Target: "codex",
					File:   filepath.Join(home, ".codex", "hooks.json"),
					Event:  "SessionStart",
					Name:   "clear-kill",
				},
				State: "missing",
			},
			{
				Hook: ExpectedHook{
					Target: "ownership",
					File:   filepath.Join(home, "ledger.json"),
					Event:  "SessionEnd",
					Name:   "unexpected",
				},
				State: "drift",
				Error: "ledger owns 1 hook absent from expectations",
			},
		}
	}
	var output bytes.Buffer
	warnings, failures := ReportHooks(&output, home, pfmconfig.Config{}, false)
	if warnings != 1 {
		t.Fatalf("warnings=%d, want 1 (the drift row)\n%s", warnings, output.String())
	}
	if failures != 1 {
		t.Fatalf("failures=%d, want 1 (the missing hook)\n%s", failures, output.String())
	}
}
