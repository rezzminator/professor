package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const fakeHerdrBinary = "/opt/herdr/bin/herdr"

// fakeHerdr is herdr behind the installer's ProcessRunner seam: an
// `integration install claude` run adds Herdr's SessionStart entry for its
// CLAUDE_CONFIG_DIR to that dir's settings.json, writing through a symlinked
// file the way Herdr 0.9.5 does, and is idempotent per dir.
type fakeHerdr struct {
	*deps.FakeRunner
	t *testing.T
}

func (runner fakeHerdr) Run(ctx context.Context, argv []string, opts deps.RunOptions) (deps.RunResult, error) {
	result, err := runner.FakeRunner.Run(ctx, argv, opts)
	if err != nil || result.ExitCode != 0 || !slices.Equal(argv, herdr.ClaudeIntegrationArgv(fakeHerdrBinary)) {
		return result, err
	}
	dir := runEnv(opts, herdr.ClaudeConfigDirEnv)
	testjail.StageHerdrClaudeHook(runner.t, dir)
	return result, nil
}

// noHerdrRunner is a process runner on a host without herdr, so a whole install
// under test never reaches a herdr the test host carries.
type noHerdrRunner struct{ deps.Runner }

func (runner noHerdrRunner) LookPath(name string) (string, error) {
	if name == herdr.Binary {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	return runner.Runner.LookPath(name)
}

func runEnv(opts deps.RunOptions, key string) string {
	value := ""
	for _, entry := range opts.Env {
		if found, ok := strings.CutPrefix(entry, key+"="); ok {
			value = found
		}
	}
	return value
}

// shieldGuardedHook is Herdr's hook for dir as a pfm that ran herdr-shield left it.
func shieldGuardedHook(dir string) string {
	return `[ -n "${HERDR_SHIELD_INNER:-}" ] || ` + testjail.HerdrClaudeHook(dir)
}

// shieldGuardedSettings is testjail.HerdrClaudeSettings with dir's hook guarded.
func shieldGuardedSettings(dir string) string {
	return strings.Replace(testjail.HerdrClaudeSettings(dir),
		fmt.Sprintf("%q", testjail.HerdrClaudeHook(dir)), fmt.Sprintf("%q", shieldGuardedHook(dir)), 1)
}

// TestInstallHerdrClaudeIntegration is the step's table over two Claude config
// dirs whose settings.json are symlinks to one shared store: each pass is one
// install, asserting its rows, the herdr runs and their CLAUDE_CONFIG_DIR, the
// release seam's calls, the store's Herdr hooks after it and that both links
// still point at the store. herdr-shield's guarded hook (RetireHerdrShieldHook)
// unwraps only once Herdr's default_shell no longer names herdr-shield.
func TestInstallHerdrClaudeIntegration(t *testing.T) {
	type pass struct {
		rows      []string
		runDirs   []string
		releases  int
		wantErr   bool
		wantHooks []string
		// wantStore is the store's whole content after the pass ("" skips the check).
		wantStore string
	}
	for _, tc := range []struct {
		name   string
		shared string
		// herdrConfig is Herdr's config.toml; empty leaves none.
		herdrConfig string
		mode        Mode
		absent      bool
		result      deps.RunResult
		passes      []pass
	}{
		{
			name:   "missing installs once under the first dir, then leaves it",
			shared: `{"theme":"dark"}`,
			mode:   ModeApply,
			passes: []pass{
				{
					rows: []string{
						"  change  herdr claude integration in {store}: " +
							"CLAUDE_CONFIG_DIR='{cc1}' herdr integration install claude",
					},
					runDirs: []string{"{cc1}"}, releases: 1, wantHooks: []string{testjail.HerdrClaudeHook("{cc1}")},
					wantStore: `{"hooks":{"SessionStart":[{"hooks":[` +
						`{"command":` + fmt.Sprintf("%q", testjail.HerdrClaudeHook("{cc1}")) + `,"timeout":10,"type":"command"}],` +
						`"matcher":"^(startup|resume|clear|compact|fork)$"}]},"theme":"dark"}`,
				},
				{
					rows:     []string{"  ok      herdr claude integration in {store}: " + testjail.HerdrClaudeHook("{cc1}")},
					releases: 1, wantHooks: []string{testjail.HerdrClaudeHook("{cc1}")},
				},
			},
		},
		{
			name:   "one hook runs nothing and writes nothing",
			shared: testjail.HerdrClaudeSettings("{cc2}"),
			mode:   ModeApply,
			passes: []pass{{
				rows:      []string{"  ok      herdr claude integration in {store}: " + testjail.HerdrClaudeHook("{cc2}")},
				releases:  1,
				wantHooks: []string{testjail.HerdrClaudeHook("{cc2}")},
				wantStore: testjail.HerdrClaudeSettings("{cc2}"),
			}},
		},
		{
			name:        "herdr-shield's guarded hook stays while Herdr still runs herdr-shield",
			shared:      shieldGuardedSettings("{cc2}"),
			herdrConfig: "[terminal]\ndefault_shell = \"herdr-shield\"\n",
			mode:        ModeApply,
			passes: []pass{{
				rows: []string{
					"  skip    herdr claude integration in {store}: Herdr's default_shell in {config} still names" +
						" herdr-shield, so its guarded hook stays — end the chats in Herdr," +
						` remove default_shell = "herdr-shield" from {config}, restart Herdr, then run pfm install --yes again`,
				},
				releases:  1,
				wantHooks: []string{shieldGuardedHook("{cc2}")},
				wantStore: shieldGuardedSettings("{cc2}"),
			}},
		},
		{
			name:   "herdr-shield's guarded hook unwraps to Herdr's plain hook once Herdr no longer runs it",
			shared: shieldGuardedSettings("{cc2}"),
			mode:   ModeApply,
			passes: []pass{
				{
					rows: []string{
						"  change  herdr claude integration in {store}: unwrap " + shieldGuardedHook("{cc2}") +
							" to Herdr's plain hook",
					},
					releases: 1, wantHooks: []string{testjail.HerdrClaudeHook("{cc2}")},
					wantStore: testjail.HerdrClaudeSettings("{cc2}"),
				},
				{
					rows:     []string{"  ok      herdr claude integration in {store}: " + testjail.HerdrClaudeHook("{cc2}")},
					releases: 1, wantHooks: []string{testjail.HerdrClaudeHook("{cc2}")},
					wantStore: testjail.HerdrClaudeSettings("{cc2}"),
				},
			},
		},
		{
			name:        "herdr-shield's guarded hook under an unreadable Herdr config fails and writes nothing",
			shared:      shieldGuardedSettings("{cc2}"),
			herdrConfig: "[terminal\n",
			mode:        ModeApply,
			passes: []pass{{
				rows: []string{
					"  FAIL    herdr claude integration in {store}: read Herdr's default_shell: parse Herdr config" +
						" {config}: toml: line 2: expected '.' or ']' to end table name, but got '\\n' instead",
				},
				wantErr:   true,
				wantStore: shieldGuardedSettings("{cc2}"),
			}},
		},
		{
			name:   "dry run leaves herdr-shield's guarded hook as it is",
			shared: shieldGuardedSettings("{cc2}"),
			mode:   ModeDryRun,
			passes: []pass{{
				rows: []string{
					"  change  herdr claude integration in {store}: unwrap " + shieldGuardedHook("{cc2}") +
						" to Herdr's plain hook",
				},
				wantStore: shieldGuardedSettings("{cc2}"),
			}},
		},
		{
			name:   "two hooks run nothing and warn",
			shared: testjail.HerdrClaudeSettings("{cc1}", "{cc2}"),
			mode:   ModeApply,
			passes: []pass{{
				rows: []string{
					"  warn    herdr claude integration: {store} holds 2 Herdr hooks, each run on every Claude " +
						"session start: " + fmt.Sprintf("%q, %q", testjail.HerdrClaudeHook("{cc1}"), testjail.HerdrClaudeHook("{cc2}")) +
						" — delete all but one, then rerun pfm install",
				},
				releases:  1,
				wantHooks: []string{testjail.HerdrClaudeHook("{cc1}"), testjail.HerdrClaudeHook("{cc2}")},
				wantStore: testjail.HerdrClaudeSettings("{cc1}", "{cc2}"),
			}},
		},
		{
			name:   "dry run prints the plan and runs nothing",
			shared: `{"theme":"dark"}`,
			mode:   ModeDryRun,
			passes: []pass{{
				rows: []string{
					"  change  herdr claude integration in {store}: " +
						"CLAUDE_CONFIG_DIR='{cc1}' herdr integration install claude",
				},
			}},
		},
		{
			name:   "no herdr on the host",
			shared: `{"theme":"dark"}`,
			mode:   ModeApply,
			absent: true,
			passes: []pass{{rows: []string{"  skip    herdr claude integration: herdr absent"}}},
		},
		{
			name:   "failed install is a FAIL row with command, stderr and error",
			shared: `{"theme":"dark"}`,
			mode:   ModeApply,
			result: deps.RunResult{ExitCode: 3, Stderr: []byte("socket refused\n")},
			passes: []pass{{
				rows: []string{
					"  change  herdr claude integration in {store}: " +
						"CLAUDE_CONFIG_DIR='{cc1}' herdr integration install claude",
					"  FAIL    herdr claude integration: CLAUDE_CONFIG_DIR='{cc1}' herdr integration install claude " +
						`(argv ["` + fakeHerdrBinary + `" "integration" "install" "claude"]): exit 3; stderr: "socket refused"`,
				},
				runDirs: []string{"{cc1}"}, wantErr: true,
			}},
		},
		{
			name:   "unreadable store fails and runs nothing",
			shared: `{"hooks":`,
			mode:   ModeApply,
			passes: []pass{{
				rows: []string{
					"  FAIL    herdr claude integration: decode Claude settings {store}: unexpected end of JSON input",
				},
				wantErr: true,
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(root, "home")
			cc1, cc2 := filepath.Join(home, ".cc", "1"), filepath.Join(home, ".cc", "2")
			store := filepath.Join(home, ".claude", "settings.json")
			herdrConfig := filepath.Join(root, "herdr", "config.toml")
			fill := strings.NewReplacer("{store}", store, "{cc1}", cc1, "{cc2}", cc2, "{config}", herdrConfig)
			for _, dir := range []string{cc1, cc2, filepath.Dir(store), filepath.Dir(herdrConfig)} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(store, []byte(fill.Replace(tc.shared)), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.herdrConfig != "" {
				if err := os.WriteFile(herdrConfig, []byte(tc.herdrConfig), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, dir := range []string{cc1, cc2} {
				if err := os.Symlink(store, filepath.Join(dir, "settings.json")); err != nil {
					t.Fatal(err)
				}
			}
			for index, want := range tc.passes {
				runner := fakeHerdr{FakeRunner: &deps.FakeRunner{}, t: t}
				if !tc.absent {
					runner.ScriptLookPath("herdr", fakeHerdrBinary, nil)
				}
				runner.Script(herdr.ClaudeIntegrationArgv(fakeHerdrBinary), tc.result, nil)
				var released []string
				var output bytes.Buffer
				installer := &engine{apply: tc.mode != ModeDryRun, options: Options{
					Mode: tc.mode, Home: home, ConfigDir: filepath.Join(home, ".claude"),
					ClaudeAccounts: []pfmconfig.Account{{ID: 1, ConfigDir: cc1}, {ID: 2, ConfigDir: cc2}},
					Env:            &paths.MapEnv{Values: map[string]string{"HERDR_CONFIG_PATH": herdrConfig}},
					ProcessRunner:  runner, Stdout: &output,
					ReleaseNativeClaudeReports: func(_ context.Context, home string) error {
						released = append(released, home)
						return nil
					},
				}}
				err := installer.wireHerdrClaudeIntegration(context.Background())
				if (err != nil) != want.wantErr {
					t.Fatalf("pass %d: err = %v, want error %t", index, err, want.wantErr)
				}
				rows := make([]string, len(want.rows))
				for at, row := range want.rows {
					rows[at] = fill.Replace(row)
				}
				if got, wantText := strings.TrimRight(
					output.String(),
					"\n",
				), strings.Join(
					rows,
					"\n",
				); got != wantText {
					t.Fatalf("pass %d rows:\n%s\nwant:\n%s", index, got, wantText)
				}
				var runDirs []string
				for _, call := range runner.Calls() {
					if !slices.Equal(call.Argv, herdr.ClaudeIntegrationArgv(fakeHerdrBinary)) {
						t.Fatalf("pass %d ran %q", index, call.Argv)
					}
					runDirs = append(runDirs, runEnv(call.Opts, herdr.ClaudeConfigDirEnv))
				}
				wantDirs := make([]string, len(want.runDirs))
				for at, dir := range want.runDirs {
					wantDirs[at] = fill.Replace(dir)
				}
				if !slices.Equal(runDirs, wantDirs) {
					t.Fatalf("pass %d install runs under %q, want %q", index, runDirs, wantDirs)
				}
				wantReleased := slices.Repeat([]string{home}, want.releases)
				if !slices.Equal(released, wantReleased) {
					t.Fatalf("pass %d release calls %q, want %q", index, released, wantReleased)
				}
				if want.wantHooks != nil {
					hooks, hooksErr := herdr.ClaudeIntegrationHooks(store)
					wantHooks := make([]string, len(want.wantHooks))
					for at, hook := range want.wantHooks {
						wantHooks[at] = fill.Replace(hook)
					}
					if hooksErr != nil || !reflect.DeepEqual(hooks, wantHooks) {
						t.Fatalf("pass %d store hooks %q (err %v), want %q", index, hooks, hooksErr, wantHooks)
					}
				}
				if want.wantStore != "" {
					wantStore := fill.Replace(want.wantStore)
					if got, readErr := os.ReadFile(store); readErr != nil || string(got) != wantStore {
						t.Fatalf("pass %d store %s = %q (err %v), want %q", index, store, got, readErr, wantStore)
					}
				}
				for _, dir := range []string{cc1, cc2} {
					if target, linkErr := os.Readlink(
						filepath.Join(dir, "settings.json"),
					); linkErr != nil ||
						target != store {
						t.Fatalf(
							"pass %d: %s/settings.json -> %q (err %v), want a link to %s",
							index,
							dir,
							target,
							linkErr,
							store,
						)
					}
				}
			}
		})
	}
}

// A release that fails is a FAIL row and fails the step after the
// integration is in place.
func TestInstallHerdrClaudeIntegrationReleaseFailure(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	settings := []byte(testjail.HerdrClaudeSettings(dir))
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := fakeHerdr{FakeRunner: &deps.FakeRunner{}, t: t}
	runner.ScriptLookPath("herdr", fakeHerdrBinary, nil)
	runner.Script(herdr.ClaudeIntegrationArgv(fakeHerdrBinary), deps.RunResult{}, nil)
	var output bytes.Buffer
	installer := &engine{apply: true, options: Options{
		Mode: ModeApply, Home: dir, ConfigDir: dir,
		Env: &paths.MapEnv{Values: map[string]string{}}, ProcessRunner: runner, Stdout: &output,
		ReleaseNativeClaudeReports: func(context.Context, string) error { return errors.New("report lock held") },
	}}
	if err := installer.wireHerdrClaudeIntegration(context.Background()); err == nil {
		t.Fatal("release failure did not fail the step")
	}
	want := "  ok      herdr claude integration in " + filepath.Join(
		dir,
		"settings.json",
	) + ": " + testjail.HerdrClaudeHook(
		dir,
	) + "\n" +
		"  FAIL    herdr claude integration: release pfm's native Claude reports: report lock held\n"
	if output.String() != want {
		t.Fatalf("rows:\n%s\nwant:\n%s", output.String(), want)
	}
}
