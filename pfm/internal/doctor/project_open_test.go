package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/update"
)

// indexChatIn records one Claude transcript that ran in cwd in the cache
// database, the way a scanned chat does.
func indexChatIn(t *testing.T, cwds ...string) {
	t.Helper()
	database, err := store.Open(store.WithWarningWriter(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	for index, cwd := range cwds {
		transcript := store.Transcript{
			UUID: "00000000-0000-0000-0000-00000000000" + string(rune('a'+index)),
			Path: filepath.Join(t.TempDir(), "t.jsonl"),
			CWD:  cwd,
		}
		if err := database.UpsertTranscript(context.Background(), transcript); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDoctorWarnsOncePerProjectNamingEveryUnresolvedRow: every adopter project
// a chat ran in — found through the cache database's cwds, a flight worktree
// standing for its project — gets one warning row naming each UPDATED, NEW and
// RETIRED-NAME entry no verb closed; a project with every row closed prints
// none; a project whose baseline cannot be read says so; and none of it moves
// doctor's exit code.
func TestDoctorWarnsOncePerProjectNamingEveryUnresolvedRow(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	t.Chdir(t.TempDir())
	var before bytes.Buffer
	beforeCode := runDoctor(nil, &before, &bytes.Buffer{}, runtime)

	blueprint := newProjectUpdatesStore(t)
	open := scaffoldAdopter(t, blueprint)
	broken := scaffoldAdopter(t, blueprint)
	writeProjectUpdatesFixture(t, broken, ".professor/baseline.json", "{not json")
	// closed was scaffolded from a blueprint that has moved nowhere since.
	closed := scaffoldAdopter(t, newProjectUpdatesStore(t))
	// UPDATED: upstream moved a pinned template; NEW: upstream added one.
	writeProjectUpdatesFixture(t, blueprint, "templates/project/settings.json", "{\"changed\": true}\n")
	writeProjectUpdatesFixture(t, blueprint, "templates/project/commands/added.md", "# added upstream\n")
	writeProjectUpdatesFixture(t, open, "scripts/legacy.sh", "#!/bin/sh\nclaude -p '/wave:refine'\n")
	if err := os.MkdirAll(filepath.Join(open, "sub", "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(open, ".worktrees", "flight-x"), 0o700); err != nil {
		t.Fatal(err)
	}
	indexChatIn(
		t,
		filepath.Join(open, "sub", "dir"),
		filepath.Join(open, ".worktrees", "flight-x"),
		closed,
		broken,
		filepath.Join(t.TempDir(), "no-project"),
		filepath.Join(t.TempDir(), "deleted-since"),
	)

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr, runtime)
	out := stdout.String()
	if code != beforeCode {
		t.Fatalf(
			"doctor code %d with unresolved projects, %d without — the rows must not move it:\n%s",
			code,
			beforeCode,
			out,
		)
	}
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "doctor: warning project_updates root=") {
			rows++
		}
	}
	if rows != 2 {
		t.Fatalf("project_updates warning rows = %d, want 2 (the open project and the unreadable one):\n%s", rows, out)
	}
	for _, want := range []string{
		"doctor: warning project_updates root=" + open + " unresolved=3 — ",
		"UPDATED .claude/settings.json",
		"NEW project/commands/added.md",
		"RETIRED-NAME scripts/legacy.sh×1",
		"pfm doctor --project-updates --root " + open,
		"doctor: warning project_updates root=" + broken + " UNREADABLE: ",
		"doctor: project_updates projects=3 unresolved=2 ",
		"doctor: machine checks clean; project_updates warnings=2\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "root="+closed) {
		t.Errorf("a project with every row closed got a row:\n%s", out)
	}
	if strings.Count(out, "root="+open) != 1 {
		t.Errorf(
			"the open project is named %d times, want once (worktree and subdirectory cwds fold into it):\n%s",
			strings.Count(out, "root="+open),
			out,
		)
	}
}

// TestDoctorSaysSoWhenTheChatIndexCannotBeRead: with no readable chat index the
// ledger names the failure and still checks the project the current directory
// is in — never a shorter list that reads as "no projects".
func TestDoctorSaysSoWhenTheChatIndexCannotBeRead(t *testing.T) {
	blueprint := newProjectUpdatesStore(t)
	open := scaffoldAdopter(t, blueprint)
	writeProjectUpdatesFixture(t, blueprint, "templates/project/commands/added.md", "# added upstream\n")
	t.Chdir(open)

	var stdout bytes.Buffer
	if warnings := printOpenProjectsDoctor(
		context.Background(), &stdout, nil, errors.New("pfm.db is locked"), t.TempDir(), &paths.MapEnv{},
	); warnings < 2 {
		t.Errorf("warnings=%d, want the unreadable index and the open project counted", warnings)
	}
	for _, want := range []string{
		"doctor: warning project_updates chat index unreadable (pfm.db is locked); ",
		"doctor: warning project_updates root=" + open + " unresolved=1 — NEW project/commands/added.md",
		"doctor: project_updates projects=1 unresolved=1 ",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, stdout.String())
		}
	}
}

// TestDoctorAbsentChatIndexIsNoAdvisoryWarning: a fresh host's chat index was
// never created, so it holds no chat; the row prints, but nothing is counted
// and doctor still ends `clean`.
func TestDoctorAbsentChatIndexIsNoAdvisoryWarning(t *testing.T) {
	t.Chdir(t.TempDir())
	var stdout bytes.Buffer
	absent := fmt.Errorf("cache database /x/pfm-cache.db: %w", fleetdb.ErrAbsent)
	if warnings := printOpenProjectsDoctor(
		context.Background(),
		&stdout,
		nil,
		absent,
		t.TempDir(),
		&paths.MapEnv{},
	); warnings != 0 {
		t.Errorf("warnings=%d, want 0 for an absent chat index:\n%s", warnings, stdout.String())
	}
	if !strings.Contains(stdout.String(), "chat index unreadable (cache database /x/pfm-cache.db: database absent)") {
		t.Errorf("the absent index row is gone:\n%s", stdout.String())
	}
}

// TestDoctorLedgerSkipsInUpdateHealthChild: pfm update's doctor child sets
// the host-only marker and names the skipped ledger, never a silent absence.
func TestDoctorLedgerSkipsInUpdateHealthChild(t *testing.T) {
	t.Chdir(t.TempDir())
	var stdout bytes.Buffer
	env := &paths.MapEnv{Values: map[string]string{update.DoctorHostOnlyEnv: "1"}}
	if warnings := printOpenProjectsDoctor(
		context.Background(),
		&stdout,
		nil,
		errors.New("pfm.db is locked"),
		t.TempDir(),
		env,
	); warnings != 0 {
		t.Errorf("warnings=%d, want 0 when the ledger is skipped", warnings)
	}
	skipped := "doctor: project_updates skipped (machine-update health check: PFM_UPDATE_DOCTOR_HOST_ONLY=1)\n"
	if got := stdout.String(); got != skipped {
		t.Errorf("output=%q, want only the explicit skip line", got)
	}
}

// TestDoctorLedgerFoldsSpellingsAndNamesWhatItCannotInspect: doctor run from
// inside a flight worktree (which carries its own copy of the baseline) and a
// chat recorded under a symlinked spelling both name the one project they
// belong to; a directory or baseline that cannot be inspected for any cause
// but absence (a symlink loop) is an UNREADABLE row counted among the
// projects, never dropped from the list.
func TestDoctorLedgerFoldsSpellingsAndNamesWhatItCannotInspect(t *testing.T) {
	blueprint := newProjectUpdatesStore(t)
	open := scaffoldAdopter(t, blueprint)
	writeProjectUpdatesFixture(t, blueprint, "templates/project/commands/added.md", "# added upstream\n")
	baseline, err := os.ReadFile(filepath.Join(open, ".professor", "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(open, ".worktrees", "flight-x")
	writeProjectUpdatesFixture(t, worktree, ".professor/baseline.json", string(baseline))
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(open, alias); err != nil {
		t.Fatal(err)
	}
	looped := t.TempDir()
	if err := os.MkdirAll(filepath.Join(looped, ".professor"), 0o700); err != nil {
		t.Fatal(err)
	}
	loopedBaseline := filepath.Join(looped, ".professor", "baseline.json")
	if err := os.Symlink(loopedBaseline, loopedBaseline); err != nil {
		t.Fatal(err)
	}
	loopedCwd := filepath.Join(t.TempDir(), "loop")
	if err := os.Symlink(loopedCwd, loopedCwd); err != nil {
		t.Fatal(err)
	}
	t.Chdir(worktree)

	roots, unreadable := adopterRoots([]string{worktree, alias, open, looped, loopedCwd})
	if len(roots) != 1 || roots[0] != open {
		t.Errorf("roots = %q, want only %q (worktree and symlinked spellings fold into it)", roots, open)
	}
	if len(unreadable) != 2 || !strings.Contains(strings.Join(unreadable, ""), "cwd="+looped+" UNREADABLE: ") ||
		!strings.Contains(strings.Join(unreadable, ""), "cwd="+loopedCwd+" UNREADABLE: ") {
		t.Errorf("unreadable rows = %q, want one for %s and one for %s", unreadable, looped, loopedCwd)
	}

	var stdout bytes.Buffer
	printOpenProjectsDoctor(context.Background(), &stdout, nil, nil, t.TempDir(), &paths.MapEnv{})
	if strings.Count(stdout.String(), "root=") != 1 ||
		!strings.Contains(stdout.String(), "root="+open+" unresolved=1 ") {
		t.Errorf("doctor run inside the worktree must name its project once:\n%s", stdout.String())
	}
}

// TestDoctorLedgerBoundsAProjectThatNeverAnswers: a project whose baseline is
// a FIFO blocks its report forever; the ledger gives it an UNREADABLE
// timed-out row at the deadline and still prints every other project's row.
func TestDoctorLedgerBoundsAProjectThatNeverAnswers(t *testing.T) {
	blueprint := newProjectUpdatesStore(t)
	open := scaffoldAdopter(t, blueprint)
	writeProjectUpdatesFixture(t, blueprint, "templates/project/commands/added.md", "# added upstream\n")
	stuck := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stuck, ".professor"), 0o700); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(stuck, ".professor", "baseline.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Release the abandoned reader: a writer that opens and closes gives it EOF.
		if writer, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = writer.Close()
		}
	})
	previous := openProjectDeadline
	openProjectDeadline = 2 * time.Second
	t.Cleanup(func() { openProjectDeadline = previous })

	rows := buildOpenProjectRows([]string{open, stuck}, t.TempDir())
	if !strings.HasPrefix(rows[0], "doctor: warning project_updates root="+open+" unresolved=1 — NEW ") {
		t.Errorf("the answering project's row = %q", rows[0])
	}
	if rows[1] != "doctor: warning project_updates root="+stuck+" UNREADABLE: report not built within 2s\n" {
		t.Errorf("the stuck project's row = %q, want the timed-out UNREADABLE row", rows[1])
	}
}

func TestDoctorUpdateHealthSkipsOnlyTheHostLedger(t *testing.T) {
	t.Setenv("PFM_UPDATE_DOCTOR_HOST_ONLY", "1")
	runtime := buildCleanDoctorHome(t)
	t.Chdir(t.TempDir())
	blueprint := newProjectUpdatesStore(t)
	project := scaffoldAdopter(t, blueprint)
	writeProjectUpdatesFixture(t, project, "notes.md", "run /wave\n")
	indexChatIn(t, project)
	var health, report bytes.Buffer
	runDoctor(nil, &health, &bytes.Buffer{}, runtime)
	skipped := "doctor: project_updates skipped (machine-update health check: PFM_UPDATE_DOCTOR_HOST_ONLY=1)\n"
	if !strings.Contains(health.String(), skipped) {
		t.Errorf("update health output lacks explicit ledger skip:\n%s", health.String())
	}
	code := runDoctor([]string{"--project-updates", "--root", project}, &report, &bytes.Buffer{}, runtime)
	if code != 1 || !strings.Contains(report.String(), "notes.md:1") ||
		!strings.Contains(report.String(), "RETIRED-NAME") {
		t.Fatalf("explicit report code=%d output=%q, want the full retired-name report", code, report.String())
	}
}

type ledgerDeadlineRunner struct {
	deps.Runner
	cancelled chan error
	release   chan struct{}
}

func (runner *ledgerDeadlineRunner) Run(ctx context.Context, _ []string, _ deps.RunOptions) (deps.RunResult, error) {
	select {
	case <-ctx.Done():
		runner.cancelled <- ctx.Err()
		return deps.RunResult{}, ctx.Err()
	case <-runner.release:
		return deps.RunResult{}, errors.New("fixture released")
	}
}

func TestDoctorLedgerCancelsGitWithItsDeadline(t *testing.T) {
	root := scaffoldAdopter(t, newProjectUpdatesStore(t))
	runner := &ledgerDeadlineRunner{cancelled: make(chan error, 1), release: make(chan struct{})}
	t.Cleanup(func() { close(runner.release) })
	t.Cleanup(update.StubRunnerForTest(runner))
	previous := openProjectDeadline
	openProjectDeadline = 100 * time.Millisecond
	t.Cleanup(func() { openProjectDeadline = previous })
	rows := buildOpenProjectRows([]string{root}, t.TempDir())
	if len(rows) != 1 || !strings.Contains(rows[0], "UNREADABLE") {
		t.Fatalf("ledger rows=%q, want an unreadable timed-out project", rows)
	}
	select {
	case err := <-runner.cancelled:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("git cancellation=%v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ledger git context was not cancelled with its deadline")
	}
}
