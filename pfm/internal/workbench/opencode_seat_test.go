package workbench

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStageOpenCodeSeatPlugin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seat", "plugin.mjs")
	if err := StageOpenCodeSeatPlugin(path); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Unix(100, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := StageOpenCodeSeatPlugin(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("unchanged plugin rewritten: %v, %v", info, err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := StageOpenCodeSeatPlugin(path); err != nil {
		t.Fatal(err)
	}
	replaced, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(replaced, body) {
		t.Fatalf("stale plugin not replaced: %s, %v", replaced, err)
	}
}

func TestStageOpenCodeSeatPluginWriteFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "plugin.mjs")
	if err := StageOpenCodeSeatPlugin(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("write failure = %v", err)
	}
}

func TestOpenCodeSeatPluginRemovesOnlyTheFleetPrompt(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("OpenCode plugin fixtures require node: %v", err)
	}
	dir := t.TempDir()
	prompt := filepath.Join(dir, "scribe.md")
	fleet := filepath.Join(dir, "clone", "pfm", "harness-prompts", "generated", "opencode.md")
	fleetBlock := "Instructions from: " + fleet + "\nYou are the fleet.\n\n"
	benchBlock := "Instructions from: " + prompt + "\nYou are scribe.\n"
	prefix := "You are opencode.\n<env>\n  Working directory: /work/acme/docs/scribe\n</env>\n"
	suffix := "Instructions from: /work/acme/AGENTS.md\nAcme rules.\n\n" + benchBlock
	joined := prefix + fleetBlock + suffix
	reshaped := "Instructions from: " + fleet + "\nYOU ARE THE FLEET.\n\n" + benchBlock
	cases := []struct {
		name                                            string
		system, want                                    []string
		config                                          map[string]any
		noFleet, noPersona, missingFleet, missingPrompt bool
		blankFleet, blankPrompt                         bool
		wantError, errorPrefix                          string
	}{
		{name: "joined shape", system: []string{joined}, want: []string{prefix + suffix}},
		{
			name: "per-part shape",
			system: []string{
				"You are opencode.",
				"<env>\n</env>",
				"Instructions from: " + fleet + "\nYou are the fleet.\n",
				benchBlock,
			},
			want: []string{"You are opencode.", "<env>\n</env>", benchBlock},
		},
		{
			name: "header names another spelling",
			system: []string{
				prefix + "Instructions from: ~/clone/pfm/harness-prompts/generated/opencode.md\nYou are the fleet.\n\n" + suffix,
			},
			want: []string{prefix + suffix},
		},
		{
			name:   "fleet text without header",
			system: []string{"You are opencode.\nYou are the fleet.\n" + benchBlock},
			want:   []string{"You are opencode.\n" + benchBlock},
		},
		{
			name:   "config without instructions",
			config: map[string]any{},
			system: []string{joined},
			want:   []string{prefix + suffix},
		},
		{
			name:   "title request",
			system: []string{"You are a title generator."},
			want:   []string{"You are a title generator."},
		},
		{
			name:   "fleet not listed",
			config: map[string]any{"instructions": []string{"/work/acme/notes.md"}},
			system: []string{benchBlock},
			want:   []string{benchBlock},
		},
		{name: "no fleet env", noFleet: true, system: []string{joined}, want: []string{joined}},
		{name: "fleet file missing", missingFleet: true, system: []string{joined}, want: []string{joined}},
		{name: "fleet file blank", blankFleet: true, system: []string{joined}, want: []string{joined}},
		{
			name:      "reshaped fleet text",
			system:    []string{reshaped},
			want:      []string{reshaped},
			wantError: "pfm workbench seat: OpenCode's system prompt carries the workbench prompt " + prompt + " but not the fleet prompt " + fleet + " its instructions list; its text was reshaped and cannot be replaced",
		},
		{
			name:          "workbench prompt missing",
			missingPrompt: true,
			system:        []string{joined},
			want:          []string{joined},
			errorPrefix:   "read workbench system prompt " + prompt + ": ",
		},
		{
			name:        "workbench prompt blank",
			blankPrompt: true,
			system:      []string{joined},
			want:        []string{joined},
			wantError:   "workbench system prompt " + prompt + " is empty",
		},
		{name: "no persona", noPersona: true, system: []string{joined}, want: []string{joined}},
	}
	plugin := filepath.Join(dir, "seat.mjs")
	if err := StageOpenCodeSeatPlugin(plugin); err != nil {
		t.Fatal(err)
	}
	driver := `import fs from "node:fs"
import assert from "node:assert/strict"
import {pathToFileURL} from "node:url"
const row = JSON.parse(fs.readFileSync(0, "utf8"))
for (const [key, value] of Object.entries(row.env)) {
  if (value) process.env[key] = value
  else delete process.env[key]
}
const module = await import(pathToFileURL(row.plugin))
assert.deepEqual(Object.keys(module), ["WorkbenchSeat"])
const hooks = await module.WorkbenchSeat()
const configBefore = JSON.stringify(row.config)
await hooks.config?.(row.config)
assert.equal(JSON.stringify(row.config), configBefore)
const output = {system: row.system}
const original = output.system
let error = "", cause = false
try {
  await hooks["experimental.chat.system.transform"]({}, output)
} catch (caught) {
  error = caught.message
  cause = caught.cause?.code === "ENOENT"
}
process.stdout.write(JSON.stringify({system: output.system, same: output.system === original, error, cause}))
`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{prompt, fleet} {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.missingPrompt {
				body := "You are scribe.\n"
				if tc.blankPrompt {
					body = " \n"
				}
				if err := os.WriteFile(prompt, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.missingFleet {
				body := "You are the fleet.\n"
				if tc.blankFleet {
					body = "  \n"
				}
				if err := os.WriteFile(fleet, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			config := tc.config
			if config == nil {
				config = map[string]any{"instructions": []string{fleet, "/work/acme/notes.md"}}
			}
			promptEnv, fleetEnv := prompt, fleet
			if tc.noPersona {
				promptEnv = ""
			}
			if tc.noFleet {
				fleetEnv = ""
			}
			payload, err := json.Marshal(map[string]any{
				"plugin": plugin, "config": config, "system": tc.system,
				"env": map[string]string{"PFM_OPENCODE_SYSTEM_FILE": promptEnv, "PFM_OPENCODE_FLEET_FILE": fleetEnv},
			})
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(node, "--input-type=module", "-e", driver)
			command.Stdin = bytes.NewReader(payload)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("Node plugin driver: %v\n%s", err, output)
			}
			var got struct {
				System []string `json:"system"`
				Same   bool     `json:"same"`
				Error  string   `json:"error"`
				Cause  bool     `json:"cause"`
			}
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatal(err)
			}
			if !got.Same || !reflect.DeepEqual(got.System, tc.want) {
				t.Errorf("system=%q, same=%t; want %q in the same array", got.System, got.Same, tc.want)
			}
			if tc.errorPrefix != "" {
				if !strings.HasPrefix(got.Error, tc.errorPrefix) || !strings.Contains(got.Error, "ENOENT") ||
					!got.Cause {
					t.Errorf(
						"read error=%q, cause=%t; want prefix %q and ENOENT cause",
						got.Error,
						got.Cause,
						tc.errorPrefix,
					)
				}
			} else if got.Error != tc.wantError {
				t.Errorf("error=%q; want %q", got.Error, tc.wantError)
			}
		})
	}
}

// Exercise the shipped JS hooks, not a Go reconstruction of native config.
func TestOpenCodeSeatPluginPinsNativeModelVariantAndPolicy(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(t.TempDir(), "seat.mjs")
	if err := StageOpenCodeSeatPlugin(plugin); err != nil {
		t.Fatal(err)
	}
	driver := `import assert from "node:assert/strict"
import {pathToFileURL} from "node:url"
const {WorkbenchSeat} = await import(pathToFileURL(process.argv[1]))
process.env.PFM_OPENCODE_MODEL = "openai/gpt-6.1-sol"
process.env.PFM_OPENCODE_VARIANT = "high"
process.env.PFM_OPENCODE_POLICY = "Launch constitution."
delete process.env.PFM_OPENCODE_SYSTEM_FILE
for (const custom of [false, true]) {
  const name = custom ? "careful" : "build"
  const policy = {prompt: "Keep my agent policy.", permission: {edit: "deny"}, tools: {bash: false}}
  const config = {instructions: ["AGENTS.md"], plugin: ["reporter.mjs"], agent: {[name]: {...policy}}}
  if (custom) config.default_agent = name
  const hooks = await WorkbenchSeat()
  await hooks.config(config)
  assert.deepEqual(config.agent[name], {...policy, model: "openai/gpt-6.1-sol", variant: "high"})
  assert.deepEqual(Object.keys(config.agent), [name])
  assert.equal(config.default_agent, custom ? name : undefined)
  assert.deepEqual(config.instructions, ["AGENTS.md"])
  assert.deepEqual(config.plugin, ["reporter.mjs"])
  // Native TUI sends remembered selection before agent.variant; the message
  // hook must pin the saved user variant before native option/metadata assembly.
  const remembered = "low"
  const message = {agent: name, model: {providerID: "openai", modelID: "gpt-6.1-sol", variant: remembered ?? config.agent[name].variant}}
  assert.equal(typeof hooks["chat.message"], "function", "native message hook must override remembered variant")
  await hooks["chat.message"]({agent: name, variant: remembered}, {message, parts: []})
  assert.equal(message.model.variant, "high")
  const child = {agent: "explore", model: {variant: "low"}}
  await hooks["chat.message"]({agent: "explore"}, {message: child, parts: []})
  assert.equal(child.model.variant, "low")
  const input = {agent: name, message, model: {providerID: "openai", id: "gpt-6.1-sol", variants: {high: {reasoningEffort: "high"}}}}
  await hooks["chat.params"](input, {options: {}})
  const nativeOptions = input.model.variants[message.model.variant]
  assert.equal(nativeOptions.reasoningEffort, "high")
  await assert.rejects(() => hooks["chat.params"]({...input, message: {model: {variant: "low"}}}, {options: {}}), /requested variant high but message carries low/)
  await assert.rejects(() => hooks["chat.params"]({...input, model: {...input.model, variants: {low: {}}}}, {options: {}}), /does not support.*high/)
  await assert.rejects(() => hooks["chat.params"]({...input, model: {...input.model, id: "other"}}, {options: {}}), /requested model/)
  await hooks["chat.params"]({...input, agent: "title", model: {id: "small"}}, {options: {}})
  const output = {system: ["Native safety.", "Project policy."]}
  await hooks["experimental.chat.system.transform"]({}, output)
  assert.deepEqual(output.system, ["Native safety.", "Project policy.", "Launch constitution."])
}
for (const agent of [{disable: true}, {hidden: true}, {mode: "subagent"}, {name: "renamed"}]) {
  const hooks = await WorkbenchSeat()
  const config = {agent: {build: {...agent, permission: {edit: "deny"}}}}
  await assert.rejects(() => hooks.config(config), /default agent build is not an available primary/)
  // Native can ignore the config error and fall back to plan. Neither hook
  // may silently accept that fallback with a different model or variant.
  await assert.rejects(() => hooks["chat.message"]({}, {message: {agent: "plan", model: {variant: "low"}}}), /not an available primary/)
  await assert.rejects(() => hooks["chat.params"]({agent: "plan", model: {id: "fallback"}}, {}), /not an available primary/)
  assert.deepEqual(config.agent.build.permission, {edit: "deny"})
}
delete process.env.PFM_OPENCODE_MODEL
for (const config of [{model: "openai/configured"}, {default_agent: "plan", agent: {plan: {model: "openai/planner", permission: {edit: "deny"}}}}]) {
  const hooks = await WorkbenchSeat()
  const name = config.default_agent || "build"
  const expected = config.agent?.[name]?.model ?? config.model
  await hooks.config(config)
  assert.equal(config.agent[name].model, expected)
  assert.equal(config.agent[name].variant, "high")
}
for (const config of [{}, {default_agent: "missing"}]) {
  const hooks = await WorkbenchSeat()
  await assert.rejects(() => hooks.config(config), /requires an explicit or configured model|default agent missing is not configured/)
  // Native logs and ignores config-hook errors; the call boundary must refuse too.
  await assert.rejects(() => hooks["chat.params"]({agent: "build", model: {id: "fallback"}}, {}), /requires an explicit or configured model|default agent missing is not configured/)
}
console.log("native launch model/variant/policy fixtures passed")
`
	command := exec.Command(node, "--input-type=module", "-e", driver, plugin)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native launch plugin: %v\n%s", err, output)
	}
}
