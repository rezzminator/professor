// Package sourcelink owns the one rule both engine mirror generators
// (codexgen, opencodegen) apply to a Claude source that is a symlink whose
// target does not resolve right now — an adopter's .claude/agents/labber.md
// pointing into an uninitialised submodule. Such a link names a source pfm
// cannot read yet, not one the adopter retired, so its generated twin is kept
// as it is, never rewritten and never swept as an orphan.
package sourcelink

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// LinkTarget is the target text of the unresolvable source link at path. An
// unreadable link names the read failure, never an empty string that would
// read as "no link".
func LinkTarget(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return fmt.Sprintf("unreadable link (%v)", err)
	}
	return target
}

// KeepTwin leaves twin, the generated output of the unresolvable source link
// source → target, exactly as it is and says so when it exists. A twin that
// was never generated has nothing to keep and returns neither; one that
// cannot be inspected is a problem, never a silent keep.
func KeepTwin(twin, source, target string) (warning, problem string) {
	if _, err := os.Lstat(twin); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ""
		}
		return "", fmt.Sprintf("inspect kept twin %s: %v", twin, err)
	}
	return fmt.Sprintf("source unresolvable: %s → %s; twin kept", source, target), ""
}
