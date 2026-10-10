package transcript

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FindSessions lists the session transcripts {root}/{project}/{prefix}*.jsonl
// under each root, roots in order and names sorted: Claude Code writes a
// session one directory below its root. A project directory reached through a
// symlink counts; a name starting with a dot does not. A root that does not
// exist holds nothing; every place the walk failed to look is one entry of
// errs, and the walk goes on.
func FindSessions(roots []string, prefix string) (found []string, errs []error) {
	for _, root := range roots {
		projects, projectErrs := directories(root)
		errs = append(errs, projectErrs...)
		for _, project := range projects {
			entries, err := os.ReadDir(project)
			if err != nil {
				errs = append(errs, fmt.Errorf("list Claude project %s: %w", project, err))
				continue
			}
			for _, entry := range entries {
				name := entry.Name()
				if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, transcriptExt) ||
					strings.HasPrefix(name, ".") {
					continue
				}
				path := filepath.Join(project, name)
				info, err := os.Stat(path)
				switch {
				case errors.Is(err, fs.ErrNotExist):
				case err != nil:
					errs = append(errs, fmt.Errorf("stat %s: %w", path, err))
				case !info.IsDir():
					found = append(found, path)
				}
			}
		}
	}
	return found, errs
}

// FindSubagents lists the sub-agent transcripts agent-{prefix}*.jsonl of every
// session under each root ({root}/{project}/{session}/subagents, at any depth
// below it), roots in order; errs as FindSessions.
func FindSubagents(roots []string, prefix string) (found []string, errs []error) {
	for _, root := range roots {
		projects, projectErrs := directories(root)
		errs = append(errs, projectErrs...)
		for _, project := range projects {
			sessions, sessionErrs := directories(project)
			errs = append(errs, sessionErrs...)
			for _, session := range sessions {
				dir := filepath.Join(session, subagentsDirName)
				info, err := os.Stat(dir)
				if err != nil {
					if !errors.Is(err, fs.ErrNotExist) {
						errs = append(errs, fmt.Errorf("stat %s: %w", dir, err))
					}
					continue
				}
				if !info.IsDir() {
					continue
				}
				paths, err := SubagentTranscripts(dir)
				if err != nil {
					errs = append(errs, err)
				}
				for _, path := range paths {
					if strings.HasPrefix(filepath.Base(path), subagentPrefix+prefix) {
						found = append(found, path)
					}
				}
			}
		}
	}
	return found, errs
}

// SubagentTranscripts lists every agent-<id>.jsonl under a session's
// subagents directory, at any depth (a workflow nests its agents in
// workflows/wf_*/), in walk order, a symlinked file included and a symlinked
// directory not followed. The first place the walk cannot read is the error.
func SubagentTranscripts(dir string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if _, ok := SubagentID(entry.Name()); ok && !entry.IsDir() {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return found, fmt.Errorf("list sub-agent transcripts under %s: %w", dir, err)
	}
	return found, nil
}

// directories lists dir's subdirectories in name order, a symlink to a
// directory included, a dot name not: dir missing holds nothing, a dangling
// link is no directory, any other failure is an error.
func directories(dir string) (found []string, errs []error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{fmt.Errorf("list %s: %w", dir, err)}
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			errs = append(errs, fmt.Errorf("stat %s: %w", path, err))
		case info.IsDir():
			found = append(found, path)
		}
	}
	return found, errs
}
