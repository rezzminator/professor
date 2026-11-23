package testjail

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// sourceRepoEnv is the variable source-repository discovery honours first
// (professor.DiscoverSourceRepo; import cycle).
const sourceRepoEnv = "PFM_SOURCE_REPO"

// runSourceRepo is the empty directory Run's jail pins sourceRepoEnv and every
// jail's source-repository marker at: no test discovers the checkout it runs
// from, so none reads that checkout's untracked or git-ignored files (its
// pfm.config.json, generated fleet prompts, dev binary). A test whose subject
// is an installed pfm's clone says so through StageSourceRepoMarker.
var runSourceRepo string

// pinRunSourceRepo makes runSourceRepo in the jail home and points
// sourceRepoEnv and home's source-repository marker at it.
func pinRunSourceRepo(home string) error {
	sourceRepo := filepath.Join(home, sourceRepoEnv)
	if err := os.Mkdir(sourceRepo, 0o700); err != nil {
		return fmt.Errorf("create the run's empty source repository %s: %w", sourceRepo, err)
	}
	if err := os.Setenv(sourceRepoEnv, sourceRepo); err != nil {
		return fmt.Errorf("set %s to %s: %w", sourceRepoEnv, sourceRepo, err)
	}
	runSourceRepo = sourceRepo
	if err := paths.WriteSourceRepoMarker(home, sourceRepo); err != nil {
		return fmt.Errorf("point %s's source repository marker at %s: %w", home, sourceRepo, err)
	}
	return nil
}

// installedCloneFiles are the tracked checkout paths, files or whole
// directories, an installed pfm reads from the clone its marker records: the
// clone's prices, the harness-prompt parts with their reviewed baselines, the
// retired lists, the skill-source registry and the global rules. A path git ignores never joins:
// unit-proof judges each copy's read of the checkout by its path.
var installedCloneFiles = []string{
	"pfm/internal/pricing/prices.json",
	"pfm/harness-prompts/claude",
	"pfm/harness-prompts/codex",
	"pfm/harness-prompts/opencode",
	"pfm/harness-prompts/share",
	"templates/global/retired.json",
	"templates/global/skills/sources.json",
	"templates/global/rules",
}

var installedClone struct {
	once sync.Once
	dir  string
	err  error
}

// StageSourceRepoMarker records, in home, the clone a healthy install came
// from: installedCloneDir.
func StageSourceRepoMarker(t testing.TB, home string) {
	t.Helper()
	clone := installedCloneDir(t)
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatalf("point %s's source repository marker at the installed clone %s: %v", home, clone, err)
	}
}

// installedCloneDir is the clone a healthy install records: a copy of this
// checkout's installedCloneFiles, made once per test binary beside
// runSourceRepo, holding none of the files a host leaves beside the tracked
// ones (copyTrackedPaths), the same clone on every machine.
func installedCloneDir(t testing.TB) string {
	t.Helper()
	installedClone.once.Do(func() {
		if runSourceRepo == "" {
			installedClone.err = fmt.Errorf("testjail.Run has not made the run's source repository")
			return
		}
		installedClone.dir = filepath.Join(filepath.Dir(runSourceRepo), "installed-clone")
		installedClone.err = copyTrackedPaths(checkoutRoot(), installedClone.dir, installedCloneFiles)
	})
	if installedClone.err != nil {
		t.Fatalf("stage the installed clone: %v", installedClone.err)
	}
	return installedClone.dir
}

// installedCloneKinds are the file extensions a directory of
// installedCloneFiles holds in git: the prompt and rule parts and the
// baselines' model and checksum files. Below a listed directory only these
// are copied, and no dot entry: the files a host leaves beside tracked ones
// (.DS_Store, an editor's .swp or .bak, a .orig) are git-ignored or untracked,
// and the unit net starts no git to ask.
var installedCloneKinds = map[string]bool{".md": true, ".model": true, ".sha256": true}

// copyTrackedPaths copies each relative path of from, a file or a directory
// of regular files, to the same relative path under to; below a directory,
// only the installedCloneKinds files outside any dot entry.
func copyTrackedPaths(from, to string, relatives []string) error {
	for _, relative := range relatives {
		root := filepath.Join(from, relative)
		err := filepath.WalkDir(
			root,
			func(source string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				target := filepath.Join(to, source[len(from):])
				if source != root && strings.HasPrefix(entry.Name(), ".") {
					if entry.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if entry.IsDir() {
					return os.MkdirAll(target, 0o700)
				}
				if source != root && !installedCloneKinds[filepath.Ext(source)] {
					return nil
				}
				if !entry.Type().IsRegular() {
					return fmt.Errorf("copy %s: not a regular file", source)
				}
				body, err := os.ReadFile(source)
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					return err
				}
				return os.WriteFile(target, body, 0o600)
			},
		)
		if err != nil {
			return fmt.Errorf("copy %s from %s to %s: %w", relative, from, to, err)
		}
	}
	return nil
}
