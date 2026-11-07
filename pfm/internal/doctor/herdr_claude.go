package doctor

import (
	"fmt"
	"io"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// printHerdrClaudeStores prints one herdr-integration row per Claude settings
// store the dirs share, on a Herdr host (herdr.HostHasHerdr): no Herdr Claude
// hook is a FAIL with the install under the store's chosen dir as its fix, one
// is ok, more than one a WARN naming each, and an unreadable settings.json a
// FAIL of its own, never "missing".
func printHerdrClaudeStores(stdout io.Writer, dependencies Dependencies, dirs []string) (warnings, failures int) {
	if !herdr.HostHasHerdr(dependencies.Env.Get, dependencies.Runner.LookPath) {
		return 0, 0
	}
	claude := herdr.Label(pfmengine.Claude)
	for _, store := range herdr.ClaudeStores(dirs) {
		switch {
		case store.Err != nil:
			failures++
			fmt.Fprintf(stdout, "%s FAIL need=readable %s settings checked=%s error=%q — "+
				"fix: repair %s so it decodes as JSON, then rerun pfm doctor\n",
				herdrIntegrationRow, claude, store.Settings, store.Err.Error(), store.Settings)
		case len(store.Hooks) == 0:
			failures++
			fix := herdr.ClaudeIntegrationCommand(herdr.HostBinary(dependencies.Env.Get), store.Chosen)
			fmt.Fprintf(stdout, "%s FAIL need=herdr %s integration checked=%s hooks=none — fix: %s\n",
				herdrIntegrationRow, claude, store.Settings, fix)
		case len(store.Hooks) == 1:
			fmt.Fprintf(stdout, "%s ok %s: herdr integration hook in %s: %s\n",
				herdrIntegrationRow, claude, store.Settings, store.Hooks[0])
		default:
			warnings++
			quoted := make([]string, len(store.Hooks))
			for at, hook := range store.Hooks {
				quoted[at] = fmt.Sprintf("%q", hook)
			}
			fmt.Fprintf(stdout, "%s WARN need=one herdr %s integration hook checked=%s hooks=%s — "+
				"fix: delete all but one from %s, then rerun pfm doctor\n",
				herdrIntegrationRow, claude, store.Settings, strings.Join(quoted, ", "), store.Settings)
		}
	}
	return warnings, failures
}
