package installer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// hookStateFixture is one staged host for TestReportHooksStates: a temp HOME
// with a converged claude[2] settings.json, its ownership ledger, the pfm
// executable every hook command names, and one Codex home with no hooks.json.
type hookStateFixture struct {
	home     string
	machine  pfmconfig.Config
	settings string
	codex    string
	binary   string
}

func stageHookStateFixture(t *testing.T) hookStateFixture {
	t.Helper()
	home, machine := stageExpectedHookFixtures(t)
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	machine.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: codexHome}}
	return hookStateFixture{
		home:     home,
		machine:  machine,
		settings: filepath.Join(home, ".cc", "2", "settings.json"),
		codex:    filepath.Join(codexHome, "hooks.json"),
		binary:   filepath.Join(home, ".local", "bin", "pfm"),
	}
}

func (fixture hookStateFixture) hook(t *testing.T, name string) ExpectedHook {
	t.Helper()
	return findExpectedHook(t, fixture.home, fixture.machine, "claude[2]", name)
}

func (fixture hookStateFixture) editSettings(t *testing.T, edit func(document map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(fixture.settings)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	edit(document)
	updated, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.settings, append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// moveHook removes hook from its own (event, matcher) and registers the same
// command under another event and matcher.
func (fixture hookStateFixture) moveHook(t *testing.T, hook ExpectedHook, event, matcher string) {
	t.Helper()
	removeHookFixture(t, hook)
	fixture.editSettings(t, func(document map[string]any) {
		appendHookWithMatcher(document, event, matcher, hook.Command)
	})
}

// eachHookObject visits every hook object registered under event.
func eachHookObject(document map[string]any, event string, visit func(hook map[string]any)) {
	events, _ := document["hooks"].(map[string]any)
	entries, _ := events[event].([]any)
	for _, entryValue := range entries {
		entry, _ := entryValue.(map[string]any)
		hooks, _ := entry["hooks"].([]any)
		for _, hookValue := range hooks {
			if hook, ok := hookValue.(map[string]any); ok {
				visit(hook)
			}
		}
	}
}

// TestReportHooksStates pins every state of the doctor hook check
// (docs/design/hooks/hooks.md § The pfm doctor check), each produced by a
// fixture file in a temp HOME, through the exact line ReportHooks prints and
// the (warnings, failures) tally doctor exits on.
func TestReportHooksStates(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(t *testing.T, fixture *hookStateFixture)
		want     func(fixture hookStateFixture) []string
		absent   []string
		warnings int
		failures int
	}{
		{
			name:   "ok",
			mutate: func(*testing.T, *hookStateFixture) {},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json SessionStart launcher-repair ok\n",
					"doctor: hook claude[2] settings.json Stop callmeter ok\n",
				}
			},
			absent: []string{"MISSING", "DRIFT", "STALE", "UNREADABLE", "codex[", "broken"},
		},
		{
			name: "settings file absent",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				if err := os.Remove(fixture.settings); err != nil {
					t.Fatal(err)
				}
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json UserPromptSubmit usage MISSING — run pfm install (settings file absent)\n",
					"doctor: hook claude[2] settings.json UserPromptSubmit usage DRIFT ledger ownership=1 file=absent\n",
				}
			},
			absent:   []string{" ok\n", "UNREADABLE"},
			warnings: 18,
			failures: 18,
		},
		{
			name: "command absent",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				removeHookFixture(t, fixture.hook(t, "usage"))
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json UserPromptSubmit usage MISSING — run pfm install\n",
					"doctor: hook claude[2] settings.json UserPromptSubmit usage DRIFT ledger ownership=1 file=0\n",
				}
			},
			warnings: 1,
			failures: 1,
		},
		{
			name: "moved under the wrong event",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				fixture.moveHook(t, fixture.hook(t, "usage"), "Stop", "")
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json UserPromptSubmit usage DRIFT event want=UserPromptSubmit got=Stop — run pfm install\n",
				}
			},
			absent:   []string{"usage MISSING"},
			warnings: 1,
			failures: 1,
		},
		{
			name: "wrong matcher",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				fixture.moveHook(t, fixture.hook(t, "explore-deny"), "PreToolUse", "Agent")
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json PreToolUse explore-deny DRIFT matcher want=Agent|Task got=Agent — run pfm install\n",
				}
			},
			absent:   []string{"explore-deny MISSING"},
			warnings: 1,
			failures: 1,
		},
		{
			name: "other binary path",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				hook := fixture.hook(t, "clear-kill")
				other := filepath.Join(fixture.home, "elsewhere", "pfm")
				fixture.editSettings(t, func(document map[string]any) {
					eachHookObject(document, hook.Event, func(object map[string]any) {
						if object[configCommandKey] == hook.Command {
							object[configCommandKey] = other + " internal clear-kill"
						}
					})
				})
			},
			want: func(fixture hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json SessionEnd clear-kill DRIFT binary want=" + fixture.binary +
						" got=" + filepath.Join(fixture.home, "elsewhere", "pfm") + " — run pfm install\n",
				}
			},
			absent:   []string{"clear-kill MISSING", "broken"},
			warnings: 1,
			failures: 1,
		},
		{
			name: "executable missing",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				if err := os.Remove(fixture.binary); err != nil {
					t.Fatal(err)
				}
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json SessionStart launcher-repair DRIFT executable want=executable got=absent — run pfm install\n",
				}
			},
			absent:   []string{" ok\n"},
			failures: 18,
		},
		{
			name: "executable not executable",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				if err := os.Chmod(fixture.binary, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json SessionEnd exit-close DRIFT executable want=executable got=not-executable — run pfm install\n",
				}
			},
			absent:   []string{" ok\n"},
			failures: 18,
		},
		{
			name: "duplicate",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				hook := fixture.hook(t, "usage")
				fixture.editSettings(t, func(document map[string]any) {
					appendHookWithMatcher(document, hook.Event, hook.Matcher, hook.Command)
				})
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json UserPromptSubmit usage DRIFT count want=1 got=2 — run pfm install\n",
					"doctor: hook claude[2] settings.json UserPromptSubmit usage DRIFT ledger ownership=1 file=2\n",
				}
			},
			warnings: 1,
			failures: 1,
		},
		{
			name: "async missing",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				hook := fixture.hook(t, "callmeter")
				fixture.editSettings(t, func(document map[string]any) {
					eachHookObject(document, "Stop", func(object map[string]any) {
						if object[configCommandKey] == hook.Command {
							delete(object, "async")
						}
					})
				})
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json Stop callmeter DRIFT async want=true got=false — run pfm install\n",
					"doctor: hook claude[2] settings.json PostToolBatch callmeter ok\n",
				}
			},
			failures: 1,
		},
		{
			name: "ledger mismatch",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				hook := fixture.hook(t, "usage")
				path := settingsHookOwnershipPath(managedRootForHome(fixture.home))
				ownership, _, err := readSettingsHookOwnership(path)
				if err != nil {
					t.Fatal(err)
				}
				delete(
					ownership[physicalSettingsPath(fixture.settings)],
					settingsHookKey{Event: hook.Event, Matcher: hook.Matcher, Command: hook.Command},
				)
				encoded, err := encodeSettingsHookOwnership(ownership)
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, path, string(encoded))
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json UserPromptSubmit usage ok\n",
					"doctor: hook claude[2] settings.json UserPromptSubmit usage DRIFT ledger ownership=0 file=1\n",
				}
			},
			warnings: 1,
		},
		{
			name: "retired hook in a Claude settings file",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				fixture.editSettings(t, func(document map[string]any) {
					appendHookWithMatcher(document, "SessionEnd", "", fixture.binary+" internal clear-hide")
				})
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook claude[2] settings.json SessionEnd clear-hide STALE clear-hide — run pfm install\n",
				}
			},
			failures: 1,
		},
		{
			name: "retired hook in a Codex hooks.json",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				writeFixture(t, fixture.codex, `{"hooks":{"SessionStart":[`+
					`{"matcher":"","hooks":[{"type":"command","command":"`+fixture.binary+` internal clear-hide"}]},`+
					`{"matcher":"`+codexClearMatcher+`","hooks":[{"type":"command","command":"`+
					fixture.binary+` internal clear-kill"}]},`+
					`{"matcher":"","hooks":[{"type":"command","command":"/opt/tools/notify.sh"}]}`+
					`]}}`)
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook codex[1] hooks.json SessionStart clear-hide STALE clear-hide — run pfm install\n",
					"doctor: hook codex[1] hooks.json SessionStart codex-clear-kill STALE codex-clear-kill — run pfm install\n",
				}
			},
			absent:   []string{"notify.sh"},
			failures: 2,
		},
		{
			name: "unparsable settings file",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				writeFixture(t, fixture.settings, "{not json")
			},
			want: func(hookStateFixture) []string {
				return []string{"doctor: hook claude[2] settings.json UNREADABLE error="}
			},
			absent:   []string{"MISSING", " ok\n", "broken", "DRIFT"},
			failures: 1,
		},
		{
			name: "settings hooks value of the wrong shape",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				writeFixture(t, fixture.settings, `{"hooks":[]}`)
			},
			want: func(hookStateFixture) []string {
				return []string{"doctor: hook claude[2] settings.json UNREADABLE error=hooks is not an object\n"}
			},
			absent:   []string{"MISSING", " ok\n", "broken", "DRIFT"},
			failures: 1,
		},
		{
			name: "unparsable Codex hooks.json",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				writeFixture(t, fixture.codex, "{not json")
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook codex[1] hooks.json UNREADABLE error=",
					"doctor: hook claude[2] settings.json SessionStart launcher-repair ok\n",
				}
			},
			failures: 1,
		},
		{
			name: "unparsable ledger",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				writeFixture(t, settingsHookOwnershipPath(managedRootForHome(fixture.home)), "{not json")
			},
			want: func(hookStateFixture) []string {
				return []string{
					"doctor: hook ownership settings-hook-ownership.json UNREADABLE error=",
					"doctor: hook claude[2] settings.json SessionStart launcher-repair ok\n",
				}
			},
			absent:   []string{"broken"},
			failures: 1,
		},
		{
			name: "empty config",
			mutate: func(t *testing.T, fixture *hookStateFixture) {
				fixture.machine = pfmconfig.Config{}
				if err := os.Remove(settingsHookOwnershipPath(managedRootForHome(fixture.home))); err != nil {
					t.Fatal(err)
				}
			},
			want: func(hookStateFixture) []string {
				return []string{"doctor: hook claude none — no Claude config dir is configured in the machine config\n"}
			},
			warnings: 1,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := stageHookStateFixture(t)
			testCase.mutate(t, &fixture)
			var output bytes.Buffer
			warnings, failures := ReportHooks(&output, fixture.home, fixture.machine, false)
			for _, wanted := range testCase.want(fixture) {
				if !strings.Contains(output.String(), wanted) {
					t.Errorf("output missing %q:\n%s", wanted, output.String())
				}
			}
			for _, unwanted := range testCase.absent {
				if strings.Contains(output.String(), unwanted) {
					t.Errorf("output carries %q:\n%s", unwanted, output.String())
				}
			}
			if warnings != testCase.warnings || failures != testCase.failures {
				t.Errorf(
					"warnings=%d failures=%d, want %d/%d:\n%s",
					warnings, failures, testCase.warnings, testCase.failures, output.String(),
				)
			}
		})
	}
}
