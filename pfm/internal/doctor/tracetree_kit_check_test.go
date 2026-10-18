package doctor

import (
	"bytes"
	"context"
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
	tracerFixPythonText = "the tracer cannot run without python3, a required dependency (the kit uses only the " +
		"Python 3 standard library and states no minimum version): install it as the doctor: dep python3 row says, " +
		"and put it on the PATH Claude is launched with"
	tracerFixKitText    = "run pfm install --yes to relink the tracetree skill"
	tracerFixRelinkText = "run pfm install --yes to relink the kit; " + tracerFixPythonText
	tracerNotRunText    = ` error="not run: python3 or the kit failed above" — fix: fix the rows above, then rerun pfm doctor`
	tracerPendingRow    = "doctor: tracetree-kit: kit not installed yet — run pfm install --yes"
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

func removeKitLink(t *testing.T, home string) {
	t.Helper()
	if err := os.Remove(filepath.Join(home, ".claude", "skills", "tracetree")); err != nil {
		t.Fatal(err)
	}
}

// writeSkillLinkLedger writes the installer's record of the directories it
// links skills into.
func writeSkillLinkLedger(t *testing.T, home, body string) {
	t.Helper()
	path := filepath.Join(installer.ManagedRoot(home), "skill-links.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// replaceKitLink swaps the healthy jail's tracetree link for a real directory
// holding the given kit source.
func replaceKitLink(t *testing.T, home, source string) {
	t.Helper()
	link := filepath.Join(home, ".claude", "skills", "tracetree")
	removeKitLink(t, home)
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatal(err)
	}
	if source == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(link, "tracetree.py"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTracetreeKitDoctor is the tracetree-kit check's whole table: each state
// of the kit's needs is a case that asserts the rows printed and doctor's exit
// status. The cases run the real python3; the kit is the shipped one only in
// the healthy case, and a stub kit otherwise.
func TestTracetreeKitDoctor(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		stage        func(t *testing.T, home string)
		rows         func(kit, python string) []string
		code         int
		failureCount string
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
			code: 3, failureCount: "2",
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
			code: 3, failureCount: "2",
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
			code: 3, failureCount: "2",
		},
		{
			name:  "kit file absent from its linked directory",
			stage: func(t *testing.T, home string) { replaceKitLink(t, home, "") },
			rows: func(kit, _ string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=kit checked=` + kit +
						` error="stat ` + kit + `: no such file or directory" — fix: ` + tracerFixKitText,
					tracetreeKitRow + ` FAIL need=run checked=python3 ` + kit + tracerNotRunText,
				}
			},
			code: 3, failureCount: "2",
		},
		{
			name: "kit placed then its source removed leaves a dangling link",
			stage: func(t *testing.T, home string) {
				removeKitLink(t, home)
				link := filepath.Join(home, ".claude", "skills", "tracetree")
				if err := os.Symlink(filepath.Join(home, "removed-kit"), link); err != nil {
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
			code: 3, failureCount: "2",
		},
		{
			name:  "kit present but failing to import",
			stage: func(t *testing.T, home string) { replaceKitLink(t, home, "import tracemap_gone\n") },
			rows: func(kit, python string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=run checked=` + python + ` ` + kit +
						" output=\"printed no `usage: tracetree.py` line: " +
						`ModuleNotFoundError: No module named 'tracemap_gone'" — fix: ` + tracerFixRelinkText,
				}
			},
			code: 3, failureCount: "1",
		},
		{
			name:  "kit running but printing no usage",
			stage: func(t *testing.T, home string) { replaceKitLink(t, home, "import sys\nsys.exit(0)\n") },
			rows: func(kit, python string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=run checked=` + python + ` ` + kit +
						" output=\"printed no `usage: tracetree.py` line: no output, exit 0\" — fix: " +
						tracerFixRelinkText,
				}
			},
			code: 3, failureCount: "1",
		},
		{
			name:  "kit not linked yet on a fresh home",
			stage: removeKitLink,
			rows:  func(_, _ string) []string { return []string{tracerPendingRow} },
			code:  0,
		},
		{
			name: "kit not linked yet and python3 absent",
			stage: func(t *testing.T, home string) {
				removeKitLink(t, home)
				t.Setenv("PATH", filepath.Join(home, ".local", "bin"))
			},
			rows: func(_, _ string) []string {
				return []string{
					tracetreeKitRow + ` FAIL need=python3 checked=` + tracerPythonChecked +
						` error="` + tracerPythonMissing + `" — fix: ` + tracerFixPythonText,
					tracerPendingRow,
				}
			},
			code: 3, failureCount: "1",
		},
		{
			name: "kit linked elsewhere than the tracer reads",
			stage: func(t *testing.T, home string) {
				elsewhere := filepath.Join(home, "elsewhere", "skills")
				removeKitLink(t, home)
				if err := os.MkdirAll(elsewhere, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(home, "removed-kit"), filepath.Join(elsewhere, "tracetree")); err != nil {
					t.Fatal(err)
				}
				writeSkillLinkLedger(t, home, `{"version":1,"link_dirs":["`+elsewhere+`"]}`)
			},
			rows: func(kit, _ string) []string {
				elsewhere := filepath.Join(strings.TrimSuffix(kit, "/.claude/skills/tracetree/tracetree.py"), "elsewhere", "skills", "tracetree")
				return []string{
					tracetreeKitRow + ` FAIL need=kit checked=` + kit + ` error="the install linked the tracetree kit at ` +
						elsewhere + `, but the tracer reads ` + filepath.Dir(kit) +
						`; the tracer cannot run on this host" — fix: pfm install --yes does not fix this: ` +
						`link the kit at the tracer's path, or install without --config-dir`,
					tracetreeKitRow + ` FAIL need=run checked=python3 ` + kit + tracerNotRunText,
				}
			},
			code: 3, failureCount: "2",
		},
		{
			name: "install records present but naming the kit nowhere",
			stage: func(t *testing.T, home string) {
				removeKitLink(t, home)
				writeSkillLinkLedger(t, home, `{"version":1,"link_dirs":["`+filepath.Join(home, ".claude", "skills")+`"]}`)
			},
			rows: func(_, _ string) []string { return []string{tracerPendingRow} },
			code: 0,
		},
		{
			name: "install records corrupt",
			stage: func(t *testing.T, home string) {
				removeKitLink(t, home)
				writeSkillLinkLedger(t, home, "{not json")
			},
			rows: func(kit, _ string) []string {
				ledger := filepath.Join(strings.TrimSuffix(kit, "/.claude/skills/tracetree/tracetree.py"),
					".local", "share", "pfm", "install", "skill-links.json")
				return []string{
					tracetreeKitRow + ` FAIL need=kit checked=` + ledger + ` error="skill link ledger ` + ledger +
						`: invalid character 'n' looking for beginning of object key string" — fix: repair or delete ` +
						ledger + `, then run pfm install --yes`,
					tracetreeKitRow + ` FAIL need=run checked=python3 ` + kit + tracerNotRunText,
				}
			},
			// The existing skill-source row fails on the same file: one failure beside the two tracetree-kit rows.
			code: 3, failureCount: "3",
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
			scenario.stage(t, home)
			python, _ := exec.LookPath("python3")
			kit := tracetreeKitPath(home)
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
			wantSummary := "doctor: failures=" + scenario.failureCount + "\n"
			if scenario.code == 0 {
				wantSummary = "doctor: clean\n"
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
	replaceKitLink(t, home, "import time\ntime.sleep(60)\n")
	previous := tracetreeKitBound
	tracetreeKitBound = time.Second
	t.Cleanup(func() { tracetreeKitBound = previous })
	python, _ := exec.LookPath("python3")
	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr, runtime, testDependencies())
	want := tracetreeKitRow + " FAIL need=run checked=" + python + " " + tracetreeKitPath(home) +
		` error="timed out after 1s" — fix: ` + tracerFixRelinkText
	if rows := tracetreeKitRows(stdout.String()); !slices.Equal(rows, []string{want}) {
		t.Fatalf("rows=%q, want %q", rows, want)
	}
	if code != 3 {
		t.Fatalf("exit code=%d, want 3; stdout:\n%s", code, stdout.String())
	}
}
