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

// tracerPython is the interpreter the scripted lookup finds; the version it
// prints is the shape the healthy row's pattern checks.
const tracerPython = "/fixture/bin/python3"

// scriptTracerPython scripts the python3 lookup and its version probe, and the
// kit run to answer kitRun.
func scriptTracerPython(fake *deps.FakeRunner, kit string, kitRun deps.RunResult) {
	fake.ScriptLookPath("python3", tracerPython, nil)
	fake.Script([]string{tracerPython, "--version"}, deps.RunResult{Stdout: []byte("Python 3.12.1\n")}, nil)
	fake.Script([]string{tracerPython, kit}, kitRun, nil)
}

// tracerUsage is what the stub kit answers: its usage line, exit 2.
var tracerUsage = deps.RunResult{Stderr: []byte(tracetreeUsage + " [-h] {check,map}\n"), ExitCode: 2}

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
// status. The cases play python3 through a scripted runner over the stub kit
// the jail stages in the flights plugin folder, or a broken kit written over it.
func TestTracetreeKitDoctor(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		stage func(t *testing.T, home string)
		// run scripts the runner and returns the python3 path the check finds; nil
		// scripts a healthy python3 and a kit printing its usage.
		run  func(fake *deps.FakeRunner, home, kit string) (python string)
		rows func(kit, python string) []string
		code int
		// count is the summary's failures= count, or its warnings= count when code is 1.
		count string
	}{
		{
			name:  "python3 absent from PATH",
			stage: func(t *testing.T, home string) { t.Setenv("PATH", filepath.Join(home, ".local", "bin")) },
			run:   func(*deps.FakeRunner, string, string) string { return "" },
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
			name:  "python3 found but its version probe failing",
			stage: func(*testing.T, string) {},
			run: func(fake *deps.FakeRunner, home, _ string) string {
				broken := filepath.Join(home, "broken-python", "python3")
				fake.ScriptLookPath("python3", broken, nil)
				fake.Script([]string{broken, "--version"}, deps.RunResult{Stderr: []byte("boom\n"), ExitCode: 1}, nil)
				return broken
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
			name:  "python3 resolving only through a relative PATH entry",
			stage: func(*testing.T, string) {},
			run: func(fake *deps.FakeRunner, _, _ string) string {
				fake.ScriptLookPath("python3", "", &exec.Error{Name: "python3", Err: exec.ErrDot})
				return ""
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
			run: func(fake *deps.FakeRunner, _, kit string) string {
				scriptTracerPython(fake, kit, deps.RunResult{
					Stderr: []byte("Traceback (most recent call last):\n" +
						"ModuleNotFoundError: No module named 'tracemap_gone'\n"),
					ExitCode: 1,
				})
				return tracerPython
			},
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
			run: func(fake *deps.FakeRunner, _, kit string) string {
				scriptTracerPython(fake, kit, deps.RunResult{})
				return tracerPython
			},
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
			run: func(*deps.FakeRunner, string, string) string { return "" },
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
			fake := &deps.FakeRunner{}
			python := tracerPython
			if scenario.run != nil {
				python = scenario.run(fake, home, kit)
			} else {
				scriptTracerPython(fake, kit, tracerUsage)
			}
			dependencies := testDependencies()
			dependencies.Runner = fake
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
			if scenario.name == "healthy" && !slices.ContainsFunc(fake.Calls(), func(call deps.RunCall) bool {
				return slices.Equal(call.Argv, []string{python, kit})
			}) {
				t.Fatalf("the check never ran %q; runs: %+v", []string{python, kit}, fake.Calls())
			}
		})
	}
}

// TestTracetreeKitDoctorTimesOut pins R3's rule that a check which could not
// finish is a FAIL naming why, never ok and never absent.
func TestTracetreeKitDoctorTimesOut(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	home := runtime.Paths.Home
	previous := tracetreeKitBound
	tracetreeKitBound = time.Second
	t.Cleanup(func() { tracetreeKitBound = previous })
	python := tracerPython
	dependencies := testDependencies()
	dependencies.Runner = kitOutrunRunner{FakeRunner: healthyDoctorRunner(), kit: healthyKit(t, home)}
	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr, runtime, dependencies)
	want := tracetreeKitRow + " FAIL need=run checked=" + python + " " + healthyKit(t, home) +
		` error="timed out after 1s" — fix: ` + tracerFixRelinkText
	if rows := tracetreeKitRows(stdout.String()); !slices.Equal(rows, []string{want}) {
		t.Fatalf("rows=%q, want %q", rows, want)
	}
	if code != 3 {
		t.Fatalf("exit code=%d, want 3; stdout:\n%s", code, stdout.String())
	}
}

// kitOutrunRunner is healthyDoctorRunner with a kit that outlives its bound:
// the run of python3 over kit returns only once its context is done, as a kit
// sleeping past tracetreeKitBound does.
type kitOutrunRunner struct {
	*deps.FakeRunner
	kit string
}

func (runner kitOutrunRunner) Run(ctx context.Context, argv []string, opts deps.RunOptions) (deps.RunResult, error) {
	if slices.Equal(argv, []string{tracerPython, runner.kit}) {
		<-ctx.Done()
		return deps.RunResult{ExitCode: -1}, ctx.Err()
	}
	return runner.FakeRunner.Run(ctx, argv, opts)
}
