package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// writeScript writes an executable shell fixture named name inside dir,
// mirroring internal/deps's own writeExecutable pattern but with caller-
// supplied content so a fixture can echo a version, fail loudly, or record
// that it ran.
func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := testjail.WriteExecutable(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fixture script %s: %v", name, err)
	}
	return path
}

// assertRunArgv fails unless runner ran exactly want, one joined argv per call,
// in order — the proof a branch never ran a tool it must not touch.
func assertRunArgv(t *testing.T, runner *deps.FakeRunner, want ...string) {
	t.Helper()
	var got []string
	for _, call := range runner.Calls() {
		got = append(got, strings.Join(call.Argv, " "))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("runner calls=%q, want %q", got, want)
	}
}

// TestInstallMarkdownToolProvisioningBranches pins every provisioning branch
// of installMarkdownTool through the installer's process seam: a rumdl at or
// above the pin is present and uv is never touched; one patch below falls
// through to the provisioning decision; a dry run reads the version and plans;
// offline and no-uv skip before any exec; a failing uv is a skip carrying its
// output; a succeeding uv installs into HOME/.local/bin. Every branch also
// writes (or plans) the rumdl user config, so Changed is 1 throughout. The
// runner's ledger proves which tools ran.
func TestInstallMarkdownToolProvisioningBranches(t *testing.T) {
	t.Parallel()
	version := func(installed string) *deps.RunResult {
		return &deps.RunResult{Stdout: []byte("rumdl " + installed + "\n")}
	}
	for _, test := range []struct {
		name         string
		rumdl        *deps.RunResult // nil: no rumdl on PATH
		uv           bool
		uvInstall    *deps.RunResult // nil: the install is never scripted
		offline      bool
		dryRun       bool
		wantOutput   []string
		wantOK       int
		wantSkipped  int
		wantCalls    []string
		wantUVBinDir bool
	}{
		{
			name: "present exactly at the pin", rumdl: version("0.2.73"), uv: true,
			wantOutput: []string{"rumdl already present (0.2.73)"}, wantOK: 1,
			wantCalls: []string{"/fixture/rumdl --version"},
		},
		{
			name: "present newer than the pin", rumdl: version("0.3.0"),
			wantOutput: []string{"rumdl already present (0.3.0)"}, wantOK: 1,
			wantCalls: []string{"/fixture/rumdl --version"},
		},
		{
			name: "one patch below the pin falls through", rumdl: version("0.2.72"), offline: true,
			wantOutput:  []string{"rumdl: offline, will not attempt uv tool install rumdl==0.2.73"},
			wantSkipped: 1, wantCalls: []string{"/fixture/rumdl --version"},
		},
		{
			name:   "dry run plans only",
			rumdl:  &deps.RunResult{Stderr: []byte("poisoned-rumdl invoked\n"), ExitCode: 1},
			uv:     true,
			dryRun: true,
			wantOutput: []string{
				"rumdl dry-run: would install rumdl==0.2.73 via uv tool install (uv=",
				"  change  write rumdl user config -> {config}\n",
			},
			wantCalls: []string{"/fixture/rumdl --version"},
		},
		{
			name: "offline never touches uv", uv: true, offline: true,
			wantOutput:  []string{"rumdl: offline, will not attempt uv tool install rumdl==0.2.73"},
			wantSkipped: 1,
		},
		{
			name:        "no uv available skips without failing",
			wantOutput:  []string{"rumdl: no uv available on PATH"},
			wantSkipped: 1,
		},
		{
			name: "uv exit failure skips with its output", uv: true,
			uvInstall: &deps.RunResult{Stderr: []byte("boom: dependency resolution failed\n"), ExitCode: 1},
			wantOutput: []string{
				"rumdl: uv tool install rumdl==0.2.73 failed:",
				"boom: dependency resolution failed",
			},
			wantSkipped: 1, wantCalls: []string{"/fixture/uv tool install rumdl==0.2.73"},
		},
		{
			name: "uv success installs into HOME/.local/bin", uv: true, uvInstall: &deps.RunResult{},
			wantOutput: []string{"rumdl installed via uv tool install rumdl==0.2.73"},
			wantOK:     1, wantCalls: []string{"/fixture/uv tool install rumdl==0.2.73"}, wantUVBinDir: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &deps.FakeRunner{}
			if test.rumdl != nil {
				runner.ScriptLookPath("rumdl", "/fixture/rumdl", nil)
				runner.Script([]string{"/fixture/rumdl", "--version"}, *test.rumdl, nil)
			}
			if test.uv {
				runner.ScriptLookPath("uv", "/fixture/uv", nil)
			}
			if test.uvInstall != nil {
				runner.Script([]string{"/fixture/uv", "tool", "install", "rumdl==0.2.73"}, *test.uvInstall, nil)
			}
			home := t.TempDir()
			config := filepath.Join(home, ".config", "rumdl", "rumdl.toml")
			var output bytes.Buffer
			eng := &engine{
				options: Options{
					Home: home, Env: &paths.MapEnv{}, Stdout: &output, Offline: test.offline, ProcessRunner: runner,
				},
				apply: !test.dryRun,
			}
			if err := eng.installMarkdownTool(context.Background()); err != nil {
				t.Fatalf("installMarkdownTool must never fail the install: %v\n%s", err, output.String())
			}
			for _, want := range test.wantOutput {
				want = strings.ReplaceAll(want, "{config}", config)
				if !strings.Contains(output.String(), want) {
					t.Fatalf("output=%q, want to contain %q", output.String(), want)
				}
			}
			if eng.report.OK != test.wantOK || eng.report.Skipped != test.wantSkipped || eng.report.Changed != 1 {
				t.Fatalf("report=%+v, want OK=%d Skipped=%d Changed=1", eng.report, test.wantOK, test.wantSkipped)
			}
			assertRunArgv(t, runner, test.wantCalls...)
			if test.dryRun {
				if _, err := os.Lstat(config); !os.IsNotExist(err) {
					t.Fatalf("dry run wrote %s: %v", config, err)
				}
			}
			if test.wantUVBinDir {
				wantBinDir := filepath.Join(home, ".local", "bin")
				gotBinDir := ""
				for _, entry := range runner.Calls()[0].Opts.Env {
					if value, ok := strings.CutPrefix(entry, "UV_TOOL_BIN_DIR="); ok {
						gotBinDir = value
					}
				}
				if gotBinDir != wantBinDir {
					t.Fatalf("UV_TOOL_BIN_DIR=%q, want %q", gotBinDir, wantBinDir)
				}
			}
		})
	}
}

func TestRemoveRumdlUserConfig(t *testing.T) {
	for _, tc := range []struct {
		name, content      string
		present, directory bool
	}{
		{name: "pfm", content: wantRumdlUserConfig, present: true},
		{name: "vanished", content: wantRumdlUserConfig, present: true},
		{name: "operator-identical", content: wantRumdlUserConfig, present: true},
		{name: "operator", content: "[global]\nline-length = 120\n", present: true},
		{name: "absent"},
		{name: "unreadable", directory: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			installer, output := presentRumdlEngine(t, home, &paths.MapEnv{})
			config := filepath.Join(home, ".config", "rumdl", "rumdl.toml")
			if tc.present {
				writeFixture(t, config, tc.content)
			} else if tc.directory {
				if err := os.MkdirAll(config, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "pfm" || tc.name == "vanished" {
				writeFixture(t, filepath.Join(managedRootForHome(home), "rumdl-user-config.json"), "\""+config+"\"\n")
			}
			if tc.name == "vanished" {
				// Another uninstall removes it between the read and the remove.
				installer.options.Stdout = &storeMutationWriter{
					match: "  change  remove rumdl user config",
					mutate: func() {
						if err := os.Remove(config); err != nil {
							t.Fatal(err)
						}
					},
				}
			}
			err := installer.removeRumdlUserConfig()
			if tc.name == "vanished" {
				if err != nil {
					t.Fatalf("a config removed meanwhile failed the removal: %v", err)
				}
				return
			}
			if tc.directory {
				if err == nil || !strings.HasPrefix(err.Error(), "read rumdl user config "+config+": ") ||
					!strings.Contains(err.Error(), "is a directory") {
					t.Fatalf("read error = %v, want named directory error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			changed, skipped := 0, 0
			if tc.name == "pfm" {
				want = "  change  remove rumdl user config " + config + "\n"
				changed = 1
				if _, err := os.Stat(config); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("pfm config removal: %v", err)
				}
				if _, err := os.Stat(filepath.Dir(config)); err != nil {
					t.Fatalf("rumdl directory removed: %v", err)
				}
			} else if tc.present {
				want = "  skip    rumdl user config " + config + " is not pfm's; kept\n"
				skipped = 1
				if got := readFixture(t, config); got != tc.content {
					t.Fatalf("operator config = %q, want %q", got, tc.content)
				}
			}
			if output.String() != want || installer.report.Changed != changed || installer.report.Skipped != skipped {
				t.Fatalf(
					"report = %+v, output = %q; want changed = %d, skipped = %d, output = %q",
					installer.report,
					output.String(),
					changed,
					skipped,
					want,
				)
			}
		})
	}
}

// TestTruncateOutputBoundsLengthWithoutMangingShortOutput pins
// truncateOutput's two branches: short output passes through trimmed but
// otherwise intact, and long output is bounded with a visible marker rather
// than silently cut.
func TestTruncateOutputBoundsLengthWithoutMangingShortOutput(t *testing.T) {
	t.Parallel()
	if got := truncateOutput([]byte("  boom  \n"), 4096); got != "boom" {
		t.Fatalf("truncateOutput(short) = %q, want %q", got, "boom")
	}
	long := strings.Repeat("x", 100)
	got := truncateOutput([]byte(long), 10)
	if got != long[:10]+"...(truncated)" {
		t.Fatalf("truncateOutput(long) = %q, want a 10-byte prefix plus the truncation marker", got)
	}
}

// wantRumdlUserConfig is the exact file pfm install writes when the host has
// no rumdl user config.
const wantRumdlUserConfig = `# Written by pfm install: rumdl runs that find no project .rumdl.toml cache nothing,
# so no stray .rumdl_cache appears in the working directory.
[global]
cache = false
`

func requireRumdlUserConfig(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("rumdl user config %s not written: %v", path, err)
	}
	if string(got) != wantRumdlUserConfig {
		t.Fatalf("rumdl user config %s = %q, want %q", path, got, wantRumdlUserConfig)
	}
}

// presentRumdlEngine is an apply engine whose rumdl is already present at the
// pin, with env as its environment.
func presentRumdlEngine(t *testing.T, home string, env paths.Env) (*engine, *bytes.Buffer) {
	t.Helper()
	runner := &deps.FakeRunner{}
	runner.ScriptLookPath("rumdl", "/fixture/rumdl", nil)
	runner.Script([]string{"/fixture/rumdl", "--version"}, deps.RunResult{Stdout: []byte("rumdl 0.2.73\n")}, nil)
	var output bytes.Buffer
	installer := &engine{options: Options{Home: home, Env: env, Stdout: &output, ProcessRunner: runner}, apply: true}
	return installer, &output
}

// TestInstallMarkdownToolRumdlUserConfig pins the stray-cache fix's user
// config: with none, install writes one whose [global] cache = false makes
// every config-less rumdl run cache nothing; the user's own config is the
// truth, never rewritten or merged; an absolute XDG_CONFIG_HOME wins over
// HOME/.config as in rumdl's own lookup; a config root that cannot hold the
// file is a named skip carrying the path, never absence and never a failed
// install.
func TestInstallMarkdownToolRumdlUserConfig(t *testing.T) {
	t.Parallel()
	own := []byte("[global]\nline-length = 120\n")
	for _, test := range []struct {
		name string
		// setup prepares home and returns the environment and the config path
		// the row expects.
		setup       func(t *testing.T, home string) (*paths.MapEnv, string)
		wantOutput  []string
		wantChanged int
		wantSkipped int
		check       func(t *testing.T, home, config string)
	}{
		{
			name: "absent is written",
			setup: func(_ *testing.T, home string) (*paths.MapEnv, string) {
				return &paths.MapEnv{}, filepath.Join(home, ".config", "rumdl", "rumdl.toml")
			},
			wantOutput:  []string{"write rumdl user config -> {config}"},
			wantChanged: 1,
			check:       func(t *testing.T, _, config string) { requireRumdlUserConfig(t, config) },
		},
		{
			name: "present is left untouched",
			setup: func(t *testing.T, home string) (*paths.MapEnv, string) {
				config := filepath.Join(home, ".config", "rumdl", "rumdl.toml")
				if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config, own, 0o600); err != nil {
					t.Fatal(err)
				}
				return &paths.MapEnv{}, config
			},
			wantOutput:  []string{"skip    rumdl user config present, left untouched: {config}"},
			wantSkipped: 1,
			check: func(t *testing.T, _, config string) {
				got, err := os.ReadFile(config)
				if err != nil || !bytes.Equal(got, own) {
					t.Fatalf("user config = %q, %v; want untouched %q", got, err, own)
				}
				if info, err := os.Stat(config); err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("user config mode changed: %v %v", info, err)
				}
			},
		},
		{
			name: "XDG_CONFIG_HOME wins over HOME/.config",
			setup: func(t *testing.T, _ string) (*paths.MapEnv, string) {
				xdg := t.TempDir()
				return &paths.MapEnv{Values: map[string]string{"XDG_CONFIG_HOME": xdg}},
					filepath.Join(xdg, "rumdl", "rumdl.toml")
			},
			wantOutput:  []string{"write rumdl user config -> {config}"},
			wantChanged: 1,
			check: func(t *testing.T, home, config string) {
				requireRumdlUserConfig(t, config)
				if _, err := os.Lstat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
					t.Fatalf("HOME/.config touched despite XDG_CONFIG_HOME: %v", err)
				}
			},
		},
		{
			name: "unwritable config root is a named skip",
			setup: func(t *testing.T, home string) (*paths.MapEnv, string) {
				blocker := filepath.Join(home, "not-a-dir")
				if err := os.WriteFile(blocker, []byte("file"), 0o644); err != nil {
					t.Fatal(err)
				}
				return &paths.MapEnv{Values: map[string]string{"XDG_CONFIG_HOME": blocker}},
					filepath.Join(blocker, "rumdl")
			},
			wantOutput:  []string{"skip    rumdl user config NOT written: ", "{config}"},
			wantSkipped: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			env, config := test.setup(t, home)
			installer, output := presentRumdlEngine(t, home, env)
			if err := installer.installMarkdownTool(context.Background()); err != nil {
				t.Fatalf("installMarkdownTool must never fail the install: %v\n%s", err, output)
			}
			for _, want := range test.wantOutput {
				want = strings.ReplaceAll(want, "{config}", config)
				if !strings.Contains(output.String(), want) {
					t.Fatalf("output=%q, want to contain %q", output, want)
				}
			}
			if installer.report.Changed != test.wantChanged || installer.report.Skipped != test.wantSkipped {
				t.Fatalf("report=%+v, want Changed=%d Skipped=%d", installer.report, test.wantChanged, test.wantSkipped)
			}
			if test.check != nil {
				test.check(t, home, config)
			}
		})
	}
}

func TestRumdlOwnershipFailureRetryPreservesOperatorAndRecovers(t *testing.T) {
	home := t.TempDir()
	e, _ := presentRumdlEngine(t, home, &paths.MapEnv{})
	receipt := filepath.Join(managedRootForHome(home), "rumdl-user-config.json")
	if err := os.MkdirAll(receipt, 0o700); err != nil {
		t.Fatal(err)
	}
	e.ensureRumdlUserConfig()
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	e.ensureRumdlUserConfig()
	if err := e.removeRumdlUserConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(e.rumdlUserConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("receipt failure left unowned config after retry: %v", err)
	}
}

func TestRumdlPendingPublicationRefusesChangedConfigAndStage(t *testing.T) {
	for _, scenario := range []string{"operator-replacement", "stage-changed", "config-directory-changed"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			env := &paths.MapEnv{}
			e, output := presentRumdlEngine(t, home, env)
			receipt := filepath.Join(managedRootForHome(home), "rumdl-user-config.json")
			if err := os.MkdirAll(receipt, 0o700); err != nil {
				t.Fatal(err)
			}
			e.ensureRumdlUserConfig()
			config := e.rumdlUserConfigPath()
			var intent rumdlConfigIntent
			pendingRaw, err := os.ReadFile(receipt + ".pending")
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(pendingRaw, &intent); err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(intent.Stage) != filepath.Dir(config) {
				t.Fatalf("stage must share config filesystem: %s", intent.Stage)
			}
			if err := os.Remove(receipt); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "operator-replacement":
				if err := os.Remove(config); err != nil {
					t.Fatal(err)
				}
				writeFixture(t, config, rumdlUserConfig)
			case "stage-changed":
				writeFixture(t, intent.Stage, "operator stage bytes")
			case "config-directory-changed":
				env.Values = map[string]string{"XDG_CONFIG_HOME": t.TempDir()}
			}
			output.Reset()
			e.ensureRumdlUserConfig()
			if !strings.Contains(output.String(), "NOT written") {
				t.Fatalf("pending publication drift was silently claimed: %s", output.String())
			}
			if _, err := os.Stat(receipt + ".pending"); err != nil {
				t.Fatalf("pending recovery record lost: %v", err)
			}
			if scenario == "stage-changed" {
				assertContent(t, config, "operator stage bytes")
			} else {
				assertContent(t, config, rumdlUserConfig)
			}
		})
	}
}
