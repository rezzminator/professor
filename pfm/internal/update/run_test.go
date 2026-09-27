package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
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

func TestUpdateIgnoresRetiredEnginesBuildTree(t *testing.T) {
	ignore, err := os.ReadFile(filepath.Join("..", "..", "..", ".gitignore"))
	if err != nil {
		t.Fatalf("read repository .gitignore: %v", err)
	}
	repo := newUpdateGitFixtureWithIgnore(t, ignore)
	buildTree := filepath.Join(repo, "engines", "wave-walker", "dist")
	if err := os.MkdirAll(buildTree, 0o700); err != nil {
		t.Fatalf("create retired engine build tree %q: %v", buildTree, err)
	}
	if err := os.WriteFile(filepath.Join(buildTree, "index.js"), []byte("built\n"), 0o600); err != nil {
		t.Fatalf("write retired engine build artifact: %v", err)
	}

	runtime := updateTestRuntime(t)
	stubUpdatePipeline(t, runtime)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--repo", repo, "--skip-harvest"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf(
			"Run() code=%d with retired engine build tree, stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
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

// stubUpdatePipeline records an owned canonical binary and stubs the build,
// install and doctor steps with no-ops, so a real Run exercises only the
// source-side checks and the reporting around them.
func stubUpdatePipeline(t *testing.T, runtime pfmconfig.Runtime) {
	t.Helper()
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
}

// commitUntaggedFixtureChange adds one commit on the source branch.
func commitUntaggedFixtureChange(t *testing.T, repo, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", name)
	gitTemp(t, repo, "commit", "-qm", "fixture "+name)
}

// When the source's nearest tag is not a release tag, the version check that
// rules out a downgrade cannot run; treating that as "not a downgrade" let
// `--to v0.9.0` roll a v0.10.0 source back.
func TestUpdateRefusesADowngradeItCannotRuleOut(t *testing.T) {
	repo := newUpdateGitFixture(t)
	gitTemp(t, repo, "merge", "--ff-only", "--quiet", "v0.10.0")
	commitUntaggedFixtureChange(t, repo, "NIGHTLY")
	gitTemp(t, repo, "tag", "nightly")
	runtime := updateTestRuntime(t)
	stubUpdatePipeline(t, runtime)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--skip-harvest", "--to", "v0.9.0", "--repo", repo}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf("Run() code = 0, want a refusal; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot rule out a downgrade to v0.9.0") ||
		!strings.Contains(stderr.String(), "nightly") {
		t.Fatalf("Run() stderr = %q, want the refusal naming the unparsable tag", stderr.String())
	}
}

// Target on the source commit itself: nothing to roll back, whatever the
// nearest tag says.
func TestUpdateToTheSourceCommitProceedsWhenTheNearestTagDoesNotParse(t *testing.T) {
	repo := newUpdateGitFixture(t)
	gitTemp(t, repo, "tag", "-a", "-m", "nightly", "nightly")
	runtime := updateTestRuntime(t)
	stubUpdatePipeline(t, runtime)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--skip-harvest", "--to", "v0.9.0", "--repo", repo}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("Run() code = %d, want success; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// Clean tags and a target no older than the source's nearest tag: the guard
// rules the downgrade out and the update goes ahead.
func TestUpdateProceedsWhenTheDowngradeIsRuledOut(t *testing.T) {
	repo := newUpdateGitFixture(t)
	gitTemp(t, repo, "merge", "--ff-only", "--quiet", "v0.10.0")
	commitUntaggedFixtureChange(t, repo, "AHEAD")
	runtime := updateTestRuntime(t)
	stubUpdatePipeline(t, runtime)

	var stdout, stderr bytes.Buffer
	if code := Run(
		[]string{"--skip-harvest", "--to", "v0.10.0", "--repo", repo},
		&stdout,
		&stderr,
		runtime,
	); code != 0 {
		t.Fatalf("Run() code = %d, want success; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
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
	stubUpdatePipeline(t, runtime)

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
	if err := paths.WriteSourceRepoMarker(home, aliasRepo); err != nil {
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
	stubUpdatePipeline(t, runtime)

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
	return newUpdateGitFixtureWithIgnore(t, nil)
}

func newUpdateGitFixtureWithIgnore(t *testing.T, ignore []byte) string {
	t.Helper()
	repo := t.TempDir()
	gitTemp(t, repo, "init", "-q")
	gitTemp(t, repo, "config", "user.email", "fixture.invalid")
	gitTemp(t, repo, "config", "user.name", "fixture-identity")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ignore != nil {
		if err := os.WriteFile(filepath.Join(repo, ".gitignore"), ignore, 0o600); err != nil {
			t.Fatalf("seed fixture .gitignore: %v", err)
		}
		gitTemp(t, repo, "add", "README.md", ".gitignore")
	} else {
		gitTemp(t, repo, "add", "README.md")
	}
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
	repo := newUpdateGitFixture(t)
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
	stubUpdatePipeline(t, runtime)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--skip-harvest", "--repo", repo}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("Run() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	return stdout.String()
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

// journalRollbackRunner plays the candidate's `install --rollback {id}` and
// hands every other process to the real runner, so the fixture's git stays
// real. Each rollback call is recorded; stderr and exit script its verdict.
type journalRollbackRunner struct {
	real       deps.Runner
	calls      *[][]string
	stderr     string
	exit       int
	onRollback func(argv []string)
}

func (runner journalRollbackRunner) Run(
	ctx context.Context,
	argv []string,
	options deps.RunOptions,
) (deps.RunResult, error) {
	return runner.real.Run(ctx, argv, options)
}

func (runner journalRollbackRunner) LookPath(name string) (string, error) {
	return runner.real.LookPath(name)
}

func (runner journalRollbackRunner) Start(
	ctx context.Context,
	argv []string,
	options deps.StartOptions,
) (deps.Process, error) {
	if !slices.Contains(argv, "--rollback") {
		return runner.real.Start(ctx, argv, options)
	}
	*runner.calls = append(*runner.calls, append([]string(nil), argv...))
	if runner.onRollback != nil {
		runner.onRollback(argv)
	}
	if options.Stderr != nil && runner.stderr != "" {
		if _, err := io.WriteString(options.Stderr, runner.stderr); err != nil {
			return nil, err
		}
	}
	var waitErr error
	if runner.exit != 0 {
		waitErr = scriptedUpdateExitError{code: runner.exit}
	}
	return scriptedUpdateProcess{waitErr: waitErr}, nil
}

// journalRollbackUpdate is one update run whose candidate install is install
// and whose gating doctor fails when doctorFails; it returns the rollback
// order ("journal-rollback", "previous-install"), the recorded journal
// rollback calls, Run's stderr and exit code. At the journal rollback the
// canonical binary must still be the candidate's: binaries restore after it.
type journalRollbackUpdate struct {
	runtime     pfmconfig.Runtime
	repo        string
	install     func() error
	doctorFails bool
	runner      journalRollbackRunner
	onRollback  func(argv []string)
}

func (update journalRollbackUpdate) run(t *testing.T) (order []string, calls [][]string, stderr string, code int) {
	t.Helper()
	canonical := filepath.Join(update.runtime.Paths.Home, ".local", "bin", "pfm")
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
		return update.install()
	}
	updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		if update.doctorFails {
			return doctorOutcome{Exit: 3, Failures: 1, Output: "doctor: failures=1\n"}, nil
		}
		return doctorOutcome{Output: "doctor: clean\n"}, nil
	}
	stubUpdateBaselineDoctor(t, doctorOutcome{})
	updateRollbackInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
		order = append(order, "previous-install")
		return nil
	}
	updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}
	runner := update.runner
	runner.real = currentUpdateRunner()
	runner.calls = &calls
	runner.onRollback = func(argv []string) {
		order = append(order, "journal-rollback")
		if got, err := os.ReadFile(canonical); err != nil || string(got) != "new\n" {
			t.Errorf("canonical binary at the journal rollback = %q, %v; want the candidate still in place", got, err)
		}
		if _, err := os.Stat(argv[0]); err != nil {
			t.Errorf("journal rollback ran %s, which is not on disk: %v", argv[0], err)
		}
		if update.onRollback != nil {
			update.onRollback(argv)
		}
	}
	t.Cleanup(StubRunnerForTest(runner))
	var stdout, stderrBuffer bytes.Buffer
	code = Run([]string{"--repo", update.repo}, &stdout, &stderrBuffer, update.runtime)
	if got, err := os.ReadFile(canonical); err != nil || string(got) != "old\n" {
		t.Errorf("canonical binary after rollback = %q, %v; want the previous binary restored", got, err)
	}
	return order, calls, stderrBuffer.String(), code
}

func updateJournalRoot(home string) string {
	return filepath.Join(home, ".local", "state", "pfm", "migrations")
}

func writeUpdateJournal(home, id string) error {
	dir := filepath.Join(updateJournalRoot(home), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "records.json"), []byte("[]\n"), 0o600)
}

func wantJournalRollbackCall(t *testing.T, calls [][]string, id string) {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("journal rollback calls = %q, want exactly one for %s", calls, id)
	}
	argv := calls[0]
	if filepath.Base(argv[0]) != "pfm-a" || !slices.Equal(argv[1:], []string{"install", "--rollback", id}) {
		t.Fatalf("journal rollback argv = %q, want {staged candidate} install --rollback %s", argv, id)
	}
}

// A candidate install that wrote a journal and then failed is replayed
// backwards by the candidate itself, before the binaries and the source come
// back; the journal that predates the update is left alone.
func TestUpdateInstallFailureRollsBackTheInstallJournalFirst(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)
	home := runtime.Paths.Home
	if err := writeUpdateJournal(home, "20260101T000000Z"); err != nil {
		t.Fatal(err)
	}
	order, calls, stderr, code := journalRollbackUpdate{runtime: runtime, repo: repo, install: func() error {
		if err := writeUpdateJournal(home, "20260927T120000Z"); err != nil {
			return err
		}
		return errors.New("injected install failure")
	}}.run(t)
	if code == 0 {
		t.Fatalf("Run() code=0, want the install failure; stderr=%q", stderr)
	}
	wantJournalRollbackCall(t, calls, "20260927T120000Z")
	if !slices.Equal(order, []string{"journal-rollback", "previous-install"}) {
		t.Fatalf("rollback order = %q, want the journal replayed before the previous install", order)
	}
}

// A step after a successful install (here the candidate doctor) that fails
// replays the install's journal first as well.
func TestUpdateLaterFailureRollsBackTheInstallJournalFirst(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)
	home := runtime.Paths.Home
	order, calls, stderr, code := journalRollbackUpdate{
		runtime:     runtime,
		repo:        repo,
		doctorFails: true,
		install:     func() error { return writeUpdateJournal(home, "20260927T120000Z") },
	}.run(t)
	if code == 0 {
		t.Fatalf("Run() code=0, want the candidate doctor failure; stderr=%q", stderr)
	}
	wantJournalRollbackCall(t, calls, "20260927T120000Z")
	if !slices.Equal(order, []string{"journal-rollback", "previous-install"}) {
		t.Fatalf("rollback order = %q, want the journal replayed before the previous install", order)
	}
}

// An install that recorded nothing leaves no journal to replay: the rest of
// the rollback runs alone.
func TestUpdateRollbackWithoutANewJournalRunsNoJournalRollback(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)
	if err := writeUpdateJournal(runtime.Paths.Home, "20260101T000000Z"); err != nil {
		t.Fatal(err)
	}
	order, calls, stderr, code := journalRollbackUpdate{runtime: runtime, repo: repo, install: func() error {
		return errors.New("injected install failure")
	}}.run(t)
	if code == 0 {
		t.Fatalf("Run() code=0, want the install failure; stderr=%q", stderr)
	}
	if len(calls) != 0 || !slices.Equal(order, []string{"previous-install"}) {
		t.Fatalf("journal rollback calls = %q, order = %q; want none and only the previous install", calls, order)
	}
}

// Two new journals are never guessed between: the failure names each id and
// its rollback command, and the rest of the rollback still runs.
func TestUpdateRollbackNamesEveryJournalWhenMoreThanOneAppeared(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)
	home := runtime.Paths.Home
	order, calls, stderr, code := journalRollbackUpdate{runtime: runtime, repo: repo, install: func() error {
		for _, id := range []string{"20260927T120000Z", "20260927T120001Z"} {
			if err := writeUpdateJournal(home, id); err != nil {
				return err
			}
		}
		return errors.New("injected install failure")
	}}.run(t)
	if code == 0 {
		t.Fatalf("Run() code=0, want the install failure; stderr=%q", stderr)
	}
	if len(calls) != 0 || !slices.Equal(order, []string{"previous-install"}) {
		t.Fatalf("journal rollback calls = %q, order = %q; want none and the previous install", calls, order)
	}
	for _, want := range []string{
		"pfm install --rollback 20260927T120000Z",
		"pfm install --rollback 20260927T120001Z",
		"rollback residue",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr=%q, want it to name %q", stderr, want)
		}
	}
}

// A journal rollback the candidate refuses is residue naming the id, the
// command and the candidate's own words; the binaries, the source and the
// previous install are still restored.
func TestUpdateRollbackReportsARefusedJournalRollback(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)
	home := runtime.Paths.Home
	refusal := "pfm install: rollback: rollback 20260927T120000Z refused: drift at " +
		filepath.Join(home, ".zshrc") + " — rerun with --force to overwrite them"
	order, calls, stderr, code := journalRollbackUpdate{
		runtime: runtime,
		repo:    repo,
		install: func() error {
			if err := writeUpdateJournal(home, "20260927T120000Z"); err != nil {
				return err
			}
			return errors.New("injected install failure")
		},
		runner: journalRollbackRunner{stderr: refusal + "\n", exit: 1},
	}.run(t)
	if code == 0 {
		t.Fatalf("Run() code=0, want the install failure; stderr=%q", stderr)
	}
	wantJournalRollbackCall(t, calls, "20260927T120000Z")
	if !slices.Equal(order, []string{"journal-rollback", "previous-install"}) {
		t.Fatalf("rollback order = %q, want the previous install after the refused journal rollback", order)
	}
	_, residue, found := strings.Cut(stderr, "rollback residue")
	if !found {
		t.Fatalf("stderr=%q, want rollback residue", stderr)
	}
	for _, want := range []string{"install journal 20260927T120000Z", "pfm install --rollback 20260927T120000Z", refusal} {
		if !strings.Contains(residue, want) {
			t.Fatalf("rollback residue=%q, want it to name %q", residue, want)
		}
	}
}

// A migrations directory that cannot be listed when the rollback starts is a
// named failure, never read as "no journal"; the rest of the rollback runs.
func TestUpdateRollbackNamesAnUnreadableJournalDirectory(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)
	root := updateJournalRoot(runtime.Paths.Home)
	order, calls, stderr, code := journalRollbackUpdate{runtime: runtime, repo: repo, install: func() error {
		if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(root, []byte("not a directory\n"), 0o600); err != nil {
			return err
		}
		return errors.New("injected install failure")
	}}.run(t)
	if code == 0 {
		t.Fatalf("Run() code=0, want the install failure; stderr=%q", stderr)
	}
	if len(calls) != 0 || !slices.Equal(order, []string{"previous-install"}) {
		t.Fatalf("journal rollback calls = %q, order = %q; want none and the previous install", calls, order)
	}
	if !strings.Contains(stderr, "list install journals in "+root) {
		t.Fatalf("stderr=%q, want the listing failure of %s named", stderr, root)
	}
}

// A migrations directory that cannot be listed before the install is a named
// failure too: the install never runs and the replaced binaries come back.
func TestUpdateRefusesToInstallWhenTheJournalDirectoryIsUnreadable(t *testing.T) {
	runtime, repo := updateRollbackTestRuntime(t)
	root := updateJournalRoot(runtime.Paths.Home)
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	installed := false
	_, calls, stderr, code := journalRollbackUpdate{runtime: runtime, repo: repo, install: func() error {
		installed = true
		return nil
	}}.run(t)
	if code == 0 || installed || len(calls) != 0 {
		t.Fatalf("Run() code=%d installed=%v calls=%q, want a failure before the install", code, installed, calls)
	}
	if !strings.Contains(stderr, "list install journals in "+root) {
		t.Fatalf("stderr=%q, want the listing failure of %s named", stderr, root)
	}
}

// Config files the candidate install moved come back through its journal
// rollback, before the previous release's installer reads --config — with the
// same --config the install was given, and no copy of their own.
func TestUpdateRollbackRestoresMovedConfigThroughTheJournal(t *testing.T) {
	runtime, repo, legacyPath, migratedPath, originalContent := updateConfigMigrationTestRuntime(t)
	home := runtime.Paths.Home
	_, calls, stderr, code := journalRollbackUpdate{
		runtime:     runtime,
		repo:        repo,
		doctorFails: true,
		install: func() error {
			if err := writeUpdateJournal(home, "20260927T120000Z"); err != nil {
				return err
			}
			return os.Rename(legacyPath, migratedPath)
		},
		onRollback: func([]string) {
			if err := os.Rename(migratedPath, legacyPath); err != nil {
				t.Errorf("journal rollback stub: %v", err)
			}
		},
	}.run(t)
	if code == 0 {
		t.Fatalf("Run() code=0, want the candidate doctor failure; stderr=%q", stderr)
	}
	if len(calls) != 1 || !slices.Equal(calls[0][1:], []string{
		"--config", legacyPath, "install", "--rollback", "20260927T120000Z",
	}) {
		t.Fatalf("journal rollback calls = %q, want --config %s install --rollback 20260927T120000Z", calls, legacyPath)
	}
	if got, err := os.ReadFile(legacyPath); err != nil || !bytes.Equal(got, originalContent) {
		t.Fatalf("config.json after rollback = %q, %v; want %q", got, err, originalContent)
	}
	if _, err := os.Stat(migratedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pfm.config.json after rollback: stat err=%v, want it gone", err)
	}
}
