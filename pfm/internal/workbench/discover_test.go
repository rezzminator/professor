package workbench

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
)

func TestDiscoverEligibility(t *testing.T) {
	root, scribe := benchFixture(t)
	lab := filepath.Join(root, ".professor", "lab")
	for _, rel := range []string{
		".professor/lab", "node_modules/x", ".git/x", ".worktrees/f/docs/scribe",
		"a/b/c/d/e/f/g", "vendor/x", "venv/x", ".hidden/x", ".",
	} {
		seedBench(t, filepath.Join(root, rel), exampleManifest)
	}
	outside := filepath.Join(t.TempDir(), "linked")
	seedBench(t, outside, exampleManifest)
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	got, walkErrors := Discover([]string{root})
	var dirs []string
	for _, bench := range got {
		dirs = append(dirs, bench.Dir)
	}
	if len(walkErrors) != 0 || !reflect.DeepEqual(dirs, []string{lab, scribe}) {
		t.Fatalf("Discover = %v, errors %v; want only %v", dirs, walkErrors, []string{lab, scribe})
	}
	for _, rel := range []string{"node_modules/x", ".git/x", ".worktrees/f/docs/scribe", "a/b/c/d/e/f/g", "vendor/x", "venv/x", ".hidden/x", "linked"} {
		if bench, found, err := Nearest(filepath.Join(root, rel)); found || err != nil {
			t.Errorf("Nearest(%s) = %#v, %t, %v; want ineligible", rel, bench, found, err)
		}
	}
}

func TestDiscoverNestedRepository(t *testing.T) {
	root, _ := benchFixture(t)
	lab := filepath.Join(root, ".professor", "lab")
	seedBench(t, lab, exampleManifest)
	if err := os.Mkdir(filepath.Join(root, ".professor", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	benches, walkErrors := Discover([]string{root})
	if len(walkErrors) != 0 || len(benches) != 2 || benches[0].Dir != lab || benches[0].Project != "acme" {
		t.Fatalf("nested repo = %#v, %v", benches, walkErrors)
	}
}

func nestedManagedRootFixture(t *testing.T) (repo, root, bench string) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(repo, "apps", "portal")
	writeBenchFile(t, professor.BaselinePath(root), "{}")
	bench = filepath.Join(root, "docs", "scribe")
	seedBench(t, bench, `{"prompt":"scribe.md","title":"Scribe"}`)
	return repo, root, bench
}

func TestDiscoverManagedRootNestedInRepository(t *testing.T) {
	_, root, _ := nestedManagedRootFixture(t)
	benches, walkErrors := Discover([]string{root})
	if len(walkErrors) != 0 || len(benches) != 1 || benches[0].Project != "acme" ||
		benches[0].Key != "acme › Scribe" || benches[0].Root != root {
		t.Fatalf("nested managed root = %#v, walk errors %v", benches, walkErrors)
	}
}

func TestDiscoverUnreadableDirectory(t *testing.T) {
	root, scribe := benchFixture(t)
	// A root that is a regular file fails its directory read for real (ENOTDIR),
	// even in a root-run fence where a chmod denial would not.
	unreadable := filepath.Join(filepath.Dir(root), "zeta")
	writeBenchFile(t, unreadable, "not a directory")
	benches, walkErrors := Discover([]string{unreadable, root})
	if len(benches) != 1 || benches[0].Dir != scribe || len(walkErrors) != 1 {
		t.Fatalf("Discover = %#v, %v; want readable bench and one error", benches, walkErrors)
	}
	got := walkErrors[0]
	want := "workbench discovery under " + unreadable + " could not read " + unreadable + ": open " + unreadable +
		": not a directory"
	if got.Root != unreadable || got.Path != unreadable || !errors.Is(got.Err, syscall.ENOTDIR) || got.Error() != want {
		t.Fatalf("WalkError = %#v, %q; want %q", got, got.Error(), want)
	}
}

// A regular file named .professor holds no baseline and no manifest: it is
// absence, never a failure that refuses every launch below it.
func TestProfessorFileIsNotAWorkbench(t *testing.T) {
	root, scribe := benchFixture(t)
	writeBenchFile(t, filepath.Join(root, "src", ".professor"), "notes\n")
	cwd := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	if bench, found, err := Nearest(cwd); err != nil || found {
		t.Fatalf("Nearest = %#v, %v, %v; want not found, nil", bench, found, err)
	}
	if persona, err := ForLaunch(cwd, pfmengine.Claude, New); err != nil || persona.Applies() {
		t.Fatalf("ForLaunch = %#v, %v; want the fleet persona", persona, err)
	}
	benches, walkErrors := Discover([]string{root})
	if len(walkErrors) != 0 || len(benches) != 1 || benches[0].Dir != scribe {
		t.Fatalf("Discover = %#v, %v; want the scribe bench alone", benches, walkErrors)
	}
}

func TestDiscoverDuplicateTitles(t *testing.T) {
	root, scribe := benchFixture(t)
	notes := filepath.Join(root, "notes", "scribe")
	seedBench(t, notes, exampleManifest)
	benches, walkErrors := Discover([]string{root})
	if len(walkErrors) != 0 || len(benches) != 2 || benches[0].Key != "acme › docs/scribe" ||
		benches[1].Key != "acme › notes/scribe" {
		t.Fatalf("keys = %#v, %v", benches, walkErrors)
	}
	owner, found := Owner(benches, filepath.Join(scribe, "notes"))
	if !found || owner.Dir != scribe {
		t.Fatalf("Owner = %#v, %t", owner, found)
	}
}

func TestDiscoverDuplicateTitlesAcrossManagedRoots(t *testing.T) {
	repo, portalRoot, _ := nestedManagedRootFixture(t)
	kioskRoot := filepath.Join(repo, "apps", "kiosk")
	writeBenchFile(t, professor.BaselinePath(kioskRoot), "{}")
	seedBench(t, filepath.Join(kioskRoot, "docs", "scribe"), `{"prompt":"scribe.md","title":"Scribe"}`)

	benches, walkErrors := Discover([]string{portalRoot, kioskRoot})
	wantKeys := []string{"acme › apps/kiosk/docs/scribe", "acme › apps/portal/docs/scribe"}
	if len(walkErrors) != 0 || len(benches) != len(wantKeys) {
		t.Fatalf("benches = %#v, walk errors %v", benches, walkErrors)
	}
	for index, bench := range benches {
		if bench.Project != "acme" || bench.Key != wantKeys[index] {
			t.Fatalf("bench %d = %#v, want Project acme and Key %q", index, bench, wantKeys[index])
		}
	}
}

func TestOwnerAndNearestInnerWins(t *testing.T) {
	root, scribe := benchFixture(t)
	docs := filepath.Join(root, "docs")
	seedBench(t, docs, exampleManifest)
	benches, _ := Discover([]string{root})
	cwd := filepath.Join(scribe, "notes")
	owner, found := Owner(benches, cwd)
	nearest, nearFound, err := Nearest(cwd)
	if !found || !nearFound || err != nil || owner.Dir != scribe || nearest.Dir != scribe {
		t.Fatalf("inner owner = %#v, %t; nearest = %#v, %t, %v", owner, found, nearest, nearFound, err)
	}
	if _, ok := Owner([]Bench{LoadBench(scribe, root)}, scribe+"-other"); ok {
		t.Fatal("path component boundary matched another directory")
	}
}

func TestNearestReadsTheLinkedWorktreesOwnBench(t *testing.T) {
	root, scribe := benchFixture(t)
	worktree := filepath.Join(root, ".worktrees", "f")
	gitdir := filepath.Join(root, ".git", "worktrees", "f")
	writeBenchFile(t, filepath.Join(worktree, ".git"), "gitdir: "+gitdir+"\n")
	writeBenchFile(t, filepath.Join(gitdir, "commondir"), "../..\n")
	ownScribe := filepath.Join(worktree, "docs", "scribe")
	seedBench(t, ownScribe, exampleManifest)
	branchOnly := filepath.Join(worktree, "docs", "draft")
	seedBench(t, branchOnly, exampleManifest)
	absent := filepath.Join(worktree, "docs", "plain")
	if err := os.MkdirAll(absent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "plain", ".professor"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeBenchFile(t, paths.WorkbenchManifest(filepath.Join(root, "docs", "plain")), exampleManifest)
	for _, test := range []struct {
		name, cwd, want string
	}{
		{"bench on both trees", ownScribe, ownScribe},
		{"bench only on the worktree branch", branchOnly, branchOnly},
		{"bench only in the main checkout", absent, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			bench, found, err := Nearest(test.cwd)
			if err != nil || found != (test.want != "") || bench.Dir != test.want {
				t.Fatalf("Nearest(%s) = %#v, %t, %v; want dir %q", test.cwd, bench, found, err, test.want)
			}
			if test.want != "" && (bench.Root != worktree || bench.Err != nil || bench.Project != "acme") {
				t.Fatalf("Nearest(%s) root %q project %q err %v; want root %q project acme",
					test.cwd, bench.Root, bench.Project, bench.Err, worktree)
			}
		})
	}
	owner, owned := Owner([]Bench{LoadBench(scribe, root)}, ownScribe)
	if !owned || owner.Dir != scribe {
		t.Fatalf("worktree owner = %#v, %t; want the main checkout's bench", owner, owned)
	}
}

func TestNearestNeedsManagedRoot(t *testing.T) {
	_, dir := benchFixture(t)
	if _, found, err := Nearest(dir); !found || err != nil {
		t.Fatalf("managed control = %t, %v", found, err)
	}
	x := filepath.Join(t.TempDir(), "scratch", "x")
	seedBench(t, x, exampleManifest)
	if _, found, err := Nearest(x); found || err != nil {
		t.Fatalf("unmanaged = %t, %v", found, err)
	}
}

func TestManagedRoots(t *testing.T) {
	root, _ := benchFixture(t)
	got, err := ManagedRoots(
		[]string{filepath.Join(root, "docs"), filepath.Join(root, ".professor", "lab"), t.TempDir()},
	)
	if err != nil || !reflect.DeepEqual(got, []string{root}) {
		t.Fatalf("ManagedRoots = %v, %v", got, err)
	}
}

func TestManagedRootsReportsStatError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "acme")
	// A .professor symlink loop gives ELOOP even in the root-run fence; a
	// regular file named .professor is absence (TestProfessorFileIsNotAWorkbench).
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".professor", filepath.Join(root, ".professor")); err != nil {
		t.Fatal(err)
	}
	_, _, resolveErr := professor.ResolveProjectRoot(root)
	got, err := ManagedRoots([]string{root})
	if err == nil || !errors.Is(err, syscall.ELOOP) || err.Error() != resolveErr.Error() || len(got) != 0 {
		t.Fatalf("stat fault = %v, %v; want %v", got, err, resolveErr)
	}
}

func TestNearestKeepsWalkingPastIneligibleManifest(t *testing.T) {
	root, scribe := benchFixture(t)
	inner := filepath.Join(scribe, "node_modules", "x")
	seedBench(t, inner, exampleManifest)
	got, found, err := Nearest(inner)
	if err != nil || !found || got.Dir != scribe {
		t.Fatalf("ineligible inner = %#v, %t, %v", got, found, err)
	}
	if err := os.Remove(paths.WorkbenchManifest(scribe)); err != nil {
		t.Fatal(err)
	}
	seedBench(t, root, exampleManifest)
	if _, found, err := Nearest(root); found || err != nil {
		t.Fatalf("depth zero = %t, %v", found, err)
	}
}
