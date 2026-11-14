package fakegit

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// fakeGitIgnoreRule is one .gitignore line.
type fakeGitIgnoreRule struct {
	pattern                   string
	negate, dirOnly, anchored bool
}

// ignoreRules reads the work tree's root .gitignore: negation, a trailing
// slash for directories only, and a slash anywhere but the end anchoring the
// pattern at the root; anything else matches a name at any depth.
func (r *fakeGitRepo) ignoreRules() []fakeGitIgnoreRule {
	raw, err := os.ReadFile(filepath.Join(r.work, ".gitignore"))
	if err != nil {
		return nil
	}
	var rules []fakeGitIgnoreRule
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var rule fakeGitIgnoreRule
		line, rule.negate = strings.CutPrefix(line, "!")
		line, rule.dirOnly = strings.CutSuffix(line, "/")
		rule.anchored = strings.Contains(line, "/")
		rule.pattern = strings.TrimPrefix(line, "/")
		rules = append(rules, rule)
	}
	return rules
}

// fakeGitIgnored reports whether rel, a directory when dir, is ignored: the
// last rule matching it decides.
func fakeGitIgnored(rules []fakeGitIgnoreRule, rel string, dir bool) bool {
	ignored := false
	for _, rule := range rules {
		if rule.dirOnly && !dir {
			continue
		}
		target := rel
		if !rule.anchored {
			target = path.Base(rel)
		}
		if ok, _ := path.Match(rule.pattern, target); ok {
			ignored = !rule.negate
		}
	}
	return ignored
}

// ignoredFiles lists, sorted, the untracked files a .gitignore rule ignores,
// every file under an ignored directory included.
func (r *fakeGitRepo) ignoredFiles() ([]string, error) {
	var files []string
	rules, index := r.ignoreRules(), r.index()
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
			if _, err := os.Lstat(filepath.Join(full, fakeGitDirName)); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if _, tracked := index[rel]; tracked {
			return nil
		}
		for dir := rel; ; {
			if fakeGitIgnored(rules, dir, dir != rel) {
				files = append(files, rel)
				return nil
			}
			parent := path.Dir(dir)
			if parent == "." {
				return nil
			}
			dir = parent
		}
	})
	sort.Strings(files)
	return files, err
}
