package doctor

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// printHerdrShellDoctor judges the config and wrapper from disk. Optional
// absence and deliberate user ownership are healthy, broken wiring is not.
func printHerdrShellDoctor(stdout io.Writer, home string, dependencies Dependencies) int {
	const row = "doctor: herdr-shell:"
	failed := false
	binary, ok := resolveHerdrBinary(dependencies.Runner, dependencies, func(checked, detail, fix string) {
		fmt.Fprintf(stdout, "%s WARN checked=%s: %s — %s\n", row, checked, detail, fix)
		failed = true
	})
	if !ok || failed {
		return 1
	}
	if binary == "" {
		fmt.Fprintln(stdout, row, "ok herdr absent")
		return 0
	}
	status, err := installer.InspectHerdrShell(home, dependencies.Env)
	if err != nil {
		fmt.Fprintf(stdout, "%s WARN %v\n", row, err)
		return 1
	}
	switch {
	case !status.Owned && status.Value == paths.HerdrShellWrapper(home) && !status.Wired:
		fmt.Fprintf(
			stdout,
			"%s WARN user-owned default_shell points to a missing or non-executable pfm wrapper in %s — restore pfm's marker block or choose an executable shell\n",
			row,
			status.Path,
		)
		return 1
	case status.Owned && status.Wired:
		fmt.Fprintf(stdout, "%s ok wired %s\n", row, status.Path)
	case status.Present && !status.Owned:
		fmt.Fprintf(
			stdout,
			"%s ok user-owned default_shell=%q in %s (picker wiring skipped)\n",
			row,
			status.Value,
			status.Path,
		)
	default:
		fmt.Fprintf(
			stdout,
			"%s WARN picker wiring missing or wrapper not executable in %s — run pfm install --yes\n",
			row,
			status.Path,
		)
		return 1
	}
	return 0
}
