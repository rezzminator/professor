package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOpenCodeEventGoldens(t *testing.T) {
	fix, session, receipts, _ := openCodePluginFixture(t)
	permission := Step{Type: StepPermission, Tool: "bash", Input: json.RawMessage(`{"command":"printf ready"}`)}
	question := Step{Type: StepQuestion, Message: "Continue?", Options: []string{"Approve", "Deny"}}
	for _, row := range []struct {
		goldens []string
		fire    func() error
	}{
		{[]string{"session-created"}, session.created},
		{[]string{"session-updated"}, session.updated},
		{[]string{"compacting"}, session.compacting},
		{[]string{"session-compacted"}, session.compacted},
		{[]string{"subagent-created", "subagent-busy", "subagent-idle"}, func() error {
			_, err := session.background(Step{Name: "Atlas helper", Status: "done", AgentID: "ses_child_atlas"})
			return err
		}},
		{[]string{"status-busy"}, func() error { return session.status("busy") }},
		{[]string{"permission-asked"}, func() error { return session.requestAttention(permission) }},
		{[]string{"permission-replied", "tool-before", "tool-after", "status-busy"}, func() error { return session.answerAttention(permission, true) }},
		{[]string{"permission-rejected"}, func() error { return session.answerAttention(permission, false) }},
		{[]string{"question-asked"}, func() error { return session.requestAttention(question) }},
		{[]string{"question-replied"}, func() error { return session.answerAttention(question, true) }},
		{[]string{"question-replied-second"}, func() error { second := question; second.Selected = 2; return session.answerAttention(second, true) }},
		{[]string{"question-rejected"}, func() error { return session.answerAttention(question, false) }},
		{[]string{"status-idle", "session-idle"}, session.stop},
		{[]string{"status-retry"}, func() error {
			return session.notification(Step{NotificationType: "session.status.retry", Message: "retrying"})
		}},
		{[]string{"session-deleted"}, func() error { return session.notification(Step{NotificationType: "session.deleted"}) }},
		{[]string{"server-disposed"}, session.disposed},
		{[]string{"global-disposed"}, func() error { return session.notification(Step{NotificationType: "global.disposed"}) }},
	} {
		t.Run(strings.Join(row.goldens, ","), func(t *testing.T) {
			if err := os.WriteFile(receipts, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := row.fire(); err != nil {
				t.Fatalf("%s: %v", row.goldens, err)
			}
			raw, err := os.ReadFile(receipts)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) != len(row.goldens) {
				t.Fatalf("%s: received %q, want goldens %v", receipts, lines, row.goldens)
			}
			for index, name := range row.goldens {
				t.Run(name, func(t *testing.T) {
					path := filepath.Join("testdata", "hooks", "opencode", name+".json")
					want, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var gotValue, wantValue any
					got := strings.ReplaceAll(lines[index], fix.work, "/fixture/atlas")
					if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(want, &wantValue); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(gotValue, wantValue) {
						t.Fatalf("%s: actual=%s, expected=%s", path, got, want)
					}
				})
			}
		})
	}
	if err := session.notification(
		Step{NotificationType: "unknown"},
	); err == nil ||
		err.Error() != `unscripted OpenCode notification "unknown"` {
		t.Fatalf("unknown notification: %v", err)
	}
	for _, selected := range []int{-1, 3} {
		invalid := question
		invalid.Selected = selected
		if err := session.answerAttention(
			invalid,
			true,
		); err == nil ||
			err.Error() != fmt.Sprintf("OpenCode question selected %d is outside 1..2", selected) {
			t.Fatalf("question selection %d: error=%v", selected, err)
		}
	}
}
