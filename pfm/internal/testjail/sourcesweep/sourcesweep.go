// Package sourcesweep holds the one rule every test sweeping pfm's module source walks by: the
// directories it never opens. A checkout's generated, untracked trees (the engine mirrors under a dot
// directory, the fleet prompts pfm install assembles) are no source, and a unit test reading them
// reaches outside what the unit proof allows. It imports nothing of pfm, so the packages testjail
// itself imports (engine, paths) sweep through it too.
package sourcesweep

import "strings"

// generatedDirs are the module-relative directories no tracked file lives in, outside any dot
// directory: the fleet prompts pfm install generates (git-ignored), and tmp, the scratch directory a
// run may leave in the module (untracked, git-ignored on some hosts), which a sweep would otherwise
// list on the one machine holding it.
var generatedDirs = []string{"harness-prompts/generated", "tmp"}

// SkipDir reports whether a sweep leaves the directory at rel, its slash path relative to the module
// root ("." for the root itself): a dot directory, or a generated one.
func SkipDir(rel string) bool {
	if rel != "." {
		for _, element := range strings.Split(rel, "/") {
			if strings.HasPrefix(element, ".") {
				return true
			}
		}
	}
	for _, dir := range generatedDirs {
		if rel == dir {
			return true
		}
	}
	return false
}
