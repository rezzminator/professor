package doctor

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
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
	tracerFixKitText     = "run pfm install --yes to relink the tracetree skill"
	tracerFixRelinkText  = "run pfm install --yes to relink the kit; " + tracerFixPythonText
	tracerFixSmokeNotRun = "fix the rows above, then rerun pfm doctor"
	tracerPendingRow     = "doctor: tracer-intake: kit not installed yet — run pfm install --yes"
)

// recordingRunner passes every call to the real runner and keeps each
// completed run's argv and stdout, so a test sees the command the check ran
// and what the kit answered.
type recordingRunner struct {
	deps.Runner
	argv    [][]string
	outputs [][]byte
}

func (recorder *recordingRunner) Run(ctx context.Context, argv []string, opts deps.RunOptions) (deps.RunResult, error) {
	result, err := recorder.Runner.Run(ctx, argv, opts)
	recorder.argv = append(recorder.argv, argv)
	recorder.outputs = append(recorder.outputs, result.Stdout)
	return result, err
}

func tracerIntakeRows(output string) []string {
	var rows []string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, tracerIntakeRow) {
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
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
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

// TestTracerIntakeDoctor is the tracer-intake check's whole table: each state
// of the hook's needs is a case that asserts the rows printed and doctor's
// exit status. The cases run the real python3 and the real /bin/sh; the kit is
// the shipped one only in the healthy case, and a stub kit otherwise.
func TestTracerIntakeDoctor(t *testing.T) {
	git, gitErr := deps.Resolve("git") // before CleanHome narrows PATH
	const noRunKit = "import sys\nif '\"subagent_type\"' in sys.stdin.read():\n" +
		"    print('{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"additionalContext\":\"x\"}}')\n"
	for _, scenario := range []struct {
		name         string
		stage        func(t *testing.T, home string)
		rows         func(command, kit, python string) []string
		code         int
		failureCount string
	}{
		{
			name:  "python3 absent from PATH",
			stage: func(t *testing.T, home string) { t.Setenv("PATH", filepath.Join(home, ".local", "bin")) },
			rows: func(command, _, _ string) []string {
				return []string{
					tracerIntakeRow + ` FAIL need=python3 checked=` + tracerPythonChecked +
						` error="` + tracerPythonMissing + `" — fix: ` + tracerFixPythonText,
					tracerIntakeRow + ` FAIL need=smoke checked=` + command +
						` error="not run: python3 or the kit failed above" — fix: ` + tracerFixSmokeNotRun,
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
			rows: func(command, _, python string) []string {
				return []string{
					tracerIntakeRow + ` FAIL need=python3 checked=` + python + ` --version` +
						` error="broken: exit status 1 raw=\"boom\"" — fix: ` + tracerFixPythonText,
					tracerIntakeRow + ` FAIL need=smoke checked=` + command +
						` error="not run: python3 or the kit failed above" — fix: ` + tracerFixSmokeNotRun,
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
			rows: func(command, _, _ string) []string {
				return []string{
					tracerIntakeRow + ` FAIL need=python3 checked=` + tracerPythonChecked +
						` error="broken: exec: \"python3\": cannot run executable found relative to current directory"` +
						` — fix: ` + tracerFixPythonText,
					tracerIntakeRow + ` FAIL need=smoke checked=` + command +
						` error="not run: python3 or the kit failed above" — fix: ` + tracerFixSmokeNotRun,
				}
			},
			code: 3, failureCount: "2",
		},
		{
			name:  "kit file absent from its linked directory",
			stage: func(t *testing.T, home string) { replaceKitLink(t, home, "") },
			rows: func(command, kit, _ string) []string {
				return []string{
					tracerIntakeRow + ` FAIL need=kit checked=` + kit +
						` error="stat ` + kit + `: no such file or directory" — fix: ` + tracerFixKitText,
					tracerIntakeRow + ` FAIL need=smoke checked=` + command +
						` error="not run: python3 or the kit failed above" — fix: ` + tracerFixSmokeNotRun,
				}
			},
			code: 3, failureCount: "2",
		},
		{
			name: "kit placed then its source removed leaves a dangling link",
			stage: func(t *testing.T, home string) {
				link := filepath.Join(home, ".claude", "skills", "tracetree")
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(home, "removed-kit"), link); err != nil {
					t.Fatal(err)
				}
			},
			rows: func(command, kit, _ string) []string {
				return []string{
					tracerIntakeRow + ` FAIL need=kit checked=` + kit +
						` error="stat ` + kit + `: no such file or directory" — fix: ` + tracerFixKitText,
					tracerIntakeRow + ` FAIL need=smoke checked=` + command +
						` error="not run: python3 or the kit failed above" — fix: ` + tracerFixSmokeNotRun,
				}
			},
			code: 3, failureCount: "2",
		},
		{
			name:  "kit present but failing to import",
			stage: func(t *testing.T, home string) { replaceKitLink(t, home, "def (:\n") },
			rows: func(command, kit, _ string) []string {
				row := func(need string) string {
					return tracerIntakeRow + ` FAIL need=` + need + ` checked=` + command +
						` stderr="File \"` + kit + `\", line 1" — fix: ` + tracerFixRelinkText
				}
				return []string{row("smoke-passthrough"), row("smoke-tracer")}
			},
			code: 3, failureCount: "2",
		},
		{
			name:  "hook command exiting non-zero",
			stage: func(t *testing.T, home string) { replaceKitLink(t, home, "import sys\nsys.exit(3)\n") },
			rows: func(command, kit, _ string) []string {
				row := func(need string) string {
					return tracerIntakeRow + ` FAIL need=` + need + ` checked=` + command +
						` stderr="pfm tracer-intake hook failed, the spawn goes on without a run: python3 ` + kit +
						` intake exited 3" — fix: ` + tracerFixRelinkText
				}
				return []string{row("smoke-passthrough"), row("smoke-tracer")}
			},
			code: 3, failureCount: "2",
		},
		{
			name:  "tracer payload output carrying no RUN line",
			stage: func(t *testing.T, home string) { replaceKitLink(t, home, noRunKit) },
			rows: func(command, _, _ string) []string {
				return []string{
					tracerIntakeRow + ` FAIL need=smoke-tracer checked=` + command +
						` error="updatedInput prompt carries no RUN: line: ` +
						`{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"additionalContext\":\"x\"}}"` +
						` — fix: the kit opened no run: check that /tmp is writable, then run pfm install --yes`,
				}
			},
			code: 3, failureCount: "1",
		},
		{
			name: "kit not linked yet on a fresh home",
			stage: func(t *testing.T, home string) {
				if err := os.Remove(filepath.Join(home, ".claude", "skills", "tracetree")); err != nil {
					t.Fatal(err)
				}
			},
			rows: func(_, _, _ string) []string { return []string{tracerPendingRow} },
			code: 0,
		},
		{
			name: "kit not linked yet and python3 absent",
			stage: func(t *testing.T, home string) {
				if err := os.Remove(filepath.Join(home, ".claude", "skills", "tracetree")); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", filepath.Join(home, ".local", "bin"))
			},
			rows: func(_, _, _ string) []string {
				return []string{
					tracerIntakeRow + ` FAIL need=python3 checked=` + tracerPythonChecked +
						` error="` + tracerPythonMissing + `" — fix: ` + tracerFixPythonText,
					tracerPendingRow,
				}
			},
			code: 3, failureCount: "1",
		},
		{
			name: "kit linked elsewhere than the hook reads",
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
			rows: func(command, kit, _ string) []string {
				elsewhere := filepath.Join(strings.TrimSuffix(kit, "/.claude/skills/tracetree/tracetree.py"), "elsewhere", "skills", "tracetree")
				return []string{
					tracerIntakeRow + ` FAIL need=kit checked=` + kit + ` error="the install linked the tracetree kit at ` +
						elsewhere + `, but the tracer and its hook read ` + filepath.Dir(kit) +
						`; the tracer cannot run on this host" — fix: pfm install --yes does not fix this: ` +
						`link the kit at the hook's path, or install without --config-dir`,
					tracerIntakeRow + ` FAIL need=smoke checked=` + command +
						` error="not run: python3 or the kit failed above" — fix: ` + tracerFixSmokeNotRun,
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
			rows: func(_, _, _ string) []string { return []string{tracerPendingRow} },
			code: 0,
		},
		{
			name: "install records corrupt",
			stage: func(t *testing.T, home string) {
				removeKitLink(t, home)
				writeSkillLinkLedger(t, home, "{not json")
			},
			rows: func(command, kit, _ string) []string {
				ledger := filepath.Join(strings.TrimSuffix(kit, "/.claude/skills/tracetree/tracetree.py"),
					".local", "share", "pfm", "install", "skill-links.json")
				return []string{
					tracerIntakeRow + ` FAIL need=kit checked=` + ledger + ` error="skill link ledger ` + ledger +
						`: invalid character 'n' looking for beginning of object key string" — fix: repair or delete ` +
						ledger + `, then run pfm install --yes`,
					tracerIntakeRow + ` FAIL need=smoke checked=` + command +
						` error="not run: python3 or the kit failed above" — fix: ` + tracerFixSmokeNotRun,
				}
			},
			// The existing skill-source row fails on the same file: one failure beside the two tracer-intake rows.
			code: 3, failureCount: "3",
		},
		{
			name:  "healthy",
			stage: func(*testing.T, string) {},
			rows: func(_, kit, python string) []string {
				return []string{tracerIntakeRow + ` ok python3=` + python + ` version=VERSION kit=` + kit + ` smoke passed`}
			},
			code: 0,
		},
		{
			// The kit reads the brief's Repo root up to the first space, quote or
			// bracket: a TMPDIR holding one must not make a healthy host FAIL.
			name: "healthy with a TMPDIR holding a space",
			stage: func(t *testing.T, _ string) {
				spaced := filepath.Join(os.Getenv("TMPDIR"), "temp dir")
				if err := os.Mkdir(spaced, 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("TMPDIR", spaced)
			},
			rows: func(_, kit, python string) []string {
				return []string{tracerIntakeRow + ` ok python3=` + python + ` version=VERSION kit=` + kit + ` smoke passed`}
			},
			code: 0,
		},
		{
			// Python's \s, which the kit's parser uses, is Unicode whitespace.
			name: "healthy with a TMPDIR holding a no-break space",
			stage: func(t *testing.T, _ string) {
				spaced := filepath.Join(os.Getenv("TMPDIR"), "temp"+"\u00a0"+"dir")
				if err := os.Mkdir(spaced, 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("TMPDIR", spaced)
			},
			rows: func(_, kit, python string) []string {
				return []string{tracerIntakeRow + ` ok python3=` + python + ` version=VERSION kit=` + kit + ` smoke passed`}
			},
			code: 0,
		},
		{
			// The kit names a run's /tmp/{project} after the git repository its
			// root sits in: a TMPDIR inside a checkout, or an inherited GIT_DIR,
			// must not leave a real project's scratch directory behind.
			name: "healthy with a TMPDIR inside a git checkout and GIT_DIR set",
			stage: func(t *testing.T, _ string) {
				repo, err := os.MkdirTemp(os.Getenv("TMPDIR"), "checkout-")
				if err != nil {
					t.Fatal(err)
				}
				stageBareGitDir(t, filepath.Join(repo, ".git"))
				inside := filepath.Join(repo, "tmp")
				if err := os.Mkdir(inside, 0o700); err != nil {
					t.Fatal(err)
				}
				// A jail home's PATH has no git; a real host's has, and the kit asks it.
				if gitErr != nil {
					t.Fatal(gitErr)
				}
				t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+filepath.Dir(git))
				t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
				t.Setenv("TMPDIR", inside)
			},
			rows: func(_, kit, python string) []string {
				return []string{tracerIntakeRow + ` ok python3=` + python + ` version=VERSION kit=` + kit + ` smoke passed`}
			},
			code: 0,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			home := runtime.Paths.Home
			scratch := t.TempDir()
			t.Setenv("TMPDIR", scratch)
			scenario.stage(t, home)
			python, _ := exec.LookPath("python3")
			hook, kit, found := claudelaunch.TracerIntakeHook(home)
			if !found {
				t.Fatal("no tracer-intake row in the hook registry")
			}
			recorder := &recordingRunner{Runner: deps.RealRunner{}}
			dependencies := testDependencies()
			dependencies.Runner = recorder
			var stdout, stderr bytes.Buffer
			code := Run(nil, &stdout, &stderr, runtime, dependencies)

			rows := tracerIntakeRows(stdout.String())
			want := scenario.rows(hook.Command, kit, python)
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

			smokes := 0
			for _, argv := range recorder.argv {
				if len(argv) == 3 && argv[0] == "/bin/sh" && argv[1] == "-c" {
					if argv[2] != hook.Command {
						t.Errorf("sh -c ran %q, want the registry's tracer-intake command %q", argv[2], hook.Command)
					}
					smokes++
				}
			}
			entries, err := os.ReadDir(os.Getenv("TMPDIR"))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("the smoke left %d entries in TMPDIR: %v", len(entries), entries)
			}
			if strings.HasPrefix(scenario.name, "healthy") {
				assertHealthySmokeCleaned(t, smokes, recorder)
			}
		})
	}
}

// stageBareGitDir writes the smallest directory git accepts as a repository.
func stageBareGitDir(t *testing.T, dir string) {
	t.Helper()
	for _, sub := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// assertHealthySmokeCleaned proves a healthy host ran both smokes and that the
// run the kit opened, and its /tmp/{project} parent, are gone afterwards.
func assertHealthySmokeCleaned(t *testing.T, smokes int, recorder *recordingRunner) {
	t.Helper()
	if smokes != 2 {
		t.Fatalf("sh -c ran %d times, want 2 (pass-through, tracer)", smokes)
	}
	var run string
	for _, output := range recorder.outputs {
		if match := regexp.MustCompile(`RUN: (/tmp/\S+/tracer/\S+?)\\n`).FindSubmatch(output); match != nil {
			run = string(match[1])
		}
	}
	if run == "" {
		t.Fatalf("no tracer run in the recorded outputs: %q", recorder.outputs)
	}
	if _, err := os.Stat(run); !os.IsNotExist(err) {
		t.Errorf("run directory %s remains (stat err=%v)", run, err)
	}
	if parent := filepath.Dir(filepath.Dir(run)); parent != "/tmp" {
		if _, err := os.Stat(parent); !os.IsNotExist(err) {
			t.Errorf("project directory %s remains (stat err=%v)", parent, err)
		}
	}
}

// TestTracerIntakeDoctorCouldNotRun pins R3's rule that a check which could not
// run is a FAIL naming why, never ok and never absent: a hook that outruns the
// bound, and a temporary directory the smoke cannot create.
func TestTracerIntakeDoctorCouldNotRun(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		stage func(t *testing.T, home, scratch string)
		rows  func(command string) *regexp.Regexp
	}{
		{
			name: "hook outrunning the bound",
			stage: func(t *testing.T, home, _ string) {
				replaceKitLink(t, home, "import time\ntime.sleep(60)\n")
				previous := tracerIntakeBound
				tracerIntakeBound = time.Second
				t.Cleanup(func() { tracerIntakeBound = previous })
			},
			rows: func(command string) *regexp.Regexp {
				row := func(need string) string {
					return regexp.QuoteMeta(tracerIntakeRow+" FAIL need="+need+" checked="+command+
						` error="timed out after 1s" — fix: `+tracerFixRelinkText) + "\n"
				}
				return regexp.MustCompile(row("smoke-passthrough") + row("smoke-tracer"))
			},
		},
		{
			name: "temporary directory that cannot be created",
			stage: func(t *testing.T, _, scratch string) {
				t.Setenv("TMPDIR", filepath.Join(scratch, "missing"))
			},
			rows: func(string) *regexp.Regexp {
				return regexp.MustCompile(regexp.QuoteMeta(tracerIntakeRow+` FAIL need=smoke checked=temporary directory `) +
					`error="(stat|mkdir) \S*missing\S*: no such file or directory" — fix: ` +
					regexp.QuoteMeta("make the temporary directory writable, then rerun pfm doctor") + "\n")
			},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			scratch := t.TempDir()
			t.Setenv("TMPDIR", scratch)
			scenario.stage(t, runtime.Paths.Home, scratch)
			hook, _, _ := claudelaunch.TracerIntakeHook(runtime.Paths.Home)
			var stdout, stderr bytes.Buffer
			code := Run(nil, &stdout, &stderr, runtime, testDependencies())
			rows := strings.Join(tracerIntakeRows(stdout.String()), "\n") + "\n"
			if pattern := scenario.rows(hook.Command); !pattern.MatchString(rows) {
				t.Fatalf("rows:\n%s\nwant a match of %s", rows, pattern)
			}
			if code != 3 {
				t.Fatalf("exit code=%d, want 3; stdout:\n%s", code, stdout.String())
			}
		})
	}
}
