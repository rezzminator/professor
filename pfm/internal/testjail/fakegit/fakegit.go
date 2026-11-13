// Package fakegit answers git from a model instead of a git process, for the
// tests whose code runs git through a deps.Runner. The model keeps each
// repository's state in the fixture tree itself (fakegit.json and a git-format
// config in .git/, or in a bare repository's root), so a fixture copied with its
// .git carries its history and tests on separate trees share one Runner. Object
// ids are git's own: blobs, trees, commits and annotated tags hash as git hashes
// them, dated 1700000000 +0000 unless GIT_AUTHOR_DATE / GIT_COMMITTER_DATE say
// otherwise. fakegit_jail_test.go runs one table through real git and the model.
//
// fakegit.go: Runner, Must, the argv dispatch and flag parsing.
// state.go: finding, loading and saving a repository. config.go: its config file.
// objects.go: object ids, revisions, ancestry.
// tree.go: the work tree, moving the work tree to a commit.
// ignore.go: .gitignore rules and the ignored files.
// read.go: rev-parse, symbolic-ref, describe, merge-base, show.
// list.go: status, ls-files, ls-tree. history.go: diff, log.
// hunks.go: a diff's line changes, hunks and their headers.
// write.go: init, config, add, commit, tag, branch.
// switch.go: checkout, reset, merge. sync.go: remote, push, fetch, ls-remote, worktree.
package fakegit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// Runner is the git model behind a test's deps.Runner: the argv shapes
// update, professor, installer and scaffold run, and the fixture verbs their
// tests build repositories with. An argv outside the model is an error naming it.
type Runner struct {
	mu sync.Mutex
}

// fakeGitCall is one git invocation: where it runs, what it may read, and
// what it prints.
type fakeGitCall struct {
	dir       string
	env       []string
	overrides map[string]string
	out       bytes.Buffer
	errOut    bytes.Buffer
}

// Run answers argv when its argv[0] is git and its verb is modelled.
func (g *Runner) Run(_ context.Context, argv []string, opts deps.RunOptions) (deps.RunResult, error) {
	if len(argv) == 0 || filepath.Base(argv[0]) != gitName {
		return deps.RunResult{}, fmt.Errorf("fakegit: argv %q is outside the git model", argv)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	call := &fakeGitCall{dir: opts.Dir, env: opts.Env, overrides: map[string]string{}}
	if call.dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return deps.RunResult{}, fmt.Errorf("fakegit: working directory: %w", err)
		}
		call.dir = wd
	}
	if call.env == nil {
		call.env = os.Environ()
	}
	args := argv[1:]
	for len(args) >= 2 && (args[0] == "-C" || args[0] == "-c") {
		if args[0] == "-C" {
			call.dir = call.path(args[1])
		} else if key, value, ok := strings.Cut(args[1], "="); ok {
			call.overrides[strings.ToLower(key)] = value
		}
		args = args[2:]
	}
	code, err := call.dispatch(fakeGitNormalize(args))
	if err != nil {
		return deps.RunResult{}, fmt.Errorf("fakegit: argv %q is outside the git model: %w", argv, err)
	}
	return deps.RunResult{Stdout: call.out.Bytes(), Stderr: call.errOut.Bytes(), ExitCode: code}, nil
}

// LookPath resolves git alone, to its own name.
func (g *Runner) LookPath(name string) (string, error) {
	if name == gitName {
		return gitName, nil
	}
	return "", fmt.Errorf("fakegit: %q is outside the git model", name)
}

// Start refuses: git is only ever run to completion.
func (g *Runner) Start(_ context.Context, argv []string, _ deps.StartOptions) (deps.Process, error) {
	return nil, fmt.Errorf("fakegit: argv %q is outside the git model: git is never started", argv)
}

// Must runs git args in dir through the model and fails t unless git exits 0,
// returning stdout with surrounding space trimmed: the fixture verbs' door.
func (g *Runner) Must(t testing.TB, dir string, args ...string) string {
	t.Helper()
	result, err := g.Run(context.Background(), append([]string{"git"}, args...), deps.RunOptions{Dir: dir})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf(
			"fake git %s in %s: exit %d: %v\n%s",
			strings.Join(args, " "),
			dir,
			result.ExitCode,
			err,
			result.Stderr,
		)
	}
	return strings.TrimSpace(string(result.Stdout))
}

// fakeGitNormalize drops the quiet flags, which change nothing the model
// prints, and splits -qm, -qb and -qB into their meaningful halves.
func fakeGitNormalize(args []string) []string {
	var out []string
	for _, arg := range args {
		switch arg {
		case "-q", "--quiet":
		case "-qm", "-qb", "-qB":
			out = append(out, "-"+arg[2:])
		default:
			out = append(out, arg)
		}
	}
	return out
}

func (c *fakeGitCall) path(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(c.dir, p)
}

func (c *fakeGitCall) getenv(key string) string {
	value := ""
	for _, entry := range c.env {
		if k, v, ok := strings.Cut(entry, "="); ok && k == key {
			value = v
		}
	}
	return value
}

// fatal prints git's fatal line and returns its exit code.
func (c *fakeGitCall) fatal(format string, args ...any) int {
	fmt.Fprintf(&c.errOut, "fatal: "+format+"\n", args...)
	return 128
}

func (c *fakeGitCall) dispatch(args []string) (int, error) {
	if len(args) == 0 {
		return 0, fmt.Errorf("no verb")
	}
	if args[0] == "init" {
		return c.initRepo(args[1:])
	}
	verbs := map[string]func(*fakeGitRepo, []string) (int, error){
		gitAdd: c.add, "branch": c.branch, "checkout": c.checkout, "commit": c.commit,
		"config": c.config, "describe": c.describe, "diff": c.diff, "fetch": c.fetch,
		"hash-object": c.hashObject, "log": c.log, "ls-files": c.lsFiles, "ls-remote": c.lsRemote,
		"ls-tree": c.lsTree,
		"merge":   c.merge, "merge-base": c.mergeBase, "push": c.push, "remote": c.remote,
		"reset": c.reset, "rev-parse": c.revParse, "show": c.show, "status": c.status,
		"symbolic-ref": c.symbolicRef, "tag": c.tag, "worktree": c.worktree,
	}
	verb, ok := verbs[args[0]]
	if !ok {
		return 0, fmt.Errorf("verb %q is not modelled", args[0])
	}
	repo, code := c.open()
	if repo == nil {
		return code, nil
	}
	code, err := verb(repo, args[1:])
	if err == nil && repo.refused != nil {
		return 0, repo.refused
	}
	if err != nil || code != 0 {
		return code, err
	}
	return 0, repo.save()
}

// fakeGitFlags splits args into the flags the verb knows (each with the
// number of values it takes) and the rest; an unknown flag is outside the model.
func fakeGitFlags(args []string, known map[string]int) (map[string][]string, []string, error) {
	flags, rest := map[string][]string{}, []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			rest = append(rest, arg)
			continue
		}
		name, value, inline := strings.Cut(arg, "=")
		count, ok := known[name]
		if !ok {
			return nil, nil, fmt.Errorf("flag %q is not modelled", arg)
		}
		switch {
		case inline:
			flags[name] = append(flags[name], value)
		case count == 1 && i+1 < len(args):
			flags[name] = append(flags[name], args[i+1])
			i++
		default:
			flags[name] = append(flags[name], "")
		}
	}
	return flags, rest, nil
}

// fakeGitPathspec splits the arguments after "--" from those before it.
func fakeGitPathspec(rest []string) (revs, paths []string) {
	for i, arg := range rest {
		if arg == "--" {
			return rest[:i], rest[i+1:]
		}
	}
	return rest, nil
}

// rootRelative is the pathspecs as git reads them from a subdirectory: relative
// to the repository's root, "." for the root itself.
func (c *fakeGitCall) rootRelative(r *fakeGitRepo, specs []string) []string {
	if r.work == "" || c.dir == r.work {
		return specs
	}
	relative := make([]string, len(specs))
	for i, spec := range specs {
		relative[i] = spec
		if rel, err := filepath.Rel(r.work, c.path(spec)); err == nil {
			relative[i] = filepath.ToSlash(rel)
		}
	}
	return relative
}

// fakeGitUnder reports whether path is spec or lies beneath it.
func fakeGitUnder(path string, specs []string) bool {
	if len(specs) == 0 {
		return true
	}
	for _, spec := range specs {
		spec = strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(spec), "./"), "/")
		if spec == "." || spec == "" || path == spec || strings.HasPrefix(path, spec+"/") {
			return true
		}
	}
	return false
}
