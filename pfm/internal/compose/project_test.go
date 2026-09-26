package compose

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A chat's project is the repository it works in, not its cwd's basename: a
// chat in a linked worktree or a repo subdirectory belongs to the repo.
func TestProjectNameFilesAChatUnderItsRepository(t *testing.T) {
	fixture := testjail.GitRepoWithWorktrees(t, "acme")
	plain := filepath.Join(fixture.Base, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name, cwd, want string
	}{
		{"non-git dir", plain, "plain"},
		{"repo root", fixture.Repo, "acme"},
		{"repo subdirectory", fixture.Subdir, "acme"},
		{"worktree under .worktrees", fixture.Inner, "acme"},
		{"worktree outside the repo", fixture.Outer, "acme"},
		{"deleted worktree", filepath.Join(fixture.Repo, ".worktrees", "gone", "pfm"), "acme"},
		{"deleted plain dir", filepath.Join(fixture.Base, "vanished"), "vanished"},
	} {
		if got := projectName(testCase.cwd); got != testCase.want {
			t.Errorf("%s: projectName(%q) = %q, want %q", testCase.name, testCase.cwd, got, testCase.want)
		}
	}
}

// A live seat an orchestrator started in a worktree lists under the repo, and
// that block leads when the picker opens in the repo root.
func TestLiveWorktreeChatLeadsUnderTheRepoProject(t *testing.T) {
	fixture := testjail.GitRepoWithWorktrees(t, "acme")
	output := Compose(Input{
		Snapshot: gather.Snapshot{
			Panes: []gather.ProbePane{{Socket: "cc-7-8-9", PaneID: "%7", CurrentPath: fixture.Inner}},
			Crumbs: []gather.Crumb{{
				Filename: "cc-7-8-9.%7", Socket: "cc-7-8-9", PaneID: "%7",
				TranscriptPath: "/missing/worktree-seat.jsonl",
			}},
		},
		Options: Options{View: AllView, CurrentDir: fixture.Repo},
	})
	row, found := rowByID(output.Rows, "worktree-seat")
	if !found || row.Project != "acme" || row.CWD != fixture.Inner {
		t.Fatalf("worktree seat row = %#v, want Project acme with CWD %q", row, fixture.Inner)
	}
	if len(output.ProjectOrder) == 0 || output.ProjectOrder[0] != "acme" {
		t.Fatalf("ProjectOrder = %q, want acme first", output.ProjectOrder)
	}
	for index := range output.Rows {
		if output.Rows[index].Project == "test-junk" {
			t.Fatalf("a row is still filed under the worktree's basename: %#v", output.Rows[index])
		}
	}
}
