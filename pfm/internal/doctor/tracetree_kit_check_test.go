package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const (
	tracerPythonChecked = "registry entry python3 on doctor's PATH (the launch passes Claude its caller's PATH, " +
		"which pfm does not set)"
	tracerPythonMissing = "python3 not found on doctor's PATH"
	tracerFixPythonText = "flights:tracer cannot run without python3, a required dependency (the kit uses only the " +
		"Python 3 standard library and states no minimum version): install it as the doctor: dep python3 row says, " +
		"and put it on the PATH Claude is launched with"
	tracerFixKitText    = "run pfm install --yes to refresh the flights plugin copy"
	tracerFixRelinkText = tracerFixKitText + "; " + tracerFixPythonText
	tracerNotRunText    = ` error="not run: python3 or the kit failed above" — fix: fix the rows above, then rerun pfm doctor`
	tracerPendingRow    = "doctor: tracetree-kit: flights plugin not installed yet — run pfm install --yes"
)

// recordingRunner passes every call to the real runner and keeps each
// completed run's argv, so a test sees the command the check ran.
type recordingRunner struct {
	deps.Runner
	argv [][]string
}

func (recorder *recordingRunner) Run(ctx context.Context, argv []string, opts deps.RunOptions) (deps.RunResult, error) {
	result, err := recorder.Runner.Run(ctx, argv, opts)
	recorder.argv = append(recorder.argv, argv)
	return result, err
}

func tracetreeKitRows(output string) []string {
	var rows []string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, tracetreeKitRow) {
			rows = append(rows, line)
		}
	}
	return rows
}

// healthyKit is the kit file the healthy jail staged in the flights plugin
// folder.
func healthyKit(t *testing.T, home string) string {
	t.Helper()
	kit, enabled, err := tracetreeKitPath(home)
	if err != nil || !enabled || kit == "" {
		t.Fatalf("the healthy jail stages no flights kit: kit=%q enabled=%v err=%v", kit, enabled, err)
	}
	return kit
}

// writeKit replaces the staged kit file with source.
func writeKit(t *testing.T, home, source string) {
	t.Helper()
	if err := os.WriteFile(healthyKit(t, home), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

// editStoreRecord rewrites the JSON document at rel under the shared Claude
// store through edit.
func editStoreRecord(t *testing.T, home, rel string, edit func(document map[string]any)) {
	t.Helper()
	path := filepath.Join(installer.ClaudeStore(home), rel)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	edit(document)
	content, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// flightsKeys calls each for every flights@ key of the record map.
func flightsKeys(records any, each func(records map[string]any, key string)) {
	table, _ := records.(map[string]any)
	for key := range table {
		if strings.HasPrefix(key, "flights@") {
			each(table, key)
		}
	}
}

// disableFlights leaves the flights plugin installed but not enabled.
func disableFlights(t *testing.T, home string) {
	t.Helper()
	editStoreRecord(t, home, "settings.json", func(document map[string]any) {
		flightsKeys(document["enabledPlugins"], func(enabled map[string]any, key string) { enabled[key] = false })
	})
}

// TestTracetreeKitDoctor is the tracetree-kit check's whole table: each state
// of the kit's needs is a case that asserts the rows printed and doctor's exit
// status. The cases run the real python3 over the stub kit the jail stages in
// the flights plugin folder, or a broken kit written over it.
func TestTracetreeKitDoctor(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		stage func(t *testing.T, home string)
		rows  func(kit, python string) []string
		code  int
		// count is the summary's failures= count, or its warnings= count when code is 1.
		count string
	}{
		{
			name:  "python3 absent from PATH",
			stage: func(t *testing.T, home string) { t.Setenv("PATH", filepath.Join(home, ".local", "bin")) },
			rows: func(kit, _ string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=python3 checked=` + tracerPythonChecked +
						` error="` + tracerPythonMissing + `" — fix: ` + tracerFixPythonText,
					tracetreeKitRow + ` FAIL need=run checked=python3 ` + kit + tracerNotRunText,
				}
			},
			code: 3, count: "2",
		},
		{
			name: "python3 found but its version probe failing",
			stage: func(t *testing.T, home string) {
				bin := filepath.Join(home, "broken-python")
				if err := os.Mkdir(bin, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := testjail.WriteExecutable(
					filepath.Join(bin, "python3"), []byte("#!/bin/sh\necho boom >&2\nexit 1\n"), 0o700,
				); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Join(home, ".local", "bin"))
			},
			rows: func(kit, python string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=python3 checked=` + python + ` --version` +
						` error="broken: exit status 1 raw=\"boom\"" — fix: ` + tracerFixPythonText,
					tracetreeKitRow + ` FAIL need=run checked=python3 ` + kit + tracerNotRunText,
				}
			},
			code: 3, count: "2",
		},
		{
			// A relative PATH entry (a project's venv/bin, say) resolves python3
			// only with exec.ErrDot: the probe never gets a path, and the row
			// still names what it checked.
			name: "python3 resolving only through a relative PATH entry",
			stage: func(t *testing.T, home string) {
				if err := os.Mkdir(filepath.Join(home, "venv-bin"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := testjail.WriteExecutable(
					filepath.Join(home, "venv-bin", "python3"), []byte("#!/bin/sh\necho 'Python 3.12.1'\n"), 0o700,
				); err != nil {
					t.Fatal(err)
				}
				t.Chdir(home)
				t.Setenv("PATH", "venv-bin"+string(os.PathListSeparator)+filepath.Join(home, ".local", "bin"))
			},
			rows: func(kit, _ string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=python3 checked=` + tracerPythonChecked +
						` error="broken: exec: \"python3\": cannot run executable found relative to current directory"` +
						` — fix: ` + tracerFixPythonText,
					tracetreeKitRow + ` FAIL need=run checked=python3 ` + kit + tracerNotRunText,
				}
			},
			code: 3, count: "2",
		},
		{
			name: "kit file absent from the plugin folder",
			stage: func(t *testing.T, home string) {
				if err := os.Remove(healthyKit(t, home)); err != nil {
					t.Fatal(err)
				}
			},
			rows: func(kit, _ string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=kit checked=` + kit +
						` error="stat ` + kit + `: no such file or directory" — fix: ` + tracerFixKitText,
					tracetreeKitRow + ` FAIL need=run checked=python3 ` + kit + tracerNotRunText,
				}
			},
			code: 3, count: "2",
		},
		{
			name: "kit path holding a directory",
			stage: func(t *testing.T, home string) {
				kit := healthyKit(t, home)
				if err := os.Remove(kit); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(kit, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			rows: func(kit, _ string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=kit checked=` + kit + ` error="not a regular file" — fix: ` +
						tracerFixKitText,
					tracetreeKitRow + ` FAIL need=run checked=python3 ` + kit + tracerNotRunText,
				}
			},
			code: 3, count: "2",
		},
		{
			name:  "kit present but failing to import",
			stage: func(t *testing.T, home string) { writeKit(t, home, "import tracemap_gone\n") },
			rows: func(kit, python string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=run checked=` + python + ` ` + kit +
						" output=\"printed no `usage: tracetree.py` line: " +
						`ModuleNotFoundError: No module named 'tracemap_gone'" — fix: ` + tracerFixRelinkText,
				}
			},
			code: 3, count: "1",
		},
		{
			name:  "kit running but printing no usage",
			stage: func(t *testing.T, home string) { writeKit(t, home, "import sys\nsys.exit(0)\n") },
			rows: func(kit, python string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=run checked=` + python + ` ` + kit +
						" output=\"printed no `usage: tracetree.py` line: no output, exit 0\" — fix: " +
						tracerFixRelinkText,
				}
			},
			code: 3, count: "1",
		},
		{
			name:  "flights plugin not enabled",
			stage: disableFlights,
			rows:  func(_, _ string) []string { return []string{tracerPendingRow} },
			// The claude_plugins row warns that flights is not enabled; the kit row only says it is pending.
			code: 1, count: "1",
		},
		{
			name: "flights plugin not enabled and python3 absent",
			stage: func(t *testing.T, home string) {
				disableFlights(t, home)
				t.Setenv("PATH", filepath.Join(home, ".local", "bin"))
			},
			rows: func(_, _ string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=python3 checked=` + tracerPythonChecked +
						` error="` + tracerPythonMissing + `" — fix: ` + tracerFixPythonText,
					tracerPendingRow,
				}
			},
			code: 3, count: "1",
		},
		{
			name: "flights plugin enabled with no folder recorded",
			stage: func(t *testing.T, home string) {
				editStoreRecord(t, home, filepath.Join("plugins", "installed_plugins.json"), func(document map[string]any) {
					flightsKeys(document["plugins"], func(records map[string]any, key string) { delete(records, key) })
				})
			},
			rows: func(kit, _ string) []string {
				claudeStore, _, _ := strings.Cut(kit, "/plugins/cache/")
				return []string{
					tracetreeKitRow + ` FAIL need=kit checked=the flights plugin records in ` + claudeStore +
						` error="the flights plugin is enabled but no record names its folder" — fix: ` + tracerFixKitText,
				}
			},
			code: 3, count: "1",
		},
		{
			name: "flights plugin records unreadable",
			stage: func(t *testing.T, home string) {
				path := filepath.Join(installer.ClaudeStore(home), "plugins", "known_marketplaces.json")
				if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			rows: func(kit, _ string) []string {
				claudeStore, _, _ := strings.Cut(kit, "/plugins/cache/")
				return []string{
					tracetreeKitRow + ` FAIL need=kit checked=the flights plugin records in ` + claudeStore +
						` error="parse ` + claudeStore + `/plugins/known_marketplaces.json: invalid character 'n' looking for ` +
						`beginning of object key string" — fix: repair the file the error names, then run pfm install --yes`,
				}
			},
			// The claude_plugins row fails on the same file: one failure beside the tracetree-kit row.
			code: 3, count: "2",
		},
		{
			name:  "healthy",
			stage: func(*testing.T, string) {},
			rows: func(kit, python string) []string {
				return []string{tracetreeKitRow + ` ok python3=` + python + ` version=VERSION kit=` + kit + ` usage printed`}
			},
			code: 0,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			home := runtime.Paths.Home
			kit := healthyKit(t, home)
			scenario.stage(t, home)
			python, _ := exec.LookPath("python3")
			recorder := &recordingRunner{Runner: deps.RealRunner{}}
			dependencies := testDependencies()
			dependencies.Runner = recorder
			var stdout, stderr bytes.Buffer
			code := Run(nil, &stdout, &stderr, runtime, dependencies)

			rows := tracetreeKitRows(stdout.String())
			want := scenario.rows(kit, python)
			if len(want) == 1 && strings.Contains(want[0], "version=VERSION") {
				// The version is the host interpreter's: shape-checked, not pinned.
				pattern := "^" + strings.Replace(regexp.QuoteMeta(want[0]), "VERSION", `\d+\.\d+\.\d+\S*`, 1) + "$"
				if len(rows) != 1 || !regexp.MustCompile(pattern).MatchString(rows[0]) {
					t.Fatalf("rows=%q, want one matching %q", rows, pattern)
				}
			} else if strings.Join(rows, "\n") != strings.Join(want, "\n") {
				t.Fatalf(
					"rows:\n%s\nwant:\n%s\nstdout:\n%s",
					strings.Join(rows, "\n"),
					strings.Join(want, "\n"),
					stdout.String(),
				)
			}
			if code != scenario.code {
				t.Fatalf(
					"exit code=%d, want %d; stdout:\n%s\nstderr: %s",
					code,
					scenario.code,
					stdout.String(),
					stderr.String(),
				)
			}
			wantSummary := "doctor: failures=" + scenario.count + "\n"
			switch scenario.code {
			case 0:
				wantSummary = "doctor: clean\n"
			case 1:
				wantSummary = "doctor: warnings=" + scenario.count + "\n"
			}
			if !strings.Contains(stdout.String(), wantSummary) {
				t.Fatalf("summary: want %q in stdout:\n%s", wantSummary, stdout.String())
			}
			if scenario.name == "healthy" && !slices.ContainsFunc(recorder.argv, func(argv []string) bool {
				return slices.Equal(argv, []string{python, kit})
			}) {
				t.Fatalf("the check never ran %q; runs: %q", []string{python, kit}, recorder.argv)
			}
		})
	}
}

// TestTracetreeKitDoctorTimesOut pins R3's rule that a check which could not
// finish is a FAIL naming why, never ok and never absent.
func TestTracetreeKitDoctorTimesOut(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	home := runtime.Paths.Home
	writeKit(t, home, "import time\ntime.sleep(60)\n")
	previous := tracetreeKitBound
	tracetreeKitBound = time.Second
	t.Cleanup(func() { tracetreeKitBound = previous })
	python, _ := exec.LookPath("python3")
	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr, runtime, testDependencies())
	want := tracetreeKitRow + " FAIL need=run checked=" + python + " " + healthyKit(t, home) +
		` error="timed out after 1s" — fix: ` + tracerFixRelinkText
	if rows := tracetreeKitRows(stdout.String()); !slices.Equal(rows, []string{want}) {
		t.Fatalf("rows=%q, want %q", rows, want)
	}
	if code != 3 {
		t.Fatalf("exit code=%d, want 3; stdout:\n%s", code, stdout.String())
	}
}
