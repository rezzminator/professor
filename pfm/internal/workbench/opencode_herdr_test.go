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

// engineGuardPluginDriver loads the staged engine-guard plugin and runs its
// tool.execute.before over each call, printing each outcome, the warnings it
// logged and whether OpenCode's event loop ran while the guard did.
const engineGuardPluginDriver = `import fs from "node:fs"
import {pathToFileURL} from "node:url"
const row = JSON.parse(fs.readFileSync(0, "utf8"))
const logs = []
const mod = await import(pathToFileURL(row.plugin).href)
const hooks = await mod.PfmEngineGuard({directory: row.directory, client: {app: {log: async ({body}) => { logs.push(body.level + " " + body.message.split("\n")[0]) }}}})
const outcomes = []
let ticks = 0
const ticker = setInterval(() => { ticks++ }, 1)
for (const call of row.calls) {
  try {
    await hooks["tool.execute.before"]({tool: call.tool, sessionID: "s", callID: "c"}, {args: call.args})
    outcomes.push("allow")
  } catch (error) {
    outcomes.push("deny " + error.message)
  }
}
clearInterval(ticker)
process.stdout.write(JSON.stringify({outcomes, logs, loopRan: ticks > 0}))
`

// TestOpenCodeEngineGuardPlugin runs the staged plugin under node against a
// recording pfm: a write tool's call reaches the guard as the hook payload,
// a deny throws its reason, a read tool never spawns it, and a guard that
// fails lets the call through with one warning; the guard never blocks
// OpenCode's event loop.
func TestOpenCodeEngineGuardPlugin(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("OpenCode plugin fixtures require node: %v", err)
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "calls.log")
	binary := filepath.Join(dir, "bin", "pfm")
	script := "#!/bin/sh\npayload=$(cat)\nprintf '%s|%s\\n' \"$*\" \"$payload\" >> '" + record + "'\n" +
		"case $payload in *deny-me*) printf '%s\\n' " +
		`'{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"no framework writes"}}'` +
		" ;; *crash*) echo broken >&2; exit 3 ;; esac\n"
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(dir, "guard.mjs")
	if err := StageOpenCodeEngineGuardPlugin(plugin, binary); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"plugin": plugin, "directory": "/proj", "calls": []map[string]any{
		{"tool": "bash", "args": map[string]any{"command": "ls", "workdir": "src"}},
		{"tool": "write", "args": map[string]any{"filePath": "/proj/deny-me"}},
		{"tool": "read", "args": map[string]any{"filePath": "/proj/AGENTS.md"}},
		{"tool": "apply_patch", "args": map[string]any{"patchText": "crash"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "--input-type=module", "-e", engineGuardPluginDriver)
	command.Env = []string{"PATH=" + filepath.Dir(node) + ":/usr/bin:/bin", "HOME=" + dir}
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Node plugin driver: %v\n%s\n%s", err, output, stderr.String())
	}
	var run struct {
		Outcomes []string `json:"outcomes"`
		Logs     []string `json:"logs"`
		LoopRan  bool     `json:"loopRan"`
	}
	if err := json.Unmarshal(output, &run); err != nil {
		t.Fatalf("driver output %q: %v", output, err)
	}
	wantOutcomes := []string{"allow", "deny no framework writes", "allow", "allow"}
	wantLogs := []string{"warn engine guard did not run (fail-open): broken"}
	if !reflect.DeepEqual(run.Outcomes, wantOutcomes) || !reflect.DeepEqual(run.Logs, wantLogs) || !run.LoopRan {
		t.Fatalf("outcomes=%q logs=%q loopRan=%v, want %q and %q and true",
			run.Outcomes, run.Logs, run.LoopRan, wantOutcomes, wantLogs)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("read %s: %v", record, err)
	}
	wantCalls := "internal engine-guard --engine opencode|" +
		`{"tool_name":"bash","tool_input":{"command":"ls","workdir":"src"},"cwd":"/proj"}` + "\n" +
		"internal engine-guard --engine opencode|" +
		`{"tool_name":"write","tool_input":{"filePath":"/proj/deny-me"},"cwd":"/proj"}` + "\n" +
		"internal engine-guard --engine opencode|" +
		`{"tool_name":"apply_patch","tool_input":{"patchText":"crash"},"cwd":"/proj"}` + "\n"
	if string(raw) != wantCalls {
		t.Fatalf("guard calls in %s=\n%s\nwant\n%s", record, raw, wantCalls)
	}
}
