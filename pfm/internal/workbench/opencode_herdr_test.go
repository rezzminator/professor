package workbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestStageOpenCodeHerdrPlugin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state", "herdr.mjs")
	binary := filepath.Join(dir, "bin dir", "pfm")
	if err := StageOpenCodeHerdrPlugin(path, binary); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(binary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "const PFM_BINARY = "+string(encoded)+"\n") {
		t.Fatalf("staged plugin does not carry the pfm binary %s:\n%.400s", encoded, body)
	}
	if strings.Contains(string(body), herdrPluginBinaryToken) {
		t.Fatalf("staged plugin still holds the placeholder %s", herdrPluginBinaryToken)
	}
	old := time.Unix(100, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := StageOpenCodeHerdrPlugin(path, binary); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("unchanged plugin rewritten: %v, %v", info, err)
	}
	other := filepath.Join(dir, "other", "pfm")
	if err := StageOpenCodeHerdrPlugin(path, other); err != nil {
		t.Fatal(err)
	}
	replaced, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(replaced), other) || bytes.Equal(replaced, body) {
		t.Fatalf("plugin for a different binary not restaged: %s, %v", replaced, err)
	}
}

// The plugin kills a reporter at its own timer: the reporter's deadline must
// end, and its error log land, before that.
func TestOpenCodeHerdrPluginKillsOnlyAfterTheReporterDeadline(t *testing.T) {
	want := fmt.Sprintf("const REPORT_TIMEOUT_MS = %d\n", herdr.EngineKillTimeout.Milliseconds())
	if !strings.Contains(openCodeHerdrPlugin, want) {
		t.Fatalf("plugin holds no %q: its kill timer is not herdr.EngineKillTimeout", want)
	}
}

func TestStageOpenCodeHerdrPluginRefusals(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "plugin.mjs")
	if err := StageOpenCodeHerdrPlugin(path, "/bin/pfm"); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("write failure = %v", err)
	}
	if err := StageOpenCodeHerdrPlugin(filepath.Join(t.TempDir(), "plugin.mjs"), "pfm"); err == nil ||
		!strings.Contains(err.Error(), "absolute pfm binary path") {
		t.Fatalf("a bare PATH name was accepted: %v", err)
	}
}

const herdrPluginDriver = `import fs from "node:fs"
import assert from "node:assert/strict"
import {pathToFileURL} from "node:url"
const row = JSON.parse(fs.readFileSync(0, "utf8"))
for (const key of ["HERDR_ENV", "TMUX"]) delete process.env[key]
for (const [key, value] of Object.entries(row.env)) process.env[key] = value
const module = await import(pathToFileURL(row.plugin))
assert.deepEqual(Object.keys(module), ["PfmHerdr"])
const logs = []
const client = {app: {log: async (input) => { logs.push(input.body) }}}
const hooks = await module.PfmHerdr({client})
if (hooks.event) {
  for (const event of row.events) {
    if (event.type === "test.prompt") {
      assert.equal(typeof hooks["chat.message"], "function", "actual prompt submit hook must be registered")
      await hooks["chat.message"](event.input, event.output)
    } else await hooks.event({event})
  }
}
process.stdout.write(JSON.stringify({hasEvent: Boolean(hooks.event), logs}))
`

type herdrPluginRun struct {
	HasEvent bool `json:"hasEvent"`
	Logs     []struct {
		Service string `json:"service"`
		Level   string `json:"level"`
		Message string `json:"message"`
	} `json:"logs"`
}

// herdrRecorderCall is one invocation of the fake pfm: its arguments and the
// JSON the plugin piped on stdin.
type herdrRecorderCall struct {
	Verb  string
	Stdin map[string]string
}

func runHerdrPlugin(
	t *testing.T,
	env map[string]string,
	events []map[string]any,
	binary string,
) (herdrPluginRun, []herdrRecorderCall) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("OpenCode plugin fixtures require node: %v", err)
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "calls.log")
	if binary == "" {
		binary = filepath.Join(dir, "bin", "pfm")
		script := "#!/bin/sh\nprintf '%s|%s\\n' \"$*\" \"$(cat)\" >> '" + record + "'\n"
		if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	plugin := filepath.Join(dir, "herdr.mjs")
	if err := StageOpenCodeHerdrPlugin(plugin, binary); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"plugin": plugin, "env": env, "events": events})
	if err != nil {
		t.Fatal(err)
	}
	// The path has no entry named pfm: only the staged absolute path can find it.
	command := exec.Command(node, "--input-type=module", "-e", herdrPluginDriver)
	command.Env = []string{"PATH=" + filepath.Dir(node) + ":/usr/bin:/bin", "HOME=" + dir}
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Node plugin driver: %v\n%s\n%s", err, output, stderr.String())
	}
	var run herdrPluginRun
	if err := json.Unmarshal(output, &run); err != nil {
		t.Fatalf("driver output %q: %v", output, err)
	}
	var calls []herdrRecorderCall
	raw, err := os.ReadFile(record)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		args, stdin, _ := strings.Cut(line, "|")
		const prefix = "internal herdr-state --engine opencode "
		if !strings.HasPrefix(args, prefix) {
			t.Fatalf("plugin ran %q, want %q…", args, prefix)
		}
		call := herdrRecorderCall{Verb: strings.TrimPrefix(args, prefix), Stdin: map[string]string{}}
		if err := json.Unmarshal([]byte(stdin), &call.Stdin); err != nil {
			t.Fatalf("plugin piped %q: %v", stdin, err)
		}
		calls = append(calls, call)
	}
	return run, calls
}

func sessionEvent(kind, session string) map[string]any {
	return map[string]any{"type": kind, "properties": map[string]any{"sessionID": session}}
}

func statusEvent(session, status string) map[string]any {
	return map[string]any{"type": "session.status", "properties": map[string]any{
		"sessionID": session, "status": map[string]any{"type": status},
	}}
}

func createdEvent(session, parent string) map[string]any {
	info := map[string]any{"id": session}
	if parent != "" {
		info["parentID"] = parent
	}
	return map[string]any{"type": "session.created", "properties": map[string]any{"info": info}}
}

func updatedEvent(session, parent, title string) map[string]any {
	info := map[string]any{"id": session, "title": title}
	if parent != "" {
		info["parentID"] = parent
	}
	return map[string]any{"type": "session.updated", "properties": map[string]any{"info": info}}
}

func askedEvent(kind, session string, extra map[string]any) map[string]any {
	properties := map[string]any{"sessionID": session}
	for key, value := range extra {
		properties[key] = value
	}
	return map[string]any{"type": kind, "properties": properties}
}

func TestOpenCodeHerdrPluginReportsTheRootSessionThroughThePfmEntry(t *testing.T) {
	events := []map[string]any{
		createdEvent("ses_root", ""),
		statusEvent("ses_root", "busy"),
		askedEvent(
			"permission.asked",
			"ses_root",
			map[string]any{"permission": "edit", "patterns": []string{"src/app.ts"}},
		),
		sessionEvent("permission.replied", "ses_root"),
		statusEvent("ses_root", "idle"),
		sessionEvent("session.idle", "ses_root"),
		createdEvent("ses_child", "ses_root"),
		statusEvent("ses_child", "busy"),
		askedEvent("permission.asked", "ses_child", map[string]any{"permission": "bash"}),
		sessionEvent("permission.replied", "ses_child"),
		statusEvent("ses_child", "idle"),
		askedEvent(
			"question.asked",
			"ses_root",
			map[string]any{"questions": []map[string]any{{"question": "Which branch?"}}},
		),
		sessionEvent("question.replied", "ses_root"),
		statusEvent("ses_root", "retry"),
		statusEvent("ses_other", "busy"),
		{"type": "session.updated", "properties": map[string]any{"info": map[string]any{"id": "ses_noise"}}},
	}
	want := []herdrRecorderCall{
		{"start", map[string]string{"session_id": "ses_root"}},
		{"working", map[string]string{"session_id": "ses_root"}},
		{"blocked", map[string]string{"session_id": "ses_root", "message": "permission: edit src/app.ts"}},
		{"working", map[string]string{"session_id": "ses_root"}},
		{"idle", map[string]string{"session_id": "ses_root"}},
		{"blocked", map[string]string{"session_id": "ses_root", "message": "Which branch?"}},
		{"working", map[string]string{"session_id": "ses_root"}},
		{"end", map[string]string{"session_id": "ses_root"}},
	}
	for name, env := range map[string]map[string]string{
		"inside Herdr":         {"HERDR_ENV": "1"},
		"inside the chat tmux": {"TMUX": "/tmp/tmux-1/default,1,0"},
		"both":                 {"HERDR_ENV": "1", "TMUX": "/tmp/tmux-1/default,1,0"},
	} {
		t.Run(name, func(t *testing.T) {
			run, calls := runHerdrPlugin(t, env, events, "")
			if !run.HasEvent {
				t.Fatalf("plugin registered no event hook %s", name)
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("pfm calls:\n got %+v\nwant %+v", calls, want)
			}
			if len(run.Logs) != 0 {
				t.Fatalf("healthy run logged %+v", run.Logs)
			}
		})
	}
}

func TestOpenCodeHerdrPluginEndsOnDisposeAndStartsAgain(t *testing.T) {
	run, calls := runHerdrPlugin(t, map[string]string{"HERDR_ENV": "1"}, []map[string]any{
		createdEvent("ses_a", ""),
		statusEvent("ses_a", "busy"),
		{"type": "server.instance.disposed", "properties": map[string]any{"directory": "/work"}},
		statusEvent("ses_a", "busy"),
	}, "")
	want := []herdrRecorderCall{
		{"start", map[string]string{"session_id": "ses_a"}},
		{"working", map[string]string{"session_id": "ses_a"}},
		{"end", map[string]string{"session_id": "ses_a"}},
		{"start", map[string]string{"session_id": "ses_a"}},
		{"working", map[string]string{"session_id": "ses_a"}},
		{"end", map[string]string{"session_id": "ses_a"}},
	}
	if !run.HasEvent || !reflect.DeepEqual(calls, want) {
		t.Fatalf("hasEvent=%t pfm calls:\n got %+v\nwant %+v", run.HasEvent, calls, want)
	}
}

// A status for a session whose parentage is unknown is ignored until its
// created event arrives: event order never makes a sub-session the main one.
func TestOpenCodeHerdrPluginIgnoresASessionWhoseParentageIsUnknown(t *testing.T) {
	run, calls := runHerdrPlugin(t, map[string]string{"HERDR_ENV": "1"}, []map[string]any{
		statusEvent("ses_child", "busy"),
		createdEvent("ses_child", "ses_root"),
		statusEvent("ses_child", "idle"),
		createdEvent("ses_root", ""),
		statusEvent("ses_root", "busy"),
	}, "")
	want := []herdrRecorderCall{
		{"start", map[string]string{"session_id": "ses_root"}},
		{"working", map[string]string{"session_id": "ses_root"}},
		{"end", map[string]string{"session_id": "ses_root"}},
	}
	if !run.HasEvent || !reflect.DeepEqual(calls, want) {
		t.Fatalf("hasEvent=%t pfm calls:\n got %+v\nwant %+v", run.HasEvent, calls, want)
	}
}

// OpenCode names a fresh session "New session - {ISO time}" (a sub-session
// "Child session - {ISO time}") until the user or the model renames it; only a
// real title of a main session reaches the Herdr label.
func TestOpenCodeHerdrPluginForwardsAMainSessionTitleChange(t *testing.T) {
	const placeholder = "New session - 2026-10-16T01:02:03.456Z"
	disposed := map[string]any{"type": "server.instance.disposed", "properties": map[string]any{"directory": "/work"}}
	title := func(session, text string) herdrRecorderCall {
		return herdrRecorderCall{"title", map[string]string{"session_id": session, "title": text}}
	}
	start := func(session string) herdrRecorderCall {
		return herdrRecorderCall{"start", map[string]string{"session_id": session}}
	}
	end := func(session string) herdrRecorderCall {
		return herdrRecorderCall{"end", map[string]string{"session_id": session}}
	}
	for _, scenario := range []struct {
		name   string
		events []map[string]any
		want   []herdrRecorderCall
	}{
		{
			name: "a renamed main session forwards once per change and never the placeholder",
			events: []map[string]any{
				createdEvent("ses_root", ""),
				updatedEvent("ses_root", "", placeholder),
				updatedEvent("ses_root", "", "Fix the login redirect"),
				updatedEvent("ses_root", "", "Fix the login redirect"),
				updatedEvent("ses_root", "", "Fix the login redirect loop"),
				updatedEvent("ses_root", "", "Child session - 2026-10-16T01:02:03.456Z"),
			},
			want: []herdrRecorderCall{
				start("ses_root"),
				title("ses_root", "Fix the login redirect"),
				title("ses_root", "Fix the login redirect loop"),
				end("ses_root"),
			},
		},
		{
			name: "a user title that only resembles the placeholder forwards",
			events: []map[string]any{
				createdEvent("ses_root", ""),
				updatedEvent("ses_root", "", "New session - notes"),
				updatedEvent("ses_root", "", "New session - 2026-10-16"),
			},
			want: []herdrRecorderCall{
				start("ses_root"),
				title("ses_root", "New session - notes"),
				title("ses_root", "New session - 2026-10-16"),
				end("ses_root"),
			},
		},
		{
			name: "a sub-session and a session of unknown parentage never forward",
			events: []map[string]any{
				createdEvent("ses_root", ""),
				createdEvent("ses_child", "ses_root"),
				updatedEvent("ses_child", "ses_root", "Explore the auth module"),
				updatedEvent("ses_stranger", "ses_root", "Never created"),
				updatedEvent("ses_unknown", "", "Never created either"),
				updatedEvent("ses_root", "", "Real work"),
			},
			want: []herdrRecorderCall{start("ses_root"), title("ses_root", "Real work"), end("ses_root")},
		},
		{
			name: "an empty title is no change",
			events: []map[string]any{
				createdEvent("ses_root", ""),
				updatedEvent("ses_root", "", ""),
				updatedEvent("ses_root", "", "Real work"),
			},
			want: []herdrRecorderCall{start("ses_root"), title("ses_root", "Real work"), end("ses_root")},
		},
		{
			name: "a title is forwarded again once the pane was released and restarted",
			events: []map[string]any{
				createdEvent("ses_root", ""),
				updatedEvent("ses_root", "", "Real work"),
				disposed,
				statusEvent("ses_root", "busy"),
				updatedEvent("ses_root", "", "Real work"),
			},
			want: []herdrRecorderCall{
				start("ses_root"),
				title("ses_root", "Real work"),
				end("ses_root"),
				start("ses_root"),
				{"working", map[string]string{"session_id": "ses_root"}},
				title("ses_root", "Real work"),
				end("ses_root"),
			},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			run, calls := runHerdrPlugin(t, map[string]string{"HERDR_ENV": "1"}, scenario.events, "")
			if !run.HasEvent || !reflect.DeepEqual(calls, scenario.want) {
				t.Fatalf("hasEvent=%t pfm calls:\n got %+v\nwant %+v", run.HasEvent, calls, scenario.want)
			}
		})
	}
}

func TestOpenCodeHerdrPluginIsInertOutsideHerdrAndTmux(t *testing.T) {
	run, calls := runHerdrPlugin(t, map[string]string{}, []map[string]any{statusEvent("ses_a", "busy")}, "")
	if run.HasEvent || len(calls) != 0 {
		t.Fatalf("hasEvent=%t, pfm calls=%+v; want an empty plugin that spawns nothing", run.HasEvent, calls)
	}
}

func TestOpenCodeHerdrPluginSaysSoWhenThePfmBinaryCannotRun(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no", "pfm")
	run, calls := runHerdrPlugin(t, map[string]string{"HERDR_ENV": "1"}, []map[string]any{
		createdEvent("ses_a", ""),
		statusEvent("ses_a", "busy"),
	}, missing)
	if len(calls) != 0 || len(run.Logs) == 0 {
		t.Fatalf("pfm calls=%+v logs=%+v; want the spawn failure logged and nothing recorded", calls, run.Logs)
	}
	for _, entry := range run.Logs {
		if entry.Service != "pfm-herdr" || entry.Level != "error" || !strings.Contains(entry.Message, missing) {
			t.Fatalf("log entry %+v does not name service pfm-herdr, level error and the binary %s", entry, missing)
		}
	}
}

func TestOpenCodeHerdrPluginReportsActualPromptWithoutPromptText(t *testing.T) {
	prompt := func(session string) map[string]any {
		return map[string]any{
			"type":  "test.prompt",
			"input": map[string]any{"sessionID": session},
			"output": map[string]any{
				"message": map[string]any{"role": "user"},
				"parts":   []map[string]any{{"type": "text", "text": "private prompt must not escape"}},
			},
		}
	}
	for name, env := range map[string]map[string]string{
		"native": {"HERDR_ENV": "1"},
		"tmux":   {"TMUX": "/jail/tmux,1,0"},
	} {
		t.Run(name, func(t *testing.T) {
			run, calls := runHerdrPlugin(t, env, []map[string]any{
				createdEvent("ses_root", ""),
				createdEvent("ses_child", "ses_root"),
				statusEvent("ses_root", "busy"),
				prompt("ses_root"), prompt("ses_root"),
				prompt("ses_child"), prompt("ses_unknown"), prompt(""),
				statusEvent("ses_root", "busy"),
			}, "")
			want := []herdrRecorderCall{
				{"start", map[string]string{"session_id": "ses_root"}},
				{"working", map[string]string{"session_id": "ses_root"}},
				{"working", map[string]string{"session_id": "ses_root", "hook_event_name": "chat.message"}},
				{"working", map[string]string{"session_id": "ses_root", "hook_event_name": "chat.message"}},
				{"working", map[string]string{"session_id": "ses_root"}},
				{"end", map[string]string{"session_id": "ses_root"}},
			}
			if !run.HasEvent || !reflect.DeepEqual(calls, want) || len(run.Logs) != 0 {
				t.Fatalf(
					"actual prompt reports must be distinct, private and main-session-only: calls=%+v want=%+v logs=%+v",
					calls,
					want,
					run.Logs,
				)
			}
		})
	}
}
