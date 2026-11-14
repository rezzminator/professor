package fakegit

import (
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakeGitFile is one work-tree file as git would stage it.
type fakeGitFile struct {
	entry fakeGitEntry
	body  []byte
}

// workFiles lists the files git sees in the work tree: every regular file and
// symlink outside .git and nested repositories, an ignored one only when the
// index tracks it. An ignored directory is entered only for the tracked
// files it holds; every untracked file in it is ignored.
func (r *fakeGitRepo) workFiles() (map[string]fakeGitFile, error) {
	files, rules, index := map[string]fakeGitFile{}, r.ignoreRules(), r.index()
	ignoredDirs := map[string]bool{}
	err := filepath.WalkDir(r.work, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(r.work, full)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.Name() == fakeGitDirName {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if ignoredDirs[path.Dir(rel)] || fakeGitIgnored(rules, rel, true) {
				holds := false
				for p := range index {
					holds = holds || strings.HasPrefix(p, rel+"/")
				}
				if !holds {
					return filepath.SkipDir
				}
				ignoredDirs[rel] = true
			}
			if _, err := os.Lstat(filepath.Join(full, fakeGitDirName)); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if _, tracked := index[rel]; !tracked && (ignoredDirs[path.Dir(rel)] || fakeGitIgnored(rules, rel, false)) {
			return nil
		}
		file, err := fakeGitReadFile(full)
		if err == nil {
			files[rel] = file
		}
		return err
	})
	return files, err
}

func fakeGitReadFile(full string) (fakeGitFile, error) {
	info, err := os.Lstat(full)
	if err != nil {
		return fakeGitFile{}, err
	}
	var body []byte
	mode := "100644"
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		link, err := os.Readlink(full)
		if err != nil {
			return fakeGitFile{}, err
		}
		body, mode = []byte(link), "120000"
	default:
		if body, err = os.ReadFile(full); err != nil {
			return fakeGitFile{}, err
		}
		if info.Mode()&0o100 != 0 {
			mode = "100755"
		}
	}
	return fakeGitFile{entry: fakeGitEntry{Mode: mode, Blob: fakeGitHash("blob", body)}, body: body}, nil
}

// writeFile puts entry's blob at rel in the work tree.
func (r *fakeGitRepo) writeFile(rel string, entry fakeGitEntry) error {
	full := filepath.Join(r.work, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(full); err != nil {
		return err
	}
	body := r.state.Blobs[entry.Blob]
	switch entry.Mode {
	case "120000":
		return os.Symlink(string(body), full)
	case "100755":
		return testjail.WriteExecutable(full, body, 0o755)
	default:
		return testjail.WriteExecutable(full, body, 0o644)
	}
}

// removeFile deletes rel and every directory it leaves empty.
func (r *fakeGitRepo) removeFile(rel string) error {
	full := filepath.Join(r.work, filepath.FromSlash(rel))
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	for dir := filepath.Dir(full); dir != r.work && strings.HasPrefix(dir, r.work); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
	}
	return nil
}

// moveTo carries the work tree and index from HEAD's tree to target's, the
// way checkout, merge --ff-only and reset --keep do: a path the two trees
// agree on keeps any local change, and a local change to a path they differ
// on, or an untracked file where target puts one, refuses the move and is
// returned; a tracked file deleted from the disk alone is no such change.
// hard overwrites every local change instead and removes every indexed path
// target lacks.
func (r *fakeGitRepo) moveTo(target string, hard bool) ([]string, error) {
	from, to := r.headTree(), r.state.Commits[target].Tree
	work, err := r.workFiles()
	if err != nil {
		return nil, err
	}
	index := r.index()
	changed := map[string]bool{}
	for p := range from {
		changed[p] = from[p] != to[p]
	}
	for p := range to {
		changed[p] = from[p] != to[p]
	}
	var conflicts []string
	for p, differs := range changed {
		if !differs || hard {
			continue
		}
		local, onDisk := work[p]
		staged, tracked := index[p]
		_, inFrom := from[p]
		if (inFrom && (!tracked || staged != from[p] || (onDisk && local.entry != staged))) ||
			(!inFrom && onDisk && local.entry != to[p]) {
			conflicts = append(conflicts, p)
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return conflicts, nil
	}
	next := map[string]fakeGitEntry{}
	for p, entry := range index {
		if !changed[p] && !hard {
			next[p] = entry
		}
	}
	for p, entry := range to {
		if changed[p] || hard {
			next[p] = entry
		}
	}
	gone := maps.Clone(from)
	if hard {
		maps.Copy(gone, index)
	}
	for p := range gone {
		if _, kept := to[p]; !kept && (changed[p] || hard) {
			if err := r.removeFile(p); err != nil {
				return nil, err
			}
		}
	}
	for p, entry := range to {
		if local, ok := work[p]; changed[p] || (hard && (!ok || local.entry != entry)) {
			if err := r.writeFile(p, entry); err != nil {
				return nil, err
			}
		}
	}
	r.setIndex(next)
	return nil, nil
}
