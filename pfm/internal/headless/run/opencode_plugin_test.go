package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
)

func opencodePluginHarness() string {
	return `node --input-type=module <<'NODE'
import fs from "node:fs"

const capture = process.env.CAPTURE_DIR
const config = JSON.parse(process.env.OPENCODE_CONFIG_CONTENT)
if (!Array.isArray(config.plugin) || config.plugin.length !== 1) throw new Error("missing plugin URL")
const module = await import(config.plugin[0])
const replies = []
const hooks = await module.default({client: {postSessionIdPermissionsPermissionId: async (value) => {replies.push(value); return {data: true}}}})
if (typeof hooks.config !== "function") throw new Error("missing config hook")

const loadedConfig = {
  permission: {"*": "allow", bash: "deny", write: "deny"},
  agent: {build: {permission: {read: "deny"}}},
  mcp: {inherited: {type: "remote"}},
  share: "enabled",
  snapshot: true,
}
hooks.config(loadedConfig)
if (loadedConfig.share !== "disabled" || loadedConfig.snapshot !== false) throw new Error("session persistence controls were not applied")
fs.writeFileSync(capture + "/plugin-config.json", JSON.stringify(loadedConfig))

const systemOutput = {system: ["inherited", "second"]}
const originalSystem = systemOutput.system
await hooks["experimental.chat.system.transform"]({}, systemOutput)
if (systemOutput.system !== originalSystem) throw new Error("system transform replaced output.system")
fs.writeFileSync(capture + "/plugin-system.json", JSON.stringify({same: systemOutput.system === originalSystem, system: systemOutput.system}))

const primingOutput = {message: {format: {type: "json_schema", schema: {type: "object"}, retryCount: 2}, tools: {}}}
await hooks["chat.message"]({sessionID: "schema-seed"}, primingOutput)
const messageOutput = {message: {id: "user", format: {type: "text"}, tools: {bash: true, read: true, edit: true, write: true, patch: true, server_tool: true, remote_extra: true, StructuredOutput: false, unrelated: true}}}
await hooks["chat.message"]({sessionID: "run"}, messageOutput)
fs.writeFileSync(capture + "/plugin-message.json", JSON.stringify(messageOutput))

const childMessageOutput = {message: {id: "child-user", format: {type: "text"}, tools: {bash: true}}}
await hooks["chat.message"]({sessionID: "child"}, childMessageOutput)
if (childMessageOutput.message.format.type !== "text") throw new Error("child session inherited the primary schema format")

await hooks.event({event: {type: "permission.asked", properties: {sessionID: "unrelated", id: "skip"}}})
await hooks.event({event: {type: "permission.asked", properties: {sessionID: "run", id: "main-ask"}}})
await hooks.event({event: {type: "permission.asked", properties: {sessionID: "child", id: "child-ask"}}})
if (JSON.stringify(replies) !== JSON.stringify([{path: {id: "run", permissionID: "main-ask"}, body: {response: "reject"}}, {path: {id: "child", permissionID: "child-ask"}, body: {response: "reject"}}])) throw new Error("headless permissions were not rejected for the active sessions")

process.env.PFM_OPENCODE_AUTO_PERMISSION = "1"
const autoReplies = []
const autoHooks = await module.default({client: {postSessionIdPermissionsPermissionId: async (value) => {autoReplies.push(value); return {data: true}}}})
await autoHooks["chat.message"]({sessionID: "auto"}, {message: {id: "auto-user"}})
await autoHooks.event({event: {type: "permission.asked", properties: {sessionID: "auto", id: "auto-ask"}}})
if (autoReplies[0]?.body.response !== "once") throw new Error("native auto permission was not approved once")
delete process.env.PFM_OPENCODE_AUTO_PERMISSION

const messagesOutput = {messages: [{info: {role: "user", tools: {bash: false, edit: false, server_tool: false, remote_extra: true}}}]}
await hooks["experimental.chat.messages.transform"]({}, messagesOutput)
const transformedTools = messagesOutput.messages[0].info.tools
if (transformedTools.bash !== true || transformedTools.edit !== false || transformedTools.server_tool !== true || transformedTools.remote_extra !== false) throw new Error("chat messages transform did not preserve the requested mask")
fs.writeFileSync(capture + "/plugin-messages-transform.json", JSON.stringify({tools: {bash: transformedTools.bash, edit: transformedTools.edit, server_tool: transformedTools.server_tool, remote_extra: transformedTools.remote_extra}}))

const assistantPath = process.env.PFM_OPENCODE_ASSISTANTS_FILE
if (assistantPath) {
  const beforeChild = fs.readFileSync(assistantPath, "utf8")
  await hooks.event({event: {type: "message.updated", properties: {info: {sessionID: "child", role: "assistant", parentID: "child-user", id: "child-assistant"}}}})
  if (fs.readFileSync(assistantPath, "utf8") !== beforeChild) throw new Error("child session changed the primary assistant marker")
}
NODE
` + opencodeValidEvents()
}

func TestRunOpenCodeLoadsPluginAndAppliesControlsThroughNode(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeOpenCodeStub(t, opencodePluginHarness()+`
for name in OPENCODE_PURE OPENCODE_CONFIG OPENCODE_CONFIG_DIR; do
  if printenv "$name" >/dev/null 2>&1; then printf '%s=present\n' "$name" >> "$CAPTURE_DIR/env"; else printf '%s=absent\n' "$name" >> "$CAPTURE_DIR/env"; fi
done
printf '%s\n' "$XDG_DATA_HOME" > "$CAPTURE_DIR/data-home"
if [ -f "$XDG_DATA_HOME/opencode/auth.json" ]; then cat "$XDG_DATA_HOME/opencode/auth.json" > "$CAPTURE_DIR/auth.json"; fi`)
	accountHome := opencodeHome(t)
	if err := os.WriteFile(
		filepath.Join(accountHome, "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture-auth"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	config := opencodeMachine(binary, accountHome)
	config.Ask.Prefs = map[pfmengine.ID]pfmconfig.EnginePrefs{
		pfmengine.OpenCode: {Model: "gpt-5.6-luna", Effort: "high"},
	}
	system := "replacement system"
	schema := json.RawMessage(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`)
	settings := "user"
	result, err := Run(context.Background(), Request{
		Config: config, Engine: pfmengine.OpenCode, Account: 7, Prompt: "hello", TempDir: t.TempDir(),
		CWD: t.TempDir(), SystemPrompt: &system, Schema: schema,
		Tools: stringPtr("bash,read,mcp__server__tool"), SettingsSources: &settings,
		StrictMCP: true, NoSessionPersistence: true, Env: testEnv(capture,
			"CAPTURE_DIR="+capture,
			"PFM_OPENCODE_PERMISSION_JSON=ambient-permission",
			"OPENCODE_PURE=1", "OPENCODE_CONFIG=ambient-config", "OPENCODE_CONFIG_DIR=ambient-dir"),
	})
	if err != nil {
		requests, _ := os.ReadFile(filepath.Join(capture, "requests"))
		t.Fatalf("OpenCode Run() error = %v; server requests = %q", err, string(requests))
	}
	if result.Engine != pfmengine.OpenCode || result.Answer != `{"answer":"answer"}` {
		t.Fatalf("result engine/answer = %q/%q", result.Engine, result.Answer)
	}
	if result.Usage == nil || result.Usage.Input != 4 || result.Usage.Output != 3 || result.Usage.CachedInput != 3 ||
		result.Usage.CacheCreation != 5 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	if result.TotalCostUSD == nil || *result.TotalCostUSD != 0.125 {
		t.Fatalf("cost = %#v, want 0.125", result.TotalCostUSD)
	}

	var pluginConfig map[string]any
	if err := json.Unmarshal(mustRead(t, filepath.Join(capture, "plugin-config.json")), &pluginConfig); err != nil {
		t.Fatal(err)
	}
	permission, ok := pluginConfig["permission"].(map[string]any)
	if !ok || permission["*"] != "allow" || permission["bash"] != "deny" || permission["write"] != "deny" ||
		permission["StructuredOutput"] != "allow" {
		t.Fatalf("plugin permission overlay = %#v", pluginConfig["permission"])
	}
	agents, ok := pluginConfig["agent"].(map[string]any)
	var buildPermission map[string]any
	if ok {
		if build, buildOK := agents["build"].(map[string]any); buildOK {
			buildPermission, _ = build["permission"].(map[string]any)
		}
	}
	if !ok || buildPermission == nil || buildPermission["read"] != "deny" ||
		buildPermission["StructuredOutput"] != "allow" {
		t.Fatalf("agent permission overlay leaked inherited settings: %#v", pluginConfig["agent"])
	}
	if _, ok := pluginConfig["mcp"].(map[string]any)["inherited"]; ok {
		t.Fatalf("strict MCP left inherited servers in plugin config: %#v", pluginConfig["mcp"])
	}
	if pluginConfig["share"] != "disabled" || pluginConfig["snapshot"] != false {
		t.Fatalf("no-session persistence controls = %#v/%#v", pluginConfig["share"], pluginConfig["snapshot"])
	}

	var systemOutput map[string]any
	if err := json.Unmarshal(mustRead(t, filepath.Join(capture, "plugin-system.json")), &systemOutput); err != nil {
		t.Fatal(err)
	}
	if systemOutput["same"] != true || fmt.Sprint(systemOutput["system"]) != "[replacement system]" {
		t.Fatalf("system transform = %#v, want same array and replacement system", systemOutput)
	}

	var messageOutput map[string]any
	if err := json.Unmarshal(mustRead(t, filepath.Join(capture, "plugin-message.json")), &messageOutput); err != nil {
		t.Fatal(err)
	}
	message := messageOutput["message"].(map[string]any)
	tools, ok := message["tools"].(map[string]any)
	if !ok || tools["bash"] != true || tools["read"] != true || tools["server_tool"] != true ||
		tools["StructuredOutput"] != true ||
		tools["edit"] != false ||
		tools["write"] != false ||
		tools["patch"] != false ||
		tools["remote_extra"] != false ||
		tools["unrelated"] != false {
		t.Fatalf("exact per-message tool mask = %#v", message["tools"])
	}
	format := message["format"].(map[string]any)
	if format["type"] != "json_schema" || format["retryCount"] != float64(2) {
		t.Fatalf("message format = %#v", format)
	}
	if _, ok := format["schema"].(map[string]any); !ok {
		t.Fatalf("message schema missing: %#v", format)
	}
	var transformed map[string]any
	if err := json.Unmarshal(
		mustRead(t, filepath.Join(capture, "plugin-messages-transform.json")),
		&transformed,
	); err != nil {
		t.Fatal(err)
	}
	transformedTools, ok := transformed["tools"].(map[string]any)
	if !ok || transformedTools["bash"] != true || transformedTools["edit"] != false ||
		transformedTools["server_tool"] != true ||
		transformedTools["remote_extra"] != false {
		t.Fatalf("chat messages transform mask = %#v", transformed["tools"])
	}
	if got := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "catalog")))); got != "GET" {
		t.Fatalf("OpenCode preflight catalog requests = %q, want GET", got)
	}
	if gets := capturedLines(t, filepath.Join(capture, "assistant-gets")); len(gets) < 2 {
		t.Fatalf(
			"schema collector accepted an incomplete assistant snapshot after %d individual GET(s), want retry",
			len(gets),
		)
	}

	for _, line := range capturedLines(t, filepath.Join(capture, "env")) {
		if strings.HasSuffix(line, "=present") {
			t.Fatalf("ambient OpenCode control leaked into child: %q", line)
		}
	}
	auth := strings.TrimSpace(string(mustRead(t, filepath.Join(capture, "auth.json"))))
	if auth != `{"tokens":{"access_token":"fixture-auth"}}` {
		t.Fatalf("staged auth = %q", auth)
	}
	if got := strings.TrimSpace(
		string(mustRead(t, filepath.Join(capture, "data-home"))),
	); got == filepath.Dir(
		accountHome,
	) {
		t.Fatalf("no-session run reused account data home %q", got)
	}
}

func TestRunOpenCodeExplicitEmptyToolsDenyKnownAndDynamicTools(t *testing.T) {
	headlessJail(t)
	capture := t.TempDir()
	binary := writeOpenCodeStub(t, `node --input-type=module <<'NODE'
import fs from "node:fs"
const config = JSON.parse(process.env.OPENCODE_CONFIG_CONTENT)
const module = await import(config.plugin[0])
const hooks = await module.default({})
const output = {message: {id: "user", format: {type: "text"}, tools: {bash: true, server_tool: true, remote_extra: true, unrelated: true}}}
await hooks["chat.message"]({sessionID: "run"}, output)
fs.writeFileSync(process.env.CAPTURE_DIR + "/empty-tools.json", JSON.stringify(output.message.tools))
NODE
`+opencodeValidEvents())
	result, err := Run(context.Background(), Request{
		Config: opencodeMachine(binary, opencodeHome(t)), Engine: pfmengine.OpenCode,
		TempDir: t.TempDir(), Prompt: "hello", Tools: stringPtr(""), Env: testEnv(capture, "CAPTURE_DIR="+capture),
	})
	if err != nil {
		t.Fatalf("OpenCode empty-tools Run() error = %v", err)
	}
	if result.Answer != "answer" {
		t.Fatalf("answer = %q, want answer", result.Answer)
	}
	var tools map[string]any
	if err := json.Unmarshal(mustRead(t, filepath.Join(capture, "empty-tools.json")), &tools); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bash", "server_tool", "remote_extra", "unrelated"} {
		if tools[name] != false {
			t.Fatalf("explicit empty tool selection left %q enabled: %#v", name, tools)
		}
	}
}
