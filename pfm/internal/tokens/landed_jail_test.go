//go:build jail

package tokens

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestMeasureLandedCountsCodeAndExcludesEachClass(t *testing.T) {
	dir, base := landedRepo(t)
	landed, err := measureLanded(context.Background(), deps.RealRunner{}, dir, base)
	if err != nil {
		t.Fatal(err)
	}
	got := [6]int64{landed.code, landed.tests, landed.docs, landed.generated, landed.locks, landed.binaryFiles}
	if want := [6]int64{9, 18, 5, 8, 6, 2}; got != want {
		t.Errorf("code, tests, docs, generated, locks, binary = %v, want %v", got, want)
	}
}

// gitDiscoveryStop is where git says its repository search ended, which
// depends on the host's mounts, not on the code: "(or any parent up to mount
// point /)" plus a filesystem-boundary line where the temp directory is its own
// mount, "(or any of the parent directories): .git" where the search reaches /.
var gitDiscoveryStop = regexp.MustCompile(
	`\(or any parent up to mount point [^)]*\)\nStopping at filesystem boundary \(GIT_DISCOVERY_ACROSS_FILESYSTEM not set\)\.` +
		`|\(or any of the parent directories\): \.git`,
)

// TestMeasureLandedNamesTheFailedCommand: a git failure is an error naming
// the command, never 0 lines.
func TestMeasureLandedNamesTheFailedCommand(t *testing.T) {
	dir, _ := landedRepo(t)
	top := runGit(t, dir, "rev-parse", "--show-toplevel")
	_, badBase := measureLanded(context.Background(), deps.RealRunner{}, dir, "no-such-base")
	outside := t.TempDir()
	_, notRepo := measureLanded(context.Background(), deps.RealRunner{}, outside, "HEAD")
	got := gitDiscoveryStop.ReplaceAllString(
		strings.NewReplacer(top, "{top}", outside, "{outside}").Replace(fmt.Sprint(badBase, " | ", notRepo)),
		"{discovery stop}",
	)
	if want := "git -C {top} diff --numstat -z no-such-base: exit 128: fatal: ambiguous argument 'no-such-base': unknown revision or path not in the working tree.\nUse '--' to separate paths from revisions, like this:\n'git <command> [<revision>...] -- [<file>...]' | git -C {outside} rev-parse --show-toplevel: exit 128: fatal: not a git repository {discovery stop}"; got != want {
		t.Errorf("bad base | outside a repository = %q, want %q", got, want)
	}
}

// TestTreeReportsLandedLinesPerMillionInput: with --worktree and --base the
// tree carries the landed lines, their rate per million input tokens and the
// excluded counts; a bad base prints the report unmeasured and exits 1 naming the command.
func TestTreeReportsLandedLinesPerMillionInput(t *testing.T) {
	dir, base := landedRepo(t)
	root := fixture(t, "tree/projects")
	code, document, stderr := treeJSON(t, root, "--tree", treeSession, "--worktree", dir, "--base", base)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	tree := document.Tree
	if tree.LandedLines == nil || *tree.LandedLines != 9 || tree.LandedNotMeasured != nil ||
		tree.LandedPerMillionInput == nil || math.Abs(*tree.LandedPerMillionInput-9/(162100/1e6)) > 1e-9 ||
		tree.LandedExcluded == nil || *tree.LandedExcluded != (landedExcluded{18, 5, 8, 6, 2}) {
		t.Errorf("landed %+v", tree)
	}
	// --worktree is the write scope: the fixture's /work/repo writes fall outside it
	if tree.WriteScope != dir || tree.WrittenBytes != 0 {
		t.Errorf("scope %q written %d, want the worktree and 0", tree.WriteScope, tree.WrittenBytes)
	}
	code, stdout, _ := treeAudit(t, root, "--tree", treeSession, "--worktree", dir, "--base", base)
	if code != 0 || !strings.Contains(stdout, "\nlanded code lines          9 ("+dir+" against "+base+"; ") ||
		!strings.Contains(stdout, "\nlanded lines per M input   55.52\n") {
		t.Errorf("exit %d stdout:\n%s", code, stdout)
	}
	// A landed-lines failure keeps the tokens, dollars and bytes already measured: the report prints
	// with the lines marked not measured and the failure named, and the run exits 1 for the gap.
	code, stdout, stderr = treeAudit(t, root, "--tree", treeSession, "--worktree", dir, "--base", "no-such-base")
	if code != 1 || !strings.Contains(stderr, "landed lines: git -C ") ||
		!strings.Contains(stderr, " diff --numstat -z no-such-base: exit 128") ||
		!strings.Contains(stdout, "\ninput tokens               162100 (") ||
		!strings.Contains(stdout,
			"\nlanded code lines          NOT MEASURED — --tree "+treeSession+": landed lines: git -C ") {
		t.Errorf("bad base: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}
