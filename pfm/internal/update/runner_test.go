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
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type scriptedUpdateProcess struct{ waitErr error }

func (process scriptedUpdateProcess) Pid() int       { return 1001 }
func (process scriptedUpdateProcess) Wait() error    { return process.waitErr }
func (process scriptedUpdateProcess) Release() error { return nil }
func (process scriptedUpdateProcess) StdinPipe() (io.WriteCloser, error) {
	return nil, errors.New("stdin pipe not scripted")
}

func (process scriptedUpdateProcess) StdoutPipe() (io.ReadCloser, error) {
	return nil, errors.New("stdout pipe not scripted")
}
func (process scriptedUpdateProcess) Kill() error      { return nil }
func (process scriptedUpdateProcess) KillGroup() error { return nil }

type scriptedUpdateRunner struct {
	stdout    string
	stderr    string
	waitErr   error
	runResult deps.RunResult
	runErr    error
	startEnv  *[]string
	startArgv *[]string
}

func (runner scriptedUpdateRunner) Run(context.Context, []string, deps.RunOptions) (deps.RunResult, error) {
	if runner.runResult.Stdout != nil || runner.runResult.Stderr != nil || runner.runResult.ExitCode != 0 ||
		runner.runErr != nil {
		return runner.runResult, runner.runErr
	}
	return deps.RunResult{ExitCode: -1}, errors.New("Run was used; update candidate must stream through Start")
}

func (runner scriptedUpdateRunner) LookPath(string) (string, error) {
	return "", errors.New("LookPath was not scripted")
}

func (runner scriptedUpdateRunner) Start(
	_ context.Context,
	argv []string,
	options deps.StartOptions,
) (deps.Process, error) {
	if runner.startEnv != nil {
		*runner.startEnv = append([]string(nil), options.Env...)
	}
	if runner.startArgv != nil {
		*runner.startArgv = append([]string(nil), argv...)
	}
	if options.Stdout != nil {
		if _, err := io.WriteString(options.Stdout, runner.stdout); err != nil {
			return nil, err
		}
	}
	if options.Stderr != nil {
		if _, err := io.WriteString(options.Stderr, runner.stderr); err != nil {
			return nil, err
		}
	}
	return scriptedUpdateProcess{waitErr: runner.waitErr}, nil
}

type scriptedUpdateExitError struct{ code int }

func (err scriptedUpdateExitError) Error() string { return "scripted candidate exit" }
func (err scriptedUpdateExitError) ExitCode() int { return err.code }

// TestRunUpdateCandidateCommandStreamsAndCapturesDoctor is a REGRESSION test
// for the candidate doctor capture boundary: the old update gate diffed the
// doctor's stdout, while stderr remained live operator output. Keeping those
// streams separate also avoids two os/exec copy goroutines writing the same
// bytes.Buffer concurrently.
func TestRunUpdateCandidateCommandStreamsAndCapturesDoctor(t *testing.T) {
	restore := StubRunnerForTest(scriptedUpdateRunner{
		stdout:  "doctor stdout\n",
		stderr:  "doctor stderr\n",
		waitErr: scriptedUpdateExitError{code: 3},
	})
	t.Cleanup(restore)

	var stdout, stderr bytes.Buffer
	err := runUpdateCandidateCommand(
		context.Background(), "/tmp/pfm-candidate", "", t.TempDir(), "", &stdout, &stderr, doctorCommand,
	)
	var doctorErr *doctorExitError
	if !errors.As(err, &doctorErr) {
		t.Fatalf("runUpdateCandidateCommand() error = %v, want doctor exit error", err)
	}
	if doctorErr.code != 3 || doctorErr.output != "doctor stdout\n" {
		t.Fatalf("doctor capture = %#v, want stdout only and exit 3", doctorErr)
	}
	if stdout.String() != "doctor stdout\n" || stderr.String() != "doctor stderr\n" {
		t.Fatalf("live output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestUpdateGitOutputUsesInjectedRunner(t *testing.T) {
	restore := StubRunnerForTest(scriptedUpdateRunner{runResult: deps.RunResult{
		Stdout:   []byte("HEAD\n"),
		ExitCode: 0,
	}})
	t.Cleanup(restore)

	got, err := updateGitOutput(context.Background(), t.TempDir(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("updateGitOutput() error = %v", err)
	}
	if got != "HEAD\n" {
		t.Fatalf("updateGitOutput() = %q, want injected stdout", got)
	}
}

func TestRunUpdateCandidateCommandClearsInheritedSourceRepo(t *testing.T) {
	var capturedEnv []string
	restore := StubRunnerForTest(scriptedUpdateRunner{startEnv: &capturedEnv})
	t.Cleanup(restore)
	t.Setenv("PFM_SOURCE_REPO", "/host/source-that-must-not-leak")

	if err := runUpdateCandidateCommand(
		context.Background(), "/tmp/pfm-candidate", "", t.TempDir(), "", io.Discard, io.Discard, "doctor",
	); err != nil {
		t.Fatalf("runUpdateCandidateCommand() error = %v", err)
	}

	for _, entry := range capturedEnv {
		if entry == "PFM_SOURCE_REPO=" {
			return
		}
	}
	t.Fatalf("candidate environment omitted an explicit empty PFM_SOURCE_REPO: %v", capturedEnv)
}

func TestApplyUpdateInstallConfig(t *testing.T) {
	for _, testcase := range []struct {
		name        string
		exists      bool
		skipHarvest bool
		want        []string
	}{
		{
			name: "config file exists", exists: true, skipHarvest: true,
			want: []string{"/tmp/pfm-candidate", "--config", "/cfg/pfm.config.json", "install", "--yes", "--skip-harvest"},
		},
		{
			name: "no config file",
			want: []string{"/tmp/pfm-candidate", "install", "--yes"},
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			var argv []string
			t.Cleanup(StubRunnerForTest(scriptedUpdateRunner{startArgv: &argv}))
			runtime := pfmconfig.Runtime{
				Config: pfmconfig.Config{Path: "/cfg/pfm.config.json", Exists: testcase.exists},
			}
			if err := applyUpdateInstall(
				context.Background(), "/tmp/pfm-candidate", t.TempDir(), "", runtime,
				testcase.skipHarvest, io.Discard, io.Discard,
			); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(argv, testcase.want) {
				t.Fatalf("install argv = %q; want %q", argv, testcase.want)
			}
		})
	}
}

func TestUpdateGitRepositoryEnvironment(t *testing.T) {
	selectors := []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE",
		"GIT_IMPLICIT_WORK_TREE", "GIT_PREFIX", "GIT_SHALLOW_FILE", "GIT_GRAFT_FILE",
		"GIT_CEILING_DIRECTORIES",
	}
	for _, name := range selectors {
		t.Setenv(name, "/foreign/"+name)
	}
	transport := map[string]string{
		"GIT_SSH_COMMAND": "ssh -F fixture-config", "GIT_SSL_CAINFO": "/fixture/ca.pem",
		"SSH_AUTH_SOCK": "/fixture/agent.sock", "GIT_CONFIG_GLOBAL": "/dev/null",
		"GIT_CONFIG_NOSYSTEM": "1",
	}
	for name, value := range transport {
		t.Setenv(name, value)
	}
	root, foreign, gitDir := t.TempDir(), t.TempDir(), t.TempDir()
	alias := filepath.Join(t.TempDir(), "source-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	operations := [][]string{
		{"rev-parse", "--git-common-dir"},
		{"rev-parse", "--verify", "HEAD"},
		{"fetch", "--tags"},
		{"tag", "--list"},
		{"status", "--porcelain", "--untracked-files=all"},
		{"merge-base", "--is-ancestor", "v0.10.0", "HEAD"},
		{"merge-base", "--is-ancestor", "HEAD", "v0.10.0"},
		{"describe", "--tags", "--abbrev=0", "HEAD"},
		{"rev-parse", "--verify", "v0.10.0^{commit}"},
		{"worktree", "add", "--detach", "--quiet", "stage", "v0.10.0"},
		{"worktree", "remove", "--force", "stage"},
		{"merge", "--ff-only", "--quiet", "v0.10.0"},
		{"ls-tree", "--name-only", "v0.10.0", "releases/"},
		{"reset", "--keep", "HEAD"},
	}
	for _, mode := range []struct {
		name, repo, mappedRoot string
		mapped                 bool
	}{
		{"ordinary source", root, "", false},
		{"explicit fence source", root, root, true},
		{"symlinked fence source", alias, root, true},
		{"different source", root, foreign, false},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv(paths.EnvDevRepoWorkTree, mode.mappedRoot)
			t.Setenv(paths.EnvDevRepoGitDir, gitDir)
			for _, operation := range operations {
				for _, output := range []bool{false, true} {
					fake := &deps.FakeRunner{}
					fake.Script([]string{"git"}, deps.RunResult{Stdout: []byte("selected source\n")}, nil)
					restore := StubRunnerForTest(fake)
					var err error
					if output {
						var got string
						got, err = updateGitOutput(context.Background(), mode.repo, operation...)
						if got != "selected source\n" {
							t.Errorf("git %v output = %q", operation, got)
						}
					} else {
						err = updateGitRun(context.Background(), mode.repo, operation...)
					}
					restore()
					if err != nil {
						t.Fatal(err)
					}
					calls := fake.Calls()
					if len(calls) != 1 || calls[0].Opts.Dir != mode.repo || calls[0].Opts.Env == nil {
						t.Fatalf("git %v calls = %#v; want requested source and explicit environment", operation, calls)
					}
					env := make(map[string]string)
					for _, entry := range calls[0].Opts.Env {
						name, value, _ := strings.Cut(entry, "=")
						if _, duplicate := env[name]; duplicate {
							t.Fatalf("duplicate environment key %s", name)
						}
						env[name] = value
					}
					for _, name := range selectors {
						want, present := "", false
						if mode.mapped && name == "GIT_DIR" {
							want, present = gitDir, true
						}
						if mode.mapped && name == "GIT_WORK_TREE" {
							want, present = mode.repo, true
						}
						value, found := env[name]
						if value != want || found != present {
							t.Errorf(
								"git %v %s = %q, present=%t; want %q, present=%t",
								operation,
								name,
								value,
								found,
								want,
								present,
							)
						}
					}
					for name, want := range transport {
						if got := env[name]; got != want {
							t.Errorf("git %v %s = %q; want %q", operation, name, got, want)
						}
					}
				}
			}
		})
	}
}

func TestUpdateSourceIgnoresForeignGitSelectors(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[rollback], func(t *testing.T) {
			repo, foreign := newUpdateGitFixture(t), newUpdateGitFixture(t)
			previous := updateGitRevision(t, repo, "HEAD")
			target := updateGitRevision(t, repo, "v0.10.0")
			foreignPrevious := updateGitRevision(t, foreign, "HEAD")
			runtime := updateTestRuntime(t)
			stubUpdatePipeline(t, runtime)
			if rollback {
				updateRunDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
					return doctorOutcome{Exit: 3, Failures: 1}, nil
				}
				oldInstall, oldDoctor := updateRollbackInstall, updateRollbackDoctor
				t.Cleanup(func() { updateRollbackInstall, updateRollbackDoctor = oldInstall, oldDoctor })
				updateRollbackInstall = func(context.Context, string, string, string, pfmconfig.Runtime, bool, io.Writer, io.Writer) error {
					return nil
				}
				updateRollbackDoctor = func(context.Context, string, pfmconfig.Runtime, string, bool, io.Writer, io.Writer) (doctorOutcome, error) {
					return doctorOutcome{}, nil
				}
			}
			t.Setenv("GIT_DIR", filepath.Join(foreign, ".git"))
			t.Setenv("GIT_WORK_TREE", foreign)
			t.Setenv("GIT_COMMON_DIR", filepath.Join(foreign, ".git"))
			t.Setenv("GIT_INDEX_FILE", filepath.Join(foreign, ".git", "index"))
			var stdout, stderr bytes.Buffer
			code := Run([]string{"--repo", repo, "--skip-harvest"}, &stdout, &stderr, runtime)
			wantCode, wantRef := 0, target
			if rollback {
				wantCode, wantRef = 5, previous
			}
			if code != wantCode {
				t.Fatalf(
					"Run() code=%d; want %d; stdout=%q stderr=%q",
					code,
					wantCode,
					stdout.String(),
					stderr.String(),
				)
			}
			for source, want := range map[string]string{repo: wantRef, foreign: foreignPrevious} {
				result, err := (deps.RealRunner{}).Run(
					context.Background(),
					[]string{"git", "rev-parse", "HEAD"},
					deps.RunOptions{Dir: source, Env: deps.WithoutGitRepoVars(os.Environ())},
				)
				if err != nil || result.ExitCode != 0 || strings.TrimSpace(string(result.Stdout)) != want {
					t.Errorf(
						"source %s HEAD=%q exit=%d err=%v; want %s",
						source,
						result.Stdout,
						result.ExitCode,
						err,
						want,
					)
				}
			}
			if _, err := os.Stat(filepath.Join(repo, ".git", "pfm-update.lock")); err != nil {
				t.Errorf("requested source ownership lock: %v", err)
			}
		})
	}
}
