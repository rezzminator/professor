package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// writeScript writes an executable shell fixture named name inside dir,
// mirroring internal/deps's own writeExecutable pattern but with caller-
// supplied content so a fixture can echo a version, fail loudly, or record
// that it ran.
func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
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
			eng := &engine{options: Options{Home: home, Stdout: &output}, apply: true}
			if err := eng.installMarkdownTool(context.Background()); err != nil {
				t.Fatalf("installMarkdownTool: %v\n%s", err, output.String())
			}
			if !strings.Contains(output.String(), test.wantOKContains) {
				t.Fatalf("output=%q, want to contain %q", output.String(), test.wantOKContains)
			}
			if eng.report.OK != 1 || eng.report.Skipped != 0 || eng.report.Changed != 0 {
				t.Fatalf("report=%+v, want OK=1 Skipped=0 Changed=0", eng.report)
			}
			assertNeverRan(t, marker, "uv")
		})
	}
}

func TestInstallMarkdownToolUsesInjectedProcessRunner(t *testing.T) {
	runner := &deps.FakeRunner{}
	runner.ScriptLookPath("rumdl", "/fixture/rumdl", nil)
	runner.Script([]string{"/fixture/rumdl", "--version"}, deps.RunResult{
		Stdout:   []byte("rumdl 0.2.73\n"),
		ExitCode: 0,
	}, nil)
	var output bytes.Buffer
	eng := &engine{options: Options{
		Home:          t.TempDir(),
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
	eng := &engine{options: Options{Home: home, Stdout: &output, HarvestOffline: true}, apply: true}
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
	eng := &engine{options: Options{Home: home, Stdout: &output}, apply: false}
	if err := eng.installMarkdownTool(context.Background()); err != nil {
		t.Fatalf("installMarkdownTool: %v\n%s", err, output.String())
	}
	want := "rumdl dry-run: would install rumdl==0.2.73 via uv tool install (uv="
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output=%q, want to contain %q", output.String(), want)
	}
	if eng.report.OK != 0 || eng.report.Skipped != 0 || eng.report.Changed != 0 {
		t.Fatalf("dry-run report=%+v, want all zero (a preview never mutates the report)", eng.report)
	}
	assertNeverRan(t, marker, "rumdl")
	assertNeverRan(t, marker, "uv")
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
	eng := &engine{options: Options{Home: home, Stdout: &output, HarvestOffline: true}, apply: true}
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
	eng := &engine{options: Options{Home: home, Stdout: &output}, apply: true}
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
	eng := &engine{options: Options{Home: home, Stdout: &output}, apply: true}
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
	eng := &engine{options: Options{Home: home, Stdout: &output}, apply: true}
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
	if got := truncateOutput([]byte("  boom  \n"), 4096); got != "boom" {
		t.Fatalf("truncateOutput(short) = %q, want %q", got, "boom")
	}
	long := strings.Repeat("x", 100)
	got := truncateOutput([]byte(long), 10)
	if got != long[:10]+"...(truncated)" {
		t.Fatalf("truncateOutput(long) = %q, want a 10-byte prefix plus the truncation marker", got)
	}
}
