package compose

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testjail/fakegit"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// A chat's project is the repository it works in, not its cwd's basename: a
// chat in a linked worktree or a repo subdirectory belongs to the repo.
func TestProjectNameFilesAChatUnderItsRepository(t *testing.T) {
	fixture := testjail.GitRepoWithWorktrees(t, &fakegit.Runner{}, "acme")
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

// A new chat files where Compose would file its row: under the workbench that
// owns its directory, else under its repository.
func TestProjectOfFilesThroughAnOwningWorkbench(t *testing.T) {
	fixture := testjail.GitRepoWithWorktrees(t, &fakegit.Runner{}, "acme")
	benches := []workbench.Bench{{Dir: fixture.Subdir, Key: "acme › bench", Project: "acme", Root: fixture.Repo}}
	if name, root := ProjectOf(fixture.Subdir, benches); name != "acme › bench" || root != fixture.Subdir {
		t.Errorf("ProjectOf(bench) = %q %q, want the bench", name, root)
	}
	if name, root := ProjectOf(fixture.Inner, benches); name != "acme" || root != fixture.Repo {
		t.Errorf("ProjectOf(worktree) = %q %q, want the repository", name, root)
	}
}

// A live seat an orchestrator started in a worktree lists under the repo, and
// that block leads when the picker opens in the repo root.
func TestLiveWorktreeChatLeadsUnderTheRepoProject(t *testing.T) {
	fixture := testjail.GitRepoWithWorktrees(t, &fakegit.Runner{}, "acme")
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

func TestRecordedSpaceFilesTheChatAndLeavesOthersFolderPlaced(t *testing.T) {
	chat := func(id string) store.Transcript {
		return transcript(
			id,
			testjail.Place("/accounts/1/projects/atlas/"+id+".jsonl"),
			testjail.Place("/work/atlas"),
			id,
			100,
			5,
			900,
		)
	}
	output := Compose(Input{
		Transcripts: []store.Transcript{chat("in-glass"), chat("in-folder")},
		Spaces:      map[string]string{"in-glass": "glass", "gone-chat": "epic"},
		ClaudeSeats: fixtureClaudeSeats(),
		Options:     Options{View: DefaultView, PrimaryAccount: 1},
	})
	got := map[string]string{}
	for _, row := range output.Rows {
		got[row.ID] = row.Space
	}
	if want := (map[string]string{"in-glass": "glass", "in-folder": ""}); got["in-glass"] != want["in-glass"] ||
		got["in-folder"] != want["in-folder"] {
		t.Fatalf("row spaces = %v, want %v", got, want)
	}
	if want := (map[string]string{"in-glass": "glass", "gone-chat": "epic"}); !reflect.DeepEqual(output.Spaces, want) {
		t.Fatalf("output spaces = %v, want every recorded space %v", output.Spaces, want)
	}
}
