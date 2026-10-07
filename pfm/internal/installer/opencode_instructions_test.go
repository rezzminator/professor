package installer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// OpenCode takes no prompt file on its command line and runs no appendix
// hook, so the composed prompt reaches it only by being named in the config's
// instructions array — and every other key, including an operator's own
// instruction entries, has to survive the write.
func TestInstallWiresOpenCodeInstructions(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clone := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, "pfm.config.json")
	writeFixture(t, configPath, "{\"version\":2}\n")
	config := OpenCodeConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, config, `{
  // operator comment
  "theme": "opencode",
  "instructions": ["./house-rules.md"]
}
`)
	if _, err := Run(context.Background(), Options{
		Mode:               ModeApply,
		Home:               home,
		SourceRepo:         clone,
		MCPConfigPath:      configPath,
		Runner:             &fakeRunner{},
		Stdout:             io.Discard,
		OpenCodeConfigPath: config,
	}); err != nil {
		t.Fatal(err)
	}
	composed := filepath.Join(clone, "pfm", "harness-prompts", "composed", "opencode.md")
	raw := readFixture(t, config)
	if !strings.Contains(raw, "// operator comment") {
		t.Fatalf("operator comment lost:\n%s", raw)
	}
	document := decodeOpenCodeFixture(t, raw)
	if document["theme"] != "opencode" {
		t.Fatalf("unrelated key lost:\n%s", raw)
	}
	entries, err := openCodeInstructionEntries(document, config)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0] != composed || entries[1] != "./house-rules.md" {
		t.Fatalf("instructions=%v, want the composed prompt ahead of the operator's entry", entries)
	}

	// A second apply is idempotent, and uninstall gives the operator's array back.
	if _, err := Run(context.Background(), Options{
		Mode:               ModeApply,
		Home:               home,
		SourceRepo:         clone,
		MCPConfigPath:      configPath,
		Runner:             &fakeRunner{},
		Stdout:             io.Discard,
		OpenCodeConfigPath: config,
	}); err != nil {
		t.Fatal(err)
	}
	again, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, readFixture(t, config)), config)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 2 || again[0] != composed {
		t.Fatalf("second apply changed instructions: %v", again)
	}
	if _, err := Run(context.Background(), Options{
		Mode:               ModeUninstall,
		Home:               home,
		SourceRepo:         clone,
		MCPConfigPath:      configPath,
		Runner:             &fakeRunner{},
		Stdout:             io.Discard,
		OpenCodeConfigPath: config,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, readFixture(t, config)), config)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0] != "./house-rules.md" {
		t.Fatalf("uninstall did not restore the operator's instructions: %v", after)
	}
}

func TestOpenCodeInstructionsWithoutMarkerSkips(t *testing.T) {
	home := t.TempDir()
	config := OpenCodeConfigPath(home)
	writeFixture(t, config, "{\"instructions\":[\"operator.md\"]}\n")
	var output strings.Builder
	installer := &engine{options: Options{Home: home, OpenCodeConfigPath: config, Stdout: &output}}
	if err := installer.wireOpenCodeInstructions(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "skip opencode instructions: no source repo recorded") {
		t.Fatalf("skip report = %q", output.String())
	}
	if raw := readFixture(t, config); raw != "{\"instructions\":[\"operator.md\"]}\n" {
		t.Fatalf("config changed: %q", raw)
	}
}

// A malformed instructions value is an error, never a silent overwrite of
// whatever the operator put there.
func TestOpenCodeInstructionsRejectsForeignShape(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{"instructions": "house-rules.md"}`, `{"instructions": [1]}`} {
		document, err := decodeJSONCObject([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := openCodeInstructionEntries(document, "fixture.jsonc"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func decodeOpenCodeFixture(t *testing.T, raw string) map[string]any {
	t.Helper()
	sanitized, err := sanitizeJSONC([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(sanitized, &document); err != nil {
		t.Fatalf("decode OpenCode fixture: %v\n%s", err, raw)
	}
	return document
}

func TestFirstInstallWiresOpenCodeInstructions(t *testing.T) {
	for _, alias := range []bool{false, true} {
		name := "clone"
		if alias {
			name = "alias"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			clone := t.TempDir()
			source := clone
			if alias {
				source = filepath.Join(t.TempDir(), "clone")
				if err := os.Symlink(clone, source); err != nil {
					t.Fatal(err)
				}
			}
			configPath := filepath.Join(home, "pfm.config.json")
			writeFixture(t, configPath, "{\"version\":2}\n")
			config := OpenCodeConfigPath(home)
			writeFixture(t, config, "{\n // operator comment\n \"instructions\": [\"operator.md\"]\n}\n")
			var output strings.Builder
			options := Options{
				Mode: ModeApply, Home: home, SourceRepo: source,
				MCPConfigPath: configPath, Runner: &fakeRunner{},
				Stdout: &output, OpenCodeConfigPath: config,
			}
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			physical, err := filepath.EvalSymlinks(clone)
			if err != nil {
				t.Fatal(err)
			}
			composed := filepath.Join(physical, "pfm", "harness-prompts", "composed", "opencode.md")
			raw := readFixture(t, config)
			entries, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, raw), config)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 || entries[0] != composed || entries[1] != "operator.md" {
				t.Fatalf("first install instructions=%v, want [%s operator.md]", entries, composed)
			}
			if !strings.Contains(raw, "// operator comment") {
				t.Fatalf("operator comment lost: %s", raw)
			}
			if marker := readFixture(t, paths.SourceRepoPath(home)); marker != physical+"\n" {
				t.Fatalf("marker=%q, want %q", marker, physical+"\n")
			}
			for _, fromMarker := range []bool{false, true} {
				if fromMarker {
					options.SourceRepo = ""
				}
				output.Reset()
				if _, err := Run(context.Background(), options); err != nil {
					t.Fatal(err)
				}
				if got := readFixture(t, config); got != raw {
					t.Fatalf("repeat install (from marker=%v) changed config: %s", fromMarker, got)
				}
				if !strings.Contains(output.String(), "ok      "+config+" OpenCode prompt wiring") {
					t.Fatalf("repeat install prompt report: %s", output.String())
				}
			}
		})
	}
}

func TestOpenCodeInstructionsRejectsUnusableClone(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "missing")
	config := OpenCodeConfigPath(home)
	original := "{\"instructions\":[\"operator.md\"]}\n"
	writeFixture(t, config, original)
	installer := &engine{options: Options{
		Home: home, SourceRepo: source, OpenCodeConfigPath: config, Stdout: io.Discard,
	}}
	err := installer.wireOpenCodeInstructions()
	if err == nil || !strings.HasPrefix(err.Error(), "resolve OpenCode prompt:") ||
		!strings.Contains(err.Error(), source) {
		t.Fatalf("unusable clone error=%v", err)
	}
	if raw := readFixture(t, config); raw != original {
		t.Fatalf("config changed: %q", raw)
	}
}

func TestOpenCodeInstructionsOtherClones(t *testing.T) {
	for _, mode := range []Mode{ModeApply, ModeUninstall} {
		name := "apply"
		if mode == ModeUninstall {
			name = "uninstall"
		}
		t.Run(name, func(t *testing.T) {
			home, clone := t.TempDir(), t.TempDir()
			config := OpenCodeConfigPath(home)
			composed := filepath.Join(clone, "pfm", "harness-prompts", "composed", "opencode.md")
			entries := []string{
				"/srv/old-clone/pfm/harness-prompts/composed/opencode.md",
				filepath.Join(home, ".local", "share", "pfm", "install", "harness-prompts", "opencode.md"),
				"./house-rules.md",
			}
			want := []string{composed, entries[0], "./house-rules.md"}
			if mode == ModeUninstall {
				entries = append(entries, composed)
				want = []string{entries[0], "./house-rules.md", composed}
			}
			raw, err := json.Marshal(map[string]any{"instructions": entries})
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, config, string(raw))
			if _, err := Run(context.Background(), Options{
				Mode: mode, Home: home, SourceRepo: clone, MCPConfigPath: testConfigPath(t),
				Runner: &fakeRunner{nameSyncIdle: true}, Stdout: io.Discard, CodexHomes: []string{},
				OpenCodeConfigPath: config,
			}); err != nil {
				t.Fatal(err)
			}
			got, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, readFixture(t, config)), config)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("instructions = %v, want %v", got, want)
			}
		})
	}
}

func TestOpenCodeInstructionsUninstallWithoutMarker(t *testing.T) {
	home := t.TempDir()
	config := OpenCodeConfigPath(home)
	writeFixture(
		t,
		config,
		`{"instructions":["/srv/old-clone/pfm/harness-prompts/composed/opencode.md","operator.md"]}`,
	)
	var output strings.Builder
	installer := &engine{options: Options{
		Mode: ModeUninstall, Home: home, OpenCodeConfigPath: config, Stdout: &output,
	}, apply: true}
	if err := installer.wireOpenCodeInstructions(); err != nil {
		t.Fatal(err)
	}
	got, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, readFixture(t, config)), config)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"/srv/old-clone/pfm/harness-prompts/composed/opencode.md", "operator.md"}) ||
		installer.report.Skipped != 0 {
		t.Fatalf("instructions = %v, report = %+v, output = %q", got, installer.report, output.String())
	}
}

func TestOpenCodeReceiptFailureRetryRecoversOwnershipAndKeepsForeignInstructions(t *testing.T) {
	home, clone := t.TempDir(), t.TempDir()
	config := OpenCodeConfigPath(home)
	writeFixture(t, config, `{"instructions":["operator.md"]}`)
	receipt := filepath.Join(managedRootForHome(home), "opencode-instructions.json")
	writer := &storeMutationWriter{match: "  change  rewrite " + config, mutate: func() {
		if err := os.MkdirAll(receipt, 0o700); err != nil {
			t.Fatal(err)
		}
	}}
	e := &engine{
		apply:   true,
		options: Options{Mode: ModeApply, Home: home, SourceRepo: clone, OpenCodeConfigPath: config, Stdout: writer},
	}
	if err := e.editOpenCodeInstructions(true); err == nil {
		t.Fatal("receipt publication failure was hidden")
	}
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	e.options.Stdout = io.Discard
	if err := e.editOpenCodeInstructions(true); err != nil {
		t.Fatal(err)
	}
	if err := e.editOpenCodeInstructions(false); err != nil {
		t.Fatal(err)
	}
	got, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, readFixture(t, config)), config)
	if err != nil || !reflect.DeepEqual(got, []string{"operator.md"}) {
		t.Fatalf("retry lost ownership or foreign instructions: %v %v", got, err)
	}
}

func TestOpenCodePreexistingComposedInstructionRemainsOperatorOwned(t *testing.T) {
	home, clone := t.TempDir(), t.TempDir()
	config := OpenCodeConfigPath(home)
	composed := filepath.Join(clone, "pfm", "harness-prompts", "composed", "opencode.md")
	raw, err := json.Marshal(map[string]any{"instructions": []string{"operator.md", composed}})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, config, string(raw))
	e := &engine{
		apply: true,
		options: Options{
			Mode:               ModeApply,
			Home:               home,
			SourceRepo:         clone,
			OpenCodeConfigPath: config,
			Stdout:             io.Discard,
		},
	}
	if err := e.editOpenCodeInstructions(true); err != nil {
		t.Fatal(err)
	}
	if err := e.editOpenCodeInstructions(false); err != nil {
		t.Fatal(err)
	}
	got, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, readFixture(t, config)), config)
	if err != nil || !reflect.DeepEqual(got, []string{composed, "operator.md"}) {
		t.Fatalf("preexisting operator instruction reclaimed: %v %v", got, err)
	}
}

func TestOpenCodePendingOwnershipSurvivesFullUninstall(t *testing.T) {
	home, clone := t.TempDir(), t.TempDir()
	config := OpenCodeConfigPath(home)
	e := &engine{
		apply:       true,
		managedRoot: managedRootForHome(home),
		options: Options{
			Mode:               ModeApply,
			Home:               home,
			OpenCodeConfigPath: config,
			SourceRepo:         clone,
			MCPPort:            8456,
			Stdout:             io.Discard,
		},
	}
	if err := e.writeMCPOpenCodeJSON([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(e.managedRoot, "opencode-instructions.json")
	e.options.Stdout = &storeMutationWriter{match: "  change  rewrite " + config, mutate: func() {
		if err := os.MkdirAll(receipt, 0o700); err != nil {
			t.Fatal(err)
		}
	}}
	if err := e.editOpenCodeInstructions(true); err == nil {
		t.Fatal("receipt failure not surfaced")
	}
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	_, err := Run(
		context.Background(),
		Options{
			Mode:               ModeUninstall,
			Home:               home,
			SourceRepo:         clone,
			OpenCodeConfigPath: config,
			MCPConfigPath:      testConfigPath(t),
			Runner:             &fakeRunner{},
			Stdout:             io.Discard,
		},
	)
	if err != nil {
		t.Fatalf("normal uninstall invalidated pending instruction recovery: %v", err)
	}
	doc := decodeOpenCodeFixture(t, readFixture(t, config))
	entries, err := openCodeInstructionEntries(doc, config)
	if err != nil || len(entries) != 0 {
		t.Fatalf("owned instruction survives uninstall: %v %v", entries, err)
	}
}

func TestOpenCodeOwnershipExcludesConcurrentPublisher(t *testing.T) {
	home, clone := t.TempDir(), t.TempDir()
	config := OpenCodeConfigPath(home)
	writeFixture(t, config, `{}`)
	alias := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	competing := &engine{
		apply: true,
		options: Options{
			Mode:               ModeApply,
			Home:               alias,
			OpenCodeConfigPath: config,
			SourceRepo:         t.TempDir(),
			Stdout:             io.Discard,
		},
	}
	var competingErr error
	writer := &storeMutationWriter{
		match:  "  change  rewrite " + config,
		mutate: func() { competingErr = competing.editOpenCodeInstructions(true) },
	}
	e := &engine{
		apply:   true,
		options: Options{Mode: ModeApply, Home: home, OpenCodeConfigPath: config, SourceRepo: clone, Stdout: writer},
	}
	err := e.editOpenCodeInstructions(true)
	if competingErr == nil || !strings.Contains(competingErr.Error(), "busy") {
		t.Fatalf("competing publisher crossed ownership boundary: %v; original=%v", competingErr, err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := e.editOpenCodeInstructions(false); err != nil {
		t.Fatal(err)
	}
	entries, err := openCodeInstructionEntries(decodeOpenCodeFixture(t, readFixture(t, config)), config)
	if err != nil || len(entries) != 0 {
		t.Fatalf("serialized publication lost ownership: %v %v", entries, err)
	}
}

func TestManagedConfigOwnershipRefusesSharedStoreAliases(t *testing.T) {
	shared, clone := t.TempDir(), t.TempDir()
	for _, apply := range []bool{false, true} {
		for _, publisher := range []string{"opencode", "rumdl"} {
			t.Run(fmt.Sprintf("%s/apply=%t", publisher, apply), func(t *testing.T) {
				home := t.TempDir()
				root := managedRootForHome(home)
				if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(shared, root); err != nil {
					t.Fatal(err)
				}
				config := OpenCodeConfigPath(home)
				e := &engine{
					apply:   apply,
					options: Options{Home: home, SourceRepo: clone, OpenCodeConfigPath: config, Stdout: io.Discard},
				}
				journalName, configBytes := "opencode-instructions.json.pending", `{"theme":"personal"}`
				if publisher == "rumdl" {
					e.options.Env = &paths.MapEnv{}
					config, configBytes = e.rumdlUserConfigPath(), rumdlUserConfig
					journalName = "rumdl-user-config.json.pending"
				}
				writeFixture(t, config, configBytes)
				journal := filepath.Join(shared, journalName)
				writeFixture(t, journal, "existing journal bytes")
				var err error
				if publisher == "opencode" {
					err = e.editOpenCodeInstructions(true)
				} else {
					err = e.publishRumdlUserConfig(false)
				}
				if err == nil || !strings.Contains(err.Error(), "shared managed ownership root") ||
					!strings.Contains(err.Error(), shared) {
					t.Fatalf("shared-store alias was not refused before ownership mutation: %v", err)
				}
				assertContent(t, config, configBytes)
				assertContent(t, journal, "existing journal bytes")
			})
		}
	}
}

func TestOpenCodePendingPublicationRefusesConfigDrift(t *testing.T) {
	for _, scenario := range []string{"operator-edit", "different-config", "different-missing-config"} {
		t.Run(scenario, func(t *testing.T) {
			home, clone := t.TempDir(), t.TempDir()
			config := OpenCodeConfigPath(home)
			writeFixture(t, config, `{"instructions":["operator.md"]}`)
			receipt := filepath.Join(managedRootForHome(home), "opencode-instructions.json")
			writer := &storeMutationWriter{match: "  change  rewrite " + config, mutate: func() {
				if err := os.MkdirAll(receipt, 0o700); err != nil {
					t.Fatal(err)
				}
			}}
			e := &engine{
				apply: true,
				options: Options{
					Mode:               ModeApply,
					Home:               home,
					SourceRepo:         clone,
					OpenCodeConfigPath: config,
					Stdout:             writer,
				},
			}
			if err := e.editOpenCodeInstructions(true); err == nil {
				t.Fatal("receipt failure hidden")
			}
			if err := os.Remove(receipt); err != nil {
				t.Fatal(err)
			}
			wanted := `{"instructions":["new operator.md"],"theme":"personal"}`
			path := config
			if scenario == "different-config" || scenario == "different-missing-config" {
				path = filepath.Join(t.TempDir(), "other.jsonc")
				e.options.OpenCodeConfigPath = path
			}
			if scenario != "different-missing-config" {
				writeFixture(t, path, wanted)
			}
			e.options.Stdout = io.Discard
			if err := e.editOpenCodeInstructions(false); err == nil {
				t.Fatal("diverged pending publication was silently discarded")
			}
			if scenario == "different-missing-config" {
				requireNoPath(t, path, "uninstall created changed config")
			} else {
				assertContent(t, path, wanted)
			}
			if _, err := os.Stat(receipt + ".pending"); err != nil {
				t.Fatalf("diverged intent lost: %v", err)
			}
		})
	}
}
