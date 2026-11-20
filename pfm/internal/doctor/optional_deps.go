package doctor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// printOptionalDepsDoctor compares the optional dependencies present now with
// the ones the last pfm install found: one that appeared since is a counted
// warning, because install wires a surface for it (Herdr's shell and theme
// blocks, rumdl through uv) only when it runs. No record is a named row, an
// unreadable record or a lookup that could not run is a counted warning.
func printOptionalDepsDoctor(stdout io.Writer, home string, runner deps.Runner) (warnings int) {
	const fix = " — fix: pfm install --yes\n"
	record, err := installer.ReadOptionalDepsRecord(home)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprint(
			stdout,
			"doctor: deps_since_install record=absent — no install has recorded the optional dependencies it found"+fix,
		)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stdout, "doctor: deps_since_install WARN record unreadable: %v"+fix, err)
		return 1
	}
	// A failed lookup is its own warning; the lookups that answered still
	// name what appeared.
	now, lookErr := installer.ProbeOptionalDeps(runner)
	if lookErr != nil {
		fmt.Fprintf(stdout, "doctor: deps_since_install WARN could not look: %v\n", lookErr)
	}
	if lookErr != nil {
		warnings++
	}
	if appeared := installer.OptionalDepsAppeared(record, now); len(appeared) != 0 {
		fmt.Fprintf(stdout, "doctor: deps_since_install WARN appeared since the last install, not yet wired: %s"+fix,
			strings.Join(appeared, ","))
		warnings++
	}
	if warnings != 0 {
		return warnings
	}
	fmt.Fprint(stdout, "doctor: deps_since_install ok none appeared since the last install\n")
	return 0
}
