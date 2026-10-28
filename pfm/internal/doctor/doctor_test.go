package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestDoctorCrumbHealthAcceptsEffortMetadataAndRejectsAnEmptyIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, paths.SIDEffortPrefix+"session-a"), []byte(`{"level":"high"}`), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.SIDEffortPrefix), []byte(`{"level":"high"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, invalid, err := crumbHealth(dir)
	if err != nil || entries != 2 || invalid != 1 {
		t.Fatalf(
			"entries=%d invalid=%d err=%v; legitimate effort must pass, empty identity must fail",
			entries,
			invalid,
			err,
		)
	}
}

// TestDoctorVerboseWritesUnderSIDDirNotCWDTmp is 1-c's regression test:
// unfixed, doctor's --verbose dir is the cwd-relative "tmp/pfm-doctor" — this
// test's cwd is a fresh temp dir with no PFM_SID_DIR relationship, so the
// printed line and the dir handed to the dependency probe / harness capture
// diverge from it, and a stray tmp/ appears in the cwd.
func TestDoctorVerboseWritesUnderSIDDirNotCWDTmp(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	wantDir := filepath.Join(runtime.Paths.SIDDir, "pfm-doctor")

	savedProbe := DependencyProbeOverride
	savedCapture := HarnessCaptureOverride
	t.Cleanup(func() {
		DependencyProbeOverride = savedProbe
		HarnessCaptureOverride = savedCapture
	})
	var gotProbeDir, gotCaptureDir string
	DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, options deps.ProbeOptions) []deps.Result {
		gotProbeDir = options.VerboseDir
		return savedProbe(context.Background(), entries, options)
	}
	HarnessCaptureOverride = func(
		ctx context.Context, home string, machine config.Config, alias, verboseDir string,
	) (HarnessCapture, error) {
		gotCaptureDir = verboseDir
		return savedCapture(ctx, home, machine, alias, verboseDir)
	}

	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	runDoctor([]string{"--verbose"}, &stdout, &stderr, runtime)

	wantLine := fmt.Sprintf("doctor: verbose output dir=%s\n", wantDir)
	if !strings.Contains(stdout.String(), wantLine) {
		t.Fatalf("stdout missing %q:\n%s", wantLine, stdout.String())
	}
	if gotProbeDir != wantDir {
		t.Fatalf("dependency probe VerboseDir=%q, want %q", gotProbeDir, wantDir)
	}
	if gotCaptureDir != wantDir {
		t.Fatalf("harness capture verboseDir=%q, want %q", gotCaptureDir, wantDir)
	}
	if _, err := os.Stat(filepath.Join(cwd, "tmp")); !os.IsNotExist(err) {
		t.Fatalf("doctor --verbose left a cwd-relative tmp/: err=%v", err)
	}
}

// TestDoctorWithoutVerboseFlagPrintsNoDirLine is 1-c's negative case: no
// --verbose, no info line, no directory created.
func TestDoctorWithoutVerboseFlagPrintsNoDirLine(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	runDoctor(nil, &stdout, &stderr, runtime)

	if strings.Contains(stdout.String(), "doctor: verbose output dir") {
		t.Fatalf("doctor without --verbose printed a verbose dir line:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(runtime.Paths.SIDDir, "pfm-doctor")); !os.IsNotExist(err) {
		t.Fatalf("doctor without --verbose created its verbose dir: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "tmp")); !os.IsNotExist(err) {
		t.Fatalf("doctor without --verbose left a cwd-relative tmp/: err=%v", err)
	}
}

// TestPrintDependenciesCountsAnUnknownProbeStateLikeMissing: a probe state
// PrintDependencies does not know is an unverified dependency — a required
// engine dependency in it is a failure (so `pfm install` preflight refuses),
// an optional one a warning; the row itself is unchanged.
func TestPrintDependenciesCountsAnUnknownProbeStateLikeMissing(t *testing.T) {
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, _ deps.ProbeOptions) []deps.Result {
		results := make([]deps.Result, 0, len(entries))
		for _, entry := range entries {
			results = append(results, deps.Result{Entry: entry, State: deps.State("wedged")})
		}
		return results
	}
	cases := []struct {
		name                   string
		entry                  deps.Entry
		wantWarning, wantFails int
	}{
		{name: "required engine dep", entry: deps.Entry{Name: "tmux", Required: true}, wantFails: 1},
		{name: "optional dep", entry: deps.Entry{Name: "jq"}, wantWarning: 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout bytes.Buffer
			warnings, failures, _ := PrintDependencies(
				context.Background(), &stdout, t.TempDir(), []deps.Entry{testCase.entry}, deps.ProbeOptions{},
			)
			if warnings != testCase.wantWarning || failures != testCase.wantFails {
				t.Fatalf("warnings=%d failures=%d, want %d/%d:\n%s",
					warnings, failures, testCase.wantWarning, testCase.wantFails, stdout.String())
			}
			want := fmt.Sprintf("doctor: dep %s broken error=unknown probe state %q\n", testCase.entry.Name, "wedged")
			if stdout.String() != want {
				t.Fatalf("row = %q, want %q", stdout.String(), want)
			}
		})
	}
}

func TestDoctorCallmeterUsesClaudeEnvHome(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	home := runtime.Paths.Home
	clone := filepath.Join(home, "clone")
	root := filepath.Join(clone, "plugins")
	writePluginLogsFile(
		t,
		filepath.Join(clone, ".claude-plugin", "marketplace.json"),
		`{"name":"professor","plugins":[{"name":"callmeter","source":"./plugins/callmeter/plugins/callmeter"}]}`,
	)
	writePluginLogsFile(
		t,
		filepath.Join(root, "callmeter", "plugins", "callmeter", ".claude-plugin", "plugin.json"),
		`{"name":"callmeter","version":"0.4.0"}`,
	)
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	runtime.Version = "0.82.0-alpha"
	runtime.Config.Claude.PluginCheckoutRoot = root
	runtime.Config.Claude.Env = map[string]string{"CALLMETER_HOME": filepath.Join(home, "configured")}
	writePluginLogsFile(
		t,
		filepath.Join(home, ".claude", "settings.json"),
		fmt.Sprintf(
			`{"enabledPlugins":{"callmeter@professor":true},"env":{"PLUGIN_LOG_DIR":%q}}`,
			filepath.Join(home, "shared-logs"),
		),
	)
	writeDoctorInstalledPlugins(t, filepath.Join(home, ".claude"), "callmeter@professor")
	t.Setenv("CALLMETER_HOME", filepath.Join(home, "ambient"))
	writePluginLogsFile(t, filepath.Join(home, "configured", "logs", "events.jsonl"), "{}\n")
	writePluginLogsFile(t, filepath.Join(home, "ambient", "logs", "events.jsonl"), "{}\n{}\n")
	writePluginLogsFile(t, filepath.Join(home, ".local", "state", "callmeter", "logs", "errors.jsonl"), "{}\n")
	var stdout, stderr bytes.Buffer
	runDoctor(nil, &stdout, &stderr, runtime)
	want := filepath.Join(home, "configured", "bin", "0.4.0", "callmeter") + " missing"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("doctor did not inspect claude.env home %s:\n%s", want, stdout.String())
	}
	// callmeter's logs follow the shared plugin-log fallback, never its home.
	wantFallback := "1 rows stranded in its fallback " + filepath.Join(home, ".local", "state", "callmeter", "logs")
	if !strings.Contains(stdout.String(), wantFallback) ||
		strings.Contains(stdout.String(), filepath.Join(home, "configured", "logs")) ||
		strings.Contains(stdout.String(), filepath.Join(home, "ambient", "logs")) {
		t.Fatalf("doctor fallback audit did not read the shared fallback:\n%s", stdout.String())
	}
}

func TestDoctorPluginLogsIncludesLedgerOnlyConfigDirs(t *testing.T) {
	for _, target := range []string{"historical", "target", "physical alias"} {
		t.Run(target, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			runtime.Version = "0.82.0-alpha"
			home := runtime.Paths.Home
			custom := filepath.Join(home, "outside-roster", "settings.json")
			if target == "physical alias" {
				custom = filepath.Join(home, "outside-roster", "shared.json")
			}
			logs := filepath.Join(home, "shared-logs")
			writePluginLogsFile(t, custom, `{}`)
			writePluginLogsFile(
				t,
				filepath.Join(home, ".claude", "settings.json"),
				fmt.Sprintf(`{"env":{"PLUGIN_LOG_DIR":%q}}`, logs),
			)
			ledger := map[string]any{
				"version": 1, "pluginLogDir": logs,
				"files": map[string]any{custom: map[string]string{"PLUGIN_LOG_DIR": logs}},
			}
			if target == "target" {
				ledger["target"] = custom
			}
			raw, err := json.Marshal(ledger)
			if err != nil {
				t.Fatal(err)
			}
			writePluginLogsFile(t, filepath.Join(installer.ManagedRoot(home), "claude-env-ownership.json"), string(raw))
			var stdout, stderr bytes.Buffer
			runDoctor(nil, &stdout, &stderr, runtime)
			findings := 0
			for _, line := range strings.Split(stdout.String(), "\n") {
				if strings.HasPrefix(line, "doctor: plugin_logs ") && strings.Contains(line, custom) {
					findings++
					if strings.Count(line, " — set ") != 1 {
						t.Errorf("want one repair: %s", line)
					}
				}
				if strings.HasPrefix(line, "doctor: claude_env FAIL") && strings.Contains(line, "PLUGIN_LOG_DIR") {
					t.Errorf("duplicate claude_env repair: %s", line)
				}
			}
			if findings != 1 {
				t.Fatalf("want one ledger-only plugin_logs finding, got %d:\n%s", findings, stdout.String())
			}
		})
	}
}
