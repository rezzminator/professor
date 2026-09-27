package installer

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
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

func TestStripAccountSettings(t *testing.T) {
	home := t.TempDir()
	raw, owned := accountSettingsFixture(home)
	updated, removed, err := stripAccountSettings(raw, home, owned)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"hooks", "statusLine", "subagentStatusLine"}; !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed=%v want=%v", removed, want)
	}
	for _, want := range []string{`"command": "operator-hook"`, `"cleanupPeriodDays": 36500`, `9007199254740993`} {
		if !strings.Contains(string(updated), want) {
			t.Errorf("strip lost %s: %s", want, updated)
		}
	}
	if leftovers, err := accountSettingsLeftovers(updated, home, nil, true); err != nil || len(leftovers) != 0 {
		t.Fatalf("strip left %v err=%v: %s", leftovers, err, updated)
	}
	if stripped, _, err := stripAccountSettings(
		[]byte(`{"hooks":{"Stop":[{"hooks":[{"command":"pfm internal clear-hide"}]}]}}`),
		home,
		nil,
	); err != nil ||
		strings.Contains(string(stripped), `"hooks"`) {
		t.Fatalf("empty hooks survived: %s err=%v", stripped, err)
	}
}

func TestCustomStatusLinePreserved(t *testing.T) {
	home := t.TempDir()
	raw := []byte(`{"statusLine":{"command":"operator-status"}}`)
	leftovers, err := accountSettingsLeftovers(raw, home, nil, true)
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("custom status reported: %v err=%v", leftovers, err)
	}
	updated, removed, err := stripAccountSettings(raw, home, nil)
	if err != nil || len(removed) != 0 || !bytes.Equal(updated, raw) {
		t.Fatalf("custom status changed: %s removed=%v err=%v", updated, removed, err)
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

func TestAccountMCPLeftoversAndStrip(t *testing.T) {
	raw := []byte(`{"counter":9007199254740993,"mcpServers":{"chat":{"command":"pfm"},"other":{"command":"operator"}}}`)
	got, err := accountMCPLeftovers(raw, []string{"chat"}, nil)
	if err != nil || !reflect.DeepEqual(got, []string{"mcpServers.chat"}) {
		t.Fatalf("leftovers=%v err=%v", got, err)
	}
	updated, removed, err := stripAccountMCP(raw, []string{"chat"}, nil)
	if err != nil || !reflect.DeepEqual(removed, got) || strings.Contains(string(updated), `"chat"`) ||
		!strings.Contains(string(updated), `"other"`) ||
		!strings.Contains(string(updated), `9007199254740993`) {
		t.Fatalf("updated=%s removed=%v err=%v", updated, removed, err)
	}
	for _, malformed := range []string{`{`, `[]`, `{"mcpServers":[]}`} {
		if _, err := accountMCPLeftovers([]byte(malformed), []string{"chat"}, nil); err == nil {
			t.Errorf("accepted wrong-shaped MCP registry %q", malformed)
		}
	}
}
