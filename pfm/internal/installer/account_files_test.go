package installer

import (
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
	want := []string{"hooks", "statusLine", "subagentStatusLine"}
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
	owned := settingsHookCounts{{Event: "Stop", Command: "private-ledger-only"}: 1}
	for _, testCase := range []struct {
		name string
		raw  string
		want []string
	}{
		{"ledger only", `{"hooks":{"Stop":[{"hooks":[{"command":"private-ledger-only"}]}]}}`, nil},
		{"command shape", fmt.Sprintf(`{"hooks":{"Stop":[{"hooks":[{"command":%q}]}]}}`, claudeHookTemplates(home)[0].Command), []string{"hooks"}},
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
	got, err := accountMCPLeftovers(raw, []string{"chat"}, nil)
	if err != nil || !reflect.DeepEqual(got, []string{"mcpServers.chat"}) {
		t.Fatalf("leftovers=%v err=%v", got, err)
	}
	for _, malformed := range []string{`{`, `[]`, `{"mcpServers":[]}`} {
		if _, err := accountMCPLeftovers([]byte(malformed), []string{"chat"}, nil); err == nil {
			t.Errorf("accepted wrong-shaped MCP registry %q", malformed)
		}
	}
}
