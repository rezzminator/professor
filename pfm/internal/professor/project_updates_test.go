package professor

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmpaths "github.com/rezzminator/professor/pfm/internal/paths"
)

// newGitScaffoldStore commits the scaffold fixture store into a real git
// repository, so a pin carries a resolvable blueprint SHA.
func newGitScaffoldStore(t *testing.T) string {
	t.Helper()
	store := newScaffoldFixtureStore(t)
	runStoreGit(t, "init", "-q", store)
	runStoreGit(t, "-C", store, "config", "user.email", "fixture.invalid")
	runStoreGit(t, "-C", store, "config", "user.name", "fixture-identity")
	runStoreGit(t, "-C", store, "add", "-A")
	runStoreGit(t, "-C", store, "commit", "-qm", "fixture")
	return store
}

// newUpdatedGitStoreProject scaffolds a project from store, points its
// manifest at store, then moves the store's CLAUDE.md template forward in the
// working tree: the project's CLAUDE.md pin reads UPDATED with a one-line
// upstream change (+upstream line).
func newUpdatedGitStoreProject(t *testing.T, store string) (project, home string) {
	t.Helper()
	project = t.TempDir()
	if _, err := Scaffold(store, project, false, io.Discard); err != nil {
		t.Fatalf("Scaffold() err = %v", err)
	}
	manifest, err := json.Marshal(map[string]any{"interview": map[string]string{"blueprint_clone_path": store}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".professor", "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(store, "templates", "project", "CLAUDE.md")
	if err := os.WriteFile(template, []byte("# fixture contract\nupstream line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return project, t.TempDir()
}

// TestProjectUpdatesDiffIgnoresTheUsersGitDiffConfig: the diff printed under
// an UPDATED row is git's own unified diff whatever the user's gitconfig
// says — color.diff=always must not put ANSI escapes into the report or the
// JSON diff, and a diff.external tool must not replace (or, failing, break)
// it. Watched failing against the plain `git diff <pinned> -- …` call: the
// external `false` made every UPDATED diff UNREADABLE and the run exit 3.
func TestProjectUpdatesDiffIgnoresTheUsersGitDiffConfig(t *testing.T) {
	store := newGitScaffoldStore(t)
	runStoreGit(t, "-C", store, "config", "color.diff", "always")
	runStoreGit(t, "-C", store, "config", "diff.external", "false")
	project, home := newUpdatedGitStoreProject(t, store)

	var stdout bytes.Buffer
	if code := RunProjectUpdates(project, home, true, &stdout); code != 1 {
		t.Fatalf("RunProjectUpdates(--json) code=%d, want 1 (review): %s", code, stdout.String())
	}
	var report struct {
		Items []projectReportItem `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("report is not one JSON object: %v\n%s", err, stdout.String())
	}
	for _, item := range report.Items {
		if item.Status != projectUpdated {
			continue
		}
		if item.DiffError != "" || !strings.Contains(item.Diff, "\n+upstream line\n") ||
			strings.Contains(item.Diff, "\x1b[") {
			t.Fatalf(
				"UPDATED %s diff=%q diffError=%q, want git's plain unified diff",
				item.Local,
				item.Diff,
				item.DiffError,
			)
		}
		return
	}
	t.Fatalf("no UPDATED item in the report: %s", stdout.String())
}

// TestProjectUpdatesDiffReadsAFencedLinkedWorktreeStore: inside the dev
// fence the blueprint is a linked worktree whose .git file names a host path
// the container cannot see; the store SHA resolves through the fence's
// GIT_DIR contract, and the UPDATED diff must use the same contract. Watched
// failing against the diff call without it: `upstream change UNREADABLE`,
// exit 3, while the report header had resolved the SHA fine.
func TestProjectUpdatesDiffReadsAFencedLinkedWorktreeStore(t *testing.T) {
	repository := newGitScaffoldStore(t)
	linkedRoot := filepath.Join(t.TempDir(), "linked-worktree")
	runStoreGit(t, "-C", repository, "worktree", "add", "--detach", "-q", linkedRoot, "HEAD")
	for _, dir := range []string{ // git carries no empty directory; the store layout needs them
		"commands", "agents", "scripts", "skills", "epics", "codex", "docs-commands", "docs-agents",
	} {
		if err := os.MkdirAll(filepath.Join(linkedRoot, "templates", "project", dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	gitDir := runStoreGit(t, "-C", linkedRoot, "rev-parse", "--absolute-git-dir")
	if err := os.WriteFile(
		filepath.Join(linkedRoot, ".git"),
		[]byte("gitdir: /nonexistent/host/path\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv(pfmpaths.EnvDevRepoWorkTree, linkedRoot)
	t.Setenv(pfmpaths.EnvDevRepoGitDir, gitDir)
	project, home := newUpdatedGitStoreProject(t, linkedRoot)

	var stdout bytes.Buffer
	code := RunProjectUpdates(project, home, false, &stdout)
	if code != 1 || strings.Contains(stdout.String(), "UNREADABLE") ||
		!strings.Contains(stdout.String(), "      +upstream line\n") {
		t.Fatalf("RunProjectUpdates() code=%d, want 1 with the upstream diff printed:\n%s", code, stdout.String())
	}
}

// TestProjectUpdatesDiffUnreadableIsAFailure pins 0-contracts § A: a pin
// whose SHA is absent from the store's git history renders the UNREADABLE
// line and a FAILED terminal (doctor exit 3, bare update exit 1).
func TestProjectUpdatesDiffUnreadableIsAFailure(t *testing.T) {
	project, home := newUpdatedGitStoreProject(t, newGitScaffoldStore(t))
	baseline, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	pin := baseline.Files[ClaudeInstructionsFile]
	pin.PinnedSHA = "0000000"
	baseline.Files[ClaudeInstructionsFile] = pin
	if err := Save(project, baseline); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if code := RunProjectUpdates(project, home, false, &stdout); code != 3 {
		t.Fatalf("RunProjectUpdates() code=%d stdout=%q, want 3 (diff unreadable is a failure)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "upstream change UNREADABLE — ") {
		t.Fatalf("stdout missing the UNREADABLE line:\n%s", stdout.String())
	}
	wantHuman := "FAILED — 1 item(s) could not be read; nothing was written."
	got := strings.TrimSpace(stdout.String())
	if !strings.HasSuffix(got, wantHuman) || strings.Contains(got, "REVIEW REQUIRED") {
		t.Fatalf("doctor terminal = %q, want %q and no review terminal", got, wantHuman)
	}

	stdout.Reset()
	if code := RunProjectUpdates(project, home, true, &stdout); code != 3 {
		t.Fatalf("RunProjectUpdates(JSON) code=%d stdout=%q, want 3", code, stdout.String())
	}
	var payload struct {
		Terminal string `json:"terminal"`
		Items    []struct {
			DiffError string `json:"diffError"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	wantJSON := "FAILED — 1 item(s) could not be read"
	if payload.Terminal != wantJSON || len(payload.Items) == 0 || payload.Items[0].DiffError == "" {
		t.Fatalf("JSON terminal/items = %q/%+v", payload.Terminal, payload.Items)
	}

	stdout.Reset()
	if code := renderProjectCheck(project, home, false, &stdout); code != 1 {
		t.Fatalf("renderProjectCheck() code=%d stdout=%q, want 1", code, stdout.String())
	}
	got = strings.TrimSpace(stdout.String())
	if !strings.HasSuffix(got, wantHuman) {
		t.Fatalf("bare update terminal = %q, want %q", got, wantHuman)
	}
}

// TestProjectUpdatesRefusesAPinnedSHAThatIsNotAnObjectName: baseline.json
// ships inside the project, so its pinnedSha is untrusted — a value git reads
// as an option (`--output=<file>`) must never reach `git diff`, where it
// overwrote the named file and the row read EMPTY. The pin is refused as an
// unreadable diff: UNREADABLE row, FAILED terminal, exit 3, file untouched.
func TestProjectUpdatesRefusesAPinnedSHAThatIsNotAnObjectName(t *testing.T) {
	project, home := newUpdatedGitStoreProject(t, newGitScaffoldStore(t))
	planted := filepath.Join(t.TempDir(), "planted")
	baseline, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	pin := baseline.Files[ClaudeInstructionsFile]
	pin.PinnedSHA = "--output=" + planted
	baseline.Files[ClaudeInstructionsFile] = pin
	if err := Save(project, baseline); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	code := RunProjectUpdates(project, home, false, &stdout)
	if _, statErr := os.Stat(planted); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("git diff wrote the pin-named file %s (stat err %v):\n%s", planted, statErr, stdout.String())
	}
	got := strings.TrimSpace(stdout.String())
	if code != 3 || !strings.Contains(got, "upstream change UNREADABLE — ") ||
		!strings.HasSuffix(got, "FAILED — 1 item(s) could not be read; nothing was written.") {
		t.Fatalf("RunProjectUpdates() code=%d, want 3 with an UNREADABLE row and a FAILED terminal:\n%s", code, got)
	}
}

// TestProjectUpdatesSelfHostedPinIsAReviewNotAFailure: a file pinned while
// the store was self-hosted (PinnedSHA self-hosted@unknown) against a git
// store has no diff to read — it keeps the self-hosted review lines and the
// REVIEW REQUIRED terminal, never counts as an unreadable diff (no FAILED
// terminal, doctor exit 1 not 3).
func TestProjectUpdatesSelfHostedPinIsAReviewNotAFailure(t *testing.T) {
	project, home := newUpdatedGitStoreProject(t, newGitScaffoldStore(t))
	baseline, err := Load(project)
	if err != nil {
		t.Fatal(err)
	}
	pin := baseline.Files[ClaudeInstructionsFile]
	pin.PinnedSHA = UnknownSelfHostedSHA
	baseline.Files[ClaudeInstructionsFile] = pin
	if err := Save(project, baseline); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	code := RunProjectUpdates(project, home, false, &stdout)
	got := strings.TrimSpace(stdout.String())
	if code != 1 || strings.Contains(got, "UNREADABLE") || strings.Contains(got, "FAILED") ||
		!strings.HasSuffix(got, "REVIEW REQUIRED — 1 items; nothing was written.") {
		t.Fatalf("RunProjectUpdates() code=%d, want 1 with a REVIEW REQUIRED terminal:\n%s", code, got)
	}
}

// TestProjectUpdatesMissingBaselineNamesAdoptCommand is a REGRESSION test for
// the "no baseline" message text: watched failing against the old text
// ".professor/baseline.json not found" (no "pfm update adopt" guidance) —
// see the RED-first record in the qa report.
func TestProjectUpdatesMissingBaselineNamesAdoptCommand(t *testing.T) {
	var stdout bytes.Buffer
	if code := RunProjectUpdates(t.TempDir(), t.TempDir(), false, &stdout); code != 3 {
		t.Fatalf("missing baseline check code=%d stdout=%q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "pfm update adopt pins an existing install") {
		t.Fatalf("missing baseline stdout=%q, want it to name pfm update adopt", stdout.String())
	}
}
