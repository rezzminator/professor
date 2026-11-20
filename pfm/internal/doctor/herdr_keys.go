package doctor

import (
	"fmt"
	"io"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// printHerdrKeysDoctor names each mapping of herdr.keys over pfm's keymap the
// Herdr config lacks: without
// them VS Code's cmd+shift+up/down (^Bi / ^Bm) and prefix+a do nothing, and
// nothing else says so. A hand-written [keys] table stays the user's, so its
// fix is to add the mapping there; an unusable herdr binary is the
// herdr-shell row's warning, named here and not counted twice.
func printHerdrKeysDoctor(stdout io.Writer, home string, dependencies Dependencies, keys *pfmconfig.HerdrKeys) int {
	const row = "doctor: herdr-keys:"
	binary, ok := resolveHerdrBinary(dependencies.Runner, dependencies, func(string, string, string) {})
	switch {
	case !ok:
		fmt.Fprintln(stdout, row, "not checked — the herdr binary is unusable (see herdr-shell)")
		return 0
	case binary == "":
		fmt.Fprintln(stdout, row, "ok herdr absent")
		return 0
	}
	status, err := installer.InspectHerdrKeys(home, dependencies.Env, keys)
	if err != nil {
		fmt.Fprintf(stdout, "%s WARN %v\n", row, err)
		return 1
	}
	missing := strings.Join(status.Missing, ", ")
	switch {
	case len(status.Missing) != 0 && status.HandWritten:
		fmt.Fprintf(stdout, "%s WARN hand-written [keys] in %s lacks %s — add them to that table "+
			"(pfm install leaves a hand-written table alone)\n", row, status.Path, missing)
	case len(status.Missing) != 0:
		fmt.Fprintf(stdout, "%s WARN %s lacks %s — run pfm install --yes\n", row, status.Path, missing)
	case status.Block && !status.Current && !status.HandWritten:
		fmt.Fprintf(stdout, "%s WARN pfm's keys block in %s is out of date — run pfm install --yes\n", row, status.Path)
	case !status.Block && !status.HandWritten:
		// herdr.keys removed every pfm mapping, so none is missing, yet no keys table is there.
		fmt.Fprintf(stdout, "%s WARN %s lacks pfm's keys block — run pfm install --yes\n", row, status.Path)
	case status.Owned():
		fmt.Fprintf(stdout, "%s ok wired %s\n", row, status.Path)
		return 0
	default:
		fmt.Fprintf(stdout, "%s ok hand-written [keys] in %s binds every pfm mapping\n", row, status.Path)
		return 0
	}
	return 1
}
