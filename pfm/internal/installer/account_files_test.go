package installer

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

func accountSettingsFixture(home string) ([]byte, settingsHookCounts) {
	owned := home + "/private-ledger-hook"
	template := claudeHookTemplates(home)[0].Command
	return []byte(fmt.Sprintf(`{
  "counter":9007199254740993,
  "cleanupPeriodDays":36500,
  "hooks":{"SessionStart":[{"matcher":"","hooks":[
    {"type":"command","command":%q},
    {"type":"command","command":%q},
    {"type":"command","command":"pfm internal clear-hide"},
    {"type":"command","command":"operator-hook"}
  ]}]},
  "statusLine":{"type":"command","command":%q},
  "subagentStatusLine":{"type":"command","command":%q}
}`, owned, template, claudelaunch.StatusLineCommand(home), claudelaunch.SubagentStatusLineCommand(home))),
		settingsHookCounts{{Event: "SessionStart", Matcher: "", Command: owned}: 1}
}

func TestAccountSettingsLeftovers(t *testing.T) {
	home := t.TempDir()
	raw, owned := accountSettingsFixture(home)
	got, err := accountSettingsLeftovers(raw, home, owned, true)
	want := []string{
		fmt.Sprintf("hook %q", home+"/.local/bin/pfm internal launcher-repair"),
		`hook "pfm internal clear-hide"`,
		"statusLine",
		"subagentStatusLine",
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("leftovers=%v err=%v want=%v", got, err, want)
	}
	for _, malformed := range []string{`{`, `[]`, `{"hooks":[]}`, `{"hooks":{"Stop":{}}}`} {
		if _, err := accountSettingsLeftovers([]byte(malformed), home, nil, true); err == nil {
			t.Errorf("accepted wrong-shaped settings %q", malformed)
		}
	}
}

func TestCustomStatusLinePreserved(t *testing.T) {
	home := t.TempDir()
	raw := []byte(`{"statusLine":{"command":"operator-status"}}`)
	leftovers, err := accountSettingsLeftovers(raw, home, nil, true)
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("custom status reported: %v err=%v", leftovers, err)
	}
}

func TestAccountSettingsUnreadableLedger(t *testing.T) {
	home := t.TempDir()
	command := home + "/.local/bin/pfm internal private-hook"
	owned := settingsHookCounts{{Event: "Stop", Command: command}: 1}
	for _, testCase := range []struct {
		name string
		raw  string
		want []string
	}{
		{"ledger only", fmt.Sprintf(`{"hooks":{"Stop":[{"hooks":[{"command":%q}]}]}}`, command), nil},
		{"command shape", fmt.Sprintf(`{"hooks":{"Stop":[{"hooks":[{"command":%q}]}]}}`, claudeHookTemplates(home)[0].Command), []string{fmt.Sprintf("hook %q", claudeHookTemplates(home)[0].Command)}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := accountSettingsLeftovers([]byte(testCase.raw), home, owned, false)
			if err != nil || (len(got) != 0 || len(testCase.want) != 0) && !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("leftovers=%v want=%v err=%v", got, testCase.want, err)
			}
		})
	}
}

func TestAccountMCPLeftovers(t *testing.T) {
	raw := []byte(`{"counter":9007199254740993,"mcpServers":{"chat":{"command":"pfm"},"other":{"command":"operator"}}}`)
	owned := ledgerOwnedMCP(
		map[string]map[string]any{"registry": {"chat": map[string]any{"command": "pfm"}}},
		"registry",
	)
	got, err := accountMCPLeftovers(raw, owned, nil)
	if err != nil || !reflect.DeepEqual(got, []string{"mcpServers.chat"}) {
		t.Fatalf("leftovers=%v err=%v", got, err)
	}
	for _, malformed := range []string{`{`, `[]`, `{"mcpServers":[]}`} {
		if _, err := accountMCPLeftovers([]byte(malformed), owned, nil); err == nil {
			t.Errorf("accepted wrong-shaped MCP registry %q", malformed)
		}
	}
}

func TestAccountSettingsOwnership(t *testing.T) {
	home := t.TempDir()
	template := home + "/.local/bin/pfm internal launcher-repair"
	private := home + "/.local/bin/pfm internal private-hook"
	operator := home + "/private-ledger-hook"
	for _, test := range []struct {
		name     string
		raw      string
		owned    settingsHookCounts
		readable bool
		want     []string
	}{
		{"template-hook", fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"command":%q}]}]}}`, template), nil, true, []string{fmt.Sprintf("hook %q", template)}},
		{"retired-bare-hook", `{"hooks":{"Stop":[{"hooks":[{"command":"pfm internal clear-hide"}]}]}}`, nil, true, []string{`hook "pfm internal clear-hide"`}},
		{"ledger-pfm-command", fmt.Sprintf(`{"hooks":{"Stop":[{"hooks":[{"command":%q}]}]}}`, private), settingsHookCounts{{Event: "Stop", Command: private}: 1}, true, []string{fmt.Sprintf("hook %q", private)}},
		{"ledger-operator-command", fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"command":%q}]}]}}`, operator), settingsHookCounts{{Event: "SessionStart", Command: operator}: 1}, true, []string{}},
		{"same-command-twice", fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"command":%q}]}],"Stop":[{"hooks":[{"command":%q}]}]}}`, template, template), nil, true, []string{fmt.Sprintf("hook %q", template)}},
		{"unreadable-ledger", fmt.Sprintf(`{"hooks":{"Stop":[{"hooks":[{"command":%q},{"command":%q}]}]}}`, private, template), settingsHookCounts{{Event: "Stop", Command: private}: 1}, false, []string{fmt.Sprintf("hook %q", template)}},
		{"bare-status-line", `{"statusLine":{"command":"pfm statusline"}}`, nil, true, []string{"statusLine"}},
		{"absolute-status-line", fmt.Sprintf(`{"statusLine":{"command":%q}}`, home+"/.local/bin/pfm statusline"), nil, true, []string{"statusLine"}},
		{"launch-status-line", fmt.Sprintf(`{"statusLine":{"command":%q}}`, claudelaunch.StatusLineCommand(home)), nil, true, []string{"statusLine"}},
		{"operator-status-line", `{"statusLine":{"command":"operator-status"}}`, nil, true, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := accountSettingsLeftovers([]byte(test.raw), home, test.owned, test.readable)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("leftovers=%v err=%v want=%v", got, err, test.want)
			}
		})
	}
}

func TestAccountMCPRegistrationOwnership(t *testing.T) {
	owned := ledgerOwnedMCP(
		map[string]map[string]any{"registry": {"owned": map[string]any{"command": "invented"}}},
		"registry",
	)
	for _, test := range []struct {
		name  string
		entry map[string]any
		want  []string
	}{
		{"unchanged-empty-env", map[string]any{"command": "invented", "env": map[string]any{}}, []string{"mcpServers.owned"}},
		{"replaced", map[string]any{"command": "operator"}, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"owned": test.entry}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := accountMCPLeftovers(raw, owned, nil)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("leftovers=%v err=%v want=%v", got, err, test.want)
			}
		})
	}
}
