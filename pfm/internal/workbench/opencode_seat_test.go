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
	fleet := filepath.Join(dir, "clone", "pfm", "harness-prompts", "composed", "opencode.md")
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
				prefix + "Instructions from: ~/clone/pfm/harness-prompts/composed/opencode.md\nYou are the fleet.\n\n" + suffix,
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
