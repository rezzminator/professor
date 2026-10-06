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

	"github.com/rezzminator/professor/pfm/internal/config"
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

	t.Run("diff ran", func(t *testing.T) {
		var stdout bytes.Buffer
		if code := RunProjectUpdates(project, home, true, &stdout, nil); code != 1 {
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
			var payload struct {
				Items []map[string]json.RawMessage `json:"items"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			for _, encoded := range payload.Items {
				if _, skipped := encoded["diffSkipped"]; skipped {
					t.Fatalf("diff ran but JSON includes diffSkipped: %s", stdout.String())
				}
			}
			return
		}
		t.Fatalf("no UPDATED item in the report: %s", stdout.String())
	})

	t.Run("post-update review", func(t *testing.T) {
		var stdout bytes.Buffer
		if code := renderProjectCheck(
			project,
			home,
			false,
			&stdout,
		); code != 1 ||
			!strings.Contains(stdout.String(), "REVIEW REQUIRED — 1 items; nothing was written.") {
			t.Fatalf("renderProjectCheck() code=%d, want 1 (review): %s", code, stdout.String())
		}
	})

	t.Run("textconv", func(t *testing.T) {
		if err := os.WriteFile(
			filepath.Join(store, ".git", "info", "attributes"),
			[]byte("* diff=upper\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		runStoreGit(t, "-C", store, "config", "diff.upper.textconv", "tr a-z A-Z")
		var stdout bytes.Buffer
		code := RunProjectUpdates(project, home, true, &stdout, nil)
		var report struct {
			Items []projectReportItem `json:"items"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		for _, item := range report.Items {
			if item.Status == projectUpdated {
				if code != 1 || item.DiffError != "" || !strings.Contains(item.Diff, "\n+upstream line\n") ||
					strings.Contains(item.Diff, "UPSTREAM LINE") {
					t.Fatalf(
						"textconv report code=%d diff=%q error=%q, want plain diff",
						code,
						item.Diff,
						item.DiffError,
					)
				}
				return
			}
		}
		t.Fatalf("no UPDATED item: %s", stdout.String())
	})
}

func TestProjectUpdatesDiffIgnoresAnInheritedForeignRepository(t *testing.T) {
	project, home := newUpdatedGitStoreProject(t, newGitScaffoldStore(t))
	foreign := newGitScaffoldStore(t)
	t.Setenv("GIT_DIR", filepath.Join(foreign, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "missing-index"))
	var stdout bytes.Buffer
	code := RunProjectUpdates(project, home, true, &stdout, nil)
	var report struct {
		Items []projectReportItem `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	for _, item := range report.Items {
		if item.Status == projectUpdated {
			if code != 1 || item.DiffError != "" || !strings.Contains(item.Diff, "\n+upstream line\n") {
				t.Fatalf("foreign repository report code=%d diff=%q error=%q", code, item.Diff, item.DiffError)
			}
			return
		}
	}
	t.Fatalf("no UPDATED item: code=%d %s", code, stdout.String())
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
	code := RunProjectUpdates(project, home, false, &stdout, nil)
	if code != 1 || strings.Contains(stdout.String(), "UNREADABLE") ||
		!strings.Contains(stdout.String(), "      +upstream line\n") {
		t.Fatalf("RunProjectUpdates() code=%d, want 1 with the upstream diff printed:\n%s", code, stdout.String())
	}
}

// TestProjectUpdatesDiffUnreadableIsAFailure pins 0-contracts § A: a pin
// whose SHA is absent from the store's git history renders the UNREADABLE
// line and a FAILED terminal (doctor and bare update both exit 3).
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
	if code := RunProjectUpdates(project, home, false, &stdout, nil); code != 3 {
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
	if code := RunProjectUpdates(project, home, true, &stdout, nil); code != 3 {
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
	if code := renderProjectCheck(project, home, false, &stdout); code != 3 {
		t.Fatalf("renderProjectCheck() code=%d stdout=%q, want 3", code, stdout.String())
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
	code := RunProjectUpdates(project, home, false, &stdout, nil)
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
	code := RunProjectUpdates(project, home, false, &stdout, nil)
	got := strings.TrimSpace(stdout.String())
	if code != 1 || strings.Contains(got, "UNREADABLE") || strings.Contains(got, "FAILED") ||
		!strings.HasSuffix(got, "REVIEW REQUIRED — 1 items; nothing was written.") {
		t.Fatalf("RunProjectUpdates() code=%d, want 1 with a REVIEW REQUIRED terminal:\n%s", code, got)
	}
	stdout.Reset()
	if code := RunProjectUpdates(project, home, true, &stdout, nil); code != 1 {
		t.Fatalf("RunProjectUpdates(--json) code=%d, want 1: %s", code, stdout.String())
	}
	var report struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	for _, item := range report.Items {
		if string(item["status"]) != `"UPDATED"` {
			continue
		}
		var skipped string
		if err := json.Unmarshal(item["diffSkipped"], &skipped); err != nil {
			t.Fatalf("diffSkipped is missing or invalid: %v\n%s", err, stdout.String())
		}
		_, diff := item["diff"]
		_, diffError := item["diffError"]
		if skipped != "self-hosted pin or store: no git history" || diff || diffError {
			t.Fatalf("skipped diff fields = %s", stdout.String())
		}
		return
	}
	t.Fatalf("no UPDATED item: %s", stdout.String())
}

// TestProjectUpdatesMissingBaselineNamesAdoptCommand is a REGRESSION test for
// the "no baseline" message text: watched failing against the old text
// ".professor/baseline.json not found" (no "pfm update adopt" guidance) —
// see the RED-first record in the qa report.
func TestProjectUpdatesMissingBaselineNamesAdoptCommand(t *testing.T) {
	var stdout bytes.Buffer
	if code := RunProjectUpdates(t.TempDir(), t.TempDir(), false, &stdout, nil); code != 3 {
		t.Fatalf("missing baseline check code=%d stdout=%q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "pfm update adopt pins an existing install") {
		t.Fatalf("missing baseline stdout=%q, want it to name pfm update adopt", stdout.String())
	}
}

func TestProjectUpdatesDistinguishesDirtyPinHistory(t *testing.T) {
	for _, kind := range []string{"dirty pin", "untracked pin"} {
		t.Run(kind, func(t *testing.T) {
			store := newGitScaffoldStore(t)
			project, home := newUpdatedGitStoreProject(t, store)
			local, template := "CLAUDE.md", "project/CLAUDE.md"
			if kind == "untracked pin" {
				local, template = "extra.md", "project/extra.md"
				if err := os.WriteFile(filepath.Join(project, local), []byte("local"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(store, "templates", filepath.FromSlash(template)),
					[]byte("dirty B\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"--root", project, local}
			if kind == "untracked pin" {
				args = []string{"--root", project, "--template", template, local}
			}
			var stdout, stderr bytes.Buffer
			if code := RunProjectUpdate("pin", args, &stdout, &stderr, config.Runtime{}); code != 0 {
				t.Fatalf("pin = %d %s", code, stderr.String())
			}
			if err := os.WriteFile(
				filepath.Join(store, "templates", filepath.FromSlash(template)),
				[]byte("upstream C\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			stdout.Reset()
			if code := RunProjectUpdates(project, home, true, &stdout, nil); code != 1 {
				t.Fatalf("report = %d %s", code, stdout.String())
			}
			var payload struct {
				Items []projectReportItem `json:"items"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range payload.Items {
				if item.Local == local {
					found = true
					if item.Status != projectUpdated || item.Diff != "" ||
						!strings.Contains(item.DiffSkipped, "uncommitted or untracked") {
						t.Errorf("dirty history = %+v; want explicitly unavailable pin diff", item)
					}
				}
			}
			if !found {
				t.Fatal("missing pinned item")
			}
			stdout.Reset()
			RunProjectUpdates(project, home, false, &stdout, nil)
			if !strings.Contains(stdout.String(), "upstream change UNAVAILABLE") ||
				!strings.Contains(stdout.String(), "compare by hand") {
				t.Errorf("human report lacks dirty history warning: %s", stdout.String())
			}
		})
	}
}

// TestProjectUpdatesReportsRetiredNamesAsReviewAndAScanFailureAsFailed: the
// scan RunProjectUpdates is handed gets every pinned local to skip; each hit
// renders as a `path:line` row under RETIRED-NAME and counts as a review
// item (exit 1), in the human body and the JSON object alike; a file the
// scan could not read renders UNREADABLE and fails the run (exit 3), never
// reading clean.
func TestProjectUpdatesReportsRetiredNamesAsReviewAndAScanFailureAsFailed(t *testing.T) {
	project, home := newUpdatedGitStoreProject(t, newGitScaffoldStore(t))
	var gotRoot string
	var gotPinned map[string]bool
	hits := func(root string, pinned map[string]bool) RetiredNameScan {
		gotRoot, gotPinned = root, pinned
		return RetiredNameScan{Hits: []RetiredNameHit{
			{Path: "scripts/legacy.sh", Line: 2, Name: "/wave", Kind: "command", Successor: "/flights:spec"},
			{Path: ".claude/codex-build.json", Line: 3, Name: "/jc", Kind: "command"},
		}}
	}

	var stdout bytes.Buffer
	if code := RunProjectUpdates(project, home, false, &stdout, hits); code != 1 {
		t.Fatalf("RunProjectUpdates() code=%d, want 1 (review):\n%s", code, stdout.String())
	}
	if gotRoot != project || !gotPinned[ClaudeInstructionsFile] {
		t.Fatalf("scan root=%q pinned=%v, want %q with %s pinned", gotRoot, gotPinned, project, ClaudeInstructionsFile)
	}
	for _, want := range []string{
		"  RETIRED-NAME  2\n",
		"    scripts/legacy.sh:2   /wave — retired command, now /flights:spec\n",
		"    .claude/codex-build.json:3   /jc — retired command, no successor\n",
		"REVIEW REQUIRED — 3 items; nothing was written.\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("report missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	if code := RunProjectUpdates(project, home, true, &stdout, hits); code != 1 {
		t.Fatalf("RunProjectUpdates(--json) code=%d, want 1:\n%s", code, stdout.String())
	}
	var payload struct {
		RetiredNames RetiredNameScan `json:"retiredNames"`
		Terminal     string          `json:"terminal"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("JSON report: %v\n%s", err, stdout.String())
	}
	if len(payload.RetiredNames.Hits) != 2 || payload.RetiredNames.Hits[0].Line != 2 ||
		payload.Terminal != "REVIEW REQUIRED — 3 items" {
		t.Fatalf("JSON retiredNames/terminal = %+v / %q", payload.RetiredNames, payload.Terminal)
	}

	unreadable := func(string, map[string]bool) RetiredNameScan {
		return RetiredNameScan{
			Failures: []RetiredNameFailure{{Path: "scripts/locked.sh", Error: "open: permission denied"}},
		}
	}
	stdout.Reset()
	if code := RunProjectUpdates(project, home, false, &stdout, unreadable); code != 3 {
		t.Fatalf("RunProjectUpdates(unreadable) code=%d, want 3:\n%s", code, stdout.String())
	}
	for _, want := range []string{
		"  RETIRED-NAME  0\n",
		"    scripts/locked.sh   retired-name scan UNREADABLE — open: permission denied\n",
		"FAILED — 1 file(s) could not be scanned for retired names; nothing was written.\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("report missing %q:\n%s", want, stdout.String())
		}
	}
}
