package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testjail/fakegit"
)

// buildSourceCloneWithPrePushHook builds a bare git working tree with a
// shipped, executable .githooks/pre-push hook — the fixture the install-time
// arm step (armSourceRepoPrePushGate) is meant to arm. No commit is needed:
// the gate only needs `git -C clone rev-parse --show-toplevel` to succeed and
// .githooks/pre-push to exist as an executable regular file.
func buildSourceCloneWithPrePushHook(t *testing.T) string {
	t.Helper()
	clone := t.TempDir()
	prepushGit.Must(t, clone, "init", "-q")
	hooksDir := filepath.Join(clone, ".githooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(hooksDir, "pre-push")
	if err := testjail.WriteExecutableNeverRun(hook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return clone
}

// gitConfigValue reads a repo-local git config key, treating "unset" (git
// config --get exits 1 with empty output) as "", never as an error.
func gitConfigValue(t *testing.T, repo, key string) string {
	t.Helper()
	result, err := prepushGit.Run(
		context.Background(), []string{"git", "-C", repo, "config", "--get", key}, deps.RunOptions{},
	)
	out := strings.TrimSpace(string(result.Stdout))
	if err == nil && result.ExitCode == 1 && out == "" {
		return ""
	}
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("git -C %s config --get %s: exit %d: %v: %s", repo, key, result.ExitCode, err, result.Stderr)
	}
	return out
}

// prepushGit is the git the pre-push gate tests' clones live in: the fixture
// builds through it and the engine's ProcessRunner reads and arms through it.
var prepushGit = &fakegit.Runner{}

// TestInstallArmsThePrePushGateInTheSourceClone is a REGRESSION test for the
// v0.77.1 release finding: `pfm install` records/keeps the source-repo
// marker but never arms the pre-push leak gate in the clone it manages, so
// `pfm doctor` run from that clone exits 1 with UNWIRED. An apply run against
// a clone that ships .githooks/pre-push but has core.hooksPath unset must
// leave core.hooksPath=.githooks configured in the clone, and the report
// must carry the change line naming that clone. FAILS on unfixed code
// because core.hooksPath is never written.
func TestInstallArmsThePrePushGateInTheSourceClone(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clone := buildSourceCloneWithPrePushHook(t)
	var stdout bytes.Buffer
	installer := &engine{
		options: Options{Home: home, SourceRepo: clone, Stdout: &stdout, ProcessRunner: prepushGit},
		apply:   true,
	}
	if err := installer.writeUpdateMetadata(); err != nil {
		t.Fatalf("writeUpdateMetadata() error = %v", err)
	}
	if got := gitConfigValue(t, clone, "core.hooksPath"); got != ".githooks" {
		t.Fatalf("core.hooksPath = %q, want .githooks (pre-push gate not armed in the source clone %s)", got, clone)
	}
	want := "arm pre-push gate core.hooksPath=.githooks in " + clone
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want the change line %q", stdout.String(), want)
	}
}

// TestInstallPreviewNamesTheUnarmedGateAndWritesNothing pins the preview
// boundary: without --apply, the arm step must still name the change (so a
// dry run tells the adopter what install *would* do) but must write nothing
// to the clone's git config. FAILS on unfixed code because no change line
// names the clone at all.
func TestInstallPreviewNamesTheUnarmedGateAndWritesNothing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clone := buildSourceCloneWithPrePushHook(t)
	var stdout bytes.Buffer
	installer := &engine{options: Options{Home: home, SourceRepo: clone, Stdout: &stdout, ProcessRunner: prepushGit}}
	if err := installer.writeUpdateMetadata(); err != nil {
		t.Fatalf("writeUpdateMetadata() error = %v", err)
	}
	if got := gitConfigValue(t, clone, "core.hooksPath"); got != "" {
		t.Fatalf("core.hooksPath = %q, want unset after a preview run (preview must write nothing)", got)
	}
	want := "arm pre-push gate core.hooksPath=.githooks in " + clone
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want the preview change line %q even though nothing is written", stdout.String(), want)
	}
}

// TestInstallReportsAnArmedGateAsOK pins the already-armed boundary: a clone
// whose core.hooksPath is already .githooks must be reported ok (named), not
// re-announced as a change. FAILS on unfixed code because no "armed" ok line
// is ever produced for the clone.
func TestInstallReportsAnArmedGateAsOK(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clone := buildSourceCloneWithPrePushHook(t)
	// Pre-arm the clone's git config directly, bypassing the step under test.
	prepushGit.Must(t, clone, "config", "core.hooksPath", ".githooks")
	// Pre-settle the other two writeUpdateMetadata steps (marker + binary
	// ownership) so only the arm step's own change/ok accounting is in play.
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	if err := RecordCanonicalBinary(home); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	installer := &engine{
		options: Options{Home: home, SourceRepo: clone, Stdout: &stdout, ProcessRunner: prepushGit},
		apply:   true,
	}
	if err := installer.writeUpdateMetadata(); err != nil {
		t.Fatalf("writeUpdateMetadata() error = %v", err)
	}
	want := "pre-push gate armed core.hooksPath=.githooks in " + clone
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want the ok line %q", stdout.String(), want)
	}
	if installer.report.Changed != 0 {
		t.Fatalf("report.Changed = %d, want 0 — an already-armed gate is ok, never a change", installer.report.Changed)
	}
}

// TestInstallSkipsAGateThatIsNotShipped pins the not-shipped boundary: a
// clone with no .githooks/pre-push at all (a fork or a pre-gate checkout)
// must be a named skip, and install must never write core.hooksPath into
// such a clone. FAILS on unfixed code because no skip line naming the clone
// is ever produced (the clone's config is untouched either way, so only the
// stdout assertion distinguishes fixed from unfixed here).
func TestInstallSkipsAGateThatIsNotShipped(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clone := t.TempDir()
	prepushGit.Must(t, clone, "init", "-q")
	var stdout bytes.Buffer
	installer := &engine{
		options: Options{Home: home, SourceRepo: clone, Stdout: &stdout, ProcessRunner: prepushGit},
		apply:   true,
	}
	if err := installer.writeUpdateMetadata(); err != nil {
		t.Fatalf("writeUpdateMetadata() error = %v", err)
	}
	if got := gitConfigValue(t, clone, "core.hooksPath"); got != "" {
		t.Fatalf("core.hooksPath = %q, want unset — a clone without a shipped hook must never be armed", got)
	}
	want := "pre-push gate not shipped in " + clone + " — nothing to arm"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want the named skip %q", stdout.String(), want)
	}
}

// TestInstallRefusesABrokenHook pins the broken-hook boundary: a
// .githooks/pre-push that exists but is not a regular executable file (here,
// a directory in its place) must fail the install with an error naming the
// path — a broken hook is never silently armed. FAILS on unfixed code
// because writeUpdateMetadata never inspects .githooks/pre-push and returns
// nil.
func TestInstallRefusesABrokenHook(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clone := buildSourceCloneWithPrePushHook(t)
	hook := filepath.Join(clone, ".githooks", "pre-push")
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	// A directory in the hook's place is present but not a regular
	// executable file.
	if err := os.MkdirAll(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	installer := &engine{
		options: Options{Home: home, SourceRepo: clone, Stdout: &stdout, ProcessRunner: prepushGit},
		apply:   true,
	}
	err := installer.writeUpdateMetadata()
	if err == nil {
		t.Fatalf("writeUpdateMetadata() error = nil, want an error naming the broken hook %s", hook)
	}
	if !strings.Contains(err.Error(), hook) {
		t.Fatalf("writeUpdateMetadata() error = %v, want it to name the broken hook path %s", err, hook)
	}
}

// TestInstallReportsAnAbsoluteHooksPathAsArmed is a REGRESSION test: a clone
// armed with the absolute equivalent of .githooks (e.g. "<clone>/.githooks", which `pfm doctor`'s inspectPrePushGateWithRunner
// already accepts as armed by resolving against the repo toplevel) must be
// reported ok and left untouched — not rewritten to the relative spelling.
// FAILS on unfixed code because armSourceRepoPrePushGate compares the raw
// core.hooksPath string to the literal ".githooks" and issues a change line
// instead of ok.
func TestInstallReportsAnAbsoluteHooksPathAsArmed(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clone := buildSourceCloneWithPrePushHook(t)
	absolute := filepath.Join(clone, ".githooks")
	prepushGit.Must(t, clone, "config", "core.hooksPath", absolute)
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	if err := RecordCanonicalBinary(home); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	installer := &engine{
		options: Options{Home: home, SourceRepo: clone, Stdout: &stdout, ProcessRunner: prepushGit},
		apply:   true,
	}
	if err := installer.writeUpdateMetadata(); err != nil {
		t.Fatalf("writeUpdateMetadata() error = %v", err)
	}
	if got := gitConfigValue(t, clone, "core.hooksPath"); got != absolute {
		t.Fatalf(
			"core.hooksPath = %q, want unchanged absolute path %q (an equivalent value must never be rewritten)",
			got,
			absolute,
		)
	}
	want := "pre-push gate armed core.hooksPath=" + absolute + " in " + clone
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want the ok line %q", stdout.String(), want)
	}
	if installer.report.Changed != 0 {
		t.Fatalf(
			"report.Changed = %d, want 0 — an already-armed absolute-path gate is ok, never a change",
			installer.report.Changed,
		)
	}
}
