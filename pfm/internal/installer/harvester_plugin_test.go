package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestMigrateHarvesterConfigPreservesPluginState(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(map[bool]string{false: "preview", true: "apply"}[apply], func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(t.TempDir(), "pfm.config.json")
			writeFixture(
				t,
				config,
				`{"version":2,"theme":"personal","ask":{"codex":{"model":"gpt-custom","effort":"high"}},"mcp":{"servers":{"chat":{"enabled":true}}}}`,
			)
			legacy := filepath.Join(filepath.Dir(config), "harvester.config.json")
			original := `{"enabled":true,"external":{"enabled":true,"passphrase":"secret","token":"opaque","stateDir":"/fixture/gateway"},"future":{"custom":[1,2,3]}}`
			writeFixture(t, legacy, original)
			path := filepath.Join(t.TempDir(), "custom", "harvester.config.json")
			var output bytes.Buffer
			inst := &engine{options: Options{
				Home: home, MCPConfigPath: config,
				Env: &paths.MapEnv{Values: map[string]string{"HARVESTER_CONFIG": path}}, Stdout: &output,
			}, apply: apply}
			if err := inst.migrateHarvesterConfig(); err != nil {
				t.Fatal(err)
			}
			if !inst.options.MCPEnabled["harvester"] {
				t.Fatal("migration plan did not enable harvester registration")
			}
			if !apply {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("preview wrote plugin config: %v", err)
				}
				return
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var before, after map[string]json.RawMessage
			if err := json.Unmarshal([]byte(original), &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(got, &after); err != nil {
				t.Fatal(err)
			}
			delete(before, "enabled")
			for key, expected := range before {
				var a, b any
				if err := json.Unmarshal(expected, &a); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(after[key], &b); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("field %s changed: %s", key, after[key])
				}
			}
			var ask struct {
				Engine string `json:"engine"`
				Codex  struct {
					Model  string `json:"model"`
					Effort string `json:"effort"`
				} `json:"codex"`
			}
			if err := json.Unmarshal(
				after["ask"],
				&ask,
			); err != nil || ask.Codex.Model != "gpt-custom" ||
				ask.Codex.Effort != "high" {
				t.Fatalf("ask preferences lost: %s %v", after["ask"], err)
			}
			if _, found := after["enabled"]; found {
				t.Fatal("plugin config retained pfm enablement")
			}
			if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("plugin config permissions: %v %v", info, err)
			}
			if _, err := os.Stat(legacy); !os.IsNotExist(err) {
				t.Fatalf("legacy config survived migration: %v", err)
			}
			loaded, err := pfmconfig.Load(config, home, nil)
			if err != nil || !loaded.MCPServers["harvester"].Enabled || !loaded.MCPServers["chat"].Enabled ||
				loaded.Theme != "personal" {
				t.Fatalf("pfm config migration: %#v %v", loaded, err)
			}
			if err := inst.migrateHarvesterConfig(); err != nil {
				t.Fatalf("repeated migration: %v", err)
			}
		})
	}
}

func TestMigrateHarvesterConfigKeepsAnExistingPluginConfig(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(t.TempDir(), "pfm.config.json")
	legacy := filepath.Join(filepath.Dir(config), "harvester.config.json")
	path := harvesterConfigPath(&paths.MapEnv{}, home)
	writeFixture(t, legacy, `{"enabled":true}`)
	writeFixture(t, path, `{"external":{"enabled":true}}`)
	var output bytes.Buffer
	inst := &engine{
		options: Options{Home: home, MCPConfigPath: config, Env: &paths.MapEnv{}, Stdout: &output},
		apply:   true,
	}
	if err := inst.migrateHarvesterConfig(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != `{"external":{"enabled":true}}` {
		t.Fatalf("existing config changed: %s %v", got, err)
	}
	if !strings.Contains(output.String(), "already exists") {
		t.Fatalf("existing config was not reported: %s", output.String())
	}
}

func TestInstallHarvesterLinksBinaryAndPropagatesOffline(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	checkout := filepath.Join(root, "harvester", "plugins", "harvester")
	writeFixture(
		t,
		filepath.Join(checkout, ".claude-plugin", "plugin.json"),
		`{"name":"harvester","version":"0.1.0-dev.1"}`,
	)
	writeFixture(t, filepath.Join(checkout, "libexec", "harvester"), "#!/bin/sh\n")
	env := &paths.MapEnv{Values: map[string]string{"HARVESTER_HOME": filepath.Join(home, "plugin-home")}}
	var output bytes.Buffer
	runner := &goRunner{output: "standalone plugin"}
	inst := &engine{options: Options{
		Home:                     home,
		Env:                      env,
		Stdout:                   &output,
		ProcessRunner:            runner,
		Runner:                   &fakeRunner{manager: true, mcpState: "inactive"},
		Version:                  "1.0.0-alpha",
		ClaudePluginCheckoutRoot: root,
		ClaudePlugins:            bareList("harvester"),
		Offline:                  true,
		MCPEnabled:               map[string]bool{"harvester": true},
	}, apply: true}
	if err := inst.ensureGoPluginBinary(
		context.Background(),
		&ClaudePluginTarget{Name: "harvester", Checkout: checkout},
	); err != nil {
		t.Fatal(err)
	}
	if err := inst.installHarvester(context.Background()); err != nil {
		t.Fatal(err)
	}
	link := mcpFamilyBinary(home, "harvester")
	if got, err := os.ReadFile(link); err != nil || string(got) != "standalone plugin" {
		t.Fatalf("PATH binary=%s err=%v", got, err)
	}
	if len(runner.calls) != 1 || runner.calls[0].argv != "setup --offline" {
		t.Fatalf("plugin calls=%#v", runner.calls)
	}
	if got := runner.argvs[0][len(runner.argvs[0])-1]; got != "./cmd/harvester" {
		t.Fatalf("build target=%s", got)
	}
}

func TestGoPluginBinaryGap(t *testing.T) {
	for _, name := range []string{"callmeter", "harvester"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			checkout := filepath.Join(t.TempDir(), "plugins", name)
			writeFixture(t, filepath.Join(checkout, ".claude-plugin", "plugin.json"), `{"version":"0.1.0-dev.1"}`)
			target := ClaudePluginTarget{Name: name, ID: name + "@" + name + "-dev", Checkout: checkout, Local: true}
			env := &paths.MapEnv{}
			binary, err := goBinaryPlugins[name].binaryPath(env, home, "0.1.0-dev.1")
			if err != nil {
				t.Fatal(err)
			}
			gap, found := GoPluginBinaryGap(env, home, target)
			if !found || !strings.Contains(gap.Problem, binary+" missing") {
				t.Fatalf("gap=%+v found=%t", gap, found)
			}
			if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
				t.Fatal(err)
			}
			writeScript(t, filepath.Dir(binary), name, "#!/bin/sh\nexit 0\n")
			if gap, found := GoPluginBinaryGap(env, home, target); found {
				t.Fatalf("executable binary gap=%+v", gap)
			}
		})
	}
}

func TestInstallHarvesterBootstrapsStablePlugin(t *testing.T) {
	home := t.TempDir()
	fixture := &harvesterReleaseFixture{binary: []byte("standalone release")}
	runner := &pluginRunner{}
	var output bytes.Buffer
	inst := &engine{options: Options{
		Home: home, Env: &paths.MapEnv{}, Stdout: &output,
		ProcessRunner: runner, Runner: &fakeRunner{manager: true, mcpState: "inactive"},
		Version: "1.0.0", MCPEnabled: map[string]bool{"harvester": true},
		GoPluginHTTPClient: &http.Client{Transport: fixture},
	}, apply: true}
	if err := inst.installHarvester(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || runner.calls[0].argv != "setup" || len(fixture.calls) != 3 {
		t.Fatalf("HTTP=%v plugin calls=%#v", fixture.calls, runner.calls)
	}
	if !callmeterExecutable(filepath.Join(home, ".local", "bin", "harvester")) {
		t.Fatal("stable binary is not executable through PATH link")
	}
	inst.options.Offline = true
	fixture.fail = "metadata"
	if err := inst.installHarvester(context.Background()); err != nil {
		t.Fatalf("verified offline reuse: %v", err)
	}
	if len(fixture.calls) != 3 || len(runner.calls) != 2 || runner.calls[1].argv != "setup --offline" {
		t.Fatalf(
			"offline reuse fetched release or lost setup flag: HTTP=%v plugin calls=%#v",
			fixture.calls,
			runner.calls,
		)
	}
}

func TestHarvesterAskMigrationKeepsExplicitPluginPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pfm.config.json")
	writeFixture(
		t,
		path,
		`{"ask":{"engine":"claude","codex":{"model":"gpt-legacy","effort":"low"}},"claude":{"binary":"custom-claude","theme":"ignored"},"codex":{"binary":"old-codex"}}`,
	)
	document := map[string]json.RawMessage{
		"ask":   json.RawMessage(`{"engine":"codex","codex":{"model":"gpt-custom"}}`),
		"codex": json.RawMessage(`{"binary":"custom-codex"}`),
	}
	if err := migrateHarvesterPreferences(document, path); err != nil {
		t.Fatal(err)
	}
	var got any
	if err := json.Unmarshal(document["ask"], &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"engine": "codex", "codex": map[string]any{"model": "gpt-custom", "effort": "low"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ask=%v want=%v", got, want)
	}
	for name, wantBinary := range map[string]string{"claude": "custom-claude", "codex": "custom-codex"} {
		var prefs map[string]string
		if err := json.Unmarshal(
			document[name],
			&prefs,
		); err != nil || prefs["binary"] != wantBinary ||
			len(prefs) != 1 {
			t.Fatalf("%s preferences=%s err=%v", name, document[name], err)
		}
	}
}

func TestHarvesterConfigPathMatchesPluginXDGContract(t *testing.T) {
	env := &paths.MapEnv{Values: map[string]string{"XDG_CONFIG_HOME": "relative-config"}}
	if got := harvesterConfigPath(
		env,
		"/fixture/home",
	); got != filepath.Join(
		"relative-config",
		"harvester",
		"harvester.config.json",
	) {
		t.Fatalf("config path=%s", got)
	}
}

func TestInstallHarvesterFailedBootstrapKeepsLegacyConfig(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(t.TempDir(), "pfm.config.json")
	writeFixture(t, config, `{"version":2}`)
	legacy := filepath.Join(filepath.Dir(config), "harvester.config.json")
	original := `{"enabled":true,"external":{"enabled":true,"passphrase":"secret"}}`
	writeFixture(t, legacy, original)
	fixture := &harvesterReleaseFixture{binary: []byte("release"), fail: "binary-download"}
	var output bytes.Buffer
	inst := &engine{options: Options{
		Home: home, Env: &paths.MapEnv{}, Stdout: &output,
		ProcessRunner: &pluginRunner{}, Version: "1.0.0", MCPConfigPath: config,
		GoPluginHTTPClient: &http.Client{Transport: fixture},
	}, apply: true}
	if err := inst.installHarvester(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "fixture binary download failed") {
		t.Fatalf("bootstrap error=%v", err)
	}
	if got, err := os.ReadFile(legacy); err != nil || string(got) != original {
		t.Fatalf("legacy config changed before binary was ready: %s %v", got, err)
	}
	if _, err := os.Stat(harvesterConfigPath(&paths.MapEnv{}, home)); !os.IsNotExist(err) {
		t.Fatalf("plugin config written before binary was ready: %v", err)
	}
}

func TestInstallHarvesterMigratesDisabledLegacyWithExplicitPluginList(t *testing.T) {
	for _, failure := range []string{"", "build", "setup"} {
		t.Run("failure="+failure, func(t *testing.T) {
			home := t.TempDir()
			clone := writeMarketplaceClone(t, []string{"harvester"}, []string{"harvester"})
			checkout := filepath.Join(clone, "plugins", "harvester", "plugins", "harvester")
			writeFixture(
				t,
				filepath.Join(checkout, ".claude-plugin", "plugin.json"),
				`{"name":"harvester","version":"0.1.0-dev.1"}`,
			)
			config := filepath.Join(t.TempDir(), "pfm.config.json")
			writeFixture(t, config, `{"version":2,"claude":{"plugins":["buddy"]}}`)
			legacy := filepath.Join(filepath.Dir(config), "harvester.config.json")
			original := `{"enabled":false,"external":{"enabled":true,"passphrase":"secret"}}`
			writeFixture(t, legacy, original)
			runner := &goRunner{output: "standalone plugin"}
			switch failure {
			case "build":
				runner.failure = "build failed"
			case "setup":
				runner.fail = map[string]string{" setup --offline": "setup failed"}
			}
			var output bytes.Buffer
			inst := &engine{options: Options{
				Home:          home,
				SourceRepo:    clone,
				Env:           &paths.MapEnv{},
				Stdout:        &output,
				ProcessRunner: runner,
				Runner:        &fakeRunner{manager: true, mcpState: "inactive"},
				Version:       "1.0.0-alpha",
				MCPConfigPath: config,
				ClaudePlugins: bareList("buddy"),
				Offline:       true,
			}, apply: true}
			err := inst.installHarvester(context.Background())
			if failure != "" && (err == nil || !strings.Contains(err.Error(), failure+" failed")) {
				t.Fatalf("failure=%s error=%v", failure, err)
			}
			if failure == "build" {
				if got, err := os.ReadFile(legacy); err != nil || string(got) != original {
					t.Fatalf("legacy config changed after failed build: %s %v", got, err)
				}
				return
			}
			if failure == "" && err != nil {
				t.Fatal(err)
			}
			loaded, err := pfmconfig.Load(config, home, nil)
			if err != nil || loaded.MCPServers["harvester"].Enabled ||
				loaded.MCPServerSource("harvester") != pfmconfig.SourceFile {
				t.Fatalf("disabled flag not lifted explicitly: %#v %v", loaded.MCPServers, err)
			}
			if len(loaded.Claude.Plugins) != 1 || loaded.Claude.Plugins[0].ID != "buddy" {
				t.Fatalf("explicit plugin list changed: %+v", loaded.Claude.Plugins)
			}
			migrated := readFixture(t, harvesterConfigPath(&paths.MapEnv{}, home))
			if !strings.Contains(migrated, `"passphrase": "secret"`) || strings.Contains(migrated, `"enabled": false`) {
				t.Fatalf("plugin config changed: %s", migrated)
			}
			if len(runner.calls) != 1 || runner.calls[0].argv != "setup --offline" || len(runner.builds) != 1 {
				t.Fatalf("builds=%d plugin calls=%#v", len(runner.builds), runner.calls)
			}
		})
	}
}

func TestHarvesterHomeRejectsRelativeOverride(t *testing.T) {
	env := &paths.MapEnv{Values: map[string]string{"HARVESTER_HOME": "relative-home"}}
	if _, err := harvesterBinaryPath(
		env,
		t.TempDir(),
		"0.1.0",
	); err == nil ||
		!strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("binary path error=%v", err)
	}
	var output bytes.Buffer
	inst := &engine{
		options: Options{
			Home:          t.TempDir(),
			Env:           env,
			Stdout:        &output,
			Version:       "1.0.0",
			ClaudePlugins: bareList("harvester"),
		},
		apply: true,
	}
	if err := inst.installHarvester(
		context.Background(),
	); err == nil ||
		!strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("stable plugin error=%v", err)
	}
}

func TestMigrateHarvesterConfigInheritsAbsentEnablement(t *testing.T) {
	for _, test := range []struct {
		name   string
		legacy string
		want   bool
	}{
		{name: "absent inherits pfm", legacy: `{"external":{"enabled":true}}`, want: true},
		{name: "explicit false overrides pfm", legacy: `{"enabled":false,"external":{"enabled":true}}`},
	} {
		for _, apply := range []bool{false, true} {
			t.Run(test.name+"/"+map[bool]string{false: "preview", true: "apply"}[apply], func(t *testing.T) {
				home := t.TempDir()
				config := filepath.Join(t.TempDir(), "pfm.config.json")
				writeFixture(t, config, `{"version":2,"mcp":{"servers":{"harvester":{"enabled":true}}}}`)
				legacy := filepath.Join(filepath.Dir(config), "harvester.config.json")
				writeFixture(t, legacy, test.legacy)
				var output bytes.Buffer
				inst := &engine{
					options: Options{Home: home, MCPConfigPath: config, Env: &paths.MapEnv{}, Stdout: &output},
					apply:   apply,
				}
				if err := inst.migrateHarvesterConfig(); err != nil {
					t.Fatal(err)
				}
				if got := inst.options.MCPEnabled["harvester"]; got != test.want {
					t.Fatalf("migration enablement=%t want=%t", got, test.want)
				}
				if apply {
					loaded, err := pfmconfig.Load(config, home, nil)
					if err != nil || loaded.MCPServers["harvester"].Enabled != test.want {
						t.Fatalf("persisted enablement=%+v error=%v want=%t", loaded.MCPServers, err, test.want)
					}
				}
			})
		}
	}
}
