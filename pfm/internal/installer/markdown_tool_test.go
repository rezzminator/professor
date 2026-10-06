package installer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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

// poisonScript writes a fixture that records it ran (by touching a marker
// file under $POISON_MARKER_DIR) and then fails loudly — used on branches
// that must never invoke the tool at all. The marker travels through the
// environment rather than through $0: installMarkdownTool invokes uv by the
// bare name "uv" on its no-provisioned-uv fallback, and exec.Command leaves
// Args[0] as the unqualified name it was given even though it resolves Path
// through $PATH — so "$(dirname "$0")" resolves to the test binary's own
// working directory, not the fixture's, and a poison that fired would go
// undetected.
func poisonScript(t *testing.T, dir, marker, name string) {
	t.Helper()
	writeScript(
		t,
		dir,
		name,
		"#!/bin/sh\ntouch \"$POISON_MARKER_DIR/"+name+".ran\"\necho poisoned-"+name+" invoked >&2\nexit 1\n",
	)
	t.Setenv("POISON_MARKER_DIR", marker)
}

func assertNeverRan(t *testing.T, marker, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(marker, name+".ran")); err == nil {
		t.Fatalf("%s ran but this branch must never invoke it", name)
	}
}

// TestInstallMarkdownToolAlreadyPresentIsANoOp pins the fast path: a rumdl on
// PATH at or above the pinned MinVersion is reported present and uv is never
// touched — the poisoned uv fixture proves it.
func TestInstallMarkdownToolAlreadyPresentIsANoOp(t *testing.T) {
	for _, test := range []struct {
		name           string
		installed      string
		wantOKContains string
	}{
		{name: "exactly pinned", installed: "0.2.73", wantOKContains: "rumdl already present (0.2.73)"},
		{name: "newer than pinned", installed: "0.3.0", wantOKContains: "rumdl already present (0.3.0)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := t.TempDir()
			writeScript(t, dir, "rumdl", "#!/bin/sh\necho 'rumdl "+test.installed+"'\n")
			poisonScript(t, dir, marker, "uv")
			t.Setenv("PATH", dir)
			home := t.TempDir()

			var output bytes.Buffer
			eng := &engine{options: Options{Home: home, Env: &paths.MapEnv{}, Stdout: &output}, apply: true}
			if err := eng.installMarkdownTool(context.Background()); err != nil {
				t.Fatalf("installMarkdownTool: %v\n%s", err, output.String())
			}
			if !strings.Contains(output.String(), test.wantOKContains) {
				t.Fatalf("output=%q, want to contain %q", output.String(), test.wantOKContains)
			}
			// Changed=1 is the rumdl user config, written on this path too.
			if eng.report.OK != 1 || eng.report.Skipped != 0 || eng.report.Changed != 1 {
				t.Fatalf("report=%+v, want OK=1 Skipped=0 Changed=1", eng.report)
			}
			assertNeverRan(t, marker, "uv")
		})
	}
}

func TestInstallMarkdownToolUsesInjectedProcessRunner(t *testing.T) {
	t.Parallel()
	runner := &deps.FakeRunner{}
	runner.ScriptLookPath("rumdl", "/fixture/rumdl", nil)
	runner.Script([]string{"/fixture/rumdl", "--version"}, deps.RunResult{
		Stdout:   []byte("rumdl 0.2.73\n"),
		ExitCode: 0,
	}, nil)
	var output bytes.Buffer
	eng := &engine{options: Options{
		Home:          t.TempDir(),
		Env:           &paths.MapEnv{},
		Stdout:        &output,
		ProcessRunner: runner,
	}, apply: true}

	if err := eng.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "rumdl already present (0.2.73)") {
		t.Fatalf("output=%q, want injected runner's rumdl version", output.String())
	}
	if eng.report.OK != 1 || eng.report.Skipped != 0 {
		t.Fatalf("report=%+v, want OK=1 Skipped=0", eng.report)
	}
	if calls := runner.Calls(); len(calls) != 1 || len(calls[0].Argv) != 2 || calls[0].Argv[0] != "/fixture/rumdl" {
		t.Fatalf("runner calls=%+v, want only injected rumdl --version", calls)
	}
}

// TestInstallMarkdownToolStaleVersionFallsThroughPastAlreadyPresent pins the
// boundary on the other side of the pin: a rumdl one patch BELOW MinVersion
// must not take the already-present branch — it must fall through to the
// real provisioning decision (here, the offline skip, chosen because it is
// the cheapest way to observe the fallthrough without invoking uv).
func TestInstallMarkdownToolStaleVersionFallsThroughPastAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "rumdl", "#!/bin/sh\necho 'rumdl 0.2.72'\n")
	t.Setenv("PATH", dir)
	home := t.TempDir()

	var output bytes.Buffer
	eng := &engine{
		options: Options{Home: home, Env: &paths.MapEnv{}, Stdout: &output, HarvestOffline: true},
		apply:   true,
	}
	if err := eng.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool: %v\n%s", err, output.String())
	}
	want := "rumdl: offline, will not attempt uv tool install rumdl==0.2.73"
	if !strings.Contains(output.String(), want) {
		t.Fatalf(
			"output=%q, want to contain %q (a stale rumdl must not short-circuit as already-present)",
			output.String(),
			want,
		)
	}
	if eng.report.OK != 0 || eng.report.Skipped != 1 {
		t.Fatalf("report=%+v, want OK=0 Skipped=1", eng.report)
	}
}

// TestInstallMarkdownToolDryRunPlansOnlyAndRunsNothing pins the preview
// branch: a dry run states the plan and returns before touching rumdl or
// uv — both fixtures here are poisoned to prove neither runs.
func TestInstallMarkdownToolDryRunPlansOnlyAndRunsNothing(t *testing.T) {
	dir := t.TempDir()
	marker := t.TempDir()
	poisonScript(t, dir, marker, "rumdl")
	poisonScript(t, dir, marker, "uv")
	t.Setenv("PATH", dir)
	home := t.TempDir()

	var output bytes.Buffer
	eng := &engine{options: Options{Home: home, Env: &paths.MapEnv{}, Stdout: &output}, apply: false}
	if err := eng.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool: %v\n%s", err, output.String())
	}
	want := "rumdl dry-run: would install rumdl==0.2.73 via uv tool install (uv="
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output=%q, want to contain %q", output.String(), want)
	}
	if eng.report.OK != 0 || eng.report.Skipped != 0 || eng.report.Changed != 1 {
		t.Fatalf("dry-run report=%+v, want one planned config change", eng.report)
	}
	assertNeverRan(t, marker, "rumdl")
	assertNeverRan(t, marker, "uv")
	config := filepath.Join(home, ".config", "rumdl", "rumdl.toml")
	if !strings.Contains(output.String(), "  change  write rumdl user config -> "+config+"\n") {
		t.Fatalf("output=%q, want the user config planned", output.String())
	}
	if _, err := os.Lstat(config); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote %s: %v", config, err)
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

// TestInstallMarkdownToolOfflineSkipsWithoutTouchingUV pins the offline
// branch under apply: HarvestOffline must skip before any uv resolution or
// exec is attempted — the poisoned uv fixture proves it never runs.
func TestInstallMarkdownToolOfflineSkipsWithoutTouchingUV(t *testing.T) {
	dir := t.TempDir()
	marker := t.TempDir()
	poisonScript(t, dir, marker, "uv")
	t.Setenv("PATH", dir)
	home := t.TempDir()

	var output bytes.Buffer
	eng := &engine{
		options: Options{Home: home, Env: &paths.MapEnv{}, Stdout: &output, HarvestOffline: true},
		apply:   true,
	}
	if err := eng.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool: %v\n%s", err, output.String())
	}
	want := "rumdl: offline, will not attempt uv tool install rumdl==0.2.73"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output=%q, want to contain %q", output.String(), want)
	}
	if eng.report.Skipped != 1 || eng.report.OK != 0 {
		t.Fatalf("report=%+v, want Skipped=1 OK=0", eng.report)
	}
	assertNeverRan(t, marker, "uv")
}

// TestInstallMarkdownToolNoUVAvailableSkipsWithoutFailingInstall pins the
// "no uv anywhere" terminal: rumdl absent, no provisioned harvestpy uv, and
// no uv on PATH must skip cleanly rather than fail the whole install.
func TestInstallMarkdownToolNoUVAvailableSkipsWithoutFailingInstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir) // empty: no rumdl, no uv anywhere
	home := t.TempDir()

	var output bytes.Buffer
	eng := &engine{options: Options{Home: home, Env: &paths.MapEnv{}, Stdout: &output}, apply: true}
	if err := eng.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool must never fail the install: %v\n%s", err, output.String())
	}
	want := "rumdl: no uv available (checked provisioned harvestpy uv and PATH)"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output=%q, want to contain %q", output.String(), want)
	}
	if eng.report.Skipped != 1 || eng.report.OK != 0 {
		t.Fatalf("report=%+v, want Skipped=1 OK=0", eng.report)
	}
}

// TestInstallMarkdownToolUVExitFailureSkipsWithOutputAndNeverFailsInstall
// pins the "uv ran but failed" terminal: a non-zero uv exit must be reported
// with actionable output, never bubble up as an install failure.
func TestInstallMarkdownToolUVExitFailureSkipsWithOutputAndNeverFailsInstall(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "uv", "#!/bin/sh\necho 'boom: dependency resolution failed' >&2\nexit 1\n")
	t.Setenv("PATH", dir)
	home := t.TempDir()

	var output bytes.Buffer
	eng := &engine{options: Options{Home: home, Env: &paths.MapEnv{}, Stdout: &output}, apply: true}
	if err := eng.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool must never fail the install: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "rumdl: uv tool install rumdl==0.2.73 failed:") {
		t.Fatalf("output=%q, want the failed-install line", output.String())
	}
	if !strings.Contains(output.String(), "boom: dependency resolution failed") {
		t.Fatalf("output=%q, want the uv failure's own output surfaced", output.String())
	}
	if eng.report.Skipped != 1 || eng.report.OK != 0 {
		t.Fatalf("report=%+v, want Skipped=1 OK=0", eng.report)
	}
}

// TestInstallMarkdownToolUVSuccessInstallsAndReportsOK pins the terminal
// success branch: a uv that exits 0 is reported installed, and the tool is
// invoked with UV_TOOL_BIN_DIR pointed at HOME/.local/bin so the binary
// lands where the rest of pfm's provisioned tools do.
func TestInstallMarkdownToolUVSuccessInstallsAndReportsOK(t *testing.T) {
	dir := t.TempDir()
	// The script cannot locate itself through $0 — exec.Command passes the
	// unqualified "uv" as argv[0] (Path is resolved, Args[0] is not), so $0
	// resolves through $PATH at the shell's own hands, not to an absolute
	// path. Marker location travels through the environment instead.
	marker := t.TempDir()
	t.Setenv("MARKER_DIR", marker)
	writeScript(t, dir, "uv", `#!/bin/sh
echo "$UV_TOOL_BIN_DIR" > "$MARKER_DIR/uv.bindir"
echo "$@" > "$MARKER_DIR/uv.args"
exit 0
`)
	t.Setenv("PATH", dir)
	home := t.TempDir()

	var output bytes.Buffer
	eng := &engine{options: Options{Home: home, Env: &paths.MapEnv{}, Stdout: &output}, apply: true}
	if err := eng.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "rumdl installed via uv tool install rumdl==0.2.73") {
		t.Fatalf("output=%q, want the installed line", output.String())
	}
	if eng.report.OK != 1 || eng.report.Skipped != 0 {
		t.Fatalf("report=%+v, want OK=1 Skipped=0", eng.report)
	}
	gotArgs, err := os.ReadFile(filepath.Join(marker, "uv.args"))
	if err != nil || strings.TrimSpace(string(gotArgs)) != "tool install rumdl==0.2.73" {
		t.Fatalf("uv invoked with args=%q, err=%v, want %q", gotArgs, err, "tool install rumdl==0.2.73")
	}
	wantBinDir := filepath.Join(home, ".local", "bin")
	gotBinDir, err := os.ReadFile(filepath.Join(marker, "uv.bindir"))
	if err != nil || strings.TrimSpace(string(gotBinDir)) != wantBinDir {
		t.Fatalf("UV_TOOL_BIN_DIR=%q, err=%v, want %q", gotBinDir, err, wantBinDir)
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

// TestInstallMarkdownToolWritesRumdlUserConfigWhenAbsent pins the stray-cache
// fix: with no user config, install writes one whose [global] cache = false
// makes every config-less rumdl run cache nothing.
func TestInstallMarkdownToolWritesRumdlUserConfigWhenAbsent(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	installer, output := presentRumdlEngine(t, home, &paths.MapEnv{})
	if err := installer.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool: %v\n%s", err, output)
	}
	config := filepath.Join(home, ".config", "rumdl", "rumdl.toml")
	requireRumdlUserConfig(t, config)
	if !strings.Contains(output.String(), "write rumdl user config -> "+config) {
		t.Fatalf("output=%q, want the write reported", output)
	}
	if installer.report.Changed != 1 {
		t.Fatalf("report=%+v, want Changed=1", installer.report)
	}
}

// TestInstallMarkdownToolLeavesPresentRumdlUserConfigUntouched pins that the
// user's own config is the truth: never rewritten or merged.
func TestInstallMarkdownToolLeavesPresentRumdlUserConfigUntouched(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	config := filepath.Join(home, ".config", "rumdl", "rumdl.toml")
	own := []byte("[global]\nline-length = 120\n")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, own, 0o600); err != nil {
		t.Fatal(err)
	}
	installer, output := presentRumdlEngine(t, home, &paths.MapEnv{})
	if err := installer.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool: %v\n%s", err, output)
	}
	got, err := os.ReadFile(config)
	if err != nil || !bytes.Equal(got, own) {
		t.Fatalf("user config = %q, %v; want untouched %q", got, err, own)
	}
	if info, err := os.Stat(config); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("user config mode changed: %v %v", info, err)
	}
	if !strings.Contains(output.String(), "skip    rumdl user config present, left untouched: "+config) {
		t.Fatalf("output=%q, want the skip reported", output)
	}
	if installer.report.Changed != 0 || installer.report.Skipped != 1 {
		t.Fatalf("report=%+v, want Changed=0 Skipped=1", installer.report)
	}
}

// TestInstallMarkdownToolRumdlUserConfigHonoursXDGConfigHome pins rumdl's own
// lookup order: an absolute XDG_CONFIG_HOME wins over HOME/.config.
func TestInstallMarkdownToolRumdlUserConfigHonoursXDGConfigHome(t *testing.T) {
	t.Parallel()
	home, xdg := t.TempDir(), t.TempDir()
	env := &paths.MapEnv{Values: map[string]string{"XDG_CONFIG_HOME": xdg}}
	installer, output := presentRumdlEngine(t, home, env)
	if err := installer.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool: %v\n%s", err, output)
	}
	requireRumdlUserConfig(t, filepath.Join(xdg, "rumdl", "rumdl.toml"))
	if _, err := os.Lstat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
		t.Fatalf("HOME/.config touched despite XDG_CONFIG_HOME: %v", err)
	}
}

// TestInstallMarkdownToolRumdlUserConfigErrorIsReportedNotFatal pins the
// failure surface: a config root that cannot hold the file is a named skip
// carrying the path and the cause, never absence and never a failed install.
func TestInstallMarkdownToolRumdlUserConfigErrorIsReportedNotFatal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	blocker := filepath.Join(home, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &paths.MapEnv{Values: map[string]string{"XDG_CONFIG_HOME": blocker}}
	installer, output := presentRumdlEngine(t, home, env)
	if err := installer.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool must never fail the install: %v\n%s", err, output)
	}
	want := "skip    rumdl user config NOT written: "
	if !strings.Contains(output.String(), want) || !strings.Contains(output.String(), filepath.Join(blocker, "rumdl")) {
		t.Fatalf("output=%q, want %q naming the path", output, want)
	}
	if installer.report.Skipped != 1 || installer.report.Changed != 0 {
		t.Fatalf("report=%+v, want Skipped=1 Changed=0", installer.report)
	}
}
