package doctor

import (
	"flag"
	"io"

	"github.com/rezzminator/professor/pfm/internal/professor"
)

const doctorUsage = "usage: pfm doctor [--verbose] [--skip-harvest] | pfm doctor --project-updates [--root DIR] [--json]" +
	" exit 0 clean, 1 warnings, 3 failures"

// projectUpdatesFlags are doctor's --project-updates mode: the project-template
// report alone, through professor.RunProjectUpdates, never the health pass.
type projectUpdatesFlags struct {
	enabled, json *bool
	root          *string
}

func bindProjectUpdatesFlags(flags *flag.FlagSet) projectUpdatesFlags {
	return projectUpdatesFlags{
		enabled: flags.Bool("project-updates", false, "report the project-template upstream diff and exit"),
		root:    flags.String("root", "", "project root for --project-updates"),
		json:    flags.Bool("json", false, "write the --project-updates report as one JSON object"),
	}
}

// dispatch returns handled=true with the exit code when the call is the
// project-updates mode or misuses its flags (usage, exit 2): --root or --json
// without --project-updates, --project-updates with a health-pass flag.
func (p projectUpdatesFlags) dispatch(
	flags *flag.FlagSet,
	healthFlagSet bool,
	home string,
	stdout io.Writer,
) (code int, handled bool) {
	switch {
	case *p.enabled && healthFlagSet, !*p.enabled && (*p.root != "" || *p.json):
		flags.Usage()
		return 2, true
	case *p.enabled:
		return professor.RunProjectUpdates(*p.root, home, *p.json, stdout), true
	}
	return 0, false
}
