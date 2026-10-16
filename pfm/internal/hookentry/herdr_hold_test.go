package hookentry

import "testing"

// A Claude tool request's block holds while its dialog is open: a parallel
// sibling tool finishing (its async PostToolUse working) changes nothing, and
// only the requested tool's own PostToolUse or PostToolUseFailure, or an event
// that is no tool's (a prompt, Stop), leaves it.
func TestHerdrStateHoldsAToolBlockAgainstASiblingToolsWorking(t *testing.T) {
	bashRequest := `{"session_id":"s1","hook_event_name":"PermissionRequest","tool_name":"Bash",` +
		`"tool_input":{"command":"make test","description":"run the tests"}}`
	blockedRig := func(t *testing.T) (*chatRig, int) {
		t.Helper()
		rig := newChatRig(t)
		rig.themeOn()
		rig.seed(rig.registered("idle", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		rig.wantQuiet(rig.engine("blocked", bashRequest))
		rig.wantCalls(listCall, reportCall("w1:p1", "blocked", "waiting for approval: Bash"))
		return rig, len(rig.themeStateCalls())
	}
	for _, test := range []struct {
		name, event, input, want string
	}{
		{
			"a sibling Read finishing leaves the pane blocked", "working",
			`{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"/w/a.go"}}`, "",
		},
		{
			"a same-name sibling with other input leaves the pane blocked", "working",
			`{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`, "",
		},
		{
			"the requested tool's own PostToolUse is working", "working",
			`{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Bash",` +
				`"tool_input":{"description":"run the tests", "command":"make test"},"tool_response":{"stdout":"ok"}}`, "working",
		},
		{
			"the requested tool's own PostToolUseFailure is working", "working",
			`{"session_id":"s1","hook_event_name":"PostToolUseFailure","tool_name":"Bash",` +
				`"tool_input":{"command":"make test","description":"run the tests"},"error":"exit 2"}`, "working",
		},
		{
			"the requested tool interrupted mid-run leaves the block to the transcript watch", "working",
			`{"session_id":"s1","hook_event_name":"PostToolUseFailure","tool_name":"Bash",` +
				`"tool_input":{"command":"make test","description":"run the tests"},"error":"interrupted","is_interrupt":true}`, "",
		},
		{"a submitted prompt is working", "working", `{"session_id":"s1","hook_event_name":"UserPromptSubmit"}`, "working"},
		{"a finished turn is idle", "idle", `{"session_id":"s1","hook_event_name":"Stop"}`, "idle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig, marks := blockedRig(t)
			sent := len(rig.herdr.calls())
			rig.wantQuiet(rig.engine(test.event, test.input))
			got, state := rig.herdr.calls()[sent:], rig.tmux.storedState(t)
			if test.want == "" {
				if len(got) != 0 || state.State != "blocked" || len(rig.themeStateCalls()) != marks {
					t.Fatalf("requests = %q, stored = %s, theme marks = %v: want none, blocked kept, no new mark",
						got, rig.tmux.stored, rig.themeStateCalls())
				}
				return
			}
			if len(got) == 0 || got[len(got)-1] != reportCall("w1:p1", test.want, "") || state.State != test.want {
				t.Fatalf("requests = %q, stored = %s, want the %s report and state", got, rig.tmux.stored, test.want)
			}
		})
	}
	// A dialog that is the tool's own interaction writes the user's answer into
	// the input the tool runs with (Claude 2.1.301: AskUserQuestion's answers,
	// ExitPlanMode's plan), so its own PostToolUse never repeats the request's input.
	for _, dialog := range []struct{ tool, request, ran string }{
		{
			"AskUserQuestion", `{"questions":[{"question":"Which?","options":[{"label":"A"}]}]}`,
			`{"questions":[{"question":"Which?","kind":"choice","options":[{"label":"A"}]}],"answers":{"Which?":"A"}}`,
		},
		{"ExitPlanMode", `{}`, `{"plan":"# Plan"}`},
	} {
		t.Run("the answered "+dialog.tool+" dialog's own PostToolUse is working", func(t *testing.T) {
			rig := newChatRig(t)
			rig.seed(rig.registered("idle", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
			rig.wantQuiet(
				rig.engine("blocked", `{"session_id":"s1","hook_event_name":"PermissionRequest","tool_name":"`+
					dialog.tool+`","tool_input":`+dialog.request+`}`),
			)
			rig.wantQuiet(rig.engine("working", `{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"`+
				dialog.tool+`","tool_input":`+dialog.ran+`}`))
			if state := rig.tmux.storedState(t); state.State != "working" {
				t.Fatalf("stored = %s, want working once the %s dialog was answered", rig.tmux.stored, dialog.tool)
			}
		})
	}
	t.Run("a block no tool asked for is left by any tool's working", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("idle", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		rig.wantQuiet(rig.engine("notification", `{"session_id":"s1","notification_type":"permission_prompt"}`))
		rig.wantQuiet(rig.engine(
			"working",
			`{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"file_path":"/w/a.go"}}`,
		))
		if state := rig.tmux.storedState(t); state.State != "working" {
			t.Fatalf("stored = %s, want working", rig.tmux.stored)
		}
	})
}
