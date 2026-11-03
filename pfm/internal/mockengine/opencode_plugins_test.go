package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func openCodePluginFixture(t *testing.T) (*fixture, *openCodeSession, string, string) {
	t.Helper()
	fix := newFixture(t)
	receipts := filepath.Join(fix.root, "plugin-events.jsonl")
	reporter := filepath.Join(fix.root, "reporter.jsonl")
	binary := filepath.Join(fix.bin, "pfm")
	script := fmt.Sprintf(
		"#!/bin/sh\nprintf '%%s|%%s|%%s|%%s\\n' \"$*\" \"$(cat)\" \"$PFM_BEAT\" \"$PFM_RUN\" >> %s\n",
		reporter,
	)
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(fix.root, "herdr.mjs")
	guard := filepath.Join(fix.root, "guard.mjs")
	if err := workbench.StageOpenCodeHerdrPlugin(native, binary); err != nil {
		t.Fatal(err)
	}
	if err := workbench.StageOpenCodeEngineGuardPlugin(guard, binary); err != nil {
		t.Fatal(err)
	}
	recorder := filepath.Join(fix.root, "recorder.mjs")
	body := fmt.Sprintf(`import {appendFileSync} from "node:fs"
export const Recorder = async () => ({
 event:async ({event}) => appendFileSync(%q,JSON.stringify(event)+"\n"),
 "chat.message":async (input,output) => appendFileSync(%q,JSON.stringify({hook:"chat.message",input,output})+"\n"),
 "tool.execute.before":async (input,output) => appendFileSync(%q,JSON.stringify({hook:"tool.execute.before",input,output})+"\n"),
 "tool.execute.after":async (input,output) => appendFileSync(%q,JSON.stringify({hook:"tool.execute.after",input,output})+"\n"),
 "experimental.session.compacting":async (input,output) => appendFileSync(%q,JSON.stringify({hook:"experimental.session.compacting",input,output})+"\n")
})`, receipts, receipts, receipts, receipts, receipts)
	if err := os.WriteFile(recorder, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"plugin": []string{native, guard, recorder}})
	channel := filepath.Join(fix.root, "action.json")
	if err := os.WriteFile(channel, []byte(`{"beat":"A","run":"synthetic-A"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	proc := &process{
		ctx:    ctx,
		cwd:    fix.work,
		env:    fix.env(map[string]string{"OPENCODE_CONFIG_CONTENT": string(config), "HERDR_ENV": "1"}),
		stderr: io.Discard,
		script: newScript(Scenario{ActionFile: channel, Version: "1.14.30"}, engineOpenCode),
	}
	plugins, err := startOpenCodePlugins(proc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if plugins.child != nil {
			if err := plugins.close(); err != nil {
				t.Error(err)
			}
			plugins.child = nil
		}
	})
	session := &openCodeSession{
		proc:      proc,
		plugins:   plugins,
		id:        "ses_atlas01",
		projectID: "prj_atlas",
		provider:  "fixture",
		modelID:   "model",
		title:     "Atlas",
		createdMS: 1700000000000,
		updatedMS: 1700000000001,
	}
	return fix, session, receipts, reporter
}

func TestOpenCodeInstalledPluginsReceiveCurrentAction(t *testing.T) {
	fix, session, _, reporter := openCodePluginFixture(t)
	if err := session.created(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		session.proc.script.ActionFile,
		[]byte(`{"beat":"B","run":"synthetic-B"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	step := Step{Type: StepPermission, Tool: "bash", Input: json.RawMessage(`{"command":"printf ready"}`)}
	if err := session.requestAttention(step); err != nil {
		t.Fatal(err)
	}
	if err := session.answerAttention(step, true); err != nil {
		t.Fatal(err)
	}
	if err := session.stop(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(reporter)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`internal herdr-state --engine opencode start|{"session_id":"ses_atlas01"}|A|synthetic-A`,
		`internal herdr-state --engine opencode blocked|{"session_id":"ses_atlas01","message":"permission: bash *"}|B|synthetic-B`,
		`internal herdr-state --engine opencode working|{"session_id":"ses_atlas01"}|B|synthetic-B`,
		`internal engine-guard --engine opencode|{"tool_name":"bash","tool_input":{"command":"printf ready"},"cwd":"` + fix.work + `"}|B|synthetic-B`,
		`internal herdr-state --engine opencode idle|{"session_id":"ses_atlas01"}|B|synthetic-B`,
	}
	if got := strings.Split(strings.TrimSpace(string(raw)), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s: reporter calls=%q, want %q", reporter, got, want)
	}
}

func TestOpenCodePluginConfigRefusesUnscriptedModules(t *testing.T) {
	fix := newFixture(t)
	for _, row := range []struct{ config, want string }{
		{`{`, "decode OpenCode plugin config: unexpected end of JSON input"},
		{`{"plugin":["some-package"]}`, "mock OpenCode plugin must name an installed local file: some-package"},
		{`{"plugin":[4]}`, "mock OpenCode plugin must name an installed local file: 4"},
	} {
		proc := &process{
			env:    fix.env(map[string]string{"OPENCODE_CONFIG_CONTENT": row.config}),
			cwd:    fix.work,
			script: newScript(Scenario{}, engineOpenCode),
		}
		if _, err := startOpenCodePlugins(proc); err == nil || err.Error() != row.want {
			t.Fatalf("config=%s: error=%v, want %q", row.config, err, row.want)
		}
	}
}

func TestOpenCodeTUIApprovalRunsNativePlugins(t *testing.T) {
	fix, prototype, _, reporter := openCodePluginFixture(t)
	if err := prototype.plugins.close(); err != nil {
		t.Fatal(err)
	}
	prototype.plugins.child = nil
	fix.write(
		Scenario{
			Pane:       openCodeShapes,
			ActionFile: prototype.proc.script.ActionFile,
			SessionID:  "ses_atlas01",
			Steps: []Step{
				{
					Type:    StepPermission,
					Tool:    "bash",
					Input:   json.RawMessage(`{"command":"printf ready"}`),
					Message: "Approve Atlas command",
				},
				{Type: StepTurn, Reply: "approved"},
				{Type: StepQuestion, Message: "Choose Atlas destination", Options: []string{"Approve", "Deny"}},
				{Type: StepTurn, Reply: "answered"},
			},
		},
	)
	session := fix.startTUI(
		"opencode",
		[]string{fix.work},
		map[string]string{"OPENCODE_CONFIG_CONTENT": prototype.proc.env("OPENCODE_CONFIG_CONTENT"), "HERDR_ENV": "1"},
	)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, openCodeShapes.Composer) })
	session.typeLine("make the Atlas report")
	session.waitFrame("permission", func(frame string) bool { return strings.Contains(frame, "Approve Atlas command") })
	if err := os.WriteFile(
		prototype.proc.script.ActionFile,
		[]byte(`{"beat":"B","run":"synthetic-B"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	session.typeLine("")
	session.waitFrame("approved", func(frame string) bool { return strings.Contains(frame, "⏺ approved") })
	raw, err := os.ReadFile(reporter)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(calls) != 8 {
		t.Fatalf("%s: calls=%q, want start, two working reports, title, blocked, working, guard, idle", reporter, calls)
	}
	want := []string{
		`internal herdr-state --engine opencode start|{"session_id":"ses_atlas01"}|A|synthetic-A`,
		`internal herdr-state --engine opencode working|{"session_id":"ses_atlas01","hook_event_name":"chat.message"}|A|synthetic-A`,
		`internal herdr-state --engine opencode working|{"session_id":"ses_atlas01"}|A|synthetic-A`,
		`internal herdr-state --engine opencode title|{"session_id":"ses_atlas01","title":"make the Atlas report"}|A|synthetic-A`,
		`internal herdr-state --engine opencode blocked|{"session_id":"ses_atlas01","message":"permission: bash *"}|A|synthetic-A`,
		`internal herdr-state --engine opencode working|{"session_id":"ses_atlas01"}|B|synthetic-B`,
		`internal engine-guard --engine opencode|{"tool_name":"bash","tool_input":{"command":"printf ready"},"cwd":"` + fix.work + `"}|B|synthetic-B`,
		`internal herdr-state --engine opencode idle|{"session_id":"ses_atlas01"}|B|synthetic-B`,
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s: actual=%q, expected=%q", reporter, calls, want)
	}

	session.typeLine("ask the Atlas question")
	session.waitFrame(
		"question",
		func(frame string) bool { return strings.Contains(frame, "Choose Atlas destination") },
	)
	session.typeRaw("\x1b")
	session.waitFrame("interrupted", func(frame string) bool { return strings.Contains(frame, "Interrupted") })
	raw, err = os.ReadFile(reporter)
	if err != nil {
		t.Fatal(err)
	}
	calls = strings.Split(strings.TrimSpace(string(raw)), "\n")
	if calls[len(calls)-1] != `internal herdr-state --engine opencode idle|{"session_id":"ses_atlas01"}|B|synthetic-B` {
		t.Fatalf("%s: cancelled question calls=%q", reporter, calls)
	}
}
