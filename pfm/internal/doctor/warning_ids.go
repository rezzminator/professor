package doctor

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// A warningID is the stable short name a doctor warning carries, so an
// operator can silence it through the machine config's doctor.ignoreWarnings
// the way a linter's ignore comment names the rule it silences. A check
// adopts IDs by declaring its constants here, listing them in warningIDs,
// and printing each warning through warningFilter.warn.
type warningID string

const (
	// warnVSCodeInspect: the VS Code ownership ledger could not be read.
	warnVSCodeInspect warningID = "vscode-inspect"
	// warnVSCodeLink: a recorded extension link is missing, broken, or in
	// a state the installer did not derive.
	warnVSCodeLink warningID = "vscode-link"
	// warnVSCodeIndex: a product's extensions.json does not register the
	// linked extension, cannot be read, or is in an underived state.
	warnVSCodeIndex warningID = "vscode-index"
	// warnVSCodeSettings: an owned settings file lost its PFM profile or
	// carries the wrong CLAUDE_CONFIG_DIR.
	warnVSCodeSettings warningID = "vscode-settings"
	// warnVSCodeUnlinked: VS Code is installed on the host but pfm install
	// --vscode never linked the professor extension.
	warnVSCodeUnlinked warningID = "vscode-unlinked"
	// warnThemeUnowned: a shipped theme's file is not installer-owned, so no
	// VS Code colour theme is rendered for it.
	warnThemeUnowned warningID = "theme-unowned"
	// warnGitPush: git cannot be shown to push the Professor clone's
	// github.com origin as an account with push permission.
	warnGitPush warningID = "git-push"
	// warnLeakTerms: the clone's private leak denylist is absent, empty,
	// broken or unreadable, so its pre-push leak gate refuses every push.
	warnLeakTerms warningID = "leak-terms"
)

// warningIDs is every ID a doctor warning carries. A doctor.ignoreWarnings
// entry outside it is itself a warning, so a typo silences nothing.
var warningIDs = []warningID{
	warnVSCodeInspect, warnVSCodeLink, warnVSCodeIndex, warnVSCodeSettings, warnVSCodeUnlinked, warnThemeUnowned,
	warnGitPush, warnLeakTerms,
}

// ignoreWarningsKey is the config key every hint and IGNORED row names.
const ignoreWarningsKey = "doctor.ignoreWarnings"

// warningFilter applies one run's doctor.ignoreWarnings.
type warningFilter struct {
	ignored    map[warningID]bool
	configPath string
}

// printIgnorableDoctor runs the VS Code and theme checks through the one
// doctor.ignoreWarnings filter of the run, and returns the warnings they
// count. A check that adopts IDs takes the run's filter: here, or
// printProfessorCloneDoctor.
func printIgnorableDoctor(stdout io.Writer, filter warningFilter, home, primaryDir string, env paths.Env) int {
	return printVSCodeDoctor(stdout, home, primaryDir, env, filter) + printThemeOwnershipDoctor(stdout, home, filter)
}

// newWarningFilter reads doctor.ignoreWarnings from the loaded machine config
// and its resolved path (PFM_CONFIG honoured by the loader). Each entry that
// names no doctor warning prints one warning row and counts one.
func newWarningFilter(stdout io.Writer, machine config.Config) (warningFilter, int) {
	filter := warningFilter{ignored: map[warningID]bool{}, configPath: machine.Path}
	unknown := 0
	for _, entry := range machine.Doctor.IgnoreWarnings {
		id := warningID(entry)
		if !slices.Contains(warningIDs, id) {
			unknown++
			fmt.Fprintf(
				stdout,
				"doctor: config %s has unknown warning id %q in %s — it silences nothing; known ids: %s\n",
				ignoreWarningsKey,
				entry,
				filter.where(),
				knownWarningIDs(),
			)
			continue
		}
		filter.ignored[id] = true
	}
	return filter, unknown
}

// warn prints one warning row and returns what it adds to the tally. row is
// the warning's state, fix its remedy ("" when there is none). An ignored ID
// prints the row as IGNORED and counts 0; any other ends with the hint naming
// the ID, the key and the config file that would silence it, and counts 1.
func (filter warningFilter) warn(stdout io.Writer, id warningID, row, fix string) int {
	if filter.ignored[id] {
		fmt.Fprintf(stdout, "%s IGNORED (%s)\n", row, ignoreWarningsKey)
		return 0
	}
	if fix != "" {
		row += " — " + fix
	}
	fmt.Fprintf(
		stdout,
		"%s · don't want this? add %q to %s in %s\n",
		row,
		string(id),
		ignoreWarningsKey,
		filter.where(),
	)
	return 1
}

// where names the config file a hint points at; with no resolved path it says
// so and names the override, never an invented location.
func (filter warningFilter) where() string {
	if filter.configPath == "" {
		return "the machine config (no config path resolved — set " + paths.EnvConfig + ")"
	}
	return filter.configPath
}

func knownWarningIDs() string {
	names := make([]string, 0, len(warningIDs))
	for _, id := range warningIDs {
		names = append(names, string(id))
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}
