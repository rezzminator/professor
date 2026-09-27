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
)

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
