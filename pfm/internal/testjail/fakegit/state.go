package fakegit

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// fakeGitStateFile is the model's record of one repository, beside its config.
const fakeGitStateFile = "fakegit.json"

// fakeGitDirName is the directory (or link file) that makes a work tree a repository.
const fakeGitDirName = ".git"

// fakeGitRepo is one opened repository: its git directory, its work tree
// ("" when bare), the linked work tree the call runs in ("" for the main
// one), the recorded state, and the error naming the first argument the
// call used that is outside the model.
type fakeGitRepo struct {
	gitDir, work, linked string
	state                fakeGitState
	refused              error
}

type fakeGitState struct {
	Bare      bool                      `json:"bare,omitempty"`
	Head      string                    `json:"head"` // refs/heads/{branch} or a commit id
	Refs      map[string]string         `json:"refs"`
	Blobs     map[string][]byte         `json:"blobs"`
	Commits   map[string]fakeGitCommit  `json:"commits"`
	Tags      map[string]string         `json:"tags"` // annotated tag id → commit id
	Index     map[string]fakeGitEntry   `json:"index"`
	Worktrees map[string]*fakeGitLinked `json:"worktrees"`
}

type fakeGitLinked struct {
	Head  string                  `json:"head"`
	Index map[string]fakeGitEntry `json:"index"`
	Admin string                  `json:"admin,omitempty"` // its directory under {git dir}/worktrees
}

// adminDir is the administrative directory git keeps for the linked work
// tree at path: its base name, suffixed 1, 2… when another took the name.
func (r *fakeGitRepo) adminDir(path string) string {
	name := filepath.Base(path)
	if linked := r.state.Worktrees[path]; linked != nil && linked.Admin != "" {
		name = linked.Admin
	}
	return filepath.Join(r.gitDir, "worktrees", name)
}

type fakeGitCommit struct {
	Tree    map[string]fakeGitEntry `json:"tree"`
	Parents []string                `json:"parents"`
	Subject string                  `json:"subject"`
}

type fakeGitEntry struct {
	Mode string `json:"mode"`
	Blob string `json:"blob"`
}

func newFakeGitState(bare bool) fakeGitState {
	return fakeGitState{
		Bare: bare, Head: "refs/heads/master", Refs: map[string]string{}, Blobs: map[string][]byte{},
		Commits: map[string]fakeGitCommit{}, Tags: map[string]string{}, Index: map[string]fakeGitEntry{},
		Worktrees: map[string]*fakeGitLinked{},
	}
}

// open finds the repository the call runs in: GIT_DIR when the environment
// names one, else the nearest .git directory, linked .git file or bare root
// at or above the call's directory.
func (c *fakeGitCall) open() (*fakeGitRepo, int) {
	if gitDir := c.getenv("GIT_DIR"); gitDir != "" {
		work := c.getenv("GIT_WORK_TREE")
		if work == "" {
			work = c.dir
		}
		return c.loadGitDir(c.path(gitDir), c.path(work))
	}
	for dir := c.dir; ; dir = filepath.Dir(dir) {
		dotGit := filepath.Join(dir, fakeGitDirName)
		if info, err := os.Stat(dotGit); err == nil && info.IsDir() {
			return c.load(&fakeGitRepo{gitDir: dotGit, work: dir})
		} else if err == nil {
			raw, readErr := os.ReadFile(dotGit)
			if readErr != nil {
				return nil, c.fatal("%v", readErr)
			}
			linked := strings.TrimSpace(strings.TrimPrefix(string(raw), "gitdir:"))
			return c.load(&fakeGitRepo{gitDir: filepath.Dir(filepath.Dir(linked)), work: dir, linked: dir})
		}
		if _, err := os.Stat(filepath.Join(dir, fakeGitStateFile)); err == nil {
			return c.load(&fakeGitRepo{gitDir: dir})
		}
		if filepath.Dir(dir) == dir {
			return nil, c.fatal("not a git repository (or any of the parent directories): .git")
		}
	}
}

// loadGitDir opens the repository GIT_DIR names: a linked work tree's
// {common}/worktrees/{name} directory reads that work tree's HEAD and index.
func (c *fakeGitCall) loadGitDir(gitDir, work string) (*fakeGitRepo, int) {
	common := filepath.Dir(filepath.Dir(gitDir))
	if _, err := os.Stat(filepath.Join(gitDir, fakeGitStateFile)); err == nil ||
		filepath.Base(filepath.Dir(gitDir)) != "worktrees" {
		return c.load(&fakeGitRepo{gitDir: gitDir, work: work})
	}
	repo, code := c.load(&fakeGitRepo{gitDir: common, work: work})
	if code != 0 {
		return nil, code
	}
	for path := range repo.state.Worktrees {
		if repo.adminDir(path) == gitDir {
			repo.linked = path
			return repo, 0
		}
	}
	return nil, c.fatal("not a git repository: '%s'", gitDir)
}

func (c *fakeGitCall) load(repo *fakeGitRepo) (*fakeGitRepo, int) {
	raw, err := os.ReadFile(filepath.Join(repo.gitDir, fakeGitStateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, c.fatal("not a git repository: '%s'", repo.gitDir)
	}
	if err != nil {
		return nil, c.fatal("%v", err)
	}
	if err := json.Unmarshal(raw, &repo.state); err != nil {
		return nil, c.fatal("%s: %v", repo.gitDir, err)
	}
	if _, known := repo.state.Worktrees[repo.linked]; repo.linked != "" && !known {
		return nil, c.fatal("not a git repository: %s is no linked work tree of %s", repo.linked, repo.gitDir)
	}
	if repo.state.Bare && repo.linked == "" {
		repo.work = ""
	}
	if line, err := repo.configCheck(); err != nil {
		return nil, c.fatal("bad config line %d in file %s", line, filepath.Join(repo.gitDir, "config"))
	}
	return repo, 0
}

func (r *fakeGitRepo) save() error {
	raw, err := json.Marshal(r.state)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.gitDir, fakeGitStateFile), raw, 0o600)
}

// head and setHead read and move the HEAD of the work tree the call runs in.
func (r *fakeGitRepo) head() string {
	if r.linked != "" {
		return r.state.Worktrees[r.linked].Head
	}
	return r.state.Head
}

func (r *fakeGitRepo) index() map[string]fakeGitEntry {
	if r.linked != "" {
		return r.state.Worktrees[r.linked].Index
	}
	return r.state.Index
}

func (r *fakeGitRepo) setIndex(index map[string]fakeGitEntry) {
	if r.linked != "" {
		r.state.Worktrees[r.linked].Index = index
		return
	}
	r.state.Index = index
}

// moveHead points HEAD's branch at id, or HEAD itself when it is detached.
func (r *fakeGitRepo) moveHead(id string) {
	if head := r.head(); strings.HasPrefix(head, "refs/") {
		r.state.Refs[head] = id
	} else if r.linked != "" {
		r.state.Worktrees[r.linked].Head = id
	} else {
		r.state.Head = id
	}
}

func (r *fakeGitRepo) setHead(head string) {
	if r.linked != "" {
		r.state.Worktrees[r.linked].Head = head
	} else {
		r.state.Head = head
	}
}

// configCheck returns the first line git would refuse to parse.
// fakeGitConfigSection is the header and variable name of a dotted key.
// configGet reads key: a -c override first, then the repository's config.
// configSet writes key = value into its section, adding the section if new.
