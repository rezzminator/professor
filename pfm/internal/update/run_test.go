package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/paths"
)

func TestUpdateRefusesDirtyWorktree(t *testing.T) {
	repo := newUpdateGitFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runtime := updateTestRuntime(t)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf(
			"Run() code = 0, want dirty-worktree refusal; stdout=%q stderr=%q",
			stdout.String(),
			stderr.String(),
		)
	}
	if !strings.Contains(stderr.String(), "dirty worktree") {
		t.Fatalf("Run() stderr = %q, want dirty-worktree diagnostic", stderr.String())
	}
}

func TestUpdateRefusesSourceDowngrade(t *testing.T) {
	repo := newUpdateGitFixture(t)
	gitTemp(t, repo, "merge", "--ff-only", "--quiet", "v0.10.0")
	runtime := updateTestRuntime(t)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--to", "v0.9.0", "--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf(
			"Run() code = 0, want source-downgrade refusal; stdout=%q stderr=%q",
			stdout.String(),
			stderr.String(),
		)
	}
	if !strings.Contains(stderr.String(), "would downgrade source from v0.10.0") {
		t.Fatalf("Run() stderr = %q, want source-downgrade diagnostic", stderr.String())
	}
}

// TestUpdateSourceOnDetachedHeadFastForwards is a REGRESSION test for the
// stale "source checkout is detached" refusal: updateRepository used to call
// `git symbolic-ref --quiet --short HEAD` right after resolving previousRef
// and return an error the moment that failed (a detached HEAD has no
// symbolic ref), refusing an update on a source clone checked out at a bare
// tag/commit rather than a branch. The fix drops that check entirely; a
// detached source now fast-forwards past it exactly like a branch checkout.
func TestUpdateSourceOnDetachedHeadFastForwards(t *testing.T) {
	repo := newDetachedUpdateGitFixture(t)
	runtime := updateTestRuntime(t)
	canonical := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installer.RecordCanonicalBinary(runtime.Paths.Home); err != nil {
		t.Fatal(err)
	}

	oldBuild := updateBuildCandidate
	oldInstall := updateApplyInstall
	oldDoctor := updateRunDoctor
	t.Cleanup(func() {
		updateBuildCandidate = oldBuild
		updateApplyInstall = oldInstall
		updateRunDoctor = oldDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--skip-harvest", "--repo", repo}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("Run() on a detached source code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "detached") {
		t.Fatalf("Run() still refuses a detached source: stderr=%q", stderr.String())
	}
	if got := updateGitRevision(t, repo, "HEAD"); got != updateGitRevision(t, repo, "v0.10.0") {
		t.Fatalf("source HEAD after update = %q, want fast-forwarded to v0.10.0", got)
	}
}

func TestPreferredUpdateSourceRepoPreservesRecordedAlias(t *testing.T) {
	home := t.TempDir()
	realRepo := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(realRepo, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(realRepo), aliasRoot); err != nil {
		t.Fatal(err)
	}
	aliasRepo := filepath.Join(aliasRoot, filepath.Base(realRepo))
	if err := installer.WriteSourceRepoMarker(home, aliasRepo); err != nil {
		t.Fatal(err)
	}

	if got := preferredUpdateSourceRepo(home, realRepo); got != aliasRepo {
		t.Fatalf("preferredUpdateSourceRepo()=%q, want recorded alias %q", got, aliasRepo)
	}
}

func TestUpdateReplacesOwnedBinaryLeavesUnownedCopyAndRunsDoctor(t *testing.T) {
	repo := newUpdateGitFixture(t)
	previousBranch := updateGitBranch(t, repo)
	runtime := updateTestRuntime(t)
	canonical := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")
	unowned := filepath.Join(t.TempDir(), "pfm")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unowned, []byte("unowned\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installer.RecordCanonicalBinary(runtime.Paths.Home); err != nil {
		t.Fatal(err)
	}

	oldBuild := updateBuildCandidate
	oldInstall := updateApplyInstall
	oldDoctor := updateRunDoctor
	oldRollbackInstall := updateRollbackInstall
	oldRollbackDoctor := updateRollbackDoctor
	t.Cleanup(func() {
		updateBuildCandidate = oldBuild
		updateApplyInstall = oldInstall
		updateRunDoctor = oldDoctor
		updateRollbackInstall = oldRollbackInstall
		updateRollbackDoctor = oldRollbackDoctor
	})
	builds := 0
	updateBuildCandidate = func(_ context.Context, _, version, output string) error {
		builds++
		if version != "v0.10.0" {
			t.Fatalf("build version=%q, want selected release v0.10.0", version)
		}
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	installCalls, doctorCalls := 0, 0
	updateApplyInstall = func(_ context.Context, candidate, workingDir, sourceRepo string, _ pfmconfig.Runtime, skipHarvest bool, _, _ io.Writer) error {
		installCalls++
		if !strings.HasSuffix(candidate, "pfm-a") {
			t.Fatalf("install candidate=%q, want first reproducible build", candidate)
		}
		if !skipHarvest {
			t.Fatal("update did not propagate --skip-harvest to install")
		}
		if workingDir != repo {
			t.Fatalf("candidate installer working directory=%q, want source repo %q", workingDir, repo)
		}
		if sourceRepo != repo {
			t.Fatalf("candidate installer source marker=%q, want %q", sourceRepo, repo)
		}
		if got := updateGitRevision(t, repo, "HEAD"); got != updateGitRevision(t, repo, "v0.10.0") {
			t.Fatalf("candidate installer saw source revision %q, want v0.10.0", got)
		}
		return nil
	}
	updateRunDoctor = func(_ context.Context, candidate string, _ pfmconfig.Runtime, _ string, skipHarvest bool, _, _ io.Writer) (doctorOutcome, error) {
		doctorCalls++
		if !strings.HasSuffix(candidate, "pfm-a") {
			t.Fatalf("doctor candidate=%q, want first reproducible build", candidate)
		}
		if !skipHarvest {
			t.Fatal("update did not propagate --skip-harvest to doctor")
		}
		return doctorOutcome{}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--skip-harvest", "--repo", repo}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("Run() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if builds != 2 {
		t.Fatalf("build calls=%d, want 2", builds)
	}
	if installCalls != 1 || doctorCalls != 1 {
		t.Fatalf("install calls=%d doctor calls=%d, want 1/1", installCalls, doctorCalls)
	}
	if got, err := os.ReadFile(canonical); err != nil || string(got) != "new\n" {
		t.Fatalf("canonical binary=%q err=%v, want new", got, err)
	}
	if got, err := os.ReadFile(unowned); err != nil || string(got) != "unowned\n" {
		t.Fatalf("unowned binary=%q err=%v, want unchanged", got, err)
	}
	if got := updateGitBranch(t, repo); got != previousBranch {
		t.Fatalf("source branch after successful update = %q, want unchanged %q", got, previousBranch)
	}
}

// TestUpdateBareRunReportsNotManagedOutsideAnyProject is a REGRESSION test
// for the bare `pfm update` post-report tail: watched failing against a
// build where the NOT-MANAGED branch was reverted to writeProjectFailure
// (running update from outside any managed project reported FAILED — a
// baseline that was never expected to exist there). Only the human-readable
// path is driven end to end through runUpdate here: the --json path cannot
// be observed as a single parseable JSON object through this entry point
// because runUpdate unconditionally writes a pre-existing "updated vX.Y.Z
// from PATH" line ahead of the project report regardless of --json (see
// update_command.go's `fmt.Fprintf(stdout, "updated %s from %s\n", ...)`,
// unrelated to this fix). That full --json integration is a NAMED GAP; the
// --json terminal contract itself is pinned directly against
// writeProjectUnmanaged in TestWriteProjectUnmanagedHumanAndJSON below.
func TestUpdateBareRunReportsNotManagedOutsideAnyProject(t *testing.T) {
	repo := newUpdateGitFixture(t)
	runtime := updateTestRuntime(t)
	canonical := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installer.RecordCanonicalBinary(runtime.Paths.Home); err != nil {
		t.Fatal(err)
	}

	oldBuild := updateBuildCandidate
	oldInstall := updateApplyInstall
	oldDoctor := updateRunDoctor
	t.Cleanup(func() {
		updateBuildCandidate = oldBuild
		updateApplyInstall = oldInstall
		updateRunDoctor = oldDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})

	outside := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := Run(
		[]string{"--skip-harvest", "--repo", repo, "--root", outside},
		&stdout,
		&stderr,
		runtime,
	); code != 0 {
		t.Fatalf("Run() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NOT-MANAGED — ") {
		t.Fatalf("stdout=%q, want a NOT-MANAGED terminal", stdout.String())
	}
	if strings.Contains(stdout.String(), "FAILED") {
		t.Fatalf("stdout=%q, want no FAILED terminal outside any managed project", stdout.String())
	}
}

func TestUpdateRollsBackAfterStagingFailure(t *testing.T) {
	repo := newUpdateGitFixture(t)
	previousBranch := updateGitBranch(t, repo)
	previousRef := updateGitRevision(t, repo, "HEAD")
	runtime := updateTestRuntime(t)
	canonical := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installer.RecordCanonicalBinary(runtime.Paths.Home); err != nil {
		t.Fatal(err)
	}

	oldBuild := updateBuildCandidate
	oldInstall := updateApplyInstall
	oldDoctor := updateRunDoctor
	oldRollbackInstall := updateRollbackInstall
	oldRollbackDoctor := updateRollbackDoctor
	t.Cleanup(func() {
		updateBuildCandidate = oldBuild
		updateApplyInstall = oldInstall
		updateRunDoctor = oldDoctor
		updateRollbackInstall = oldRollbackInstall
		updateRollbackDoctor = oldRollbackDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	managedMutation := filepath.Join(runtime.Paths.Home, ".local", "share", "pfm", "install", "new-asset")
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		if err := os.MkdirAll(filepath.Dir(managedMutation), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(managedMutation, []byte("partially installed\n"), 0o600); err != nil {
			return err
		}
		return errors.New("injected install failure")
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		t.Fatal("doctor ran after install failure")
		return doctorOutcome{}, nil
	}
	updateRollbackInstall = func(_ context.Context, candidate, workingDir, sourceRepo string, _ pfmconfig.Runtime, _ bool, _, _ io.Writer) error {
		if !strings.Contains(candidate, "previous-") {
			t.Fatalf("rollback installer candidate=%q, want preserved previous binary", candidate)
		}
		if workingDir != repo {
			t.Fatalf("rollback installer working directory=%q, want restored source repo %q", workingDir, repo)
		}
		if sourceRepo != repo {
			t.Fatalf("rollback installer source marker=%q, want %q", sourceRepo, repo)
		}
		if got := updateGitRevision(t, repo, "HEAD"); got != previousRef {
			t.Fatalf("rollback installer saw source revision %q, want previous %q", got, previousRef)
		}
		return os.RemoveAll(filepath.Dir(managedMutation))
	}
	updateRollbackDoctor = func(_ context.Context, candidate string, _ pfmconfig.Runtime, _ string, _ bool, _, _ io.Writer) (doctorOutcome, error) {
		if !strings.Contains(candidate, "previous-") {
			t.Fatalf("rollback doctor candidate=%q, want preserved previous binary", candidate)
		}
		return doctorOutcome{}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf("Run() code=0, want failure; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if got, err := os.ReadFile(canonical); err != nil || string(got) != "old\n" {
		t.Fatalf("canonical after rollback=%q err=%v, want old", got, err)
	}
	if !strings.Contains(stderr.String(), "rolled back") {
		t.Fatalf("rollback diagnostic=%q", stderr.String())
	}
	if _, err := os.Stat(managedMutation); !os.IsNotExist(err) {
		t.Fatalf("installer mutation survived rollback: %v", err)
	}
	if got := updateGitBranch(t, repo); got != previousBranch {
		t.Fatalf("source branch after failed update = %q, want unchanged %q", got, previousBranch)
	}
	if got := updateGitRevision(t, repo, "HEAD"); got != previousRef {
		t.Fatalf("source revision after failed update = %q, want unchanged %q", got, previousRef)
	}
}

// stubUpdateBaselineDoctor swaps the production updateBaselineDoctor seam
// (a subprocess of os.Executable()) for a fixed outcome, restored on
// cleanup. Every test that drives a real Run() to completion needs
// this: left unstubbed, updateBaselineDoctor would exec the `go test`
// binary itself with `doctor` as its first argument — a non-flag argument
// go's testing flag parser leaves untouched, so the binary would run this
// entire test package a second time as a child process instead of failing
// fast. Test coverage of the real subprocess baseline path itself is a
// named gap (see the report); jailTest already sandboxes HOME but not the
// binary a bare os.Executable() resolves to.
func stubUpdateBaselineDoctor(t *testing.T, outcome doctorOutcome) {
	t.Helper()
	saved := updateBaselineDoctor
	t.Cleanup(func() { updateBaselineDoctor = saved })
	updateBaselineDoctor = func(context.Context, pfmconfig.Runtime, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return outcome, nil
	}
}

// updateRollbackTestRuntime stages the canonical binary and ownership
// ledger every doctor-tier rollback-gate test below needs, returning the
// runtime and the fixture repo.
func updateRollbackTestRuntime(t *testing.T) (runtime pfmconfig.Runtime, repo string) {
	t.Helper()
	repo = newUpdateGitFixture(t)
	runtime = updateTestRuntime(t)
	canonical := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installer.RecordCanonicalBinary(runtime.Paths.Home); err != nil {
		t.Fatal(err)
	}
	return runtime, repo
}

// TestUpdateProceedsWhenTheCandidateDoctorHasOnlyStandingWarnings is M2's
// regression test for issue #24 finding 1 (the headline): a host carrying
// pre-existing warnings the candidate's install did not introduce must not
// be rolled back. Unfixed, the only way to express "doctor exited 1" is a
// non-nil error from the old `error`-returning seam, and that always rolls
// back — this test is written against the new (doctorOutcome, error) seam,
// so it does not compile against the unfixed code; that compile failure IS
// the watched-failing run.
func TestUpdateProceedsWhenTheCandidateDoctorHasOnlyStandingWarnings(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)
	canonical := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")

	oldBuild, oldInstall, oldRunDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
	oldRollbackInstall, oldRollbackDoctor := updateRollbackInstall, updateRollbackDoctor
	t.Cleanup(func() {
		updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldRunDoctor
		updateRollbackInstall, updateRollbackDoctor = oldRollbackInstall, oldRollbackDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	standingWarnings := doctorOutcome{Exit: 1, Warnings: 5, Output: "doctor: warnings=5\n"}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return standingWarnings, nil
	}
	stubUpdateBaselineDoctor(t, standingWarnings)
	rollbackInstallCalled, rollbackDoctorCalled := false, false
	updateRollbackInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		rollbackInstallCalled = true
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		rollbackDoctorCalled = true
		return doctorOutcome{}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("Run() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got, err := os.ReadFile(canonical); err != nil || string(got) != "new\n" {
		t.Fatalf("canonical binary=%q err=%v, want the candidate bytes", got, err)
	}
	if !strings.Contains(stdout.String(), "doctor after update: warnings=5 (before update: 5)") {
		t.Fatalf("stdout=%q, want the warnings-delta line", stdout.String())
	}
	if rollbackInstallCalled || rollbackDoctorCalled {
		t.Fatalf(
			"rollback ran despite a warnings-only candidate doctor (installCalled=%v doctorCalled=%v)",
			rollbackInstallCalled,
			rollbackDoctorCalled,
		)
	}
}

// TestUpdateRollsBackWhenTheCandidateDoctorReportsAFailure pins the other
// half of the gate: a candidate doctor exit of 3 (failures) still rolls
// back, and names the failure count in the reported message.
func TestUpdateRollsBackWhenTheCandidateDoctorReportsAFailure(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)
	canonical := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")

	oldBuild, oldInstall, oldRunDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
	oldRollbackInstall, oldRollbackDoctor := updateRollbackInstall, updateRollbackDoctor
	t.Cleanup(func() {
		updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldRunDoctor
		updateRollbackInstall, updateRollbackDoctor = oldRollbackInstall, oldRollbackDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{Exit: 3, Failures: 1, Output: "doctor: failures=1\n"}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	updateRollbackInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf("Run() code=0, want the candidate failure to roll back; stdout=%q", stdout.String())
	}
	if got, err := os.ReadFile(canonical); err != nil || string(got) != "old\n" {
		t.Fatalf("canonical after rollback=%q err=%v, want old", got, err)
	}
	if !strings.Contains(stderr.String(), "1 failure(s)") {
		t.Fatalf("stderr=%q, want the failure count in the rollback message", stderr.String())
	}
}

// TestUpdateNamesNewWarningRowsIntroducedByTheCandidate pins the delta
// report: a candidate warning row with no textual twin in the baseline
// output is named under "new warning rows", while rows the baseline already
// carried are not repeated.
func TestUpdateNamesNewWarningRowsIntroducedByTheCandidate(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)

	oldBuild, oldInstall, oldRunDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
	t.Cleanup(func() {
		updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldRunDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{
			Exit: 1, Warnings: 3,
			Output: "doctor: row=A\ndoctor: row=B\ndoctor: row=C new-warning\ndoctor: warnings=3\n",
		}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{
		Exit: 1, Warnings: 2,
		Output: "doctor: row=A\ndoctor: row=B\ndoctor: warnings=2\n",
	})

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("Run() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "new warning rows — read them before the next update:") {
		t.Fatalf("stdout=%q, want the new-warning-rows header", stdout.String())
	}
	if !strings.Contains(stdout.String(), "doctor: row=C new-warning") {
		t.Fatalf("stdout=%q, want the new row C listed", stdout.String())
	}
	header := strings.Index(stdout.String(), "new warning rows")
	if header < 0 || strings.Contains(stdout.String()[header:], "doctor: row=A") ||
		strings.Contains(stdout.String()[header:], "doctor: row=B") {
		t.Fatalf("stdout=%q, the baseline's own rows A/B must not be repeated as new", stdout.String())
	}
}

// TestUpdateRollbackDoctorWarningsAreNotResidue pins the rollback half of
// the gate: a rollback doctor exiting 1 with a tier-aware output (a
// `doctor: failures=` line, even at 0) reports its warnings and is never
// claimed as residue.
func TestUpdateRollbackDoctorWarningsAreNotResidue(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)

	oldBuild, oldInstall, oldRunDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
	oldRollbackInstall, oldRollbackDoctor := updateRollbackInstall, updateRollbackDoctor
	t.Cleanup(func() {
		updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldRunDoctor
		updateRollbackInstall, updateRollbackDoctor = oldRollbackInstall, oldRollbackDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return errors.New("injected install failure")
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		t.Fatal("candidate doctor ran after install failure")
		return doctorOutcome{}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	updateRollbackInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		// doctor.go's printCombinedDoctor only ever prints "doctor: failures="
		// when failures > 0 (doctor.go:304-306) — a tier-aware binary with
		// warnings alone prints ONLY "doctor: warnings=N", never a
		// "doctor: failures=0" line. A test stub carrying a shape production
		// never emits would pass against F3's bug; this is the real shape.
		return doctorOutcome{Exit: 1, Warnings: 3, Output: "doctor: warnings=3\n"}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf("Run() code=0, want failure; stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "rolled back update-owned changes") {
		t.Fatalf("stderr=%q, want the no-residue rollback message", stderr.String())
	}
	if strings.Contains(stderr.String(), "rollback residue") {
		t.Fatalf("stderr=%q, a warnings-only rollback doctor must not be claimed as residue", stderr.String())
	}
	if strings.Contains(stderr.String(), "an older pfm exits 1") {
		t.Fatalf(
			"stderr=%q, a tier-aware warnings-only rollback must never be misreported as an older binary",
			stderr.String(),
		)
	}
}

// TestUpdateRollbackDoctorFromAnOlderBinaryIsNamedNotClaimedAsResidue pins
// the other half: a rollback doctor exiting 1 whose output carries neither
// `doctor: failures=` nor `doctor: clean` predates M2's failure tiers, and
// must be named as such rather than reported as rollback residue.
func TestUpdateRollbackDoctorFromAnOlderBinaryIsNamedNotClaimedAsResidue(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)

	oldBuild, oldInstall, oldRunDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
	oldRollbackInstall, oldRollbackDoctor := updateRollbackInstall, updateRollbackDoctor
	t.Cleanup(func() {
		updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldRunDoctor
		updateRollbackInstall, updateRollbackDoctor = oldRollbackInstall, oldRollbackDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return errors.New("injected install failure")
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		t.Fatal("candidate doctor ran after install failure")
		return doctorOutcome{}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	updateRollbackInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		// A genuinely pre-M2 binary's doctor output carries NONE of the
		// three tier markers (issue #24 F3): not "doctor: failures=", not
		// "doctor: warnings=" (only ever emitted when warnings > 0, and only
		// by a tier-aware binary), not "doctor: clean" — just whatever text
		// that release printed before exiting 1.
		return doctorOutcome{Exit: 1, Warnings: 3, Output: "3 warnings found\n"}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf("Run() code=0, want failure; stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "an older pfm exits 1 on warnings alone") {
		t.Fatalf("stderr=%q, want the older-binary diagnostic", stderr.String())
	}
}

func updateTestRuntime(t *testing.T) pfmconfig.Runtime {
	t.Helper()
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	return pfmconfig.Runtime{Paths: paths.Values{Home: home}}
}

func newUpdateGitFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitTemp(t, repo, "init", "-q")
	gitTemp(t, repo, "config", "user.email", "fixture.invalid")
	gitTemp(t, repo, "config", "user.name", "fixture-identity")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "README.md")
	gitTemp(t, repo, "commit", "-qm", "fixture")
	gitTemp(t, repo, "tag", "v0.9.0")
	if err := os.WriteFile(filepath.Join(repo, "RELEASE"), []byte("next\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "RELEASE")
	gitTemp(t, repo, "commit", "-qm", "fixture next release")
	gitTemp(t, repo, "tag", "v0.10.0")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, remote, "init", "--bare", "-q")
	gitTemp(t, repo, "remote", "add", "origin", remote)
	gitTemp(t, repo, "push", "-q", "origin", "HEAD", "--tags")
	gitTemp(t, repo, "checkout", "-qb", "installed", "v0.9.0")
	return repo
}

// newDetachedUpdateGitFixture is newUpdateGitFixture, except the source
// clone is checked out at v0.9.0 detached rather than on a branch named
// "installed" — the shape a maintainer's clone takes after `git checkout
// v0.9.0` directly.
func newDetachedUpdateGitFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitTemp(t, repo, "init", "-q")
	gitTemp(t, repo, "config", "user.email", "fixture.invalid")
	gitTemp(t, repo, "config", "user.name", "fixture-identity")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "README.md")
	gitTemp(t, repo, "commit", "-qm", "fixture")
	gitTemp(t, repo, "tag", "v0.9.0")
	if err := os.WriteFile(filepath.Join(repo, "RELEASE"), []byte("next\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "RELEASE")
	gitTemp(t, repo, "commit", "-qm", "fixture next release")
	gitTemp(t, repo, "tag", "v0.10.0")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, remote, "init", "--bare", "-q")
	gitTemp(t, repo, "remote", "add", "origin", remote)
	gitTemp(t, repo, "push", "-q", "origin", "HEAD", "--tags")
	gitTemp(t, repo, "checkout", "-q", "--detach", "v0.9.0")
	return repo
}

// newReleaseNotesUpdateFixture is newUpdateGitFixture, plus a releases/ file
// per name in releaseFiles committed and tagged v0.10.0 (the update target),
// so a real runUpdate can be driven end to end to observe the printed
// release-notes report lines.
func newReleaseNotesUpdateFixture(t *testing.T, releaseFiles []string) string {
	t.Helper()
	repo := t.TempDir()
	gitTemp(t, repo, "init", "-q")
	gitTemp(t, repo, "config", "user.email", "fixture.invalid")
	gitTemp(t, repo, "config", "user.name", "fixture-identity")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "README.md")
	gitTemp(t, repo, "commit", "-qm", "fixture")
	gitTemp(t, repo, "tag", "v0.9.0")
	// Always commit at least the marker file, even with no release notes
	// between the two tags, so "nothing between" is a real, empty
	// releases/ listing rather than an aborted commit.
	if err := os.WriteFile(filepath.Join(repo, "NEXT"), []byte("next\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "NEXT")
	for _, name := range releaseFiles {
		path := filepath.Join(repo, "releases", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitTemp(t, repo, "add", filepath.Join("releases", name))
	}
	gitTemp(t, repo, "commit", "-qm", "fixture release notes")
	gitTemp(t, repo, "tag", "v0.10.0")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, remote, "init", "--bare", "-q")
	gitTemp(t, repo, "remote", "add", "origin", remote)
	gitTemp(t, repo, "push", "-q", "origin", "HEAD", "--tags")
	gitTemp(t, repo, "checkout", "-qb", "installed", "v0.9.0")
	return repo
}

// newUntaggedPreviousReleaseNotesFixture is newReleaseNotesUpdateFixture,
// except the source clone's checked-out branch sits at an UNTAGGED commit
// (no tag reaches it), so `git describe --tags` on previousRef fails and
// releaseNotesForUpdate reports its own error rather than an empty listing.
func newUntaggedPreviousReleaseNotesFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitTemp(t, repo, "init", "-q")
	gitTemp(t, repo, "config", "user.email", "fixture.invalid")
	gitTemp(t, repo, "config", "user.name", "fixture-identity")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "README.md")
	gitTemp(t, repo, "commit", "-qm", "fixture untagged base")
	gitTemp(t, repo, "branch", "installed")
	if err := os.WriteFile(filepath.Join(repo, "TAGGED"), []byte("tagged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "TAGGED")
	gitTemp(t, repo, "commit", "-qm", "fixture v0.9.0")
	gitTemp(t, repo, "tag", "v0.9.0")
	if err := os.WriteFile(filepath.Join(repo, "RELEASE"), []byte("next\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "RELEASE")
	gitTemp(t, repo, "commit", "-qm", "fixture v0.10.0")
	gitTemp(t, repo, "tag", "v0.10.0")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, remote, "init", "--bare", "-q")
	gitTemp(t, repo, "remote", "add", "origin", remote)
	gitTemp(t, repo, "push", "-q", "origin", "HEAD", "--tags")
	gitTemp(t, repo, "checkout", "-q", "installed")
	return repo
}

// updateWithReleaseNotesFakes drives a real runUpdate against repo with
// build/install/doctor stubbed to no-ops, returning stdout for the caller to
// inspect the printed release-notes report line.
func updateWithReleaseNotesFakes(t *testing.T, repo string) string {
	t.Helper()
	runtime := updateTestRuntime(t)
	canonical := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installer.RecordCanonicalBinary(runtime.Paths.Home); err != nil {
		t.Fatal(err)
	}
	oldBuild, oldInstall, oldDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
	t.Cleanup(func() {
		updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldDoctor
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--skip-harvest", "--repo", repo}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("Run() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// TestUpdateReportsReleaseNotesToRead pins the "files between" branch of the
// release-notes report: the printed line names the previous and target tags
// and the release count, and lists each file oldest first.
func TestUpdateReportsReleaseNotesToRead(t *testing.T) {
	repo := newReleaseNotesUpdateFixture(t, []string{"v0.9.5.md", "v0.9.1.md"})
	stdout := updateWithReleaseNotesFakes(t, repo)
	if !strings.Contains(stdout, "release notes to read (v0.9.0 → v0.10.0, 2 release(s)):") {
		t.Fatalf("stdout=%q, want the release-notes-to-read header", stdout)
	}
	first := strings.Index(stdout, "v0.9.1.md")
	second := strings.Index(stdout, "v0.9.5.md")
	if first < 0 || second < 0 || second < first {
		t.Fatalf("stdout=%q, want v0.9.1.md listed before v0.9.5.md (oldest first)", stdout)
	}
}

// TestUpdateReportsNoReleaseNotesBetween is a REGRESSION test for the
// none-between branch: watched failing against a build that collapsed
// releaseNotesForUpdate's error branch into this one (see
// TestUpdateReportsReleaseNotesCannotList) — the two must stay visibly
// distinct, since one names a real absence and the other a failed read.
func TestUpdateReportsNoReleaseNotesBetween(t *testing.T) {
	repo := newReleaseNotesUpdateFixture(t, nil)
	stdout := updateWithReleaseNotesFakes(t, repo)
	if !strings.Contains(stdout, "release notes: none between v0.9.0 and v0.10.0") {
		t.Fatalf("stdout=%q, want the none-between report line", stdout)
	}
}

// TestUpdateReportsReleaseNotesCannotList pins the error branch: a previous
// revision `git describe` cannot resolve to any tag must render as "cannot
// list", never silently fold into "none between" — an error is never
// absence.
func TestUpdateReportsReleaseNotesCannotList(t *testing.T) {
	repo := newUntaggedPreviousReleaseNotesFixture(t)
	stdout := updateWithReleaseNotesFakes(t, repo)
	if !strings.Contains(stdout, "release notes: cannot list (") {
		t.Fatalf("stdout=%q, want the cannot-list report line", stdout)
	}
	if strings.Contains(stdout, "none between") {
		t.Fatalf("stdout=%q, cannot-list collapsed into none-between", stdout)
	}
}

func gitTemp(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func updateGitBranch(t *testing.T, repo string) string {
	t.Helper()
	command := exec.Command("git", "symbolic-ref", "--quiet", "--short", "HEAD")
	command.Dir = repo
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve fixture branch: %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func updateGitRevision(t *testing.T, repo, revision string) string {
	t.Helper()
	command := exec.Command("git", "rev-parse", "--verify", revision)
	command.Dir = repo
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve fixture revision %s: %v\n%s", revision, err, output)
	}
	return strings.TrimSpace(string(output))
}

// TestBuildUpdateCandidateSurvivesAStrayGitDirectoryAboveTheWorktree pins
// -buildvcs=false. pfm update stages its source as a git worktree, whose .git
// is a FILE cmd/go does not accept as a VCS root, so VCS stamping walks up and
// dies on any .git DIRECTORY above it — an empty $HOME/.git did it live:
// "error obtaining VCS status: exit status 128".
func TestBuildUpdateCandidateSurvivesAStrayGitDirectoryAboveTheWorktree(t *testing.T) {
	for _, tool := range []string{"git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not installed")
		}
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "source")
	for name, content := range map[string]string{
		"pfm/go.mod":          "module probe\n\ngo 1.24\n",
		"pfm/cmd/pfm/main.go": "package main\n\nvar version = \"dev\"\n\nfunc main() { println(version) }\n",
	} {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		command := exec.Command(
			"git",
			append(
				[]string{
					"-C",
					repo,
					"-c",
					"user.name=probe",
					"-c",
					"user.email=probe@example.invalid",
					"-c",
					"commit.gpgsign=false",
				},
				args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "probe")
	stage := filepath.Join(root, "stage")
	git("worktree", "add", "--detach", "-q", stage, "HEAD")

	candidate := filepath.Join(root, "pfm-candidate")
	if err := buildUpdateCandidate(context.Background(), stage, "v9.9.9", candidate); err != nil {
		t.Fatalf("candidate build beneath a stray .git directory: %v", err)
	}
	printed, err := exec.Command(candidate).CombinedOutput()
	if err != nil || strings.TrimSpace(string(printed)) != "v9.9.9" {
		t.Fatalf("candidate printed %q, %v; want the stamped v9.9.9", printed, err)
	}
}

// updateHookRollbackFixture drives a real `runUpdate` whose candidate install
// rewrites an account's Claude settings with a hook only the newer release
// knows, then fails at doctor. between runs after that install and before the
// rollback — the window in which something other than the update may write.
func updateHookRollbackFixture(
	t *testing.T,
	between func(settings string),
) (settings string, original []byte, stderr string) {
	t.Helper()
	repo := newUpdateGitFixture(t)
	runtime := updateTestRuntime(t)
	home := runtime.Paths.Home
	runtime.Config = pfmconfig.Defaults(home, []string{filepath.Join(home, ".cc", "1", "projects")})
	settings = filepath.Join(home, ".cc", "1", "settings.json")
	canonical := filepath.Join(home, ".local", "bin", "pfm")
	original = []byte("{\n  \"hooks\": {}\n}\n")
	for path, content := range map[string][]byte{canonical: []byte("old\n"), settings: original} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installer.RecordCanonicalBinary(home); err != nil {
		t.Fatal(err)
	}

	saved := []any{
		updateBuildCandidate,
		updateApplyInstall,
		updateRunDoctor,
		updateRollbackInstall,
		updateRollbackDoctor,
	}
	t.Cleanup(func() {
		updateBuildCandidate = saved[0].(func(context.Context, string, string, string) error)
		updateApplyInstall = saved[1].(func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error)
		updateRunDoctor = saved[2].(func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error))
		updateRollbackInstall = saved[3].(func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error)
		updateRollbackDoctor = saved[4].(func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error))
	})
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		return os.WriteFile(output, []byte("new\n"), 0o755)
	}
	updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return os.WriteFile(
			settings,
			[]byte(
				"{\n  \"hooks\": {\"UserPromptSubmit\": [{\"hooks\": [{\"command\": \"pfm internal hook-only-the-new-release-knows\"}]}]}\n}\n",
			),
			0o600,
		)
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		between(settings)
		return doctorOutcome{Exit: 3, Failures: 1}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	// The previous release's installer recognises only hooks IT generates, so
	// it leaves the newer hook alone — exactly the live behaviour.
	updateRollbackInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}

	var stdout, stderrBuffer bytes.Buffer
	if code := Run([]string{"--repo", repo}, &stdout, &stderrBuffer, runtime); code == 0 {
		t.Fatalf("Run() code=0, want failure; stderr=%q", stderrBuffer.String())
	}
	return settings, original, stderrBuffer.String()
}

// TestUpdateRollbackRestoresHookFilesTheCandidateInstallChanged pins the
// rollback-residue regression: a hook only the newer release registered
// survived the rollback and ran a subcommand the restored binary lacks —
// "UserPromptSubmit operation blocked by hook" on every prompt, twice live.
func TestUpdateRollbackRestoresHookFilesTheCandidateInstallChanged(t *testing.T) {
	settings, original, stderr := updateHookRollbackFixture(t, func(string) {})
	if got, err := os.ReadFile(settings); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("settings after rollback = %q, %v; want the pre-update bytes %q", got, err, original)
	}
	physicalSettings, err := filepath.EvalSymlinks(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "restored "+physicalSettings) {
		t.Fatalf("rollback did not report the restored hook file: %q", stderr)
	}
}

// TestUpdateRollbackLeavesAHookFileSomethingElseRewroteAndReportsIt pins the
// race guard: a live chat can save its settings while the update runs, and a
// byte restore would erase that edit. A file that no longer holds exactly
// what the candidate's install wrote is left as is and named as residue.
func TestUpdateRollbackLeavesAHookFileSomethingElseRewroteAndReportsIt(t *testing.T) {
	edited := []byte("{\n  \"hooks\": {},\n  \"theme\": \"saved by a live chat\"\n}\n")
	settings, _, stderr := updateHookRollbackFixture(t, func(settings string) {
		if err := os.WriteFile(settings, edited, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if got, err := os.ReadFile(settings); err != nil || !bytes.Equal(got, edited) {
		t.Fatalf("settings after rollback = %q, %v; want the concurrent edit kept %q", got, err, edited)
	}
	if !strings.Contains(stderr, settings) || !strings.Contains(stderr, "rollback residue") {
		t.Fatalf("rollback did not name the untouched file as residue: %q", stderr)
	}
}
