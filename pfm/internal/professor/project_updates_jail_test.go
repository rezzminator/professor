//go:build jail

package professor

// Real git is the subject here: these tests prove the store's git calls hold
// against the user's own git configuration (color.diff, diff.external, a
// textconv driver), which the fake git in testmain_test.go does not read.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// newRealGitScaffoldStore commits the scaffold fixture store with real git.
func newRealGitScaffoldStore(t *testing.T) string {
	t.Helper()
	store := newScaffoldFixtureStore(t)
	realStoreGit(t, "init", "-q", store)
	realStoreGit(t, "-C", store, "config", "user.email", "fixture.invalid")
	realStoreGit(t, "-C", store, "config", "user.name", "fixture-identity")
	realStoreGit(t, "-C", store, "add", "-A")
	realStoreGit(t, "-C", store, "commit", "-qm", "fixture")
	return store
}

func realStoreGit(t *testing.T, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// TestProjectUpdatesDiffIgnoresTheUsersGitDiffConfig: the diff printed under
// an UPDATED row is git's own unified diff whatever the user's gitconfig
// says — color.diff=always must not put ANSI escapes into the report or the
// JSON diff, and a diff.external tool must not replace (or, failing, break)
// it. Watched failing against the plain `git diff <pinned> -- …` call: the
// external `false` made every UPDATED diff UNREADABLE and the run exit 3.
func TestProjectUpdatesDiffIgnoresTheUsersGitDiffConfig(t *testing.T) {
	t.Cleanup(StubStoreRunnerForTest(obs.Runner(deps.RealRunner{})))
	store := newRealGitScaffoldStore(t)
	realStoreGit(t, "-C", store, "config", "color.diff", "always")
	realStoreGit(t, "-C", store, "config", "diff.external", "false")
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
			nil,
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
		realStoreGit(t, "-C", store, "config", "diff.upper.textconv", "tr a-z A-Z")
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
