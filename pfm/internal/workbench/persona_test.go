package workbench

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// settingsHome is a home whose source clone holds the Claude fleet prompt.
func settingsHome(t *testing.T) string {
	t.Helper()
	home, repo := t.TempDir(), t.TempDir()
	writeBenchFile(t, paths.GeneratedHarnessPromptIn(repo, pfmengine.Claude), "Fleet prompt.\n")
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestForLaunch(t *testing.T) {
	for _, test := range []struct {
		name     string
		engines  []pfmengine.ID
		modes    []Mode
		settings string
	}{
		{"New enabled", []pfmengine.ID{pfmengine.Claude}, []Mode{New}, ""},
		{"New disabled engine", []pfmengine.ID{pfmengine.Codex}, []Mode{New}, ""},
		{"Resume disabled engine", []pfmengine.ID{pfmengine.Codex}, []Mode{Resume}, ""},
		{"invalid manifest", []pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.OpenCode}, []Mode{New, Resume}, ""},
		{"outside a bench", []pfmengine.ID{pfmengine.Claude}, []Mode{New}, ""},
		{
			"settings extend",
			[]pfmengine.ID{pfmengine.Claude},
			[]Mode{New, Resume},
			`{"harnessPrompt":{"extend":"team.md"},"envs":{"TEAM_MODE":"strict"}}`,
		},
		{
			"settings replace",
			[]pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.OpenCode},
			[]Mode{New},
			`{"harnessPrompt":{"replace":"team.md"}}`,
		},
		{
			"settings envs only",
			[]pfmengine.ID{pfmengine.Claude, pfmengine.Codex},
			[]Mode{New},
			`{"envs":{"TEAM_MODE":"strict","ENGINE_SYNC_DISABLE":"codex"},"claude":{"settings":{"showThinkingSummaries":false}}}`,
		},
		{
			"settings both keys",
			[]pfmengine.ID{pfmengine.Claude},
			[]Mode{New},
			`{"harnessPrompt":{"extend":"team.md","replace":"team.md"},"envs":{"TEAM_MODE":"strict"}}`,
		},
		{"settings unknown key", []pfmengine.ID{pfmengine.Claude}, []Mode{New}, `{"drop":true}`},
		{
			"settings path escaping the root",
			[]pfmengine.ID{pfmengine.Claude},
			[]Mode{New},
			`{"harnessPrompt":{"replace":"../outside.md"}}`,
		},
		{
			"nested workbench beside settings",
			[]pfmengine.ID{pfmengine.Claude},
			[]Mode{New},
			`{"harnessPrompt":{"replace":"team.md"},"envs":{"TEAM_MODE":"strict"},"claude":{"settings":{"outputStyle":"x"}}}`,
		},
		{
			"nested workbench beside a settings prompt it never reads",
			[]pfmengine.ID{pfmengine.Claude},
			[]Mode{New},
			`{"harnessPrompt":{"extend":"gone.md"},"envs":{"TEAM_MODE":"strict"},"claude":{"settings":{"outputStyle":"x"}}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, dir := benchFixture(t)
			home := settingsHome(t)
			settingsPath := pfmconfig.PathIn(root)
			if test.settings != "" {
				writeBenchFile(t, settingsPath, test.settings)
				writeBenchFile(t, filepath.Join(root, "team.md"), "Team rules.\n")
				writeBenchFile(t, filepath.Join(filepath.Dir(root), "outside.md"), "Outside.\n")
			}
			cwd, wantError := dir, ""
			want := Persona{}
			switch test.name {
			case "New enabled":
				want = Persona{
					Bench: LoadBench(dir, root), Prompt: filepath.Join(dir, ".professor", "scribe.md"),
					Body: "You are scribe.", Effort: "xhigh",
				}
			case "New disabled engine":
				wantError = "workbench " + dir + ` does not enable codex: add "codex" to "engines" in ` + paths.WorkbenchManifest(
					dir,
				)
			case "invalid manifest":
				writeBenchFile(t, paths.WorkbenchManifest(dir), `{"prompt":""}`)
				wantError = paths.WorkbenchManifest(dir) + `: "prompt" is required`
			case "outside a bench":
				cwd = filepath.Join(root, "src")
			case "settings extend":
				cwd = filepath.Join(root, "src")
				want = Persona{
					Prompt: paths.SettingsPrompt(home, root, pfmengine.Claude),
					Body:   "Fleet prompt.\n\nTeam rules.\n",
					Envs:   map[string]string{"TEAM_MODE": "strict"},
				}
			case "settings replace":
				cwd = filepath.Join(root, "src")
				want = Persona{Prompt: filepath.Join(root, "team.md"), Body: "Team rules.\n"}
			case "settings envs only":
				cwd = filepath.Join(root, "src")
				want = Persona{
					Envs:           map[string]string{"TEAM_MODE": "strict", "ENGINE_SYNC_DISABLE": "codex"},
					ClaudeSettings: map[string]any{"showThinkingSummaries": false},
				}
			case "settings both keys":
				cwd = filepath.Join(root, "src")
				want = Persona{Warnings: []string{
					"config " + settingsPath + `: harnessPrompt sets both "extend" and "replace": keep one; launching without that file`,
				}}
			case "settings unknown key":
				cwd = filepath.Join(root, "src")
				want = Persona{Warnings: []string{
					"parse config " + settingsPath + `: json: unknown field "drop"; launching without that file`,
				}}
			case "settings path escaping the root":
				cwd = filepath.Join(root, "src")
				want = Persona{Warnings: []string{
					"config " + settingsPath + `: harnessPrompt.replace: "../outside.md" must be a path inside ` + root +
						`, relative to it; launching without that file`,
				}}
			case "nested workbench beside settings", "nested workbench beside a settings prompt it never reads":
				want = Persona{
					Bench: LoadBench(dir, root), Prompt: filepath.Join(dir, ".professor", "scribe.md"),
					Body: "You are scribe.", Effort: "xhigh", Envs: map[string]string{"TEAM_MODE": "strict"},
					ClaudeSettings: map[string]any{"outputStyle": "x"},
				}
			}
			for _, engine := range test.engines {
				for _, mode := range test.modes {
					got, err := ForLaunch(cwd, home, engine, mode)
					if wantError != "" {
						if err == nil || err.Error() != wantError {
							t.Errorf("ForLaunch %s/%d = %v; want %q", engine, mode, err, wantError)
						}
						continue
					}
					if err != nil || !reflect.DeepEqual(got, want) || got.Applies() != want.Applies() {
						t.Fatalf("ForLaunch %s/%d = %#v, %v; want %#v", engine, mode, got, err, want)
					}
					switch test.name {
					case "New enabled":
						writeBenchFile(t, got.Prompt, "Edited prompt.")
						updated, err := ForLaunch(dir, home, pfmengine.Claude, New)
						if err != nil || updated.Body != "Edited prompt." {
							t.Fatalf("prompt edit = %#v, %v", updated, err)
						}
					case "settings extend":
						if raw, err := os.ReadFile(got.Prompt); err != nil || string(raw) != want.Body {
							t.Fatalf("composed %s = %q, %v; want %q", got.Prompt, raw, err, want.Body)
						}
						writeBenchFile(t, filepath.Join(root, "team.md"), "Edited rules.\n")
						updated, err := ForLaunch(cwd, home, engine, mode)
						raw, readErr := os.ReadFile(got.Prompt)
						if err != nil || readErr != nil || updated.Body != "Fleet prompt.\n\nEdited rules.\n" ||
							string(raw) != updated.Body {
							t.Fatalf("adopter edit = %#v, %v; file %q, %v", updated, err, raw, readErr)
						}
						writeBenchFile(t, filepath.Join(root, "team.md"), "Team rules.\n")
					}
				}
			}
		})
	}
}

// Each guard on the settings file drops the whole file, envs included, and
// names the fault; a valid file beside it proves the read ran.
func TestSettingsFaults(t *testing.T) {
	for _, test := range []struct{ name, settings, want string }{
		{"valid", `{"harnessPrompt":{"replace":"team.md"},"envs":{"TEAM_MODE":"strict"},"claude":{"settings":{"env":{"X":"1"}}}}`, ""},
		{"null envs", `{"envs":null}`, ""},
		{"null claude settings", `{"claude":{"settings":null}}`, ""},
		{"claude settings not an object", `{"claude":{"settings":["x"]}}`, `claude.settings: not a JSON object (json: cannot unmarshal array into Go value of type map[string]interface {})`},
		{"claude settings hooks not an object", `{"claude":{"settings":{"hooks":[]}}}`, `claude.settings: "hooks" is not a JSON object`},
		{"claude settings env reserved", `{"claude":{"settings":{"env":{"CLAUDE_CONFIG_DIR":"/x"}}}}`, `claude.settings.env.CLAUDE_CONFIG_DIR is set by pfm itself on launch`},
		{"unknown claude key", `{"claude":{"permissions":{}}}`, `not valid: json: unknown field "permissions"`},
		{"trailing value", `{"envs":{}} {}`, `not valid: multiple JSON values`},
		{"neither prompt key", `{"harnessPrompt":{},"envs":{"TEAM_MODE":"strict"}}`, `harnessPrompt needs "extend" or "replace"`},
		{"lower-case env name", `{"envs":{"team_mode":"strict"}}`, `envs name "team_mode" must be upper snake: A-Z, 0-9 and _, starting with a letter`},
		{"NUL in an env value", `{"envs":{"TEAM_MODE":"a\u0000b"}}`, `envs.TEAM_MODE must not contain NUL`},
		{"pfm's state database", `{"envs":{"PFM_STATE_DB":"/adopter/state.db"}}`, `envs.PFM_STATE_DB is set by pfm itself on launch`},
		{"pfm's Codex home", `{"envs":{"CODEX_HOME":"/adopter/codex"}}`, `envs.CODEX_HOME is set by pfm itself on launch`},
		{"pfm's Claude store", `{"envs":{"CLAUDE_CONFIG_DIR":"/adopter/store"}}`, `envs.CLAUDE_CONFIG_DIR is set by pfm itself on launch`},
		{"settings file a link", `{"envs":{"TEAM_MODE":"strict"}}`, `not a regular file`},
		{"absolute path", `{"harnessPrompt":{"replace":"/etc/hostname"}}`, `harnessPrompt.replace: "/etc/hostname" must be a path inside ROOT, relative to it`},
		{"link leaving the root", `{"harnessPrompt":{"replace":"link.md"}}`, `harnessPrompt.replace: ROOT/link.md resolves to OUTSIDE, outside ROOT`},
		{"empty file", `{"harnessPrompt":{"replace":"empty.md"}}`, `harnessPrompt.replace: ROOT/empty.md is empty`},
		{"directory", `{"harnessPrompt":{"replace":"src"}}`, `harnessPrompt.replace: ROOT/src is not a regular file`},
		{"missing file", `{"harnessPrompt":{"replace":"gone.md"}}`, `harnessPrompt.replace: resolve ROOT/gone.md: lstat ROOT/gone.md: no such file or directory`},
		{"extend without a fleet prompt", `{"harnessPrompt":{"extend":"team.md"}}`, `harnessPrompt.extend: resolve generated harness prompt: read source repository marker <home>/.local/share/pfm/install/source-repo: no source repository recorded: open <home>/.local/share/pfm/install/source-repo: no such file or directory`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := benchFixture(t)
			outside := filepath.Join(filepath.Dir(root), "outside.md")
			writeBenchFile(t, outside, "Outside.\n")
			writeBenchFile(t, filepath.Join(root, "team.md"), "Team rules.\n")
			writeBenchFile(t, filepath.Join(root, "empty.md"), "")
			writeBenchFile(t, filepath.Join(root, "src", "main.go"), "package main\n")
			if err := os.Symlink(outside, filepath.Join(root, "link.md")); err != nil {
				t.Fatal(err)
			}
			if test.name == "settings file a link" {
				target := filepath.Join(filepath.Dir(root), "settings.json")
				writeBenchFile(t, target, test.settings)
				if err := os.Symlink(target, pfmconfig.PathIn(root)); err != nil {
					t.Fatal(err)
				}
			} else {
				writeBenchFile(t, pfmconfig.PathIn(root), test.settings)
			}
			home := t.TempDir()
			got, err := ForLaunch(filepath.Join(root, "src"), home, pfmengine.Claude, New)
			want := Persona{
				Prompt:         filepath.Join(root, "team.md"),
				Body:           "Team rules.\n",
				Envs:           map[string]string{"TEAM_MODE": "strict"},
				ClaudeSettings: map[string]any{"env": map[string]any{"X": "1"}},
			}
			switch test.name {
			case "null envs", "null claude settings":
				want = Persona{}
			case "valid":
			default:
				message := strings.NewReplacer("ROOT", root, "OUTSIDE", outside, "<home>", home).Replace(test.want)
				// A file the JSON decoder refuses is a parse fault; a value it decoded is a config fault.
				warning := "config " + pfmconfig.PathIn(root) + ": " + message
				if decoded, parse := strings.CutPrefix(message, "not valid: "); parse {
					warning = "parse config " + pfmconfig.PathIn(root) + ": " + decoded
				}
				want = Persona{Warnings: []string{warning + "; launching without that file"}}
			}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("ForLaunch = %#v, %v; want %#v", got, err, want)
			}
		})
	}
}

// TestSettingsTemplate: the template pfm init copies into a project as pfm.config.json sets exactly the
// keys the project scope reads, and a launch reads it as written, with no warning.
func TestSettingsTemplate(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	path := filepath.Join(filepath.Dir(source), "..", "..", "..", "templates", "project", pfmconfig.FileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var keys struct {
		Top    map[string]json.RawMessage
		Claude map[string]json.RawMessage `json:"claude"`
	}
	if err := json.Unmarshal(raw, &keys.Top); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if err := json.Unmarshal(keys.Top["claude"], &keys.Claude); err != nil {
		t.Fatalf("decode %s claude: %v", path, err)
	}
	if got, want := slices.Sorted(
		maps.Keys(keys.Top),
	), []string{
		"claude",
		"envs",
		"harnessPrompt",
	}; !slices.Equal(
		got,
		want,
	) {
		t.Errorf("%s top-level keys = %q, want %q", path, got, want)
	}
	if got, want := slices.Sorted(maps.Keys(keys.Claude)), []string{"settings"}; !slices.Equal(got, want) {
		t.Errorf("%s claude keys = %q, want %q", path, got, want)
	}
	var template struct {
		Claude struct {
			Settings map[string]any `json:"settings"`
		} `json:"claude"`
	}
	if err := json.Unmarshal(raw, &template); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	root, _ := benchFixture(t)
	writeBenchFile(t, pfmconfig.PathIn(root), string(raw))
	got, err := ForLaunch(filepath.Join(root, "src"), t.TempDir(), pfmengine.Claude, New)
	want := Persona{Envs: map[string]string{}, ClaudeSettings: template.Claude.Settings}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ForLaunch over %s = %#v, %v; want %#v", path, got, err, want)
	}
}

// TestSettingsScopes: the machine file's scope keys apply at every launch, the project's over them per
// key; a project key the project scope does not read, and a leftover pfm.settings.json, are warnings;
// a project file that is the machine file is read once, as the machine's.
func TestSettingsScopes(t *testing.T) {
	const machineFile = `{"version":2,"envs":{"TEAM_MODE":"machine","MACHINE_ONLY":"1"},` +
		`"harnessPrompt":{"replace":"machine.md"},` +
		`"claude":{"settings":{"outputStyle":"machine","env":{"A":"machine","B":"machine"}}}}`
	for _, test := range []struct{ name, project, leftover string }{
		{name: "machine alone outside a managed root"},
		{name: "machine alone in a root without a project file"},
		{
			name: "project over machine",
			project: `{"envs":{"TEAM_MODE":"project"},"harnessPrompt":{"replace":"team.md"},` +
				`"claude":{"settings":{"env":{"B":"project"}}}}`,
		},
		{
			name:    "machine-only and retired keys in the project",
			project: `{"envs":{"TEAM_MODE":"project"},"mcp":{},"claude":{"theme":"x","helperModel":"y"}}`,
		},
		{name: "leftover settings file", leftover: `{"envs":{"TEAM_MODE":"leftover"}}`},
		{name: "project file is the machine file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := benchFixture(t)
			cwd := filepath.Join(root, "src")
			machineDir := t.TempDir()
			machinePath := filepath.Join(machineDir, pfmconfig.FileName)
			writeBenchFile(t, filepath.Join(machineDir, "machine.md"), "Machine rules.\n")
			writeBenchFile(t, filepath.Join(root, "team.md"), "Team rules.\n")
			if test.project != "" {
				writeBenchFile(t, pfmconfig.PathIn(root), test.project)
			}
			if test.leftover != "" {
				writeBenchFile(t, filepath.Join(root, pfmconfig.RetiredProjectFile), test.leftover)
			}
			machine := Persona{
				Prompt: filepath.Join(machineDir, "machine.md"), Body: "Machine rules.\n",
				Envs: map[string]string{"TEAM_MODE": "machine", "MACHINE_ONLY": "1"},
				ClaudeSettings: map[string]any{
					"outputStyle": "machine", "env": map[string]any{"A": "machine", "B": "machine"},
				},
			}
			want := machine
			switch test.name {
			case "machine alone outside a managed root":
				cwd = t.TempDir()
			case "project over machine":
				want = Persona{
					Prompt: filepath.Join(root, "team.md"), Body: "Team rules.\n",
					Envs: map[string]string{"TEAM_MODE": "project", "MACHINE_ONLY": "1"},
					ClaudeSettings: map[string]any{
						"outputStyle": "machine", "env": map[string]any{"A": "machine", "B": "project"},
					},
				}
			case "machine-only and retired keys in the project":
				path := pfmconfig.PathIn(root)
				want.Envs = map[string]string{"TEAM_MODE": "project", "MACHINE_ONLY": "1"}
				want.Warnings = []string{
					"claude.helperModel is retired: set claude.settings.env.ANTHROPIC_SMALL_FAST_MODEL instead; ignored in " + path,
					"claude.theme is machine-only; ignored in " + path,
					"mcp is machine-only; ignored in " + path,
				}
			case "leftover settings file":
				want.Warnings = []string{
					root + ": pfm.settings.json is no longer read: move its keys into pfm.config.json",
				}
			case "project file is the machine file":
				machinePath = pfmconfig.PathIn(root)
				want.Prompt = filepath.Join(root, "machine.md")
				writeBenchFile(t, want.Prompt, "Machine rules.\n")
			}
			writeBenchFile(t, machinePath, machineFile)
			t.Setenv(paths.EnvConfig, machinePath)
			got, err := ForLaunch(cwd, t.TempDir(), pfmengine.Claude, New)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("ForLaunch = %#v, %v; want %#v", got, err, want)
			}
		})
	}
}

func TestPersonaPrecedence(t *testing.T) {
	persona := Persona{Effort: "xhigh", Model: "bench-model"}
	if persona.EffortOr("high") != "high" || persona.EffortOr("") != "xhigh" ||
		persona.ModelOr(
			"explicit-model",
		) != "explicit-model" || persona.ModelOr("") != "bench-model" || (Persona{}).ModelOr("") != "" {
		t.Fatal("explicit > persona > empty precedence failed")
	}
}
