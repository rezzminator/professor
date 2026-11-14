//go:build jail

package fakegit

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// fakeGitContractStep writes files, removes files, links symlinks, then runs
// one git argv in the repository directory.
type fakeGitContractStep struct {
	write   map[string]string
	remove  []string
	symlink map[string]string // path → the target it names
	dir     string            // the directory under the repository to run in
	argv    []string
	stderr  bool // compare stderr too: the caller reads git's wording
}

// TestFakeGitAgreesWithRealGit runs one table through real git and through
// Runner, each in its own base holding repo/ and remote.git/, and compares
// every step's stdout and exit code and the work tree after it. Real git is
// the subject, so the test lives in a jail file.
func TestFakeGitAgreesWithRealGit(t *testing.T) {
	gitBinary, err := deps.RealRunner{}.LookPath("git")
	if err != nil {
		t.Fatalf("the contract test needs real git: %v", err)
	}
	steps := []fakeGitContractStep{
		{argv: []string{"init", "-q"}},
		{argv: []string{"config", "user.email", "fixture.invalid"}},
		{argv: []string{"config", "user.name", "fixture-identity"}},
		{
			write: map[string]string{
				"README.md":   "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\n",
				"prices.json": "{}\n",
			},
			argv: []string{"add", "README.md", "prices.json"},
		},
		{argv: []string{"commit", "-qm", "A"}},
		{argv: []string{"tag", "v1.0.0"}},
		{
			write: map[string]string{
				"README.md":          "one\ntwo\nthree\nfour\nFIVE\nsix\nseven\neight\n",
				"releases/v2.0.0.md": "notes\n",
			},
			argv: []string{"add", "-A"},
		},
		{argv: []string{"commit", "-qm", "B release"}},
		{argv: []string{"tag", "-a", "-m", "v2.0.0", "v2.0.0"}},
		{argv: []string{"show", "v1.0.0:releases/v2.0.0.md"}, stderr: true},
		{argv: []string{"show", "v1.0.0:README.md"}},
		{argv: []string{"init", "--bare", "-q", "../remote.git"}},
		{argv: []string{"remote", "add", "origin", "../remote.git"}},
		{argv: []string{"push", "-q", "origin", "HEAD", "--tags"}},
		{argv: []string{"checkout", "-qb", "installed", "v1.0.0"}},
		{argv: []string{"show", "v2.0.0:releases/v2.0.0.md"}},
		{argv: []string{"show", "HEAD:releases/v2.0.0.md"}, stderr: true},
		{argv: []string{"show", "v9.9.9:README.md"}, stderr: true},
		{argv: []string{"rev-parse", "--verify", "HEAD"}},
		{argv: []string{"rev-parse", "--verify", "v2.0.0"}},
		{argv: []string{"rev-parse", "--verify", "v2.0.0^{commit}"}},
		{argv: []string{"rev-parse", "--verify", "v9.9.9^{commit}"}},
		{argv: []string{"rev-parse", "--short", "HEAD"}},
		{argv: []string{"rev-parse", "--git-common-dir"}},
		{argv: []string{"rev-parse", "--is-inside-work-tree"}},
		{argv: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}},
		{argv: []string{"fetch", "--tags"}},
		{argv: []string{"tag", "--list"}},
		{argv: []string{"describe", "--tags", "--abbrev=0", "HEAD"}},
		{argv: []string{"describe", "--tags", "--abbrev=0", "v2.0.0"}},
		{argv: []string{"merge-base", "--is-ancestor", "v2.0.0", "HEAD"}},
		{argv: []string{"merge-base", "--is-ancestor", "HEAD", "v2.0.0"}},
		{argv: []string{"ls-tree", "--name-only", "v2.0.0", "releases/"}},
		{argv: []string{"ls-tree", "--name-only", "v1.0.0", "releases/"}},
		{
			write: map[string]string{"prices.json": "{\"refreshed\":true}\n", "notes.txt": "untracked\n"},
			argv:  []string{"status", "--porcelain", "--untracked-files=all"},
		},
		{argv: []string{"status", "--porcelain", "--", "prices.json"}},
		{argv: []string{"checkout", "--", "prices.json"}},
		{argv: []string{"status", "--porcelain", "--untracked-files=all"}},
		{
			write: map[string]string{".gitignore": "out/\n", "out/built.js": "built\n"},
			argv:  []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--deduplicate"},
		},
		{argv: []string{"status", "--porcelain", "--untracked-files=all", "--ignored", "--", "."}},
		{
			remove: []string{".gitignore", "out/built.js", "notes.txt"},
			argv:   []string{"worktree", "add", "--detach", "--quiet", "../stage", "v2.0.0"},
		},
		{argv: []string{"worktree", "remove", "--force", "../stage"}},
		{argv: []string{"merge", "--ff-only", "--quiet", "v2.0.0"}},
		{argv: []string{"rev-parse", "--verify", "HEAD"}},
		{argv: []string{"rev-parse", "--verify", "-q", "HEAD:./README.md"}},
		{argv: []string{"rev-parse", "--verify", "-q", "HEAD:./missing.md"}},
		{dir: "releases", argv: []string{"rev-parse", "--verify", "-q", "HEAD:./v2.0.0.md"}},
		{argv: []string{"hash-object", "--", "README.md"}},
		{dir: "releases", argv: []string{"hash-object", "--", "v2.0.0.md"}},
		{dir: "releases", argv: []string{"status", "--porcelain", "--untracked-files=all", "--ignored", "--", "."}},
		{argv: []string{"hash-object", "--", "missing.md"}, stderr: true},
		{
			write: map[string]string{"prices.json": "{\"refreshed\":true}\n"},
			argv:  []string{"reset", "--keep", "v1.0.0"},
		},
		{argv: []string{"rev-parse", "--verify", "HEAD"}},
		{argv: []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "v2.0.0", "--", "README.md"}},
		{
			argv: []string{
				"log",
				"--no-color",
				"--no-show-signature",
				"--reverse",
				"--format=%h %s",
				"v1.0.0..v2.0.0",
				"--",
				"README.md",
			},
		},
		{argv: []string{"checkout", "--", "missing.md"}, stderr: true},
		{remove: []string{"README.md"}, argv: []string{"merge", "--ff-only", "--quiet", "v2.0.0"}},
		{argv: []string{"status", "--porcelain", "--untracked-files=all"}},
		{remove: []string{"README.md"}, argv: []string{"reset", "--keep", "v1.0.0"}},
		{argv: []string{"status", "--porcelain", "--untracked-files=all"}},
		{remove: []string{"README.md"}, argv: []string{"checkout", "--quiet", "--detach", "v2.0.0"}},
		{argv: []string{"status", "--porcelain", "--untracked-files=all"}},
		{argv: []string{"checkout", "--quiet", "installed"}},
		{argv: []string{"merge", "--ff-only", "--quiet", "v2.0.0"}},
		{
			write: map[string]string{
				"build/kept.js": "kept\n",
				"seq.txt":       fakeGitContractSeq,
				"f.go":          fakeGitContractFuncs,
			},
			argv: []string{"add", "-A"},
		},
		{argv: []string{"commit", "-qm", "C"}},
		{
			write: map[string]string{".gitignore": "build/\n", "build/kept.js": "changed\n", "build/new.js": "new\n"},
			argv:  []string{"status", "--porcelain", "--untracked-files=all"},
		},
		{argv: []string{"status", "--porcelain", "--untracked-files=all", "--ignored", "--", "."}},
		{write: map[string]string{"notes/a.md": "a\n"}, argv: []string{"status", "--porcelain"}},
		{argv: []string{"status", "--porcelain", "--", "notes/a.md"}},
		{argv: []string{"status", "--porcelain", "--untracked-files=no"}},
		{
			write: map[string]string{
				"seq.txt": strings.Replace(
					strings.Replace(fakeGitContractSeq, "\n2\n", "\ntwo\n", 1), "\n20\n", "\ntwenty\n", 1,
				),
				"f.go": strings.Replace(
					strings.Replace(fakeGitContractFuncs, "\tl6\n", "\tL6\n", 1), "\tl18\n", "\tL18\n", 1,
				),
			},
			argv: []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "HEAD", "--", "seq.txt", "f.go"},
		},
		{write: map[string]string{"q.txt": "q\n"}, argv: []string{"add", "q.txt"}},
		{
			remove: []string{"q.txt"},
			argv:   []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "HEAD", "--", "q.txt"},
		},
		{symlink: map[string]string{"link.md": "README.md"}, argv: []string{"hash-object", "--", "link.md"}},
		{argv: []string{"rev-parse", "--verify", "-q", "HEAD:releases"}},
		{dir: "releases", argv: []string{"rev-parse", "--verify", "-q", "HEAD:./"}},
		{dir: "releases", argv: []string{"rev-parse", "--verify", "-q", "HEAD:../../x"}},
		{
			dir:  "releases",
			argv: []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--deduplicate"},
		},
	}
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=fixture-identity", "GIT_AUTHOR_EMAIL=fixture.invalid", "GIT_AUTHOR_DATE="+fakeGitDefaultDate,
		"GIT_COMMITTER_NAME=fixture-identity", "GIT_COMMITTER_EMAIL=fixture.invalid",
		"GIT_COMMITTER_DATE="+fakeGitDefaultDate, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
	)
	realRepo, model := filepath.Join(t.TempDir(), "repo"), filepath.Join(t.TempDir(), "repo")
	fake := &Runner{}
	for _, dir := range []string{realRepo, model} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for number, step := range steps {
		results := [2]deps.RunResult{}
		for side, dir := range []string{realRepo, model} {
			for rel, body := range step.write {
				writeFakeGitFile(t, dir, rel, body)
			}
			for _, rel := range step.remove {
				if err := os.Remove(filepath.Join(dir, rel)); err != nil {
					t.Fatal(err)
				}
			}
			for rel, target := range step.symlink {
				if err := os.Symlink(target, filepath.Join(dir, rel)); err != nil {
					t.Fatal(err)
				}
			}
			var runner deps.Runner = deps.RealRunner{}
			argv := append([]string{gitBinary}, step.argv...)
			if side == 1 {
				runner, argv = fake, append([]string{"git"}, step.argv...)
			}
			result, err := runner.Run(
				context.Background(), argv, deps.RunOptions{Dir: filepath.Join(dir, step.dir), Env: env},
			)
			if err != nil {
				t.Fatalf("step %d git %q (side %d): %v", number, step.argv, side, err)
			}
			results[side] = result
		}
		if !bytes.Equal(results[0].Stdout, results[1].Stdout) || results[0].ExitCode != results[1].ExitCode ||
			step.stderr && !bytes.Equal(results[0].Stderr, results[1].Stderr) {
			t.Fatalf("step %d git %q:\nreal  exit %d stdout %q stderr %q\nmodel exit %d stdout %q stderr %q",
				number, step.argv, results[0].ExitCode, results[0].Stdout, results[0].Stderr,
				results[1].ExitCode, results[1].Stdout, results[1].Stderr)
		}
		if realTree, modelTree := fakeGitTree(t, realRepo), fakeGitTree(t, model); realTree != modelTree {
			t.Fatalf("step %d git %q: work tree\nreal  %q\nmodel %q", number, step.argv, realTree, modelTree)
		}
	}
}

// fakeGitContractSeq is thirty numbered lines, room for two hunks.
var fakeGitContractSeq = func() string {
	var lines strings.Builder
	for i := 1; i <= 30; i++ {
		lines.WriteString(strconv.Itoa(i) + "\n")
	}
	return lines.String()
}()

// fakeGitContractFuncs opens a function with trailing spaces and holds a line
// opening with a non-ASCII letter: the hunk header context git picks.
const fakeGitContractFuncs = "package x\nfunc alpha() {  \n\tl3\n\tl4\n\tl5\n\tl6\n\tl7\n\tl8\n\u00e9 sigma\n\tl10\n" +
	"\tl11\n\tl12\n\tl13\n\tl14\n\tl15\n\tl16\n\tl17\n\tl18\n\tl19\n\tl20\n\tl21\n"
