package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestUpdateRollbackRestoresBinaryAndSourceBeforePreviousInstall(t *testing.T) {
	for _, phase := range []string{"install", "doctor"} {
		t.Run(phase, func(t *testing.T) {
			runtime, repo := updateRollbackTestRuntime(t)
			previousRef := updateGitRevision(t, repo, "HEAD")
			previousBranch := updateGitBranch(t, repo)
			canonical := filepath.Join(runtime.Paths.Home, ".local", "bin", "pfm")
			oldBuild, oldInstall, oldDoctor := updateBuildCandidate, updateApplyInstall, updateRunDoctor
			oldRollbackInstall, oldRollbackDoctor := updateRollbackInstall, updateRollbackDoctor
			t.Cleanup(func() {
				updateBuildCandidate, updateApplyInstall, updateRunDoctor = oldBuild, oldInstall, oldDoctor
				updateRollbackInstall, updateRollbackDoctor = oldRollbackInstall, oldRollbackDoctor
			})
			updateBuildCandidate = func(_ context.Context, _, _, output string) error {
				return testjail.WriteExecutable(output, []byte("new\n"), 0o755)
			}
			var order []string
			updateApplyInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
				order = append(order, "candidate-install")
				if got, err := os.ReadFile(canonical); err != nil || string(got) != "new\n" {
					t.Fatalf("candidate install binary = %q, %v; want new", got, err)
				}
				if got := updateGitRevision(t, repo, "HEAD"); got == previousRef {
					t.Fatal("source did not advance before candidate install")
				}
				if phase == "install" {
					return errors.New("injected install failure")
				}
				return nil
			}
			updateRunDoctor = func(_ context.Context, _ string, _ pfmconfig.Runtime, configPath string, _ bool, _, _ io.Writer) (doctorOutcome, error) {
				order = append(order, "candidate-doctor")
				if configPath != runtime.Config.Path {
					t.Fatalf("candidate doctor config = %q, want %q", configPath, runtime.Config.Path)
				}
				return doctorOutcome{Exit: 3, Failures: 1}, nil
			}
			stubUpdateBaselineDoctor(t, doctorOutcome{})
			var previousBinary string
			updateRollbackInstall = func(_ context.Context, binary, workingDir, sourceRepo string, _ pfmconfig.Runtime, _ bool, _, _ io.Writer) error {
				order = append(order, "previous-install")
				previousBinary = binary
				for _, path := range []string{canonical, binary} {
					if got, err := os.ReadFile(path); err != nil || string(got) != "old\n" {
						t.Errorf("previous install binary %s = %q, %v; want old", path, got, err)
					}
				}
				if workingDir != repo || sourceRepo != repo {
					t.Errorf("previous install repos = %q, %q; want %q", workingDir, sourceRepo, repo)
				}
				if got := updateGitRevision(t, repo, "HEAD"); got != previousRef {
					t.Errorf("previous install source = %q, want %q", got, previousRef)
				}
				return nil
			}
			updateRollbackDoctor = func(_ context.Context, binary string, _ pfmconfig.Runtime, configPath string, _ bool, _, _ io.Writer) (doctorOutcome, error) {
				order = append(order, "previous-doctor")
				if binary != previousBinary || configPath != runtime.Config.Path {
					t.Errorf(
						"previous doctor binary/config = %q/%q, want %q/%q",
						binary,
						configPath,
						previousBinary,
						runtime.Config.Path,
					)
				}
				return doctorOutcome{}, nil
			}
			var stdout, stderr bytes.Buffer
			if code := Run([]string{"--repo", repo}, &stdout, &stderr, runtime); code != 1 {
				t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
			}
			want := []string{"candidate-install", "previous-install", "previous-doctor"}
			if phase == "doctor" {
				want = []string{"candidate-install", "candidate-doctor", "previous-install", "previous-doctor"}
			}
			if !slices.Equal(order, want) {
				t.Fatalf("update order = %q, want %q", order, want)
			}
			if got := updateGitBranch(t, repo); got != previousBranch {
				t.Errorf("source branch = %q, want %q", got, previousBranch)
			}
		})
	}
}

func TestUpdateRollbackJoinsStepFailures(t *testing.T) {
	for _, step := range []string{"source", "install", "doctor spawn", "doctor verdict"} {
		t.Run(step, func(t *testing.T) {
			runtime, repo := updateRollbackTestRuntime(t)
			previousRef := updateGitRevision(t, repo, "HEAD")
			oldInstall, oldDoctor := updateRollbackInstall, updateRollbackDoctor
			t.Cleanup(func() { updateRollbackInstall, updateRollbackDoctor = oldInstall, oldDoctor })
			injected := errors.New("injected step failure")
			updateRollbackInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
				if step == "install" {
					return injected
				}
				return nil
			}
			updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
				if step == "doctor spawn" {
					return doctorOutcome{}, injected
				}
				return doctorOutcome{Exit: 3}, nil
			}
			if step == "source" {
				previousRef = "missing-revision"
			}
			target := filepath.Join(t.TempDir(), "binary")
			replacements := []updateReplacement{
				{target: target, backup: filepath.Join(t.TempDir(), "missing-backup"), replaced: true},
			}
			err := rollbackUpdateState(
				context.Background(),
				repo,
				repo,
				previousRef,
				step == "source",
				replacements,
				runtime,
				false,
				io.Discard,
				io.Discard,
			)
			if err == nil || !strings.Contains(err.Error(), target) {
				t.Fatalf("rollback error = %v, want binary restoration failure naming %s", err, target)
			}
			want := map[string]string{
				"source":         "restore source revision missing-revision:",
				"install":        "reapply previous installer state: injected step failure",
				"doctor spawn":   "doctor after rollback: injected step failure",
				"doctor verdict": "doctor after rollback: exited 3 — see the doctor rows above",
			}[step]
			if !strings.Contains(err.Error(), want) {
				t.Errorf("rollback error = %v, want %q joined with the restoration failure", err, want)
			}
			if (step == "install" || step == "doctor spawn") && !errors.Is(err, injected) {
				t.Errorf("rollback error = %v, want wrapped injected error", err)
			}
		})
	}
}
