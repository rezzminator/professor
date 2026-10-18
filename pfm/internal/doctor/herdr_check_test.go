package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const (
	herdrStatusCommand = "herdr integration status"
	// herdrStatusHeader and herdrStatusTail are the other engines' lines of
	// `herdr integration status` as Herdr prints them: the check judges only
	// claude, codex and opencode and must not trip over the rest.
	herdrStatusHeader = "pi: not installed (/h/.pi/agent/extensions/herdr-agent-state.ts)\n"
	herdrStatusTail   = "copilot: current (v4) (/h/.copilot/hooks/herdr-agent-state.sh)\n" +
		"letta (experimental): not installed (/h/.letta/hooks/herdr-agent-session.sh)\n"
	herdrOKRow = herdrIntegrationRow + " ok claude, codex, opencode: no bundled Herdr integration installed"
)

// herdrStatus renders `herdr integration status` for the three judged engines'
// states (the part after "{engine}: ").
func herdrStatus(claude, codex, opencode string) string {
	return herdrStatusHeader +
		"claude: " + claude + "\n" + "codex: " + codex + "\n" + herdrStatusTail + "opencode: " + opencode + "\n"
}

func herdrFailRow(engine, state string) string {
	return herdrIntegrationRow + " FAIL need=no bundled " + engine + " integration checked=" + herdrStatusCommand +
		` state="` + state + `" — fix: herdr integration uninstall ` + engine + "\n" +
		"doctor: herdr integration " + engine + " installed — a second reporter on pfm panes; run herdr integration uninstall " + engine
}

const (
	notInstalledClaude   = "not installed (/h/.cc/hooks/herdr-agent-state.sh)"
	notInstalledCodex    = "not installed (/h/.codex/herdr-agent-state.sh)"
	notInstalledOpenCode = "not installed (/h/.config/opencode/plugins/herdr-agent-state.js)"
	currentClaude        = "current (v7) (/h/.cc/hooks/herdr-agent-state.sh)"
	currentCodex         = "current (v10) (/h/.codex/herdr-agent-state.sh)"
	outdatedOpenCode     = "outdated (v9) (/h/.config/opencode/plugins/herdr-agent-state.js)"
)

// TestHerdrIntegrationDoctor is the check's table: every verdict for each of
// the three engines, from a faked `herdr integration status`, asserting the
// rendered rows and the counts doctor adds to its exit.
func TestHerdrIntegrationDoctor(t *testing.T) {
	const herdrPath = "/opt/herdr/bin/herdr"
	status := func(out string, code int, stderr string, err error) func(*deps.FakeRunner) {
		return func(runner *deps.FakeRunner) {
			runner.ScriptLookPath("herdr", herdrPath, nil)
			runner.Script(
				[]string{herdrPath, "integration", "status"},
				deps.RunResult{Stdout: []byte(out), Stderr: []byte(stderr), ExitCode: code}, err,
			)
		}
	}
	for _, scenario := range []struct {
		name         string
		script       func(*deps.FakeRunner)
		binEnv       string
		wantRows     []string
		wantWarnings int
		wantFailures int
		wantArgv0    string
	}{
		{
			name:      "herdr not installed",
			script:    func(*deps.FakeRunner) {},
			wantRows:  []string{herdrIntegrationRow + " ok n/a (herdr not installed)"},
			wantArgv0: "",
		},
		{
			name:      "none of the three installed",
			script:    status(herdrStatus(notInstalledClaude, notInstalledCodex, notInstalledOpenCode), 0, "", nil),
			wantRows:  []string{herdrOKRow},
			wantArgv0: herdrPath,
		},
		{
			name:         "claude installed",
			script:       status(herdrStatus(currentClaude, notInstalledCodex, notInstalledOpenCode), 0, "", nil),
			wantRows:     []string{herdrFailRow("claude", currentClaude)},
			wantFailures: 1,
			wantArgv0:    herdrPath,
		},
		{
			// A second claude line that is installed is never shadowed by an
			// earlier "not installed" one.
			name: "claude installed on a later line",
			script: status(
				"claude: "+notInstalledClaude+"\n"+herdrStatus(currentClaude, notInstalledCodex, notInstalledOpenCode),
				0, "", nil,
			),
			wantRows:     []string{herdrFailRow("claude", currentClaude)},
			wantFailures: 1,
			wantArgv0:    herdrPath,
		},
		{
			name:         "codex installed",
			script:       status(herdrStatus(notInstalledClaude, currentCodex, notInstalledOpenCode), 0, "", nil),
			wantRows:     []string{herdrFailRow("codex", currentCodex)},
			wantFailures: 1,
			wantArgv0:    herdrPath,
		},
		{
			name:         "opencode outdated counts as installed",
			script:       status(herdrStatus(notInstalledClaude, notInstalledCodex, outdatedOpenCode), 0, "", nil),
			wantRows:     []string{herdrFailRow("opencode", outdatedOpenCode)},
			wantFailures: 1,
			wantArgv0:    herdrPath,
		},
		{
			name:   "all three installed",
			script: status(herdrStatus(currentClaude, currentCodex, outdatedOpenCode), 0, "", nil),
			wantRows: []string{
				herdrFailRow("claude", currentClaude),
				herdrFailRow("codex", currentCodex),
				herdrFailRow("opencode", outdatedOpenCode),
			},
			wantFailures: 3,
			wantArgv0:    herdrPath,
		},
		{
			name:   "HERDR_BIN_PATH wins over PATH",
			binEnv: "/pane/herdr",
			script: func(runner *deps.FakeRunner) {
				runner.ScriptLookPath("/pane/herdr", "/pane/herdr", nil)
				runner.Script(
					[]string{"/pane/herdr", "integration", "status"},
					deps.RunResult{Stdout: []byte(herdrStatus(currentClaude, notInstalledCodex, notInstalledOpenCode))}, nil,
				)
			},
			wantRows:     []string{herdrFailRow("claude", currentClaude)},
			wantFailures: 1,
			wantArgv0:    "/pane/herdr",
		},
		{
			name:   "HERDR_BIN_PATH not executable",
			binEnv: "/pane/gone",
			script: func(runner *deps.FakeRunner) {
				runner.ScriptLookPath("/pane/gone", "", errors.New("stat /pane/gone: no such file or directory"))
			},
			wantRows: []string{
				herdrIntegrationRow + ` WARN need=herdr binary checked=HERDR_BIN_PATH=/pane/gone ` +
					`error="stat /pane/gone: no such file or directory" — fix: unset HERDR_BIN_PATH or point it at ` +
					`the herdr executable, then rerun pfm doctor`,
			},
			wantWarnings: 1,
		},
		{
			name: "herdr lookup failing past not found",
			script: func(runner *deps.FakeRunner) {
				runner.ScriptLookPath("herdr", "", errors.New(`exec: "herdr": cannot run executable found relative to current directory`))
			},
			wantRows: []string{
				herdrIntegrationRow + ` WARN need=herdr binary checked=herdr on doctor's PATH ` +
					`error="exec: \"herdr\": cannot run executable found relative to current directory" — fix: ` +
					`remove the relative entry from PATH, then rerun pfm doctor`,
			},
			wantWarnings: 1,
		},
		{
			name:   "status exits non-zero",
			script: status("", 2, "error: socket gone\nmore", nil),
			wantRows: []string{
				herdrIntegrationRow + ` WARN need=integration status checked=` + herdrStatusCommand +
					` exit=2 stderr="error: socket gone" — fix: run ` + herdrStatusCommand +
					` by hand and repair what it reports, then rerun pfm doctor`,
			},
			wantWarnings: 1,
			wantArgv0:    herdrPath,
		},
		{
			name:   "status cannot run",
			script: status("", 0, "", errors.New("fork/exec "+herdrPath+": permission denied")),
			wantRows: []string{
				herdrIntegrationRow + ` WARN need=integration status checked=` + herdrStatusCommand +
					` error="fork/exec ` + herdrPath + `: permission denied" — fix: run ` + herdrStatusCommand +
					` by hand and repair what it reports, then rerun pfm doctor`,
			},
			wantWarnings: 1,
			wantArgv0:    herdrPath,
		},
		{
			name:   "status output empty",
			script: status("", 0, "", nil),
			wantRows: []string{
				herdrIntegrationRow + ` WARN need=integration status checked=` + herdrStatusCommand +
					` error="no line for claude, codex, opencode in the output" — fix: run ` + herdrStatusCommand +
					` by hand and repair what it reports, then rerun pfm doctor`,
			},
			wantWarnings: 1,
			wantArgv0:    herdrPath,
		},
		{
			name:   "status output missing one engine's line",
			script: status("claude: "+notInstalledClaude+"\nopencode: "+notInstalledOpenCode+"\n", 0, "", nil),
			wantRows: []string{
				herdrIntegrationRow + ` WARN need=integration status checked=` + herdrStatusCommand +
					` error="no line for codex in the output" — fix: run ` + herdrStatusCommand +
					` by hand and repair what it reports, then rerun pfm doctor`,
			},
			wantWarnings: 1,
			wantArgv0:    herdrPath,
		},
		{
			name:   "an installed engine is reported beside an unparseable one",
			script: status("claude: "+currentClaude+"\nopencode: "+notInstalledOpenCode+"\n", 0, "", nil),
			wantRows: []string{
				herdrFailRow("claude", currentClaude),
				herdrIntegrationRow + ` WARN need=integration status checked=` + herdrStatusCommand +
					` error="no line for codex in the output" — fix: run ` + herdrStatusCommand +
					` by hand and repair what it reports, then rerun pfm doctor`,
			},
			wantWarnings: 1,
			wantFailures: 1,
			wantArgv0:    herdrPath,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runner := &deps.FakeRunner{}
			scenario.script(runner)
			env := &paths.MapEnv{Values: map[string]string{}}
			if scenario.binEnv != "" {
				env.Values["HERDR_BIN_PATH"] = scenario.binEnv
			}
			dependencies := Dependencies{Runner: runner, Env: env}
			var stdout bytes.Buffer
			warnings, failures := printHerdrDoctor(context.Background(), &stdout, dependencies)

			got := strings.TrimRight(stdout.String(), "\n")
			if want := strings.Join(scenario.wantRows, "\n"); got != want {
				t.Fatalf("rows:\n%s\nwant:\n%s", got, want)
			}
			if warnings != scenario.wantWarnings || failures != scenario.wantFailures {
				t.Fatalf("warnings=%d failures=%d, want %d and %d",
					warnings, failures, scenario.wantWarnings, scenario.wantFailures)
			}
			calls := runner.Calls()
			if scenario.wantArgv0 == "" {
				if len(calls) != 0 {
					t.Fatalf("ran %v with no herdr to run", calls)
				}
				return
			}
			if len(calls) != 1 || strings.Join(calls[0].Argv, " ") != scenario.wantArgv0+" integration status" {
				t.Fatalf("ran %v, want one %s integration status", calls, scenario.wantArgv0)
			}
		})
	}
}

// TestHerdrIntegrationDoctorExit runs the whole doctor against a stub herdr
// the HERDR_BIN_PATH names: an installed bundled integration raises doctor's
// failure exit and summary, and the stub's healthy status leaves it clean.
func TestHerdrIntegrationDoctorExit(t *testing.T) {
	healthy := herdrStatus(notInstalledClaude, notInstalledCodex, notInstalledOpenCode)
	for _, scenario := range []struct {
		name    string
		status  string
		code    int
		rows    []string
		summary string
	}{
		{
			name: "healthy", status: healthy, code: 0, rows: []string{herdrOKRow}, summary: "doctor: clean\n",
		},
		{
			name:    "codex installed",
			status:  herdrStatus(notInstalledClaude, currentCodex, notInstalledOpenCode),
			code:    3,
			rows:    []string{herdrFailRow("codex", currentCodex)},
			summary: "doctor: failures=1\n",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			stub := filepath.Join(t.TempDir(), "herdr")
			script := "#!/bin/sh\n[ \"$*\" = \"integration status\" ] || exit 9\nprintf '%s' '" + scenario.status + "'\n"
			if err := testjail.WriteExecutable(stub, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_BIN_PATH", stub)
			t.Setenv(herdr.EnvSocket, serveHerdrDoctor(t, `{"type":"pong","version":"0.9.5","protocol":22}`))
			t.Setenv("HERDR_CONFIG_PATH", filepath.Join(t.TempDir(), "config.toml"))
			var stdout, stderr bytes.Buffer
			code := Run(nil, &stdout, &stderr, runtime, testDependencies())

			var rows []string
			for _, line := range strings.Split(stdout.String(), "\n") {
				if strings.HasPrefix(line, herdrIntegrationRow) ||
					strings.HasPrefix(line, "doctor: herdr integration ") {
					rows = append(rows, line)
				}
			}
			if strings.Join(rows, "\n") != strings.Join(scenario.rows, "\n") {
				t.Fatalf(
					"rows:\n%s\nwant:\n%s\nstdout:\n%s",
					strings.Join(rows, "\n"),
					strings.Join(scenario.rows, "\n"),
					stdout.String(),
				)
			}
			if code != scenario.code || !strings.Contains(stdout.String(), scenario.summary) {
				t.Fatalf("code=%d want %d, summary %q; stdout:\n%s\nstderr: %s",
					code, scenario.code, scenario.summary, stdout.String(), stderr.String())
			}
		})
	}
}

func herdrReporterWarnRow(dir string, count int, newest, session, at string) string {
	return fmt.Sprintf(
		"%s WARN need=no reporter errors in the last 24h checked=%s count=%d newest=%q session=%s at=%s — "+
			"fix: read %s and repair what it reports; the row clears 24h after the last error",
		herdrReporterRow, dir, count, newest, session, at, filepath.Join(dir, session+herdr.ErrorLogSuffix),
	)
}

// TestHerdrReporterErrorsDoctor is the row's table: every state of the log
// directory, rendered from a log seeded through herdr.AppendError, with the
// counts doctor adds to its exit.
func TestHerdrReporterErrorsDoctor(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	logLine := func(session string, at time.Time, message string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			t.Helper()
			if err := herdr.AppendError(dir, session, at, message); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed := func(steps ...func(t *testing.T, dir string)) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			t.Helper()
			for _, step := range steps {
				step(t, dir)
			}
		}
	}
	for _, scenario := range []struct {
		name         string
		seed         func(t *testing.T, dir string)
		wantRow      func(t *testing.T, dir string) string
		wantWarnings int
	}{
		{
			name:    "missing log directory",
			seed:    func(*testing.T, string) {},
			wantRow: func(*testing.T, string) string { return herdrReporterRow + " ok none in the last 24h" },
		},
		{
			name: "empty log directory",
			seed: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantRow: func(*testing.T, string) string { return herdrReporterRow + " ok none in the last 24h" },
		},
		{
			name:    "only errors older than 24h",
			seed:    logLine("cc-1", now.Add(-25*time.Hour), "lock timed out"),
			wantRow: func(*testing.T, string) string { return herdrReporterRow + " ok none in the last 24h" },
		},
		{
			name: "one error",
			seed: logLine("cc-1", now.Add(-time.Hour), "name pane w1:p1: herdr refused code 7"),
			wantRow: func(_ *testing.T, dir string) string {
				return herdrReporterWarnRow(dir, 1, "name pane w1:p1: herdr refused code 7", "cc-1", "2026-10-16T11:00:00Z")
			},
			wantWarnings: 1,
		},
		{
			name: "many errors across sessions name the newest and count all",
			seed: seed(
				logLine("cc-1", now.Add(-23*time.Hour), "first"),
				logLine("cc-2", now.Add(-time.Minute), "newest\nmessage"),
				logLine("cc-1", now.Add(-time.Hour), "middle"),
				logLine("cc-3", now.Add(-48*time.Hour), "expired"),
			),
			wantRow: func(_ *testing.T, dir string) string {
				return herdrReporterWarnRow(dir, 3, "newest message", "cc-2", "2026-10-16T11:59:00Z")
			},
			wantWarnings: 1,
		},
		{
			name: "malformed line is named, never read as none",
			seed: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "cc-1.log"), []byte("garbage\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantRow: func(_ *testing.T, dir string) string {
				return fmt.Sprintf(
					"%s WARN need=readable reporter error log checked=%s error=%q — "+
						"fix: repair or delete the log the error names, then rerun pfm doctor",
					herdrReporterRow, dir,
					fmt.Sprintf("herdr error log: %s holds a malformed line %q", filepath.Join(dir, "cc-1.log"), "garbage"),
				)
			},
			wantWarnings: 1,
		},
		{
			name: "log directory is a file",
			seed: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dir, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantRow: func(t *testing.T, dir string) string {
				t.Helper()
				_, err := herdr.RecentErrors(dir, time.Time{})
				if err == nil || !strings.HasPrefix(err.Error(), "herdr error log: read "+dir+": ") {
					t.Fatalf("RecentErrors over a file = %v, want a read error naming %s", err, dir)
				}
				return fmt.Sprintf(
					"%s WARN need=readable reporter error log checked=%s error=%q — "+
						"fix: repair or delete the log the error names, then rerun pfm doctor",
					herdrReporterRow, dir, err.Error(),
				)
			},
			wantWarnings: 1,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "herdr")
			scenario.seed(t, dir)
			var stdout bytes.Buffer
			warnings := printHerdrReporterErrors(&stdout, dir, now)
			if got, want := strings.TrimRight(stdout.String(), "\n"), scenario.wantRow(t, dir); got != want {
				t.Fatalf("row:\n%s\nwant:\n%s", got, want)
			}
			if warnings != scenario.wantWarnings {
				t.Fatalf("warnings=%d, want %d", warnings, scenario.wantWarnings)
			}
		})
	}
}

// TestHerdrReporterErrorsDoctorExit runs the whole doctor over a home whose
// log directory holds a fresh reporter error: the row prints from the home's
// own log directory and raises doctor's warning exit.
func TestHerdrReporterErrorsDoctorExit(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	dir := paths.HerdrLockDir(runtime.Paths.Home)
	if err := herdr.AppendError(dir, "cc-9", time.Now(), "agent.rename refused"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr, runtime, testDependencies())
	var rows []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, herdrReporterRow) {
			rows = append(rows, line)
		}
	}
	if len(rows) != 1 || !strings.Contains(rows[0], " WARN ") ||
		!strings.Contains(rows[0], `newest="agent.rename refused" session=cc-9`) ||
		!strings.Contains(rows[0], "checked="+dir+" ") {
		t.Fatalf("reporter error rows = %q; stdout:\n%s", rows, stdout.String())
	}
	if code != 1 || !strings.Contains(stdout.String(), "doctor: warnings=1\n") {
		t.Fatalf("code=%d want 1, stdout:\n%s\nstderr: %s", code, stdout.String(), stderr.String())
	}
}
