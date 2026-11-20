package doctor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/rezzminator/professor/pfm/internal/installer"
)

// printThemeOwnershipDoctor warns once per shipped theme install left alone
// as the user's work: no VS Code colour theme is rendered for it, which is
// otherwise visible only as the one theme missing from the picker. A host
// with no source clone listing is a named row, never "every theme owned".
func printThemeOwnershipDoctor(stdout io.Writer, home string, filter warningFilter) int {
	names, err := installer.UnownedShippedThemes(home)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stdout, "doctor: theme_ownership not checked — no source clone theme listing (%v)\n", err)
		return 0
	}
	if err != nil {
		return filter.warn(
			stdout,
			warnThemeUnowned,
			fmt.Sprintf("doctor: theme_ownership unreadable error=%v", err),
			"",
		)
	}
	warnings := 0
	for _, name := range names {
		warnings += filter.warn(stdout, warnThemeUnowned,
			"doctor: theme "+name+" is not installer-owned: no VS Code colour theme is rendered for it",
			"move its file aside (pfm install names it), then run pfm install --yes")
	}
	if len(names) == 0 {
		fmt.Fprintln(stdout, "doctor: theme_ownership ok every shipped theme present is installer-owned")
	}
	return warnings
}
